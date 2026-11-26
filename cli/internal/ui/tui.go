package ui

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

// LiveSource is everything the TUI can read. Any field may be nil; the view
// that needs it then shows why it is unavailable instead of failing the whole
// report. Spectrum reads exist only on the CODA6X family and are separate from
// Snapshot so the summary never waits for the slow analyser.
type LiveSource struct {
	Snapshot      func(context.Context) (*hitron.Snapshot, error)
	Channels      func(context.Context) ([]hitron.SpectrumChannel, error)
	Sweep         func(context.Context, float64) (*hitron.SpectrumSweep, error)
	RealTime      func(context.Context) ([]hitron.RealTimeFrame, error)
	Constellation func(context.Context, int) (*hitron.Constellation, error)
	// ResetFEC is a write. The TUI only calls it when the user presses x, and
	// only on families that expose the counters.
	ResetFEC func(context.Context) (hitron.FECReset, error)
}

type viewMode int

const (
	viewSummary viewMode = iota
	viewSpectrum
	viewConstellation
)

// RunTUI draws a live report until the user quits or the context is
// cancelled. The active view is refreshed once immediately and then on every
// tick.
func RunTUI(ctx context.Context, src LiveSource, interval time.Duration, host string) error {
	if !IsTerminal() {
		return fmt.Errorf("stdin is not a terminal; use --once for a one-shot report")
	}
	if interval < time.Second {
		interval = time.Second
	}

	raw, err := EnterRaw()
	if err != nil {
		return err
	}
	defer raw.Restore()

	fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?25l")
	defer fmt.Fprint(os.Stdout, "\x1b[?25h\x1b[?1049l")

	keys := make(chan byte, 8)
	go func() {
		for {
			key, err := ReadKey()
			if err != nil {
				close(keys)
				return
			}
			select {
			case keys <- key:
			case <-ctx.Done():
				return
			}
		}
	}()

	var (
		mode        = viewSummary
		snapshot    *hitron.Snapshot
		snapshotErr error

		channels       []hitron.SpectrumChannel
		channelsErr    error
		channelsLoaded bool

		centerMHz float64
		sweep     *hitron.SpectrumSweep
		sweepErr  error
		realTime  []hitron.RealTimeFrame

		portID  int = -1
		cons    *hitron.Constellation
		consErr error
	)

	refreshSnapshot := func() {
		if src.Snapshot == nil {
			snapshotErr = fmt.Errorf("snapshot reader is unavailable")
			return
		}
		next, err := src.Snapshot(ctx)
		if err != nil {
			snapshotErr = err
			return
		}
		snapshot = next
		snapshotErr = nil
	}

	loadChannels := func() {
		channelsLoaded = true
		if src.Channels == nil {
			channelsErr = fmt.Errorf("spectrum reader is unavailable on this device")
			return
		}
		list, err := src.Channels(ctx)
		if err != nil {
			channelsErr = err
			return
		}
		channels = list
		channelsErr = nil
		if centerMHz == 0 && len(list) > 0 {
			centerMHz = list[0].FreqMHz
		}
		if portID < 0 && len(list) > 0 {
			portID = list[0].PortID
		}
	}

	refreshSpectrum := func() {
		if !channelsLoaded {
			loadChannels()
		}
		if centerMHz == 0 {
			centerMHz = 645
		}
		if src.Sweep == nil {
			sweepErr = fmt.Errorf("spectrum reader is unavailable on this device")
			return
		}
		next, err := src.Sweep(ctx, centerMHz)
		if err != nil {
			sweepErr = err
			return
		}
		sweep = next
		sweepErr = nil
		if src.RealTime != nil {
			if frames, err := src.RealTime(ctx); err == nil {
				realTime = frames
			}
		}
	}

	refreshConstellation := func() {
		if !channelsLoaded {
			loadChannels()
		}
		if portID < 0 {
			portID = 24
		}
		if src.Constellation == nil {
			consErr = fmt.Errorf("constellation reader is unavailable on this device")
			return
		}
		next, err := src.Constellation(ctx, portID)
		if err != nil {
			consErr = err
			return
		}
		cons = next
		consErr = nil
	}

	refreshActive := func() {
		switch mode {
		case viewSpectrum:
			refreshSpectrum()
		case viewConstellation:
			refreshConstellation()
		default:
			refreshSnapshot()
		}
	}

	// stepChannel moves the shared selection to the previous/next analyser
	// channel. From a fine-tuned centre it lands on the next channel in the
	// direction of travel; without a frequency list it falls back to 6 MHz
	// jumps.
	stepChannel := func(delta int) {
		if len(channels) == 0 {
			centerMHz = math.Max(49, math.Min(1788, centerMHz+float64(delta)*6))
			return
		}
		next, ok := nextChannel(channels, centerMHz, delta)
		if !ok {
			return
		}
		centerMHz = next.FreqMHz
		portID = next.PortID
	}

	refreshSnapshot()
	auto := true
	showHelp := false
	fecStatus := ""
	fecFailed := false
	fecRunning := false
	type fecOutcome struct {
		result hitron.FECReset
		err    error
	}
	fecDone := make(chan fecOutcome, 1)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	header := func() string {
		state := "paused"
		if auto {
			state = "auto " + interval.String()
		}
		title := Bold(host)
		if snapshot != nil {
			title = Title(snapshot)
		}
		return Bold("hitron-cli") + "  " + title + "  " + Dim(time.Now().Format("15:04:05")+"  "+state)
	}

	draw := func() {
		width, height := Size()
		var lines []string
		if showHelp {
			lines = HelpOverlay(width)
			lines = append(lines, "", "  "+Dim("h closes this help · q quits"))
		} else {
			switch mode {
			case viewSpectrum:
				lines = SpectrumFrame(header(), centerMHz, sweep, channels, realTime, width, height, errorText(joinedError(sweepErr, channelsErr)))
			case viewConstellation:
				lines = ConstellationFrame(header(), cons, channels, width, height, errorText(joinedError(consErr, channelsErr)))
			default:
				if snapshot == nil {
					lines = []string{
						"",
						"  " + Bold("hitron-cli") + "  " + Dim(host),
						"",
						"  " + Red("no data yet") + "  " + Dim(interval.String()),
					}
					if snapshotErr != nil {
						lines = append(lines, "  "+Red("error: ")+snapshotErr.Error())
					}
					lines = append(lines, "", "  "+Dim("r retry  q quit"))
				} else {
					lines = Frame(snapshot, width, height, interval, auto, errorText(snapshotErr))
				}
			}
		}
		if !showHelp && fecStatus != "" {
			text := "  " + Dim(fecStatus)
			if fecFailed {
				text = "  " + Red(fecStatus)
			}
			lines = withStatus(lines, text)
		}
		var builder strings.Builder
		builder.WriteString("\x1b[H")
		for _, line := range lines {
			builder.WriteString(line)
			builder.WriteString("\x1b[K\r\n")
		}
		builder.WriteString("\x1b[J")
		fmt.Fprint(os.Stdout, builder.String())
	}

	for {
		draw()
		select {
		case <-ctx.Done():
			return nil
		case key, ok := <-keys:
			if !ok {
				return nil
			}
			switch key {
			case 'q', 'Q', 3: // 3 is Ctrl-C in raw mode
				return nil
			case 'r', 'R', ' ':
				refreshActive()
			case 'a', 'A':
				auto = !auto
			case 'x', 'X':
				if src.ResetFEC == nil {
					fecStatus = "FEC reset is unavailable on this device"
					fecFailed = true
					break
				}
				if fecRunning {
					break
				}
				// The OFDM stop is retried, so the whole flow can take a few
				// seconds. Run it off the draw loop and keep the screen live.
				fecRunning = true
				fecFailed = false
				fecStatus = "FEC reset: clearing 3.0 and OFDM counters…"
				go func() {
					result, err := src.ResetFEC(ctx)
					select {
					case fecDone <- fecOutcome{result: result, err: err}:
					case <-ctx.Done():
					}
				}()
			case 'h', 'H', '?':
				showHelp = !showHelp
			case '1':
				mode = viewSummary
				refreshSnapshot()
			case '2', 's', 'S':
				mode = viewSpectrum
				refreshSpectrum()
			case '3', 'c', 'C':
				mode = viewConstellation
				refreshConstellation()
			case '[', ']', 'p', 'P':
				delta := -1
				if key == ']' || key == 'p' || key == 'P' {
					delta = 1
				}
				stepChannel(delta)
				refreshActive()
			case ',', '.':
				delta := -1.0
				if key == '.' {
					delta = 1
				}
				centerMHz = math.Max(49, math.Min(1788, centerMHz+delta))
				refreshActive()
			}
		case outcome := <-fecDone:
			fecRunning = false
			if outcome.err != nil {
				fecStatus = "FEC reset failed: " + outcome.err.Error()
				fecFailed = true
				break
			}
			fecStatus = outcome.result.String() + " · " + time.Now().Format("15:04:05")
			fecFailed = false
			refreshActive()
		case <-ticker.C:
			if auto && !fecRunning {
				refreshActive()
			}
		}
	}
}

