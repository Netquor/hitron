package hitron

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The CODA6X ships a spectrum analyser behind /1/Device/Spectrum. Three of its
// four branches shell out to /usr/sbin/hitrondec, so every payload is a thin
// JSON wrapper around the tool's output:
//
//	FreqList      -l        the downstream channels the analyser can tune to
//	RTData        -m        per-channel power and MER snapshots
//	FreqPower     -f c f t  a frequency/power trace for one capture window
//	Constellation -c port   I/Q points for one downstream port
//
// The tuning is a single shared hardware tuner. A ~6 MHz window is the only
// span this firmware captures reliably; wider requests come back empty or with
// an unrelated slice, so the client always asks for center±3 MHz and never
// fires two captures concurrently.

// SpectrumChannel is one downstream channel the analyser can tune to. PortID
// is the analyser's own port index ("ri" in FreqList); it is not the DOCSIS
// channel id and the ids are not sequential (a live CODA-60 reports 0,25..31).
type SpectrumChannel struct {
	FreqMHz float64 `json:"freq_mhz"`
	PortID  int     `json:"port_id"`
}

// SpectrumPoint is one frequency/power sample, in MHz and dBmV.
type SpectrumPoint struct {
	FreqMHz   float64 `json:"freq_mhz"`
	PowerDBmV float64 `json:"power_dbmv"`
}

// SpectrumSweep is one captured window. From/To are the frequencies actually
// returned, which can differ from what was requested.
type SpectrumSweep struct {
	CenterMHz float64         `json:"center_mhz"`
	FromMHz   float64         `json:"from_mhz"`
	ToMHz     float64         `json:"to_mhz"`
	Points    []SpectrumPoint `json:"points"`
	At        time.Time       `json:"at"`
}

// RealTimeChannel is one channel in an RTData snapshot. MER is carried in the
// payload's "m" field; it is not a max-hold power.
type RealTimeChannel struct {
	FreqMHz   float64 `json:"freq_mhz"`
	PowerDBmV float64 `json:"power_dbmv"`
	MERdB     float64 `json:"mer_db"`
}

// RealTimeFrame is one timestamped RTData snapshot.
type RealTimeFrame struct {
	Time     string            `json:"time"`
	Channels []RealTimeChannel `json:"channels"`
}

// IQPoint is one constellation sample.
type IQPoint struct {
	I float64 `json:"i"`
	Q float64 `json:"q"`
}

// Constellation is the decoded constellation for one downstream port.
type Constellation struct {
	PortID       int       `json:"port_id"`
	BandwidthMHz float64   `json:"bandwidth_mhz,omitempty"`
	PowerDBmV    float64   `json:"power_dbmv,omitempty"`
	MERdB        float64   `json:"mer_db,omitempty"`
	Modulation   string    `json:"modulation,omitempty"`
	Max          int       `json:"max,omitempty"`
	Points       []IQPoint `json:"points"`
	At           time.Time `json:"at"`
}

func (c *Client) spectrumOnly(what string) error {
	if c.Kind != KindCoda6X {
		return fmt.Errorf("%s: %w (CODA6X only)", what, ErrUnsupported)
	}
	return nil
}

// SpectrumChannels reads the analyser's frequency list.
func (c *Client) SpectrumChannels(ctx context.Context) ([]SpectrumChannel, error) {
	if err := c.spectrumOnly("spectrum channels"); err != nil {
		return nil, err
	}
	value, err := c.getJSON(ctx, "/1/Device/Spectrum/FreqList")
	if err != nil {
		return nil, err
	}
	object, _ := value.(map[string]any)
	channels := make([]SpectrumChannel, 0, 8)
	for _, entry := range asSlice(object["frequency"]) {
		item := asMap(entry)
		khz, ok := num(item["f"])
		if !ok {
			continue
		}
		port, _ := num(item["ri"])
		channels = append(channels, SpectrumChannel{FreqMHz: khz / 1000, PortID: int(port)})
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].FreqMHz < channels[j].FreqMHz })
	return channels, nil
}