// nextChannel picks the analyser channel delta steps away from centerMHz. An
// exact match advances from that channel; a fine-tuned centre lands on the
// first channel in the direction of travel, wrapping at both ends.
func nextChannel(channels []hitron.SpectrumChannel, centerMHz float64, delta int) (hitron.SpectrumChannel, bool) {
	if len(channels) == 0 {
		return hitron.SpectrumChannel{}, false
	}
	index := -1
	for i, channel := range channels {
		if math.Abs(channel.FreqMHz-centerMHz) < 0.5 {
			index = i
			break
		}
	}
	switch {
	case index >= 0:
		index = (index + delta + len(channels)) % len(channels)
	case delta > 0:
		index = 0
		for i, channel := range channels {
			if channel.FreqMHz > centerMHz {
				index = i
				break
			}
		}
	default:
		index = len(channels) - 1
		for i := len(channels) - 1; i >= 0; i-- {
			if channels[i].FreqMHz < centerMHz {
				index = i
				break
			}
		}
	}
	return channels[index], true
}

// withStatus puts a one-line status where the frame already has its blank line
// above the footer, so the status does not push the footer off the screen. If
// that slot is taken it appends instead.
func withStatus(lines []string, text string) []string {
	if text == "" {
		return lines
	}
	if n := len(lines); n >= 2 && strings.TrimSpace(lines[n-2]) == "" {
		lines[n-2] = text
		return lines
	}
	return append(lines, text)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func joinedError(primary, secondary error) error {
	switch {
	case primary == nil:
		return secondary
	case secondary == nil:
		return primary
	default:
		return fmt.Errorf("%v; %v", primary, secondary)
	}
}