// SpectrumSweep captures a 6 MHz window centred on centerMHz. The firmware
// validates 49..1788 MHz; a center outside that range is clamped rather than
// rejected, because the panel may be pointed at an arbitrary frequency.
func (c *Client) SpectrumSweep(ctx context.Context, centerMHz float64) (*SpectrumSweep, error) {
	if err := c.spectrumOnly("spectrum sweep"); err != nil {
		return nil, err
	}
	if math.IsNaN(centerMHz) {
		centerMHz = 645
	}
	centerMHz = math.Max(49, math.Min(1788, centerMHz))
	from := centerMHz - 3
	to := centerMHz + 3
	path := fmt.Sprintf("/1/Device/Spectrum/FreqPower?centFreq=%s&fromFreq=%s&toFreq=%s",
		formatFreq(centerMHz), formatFreq(from), formatFreq(to))
	value, err := c.getJSON(ctx, path)
	if err != nil {
		return nil, err
	}
	object, _ := value.(map[string]any)
	sweep := &SpectrumSweep{CenterMHz: centerMHz, FromMHz: from, ToMHz: to, At: time.Now()}
	for _, entry := range asSlice(object["spectrum"]) {
		item := asMap(entry)
		freq, ok := num(item["f"])
		if !ok {
			continue
		}
		power, ok := signal(item["p"])
		if !ok {
			continue
		}
		sweep.Points = append(sweep.Points, SpectrumPoint{FreqMHz: freq, PowerDBmV: power})
	}
	sort.Slice(sweep.Points, func(i, j int) bool {
		return sweep.Points[i].FreqMHz < sweep.Points[j].FreqMHz
	})
	if len(sweep.Points) > 0 {
		sweep.FromMHz = sweep.Points[0].FreqMHz
		sweep.ToMHz = sweep.Points[len(sweep.Points)-1].FreqMHz
	}
	return sweep, nil
}

// RealTimeSpectrum reads the per-channel RTData snapshots.
func (c *Client) RealTimeSpectrum(ctx context.Context) ([]RealTimeFrame, error) {
	if err := c.spectrumOnly("real-time spectrum"); err != nil {
		return nil, err
	}
	value, err := c.getJSON(ctx, "/1/Device/Spectrum/RTData")
	if err != nil {
		return nil, err
	}
	object, _ := value.(map[string]any)
	frames := make([]RealTimeFrame, 0, 4)
	for _, entry := range asSlice(object["rtdata"]) {
		item := asMap(entry)
		frame := RealTimeFrame{Time: str(item["t"])}
		for _, raw := range asSlice(item["ps"]) {
			sample := asMap(raw)
			freq, ok := num(sample["f"])
			if !ok {
				continue
			}
			channel := RealTimeChannel{FreqMHz: freq}
			channel.PowerDBmV, _ = signal(sample["p"])
			channel.MERdB, _ = signal(sample["m"])
			frame.Channels = append(frame.Channels, channel)
		}
		if len(frame.Channels) > 0 {
			frames = append(frames, frame)
		}
	}
	sort.SliceStable(frames, func(i, j int) bool { return frames[i].Time < frames[j].Time })
	return frames, nil
}

// Constellation reads the I/Q cloud for one analyser port. Inactive ports
// answer 200 with an empty constellation; that is not an error.
func (c *Client) Constellation(ctx context.Context, portID int) (*Constellation, error) {
	if err := c.spectrumOnly("constellation"); err != nil {
		return nil, err
	}
	if portID < 0 || portID > 31 {
		portID = 24
	}
	path := fmt.Sprintf("/1/Device/Spectrum/Constellation?portId=%d", portID)
	value, err := c.getJSON(ctx, path)
	if err != nil {
		return nil, err
	}
	object, _ := value.(map[string]any)
	result := &Constellation{PortID: portID, At: time.Now()}
	list := asSlice(object["constellation"])
	if len(list) == 0 {
		return result, nil
	}
	item := asMap(list[0])
	if item == nil {
		return result, nil
	}
	result.Modulation = str(item["modulation"])
	result.BandwidthMHz = parseMHz(str(item["bandwidth"]))
	result.PowerDBmV, _ = signal(item["power"])
	result.MERdB, _ = signal(item["mer"])
	if maximum, ok := num(item["max"]); ok {
		result.Max = int(maximum)
	}
	for _, raw := range asSlice(item["cons"]) {
		point := asMap(raw)
		i, ok := num(point["i"])
		if !ok {
			continue
		}
		q, ok := num(point["q"])
		if !ok {
			continue
		}
		result.Points = append(result.Points, IQPoint{I: i, Q: q})
	}
	return result, nil
}

// formatFreq renders a frequency the way the firmware's validator expects:
// "273" or "637.5", never "273.000". The handler's regex allows at most one
// non-zero fractional digit, so a three-decimal value is silently replaced by
// the 645/637.5/643.5 defaults and the response comes from the wrong band.
func formatFreq(value float64) string {
	if math.Abs(value-math.Round(value)) < 0.001 {
		return strconv.FormatFloat(math.Round(value), 'f', 0, 64)
	}
	text := strconv.FormatFloat(value, 'f', 1, 64)
	return strings.TrimSuffix(text, ".0")
}

// parseMHz turns "6.0000MHz" into 6. The firmware suffixes the unit but leaves
// the number as a string.
func parseMHz(text string) float64 {
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.ToLower(text), "mhz"))
	value, ok := num(trimmed)
	if !ok {
		return 0
	}
	return value
}
