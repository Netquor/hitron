package hitron

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"testing"
	"time"
)

func fakeSpectrum() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/1/Device/Spectrum/FreqList":
			fmt.Fprint(w, `{"errCode":"000","errMsg":"","frequency":[
				{"f":291000,"ri":25},{"f":267000,"ri":0},{"f":309000,"ri":31}
			]}`)
		case "/1/Device/Spectrum/RTData":
			fmt.Fprint(w, `{"errCode":"000","errMsg":"","rtdata":[
				{"t":"11:37:19","ps":[{"f":267,"p":4.47,"m":43.38}]},
				{"t":"11:37:10","ps":[{"f":267,"p":4.38,"m":43.38},{"f":291,"p":3.59,"m":44.63}]}
			]}`)
		case "/1/Device/Spectrum/FreqPower":
			query := r.URL.Query()
			center, _ := strconv.ParseFloat(query.Get("centFreq"), 64)
			from, _ := strconv.ParseFloat(query.Get("fromFreq"), 64)
			to, _ := strconv.ParseFloat(query.Get("toFreq"), 64)
			if to-from < 5.9 || to-from > 6.1 {
				http.Error(w, "expected a 6 MHz window", http.StatusBadRequest)
				return
			}
			fmt.Fprintf(w, `{"errCode":"000","errMsg":"","spectrum":[
				{"f":%.3f,"p":-10.5},{"f":%.3f,"p":4.25},{"f":%.3f,"p":7.75}
			]}`, from, center, to)
		case "/1/Device/Spectrum/Constellation":
			if r.URL.Query().Get("portId") != "0" {
				fmt.Fprint(w, `{"errCode":"000","errMsg":"","constellation":[]}`)
				return
			}
			fmt.Fprint(w, `{"errCode":"000","errMsg":"","constellation":[{
				"bandwidth":"6.0000MHz","power":"4.43dBmV","mer":"43.38dB",
				"modulation":"256 QAM","max":25,
				"cons":[{"i":-1.5,"q":2.5},{"i":3.5,"q":-4.5}]
			}]}`)
		default:
			http.NotFound(w, r)
		}
	}
}

func spectrumClient(t *testing.T) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(fakeSpectrum())
	client, err := NewClient(server.URL, Credentials{}, 3*time.Second)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	client.Kind = KindCoda6X
	return client, server.Close
}

func TestSpectrumChannels(t *testing.T) {
	client, closeServer := spectrumClient(t)
	defer closeServer()

	channels, err := client.SpectrumChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 3 {
		t.Fatalf("channels = %+v", channels)
	}
	if channels[0].FreqMHz != 267 || channels[0].PortID != 0 {
		t.Fatalf("not sorted by frequency: %+v", channels)
	}
	if channels[1].FreqMHz != 291 || channels[1].PortID != 25 {
		t.Fatalf("kHz was not converted to MHz: %+v", channels[1])
	}
}

func TestRealTimeSpectrumSortsFrames(t *testing.T) {
	client, closeServer := spectrumClient(t)
	defer closeServer()

	frames, err := client.RealTimeSpectrum(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || frames[0].Time != "11:37:10" {
		t.Fatalf("frames = %+v", frames)
	}
	if len(frames[0].Channels) != 2 {
		t.Fatalf("channels = %+v", frames[0].Channels)
	}
	if frames[0].Channels[1].MERdB != 44.63 {
		t.Fatalf("MER = %+v", frames[0].Channels[1])
	}
}

func TestSpectrumSweepRequestsSixMHzWindow(t *testing.T) {
	client, closeServer := spectrumClient(t)
	defer closeServer()

	sweep, err := client.SpectrumSweep(context.Background(), 291)
	if err != nil {
		t.Fatal(err)
	}
	if sweep.CenterMHz != 291 {
		t.Fatalf("center = %f", sweep.CenterMHz)
	}
	if len(sweep.Points) != 3 {
		t.Fatalf("points = %+v", sweep.Points)
	}
	if sweep.FromMHz != 288 || sweep.ToMHz != 294 {
		t.Fatalf("actual window = %.1f..%.1f", sweep.FromMHz, sweep.ToMHz)
	}
	if sweep.Points[1].PowerDBmV != 4.25 {
		t.Fatalf("power = %+v", sweep.Points[1])
	}
}

func TestSpectrumSweepClampsCenter(t *testing.T) {
	client, closeServer := spectrumClient(t)
	defer closeServer()

	sweep, err := client.SpectrumSweep(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if sweep.CenterMHz != 49 {
		t.Fatalf("center = %f, want clamp to 49", sweep.CenterMHz)
	}
}

func TestConstellationDecodesCloud(t *testing.T) {
	client, closeServer := spectrumClient(t)
	defer closeServer()

	cons, err := client.Constellation(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if cons.PortID != 0 || cons.Modulation != "256 QAM" {
		t.Fatalf("constellation = %+v", cons)
	}
	if cons.BandwidthMHz != 6 || cons.PowerDBmV != 4.43 || cons.MERdB != 43.38 {
		t.Fatalf("metadata = %+v", cons)
	}
	if len(cons.Points) != 2 || cons.Points[0].I != -1.5 || cons.Points[1].Q != -4.5 {
		t.Fatalf("points = %+v", cons.Points)
	}
}

func TestConstellationEmptyIsNotAnError(t *testing.T) {
	client, closeServer := spectrumClient(t)
	defer closeServer()

	cons, err := client.Constellation(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if cons == nil || len(cons.Points) != 0 || cons.PortID != 1 {
		t.Fatalf("constellation = %+v", cons)
	}
}

func TestSpectrumUnsupportedOnOtherFamilies(t *testing.T) {
	client, _ := NewClient("127.0.0.1", Credentials{}, time.Second)
	client.Kind = KindCoda57

	if _, err := client.SpectrumChannels(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("channels err = %v", err)
	}
	if _, err := client.SpectrumSweep(context.Background(), 645); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("sweep err = %v", err)
	}
	if _, err := client.RealTimeSpectrum(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("rtdata err = %v", err)
	}
	if _, err := client.Constellation(context.Background(), 0); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("constellation err = %v", err)
	}
}

func TestFormatFreqMatchesFirmwareValidator(t *testing.T) {
	// SpectrumInfo.php accepts at most one non-zero fractional digit. A value
	// that misses this regex silently falls back to 645/637.5/643.5.
	valid := regexp.MustCompile(`^[1-9][0-9][0-9]?[0-9]?\.?[1-9]?$`)
	for _, value := range []float64{49, 273, 637.5, 642.9, 1788, 270.96, 291.5} {
		text := formatFreq(value)
		if !valid.MatchString(text) {
			t.Fatalf("formatFreq(%v) = %q does not satisfy the firmware validator", value, text)
		}
	}
	if got := formatFreq(273); got != "273" {
		t.Fatalf("formatFreq(273) = %q", got)
	}
	if got := formatFreq(637.5); got != "637.5" {
		t.Fatalf("formatFreq(637.5) = %q", got)
	}
}

func TestSpectrumSweepSendsValidatorFriendlyFrequencies(t *testing.T) {
	var query url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		fmt.Fprint(w, `{"errCode":"000","spectrum":[]}`)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{}, 2*time.Second)
	client.Kind = KindCoda6X
	if _, err := client.SpectrumSweep(context.Background(), 291.5); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"centFreq": "291.5", "fromFreq": "288.5", "toFreq": "294.5"} {
		if got := query.Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestParseMHz(t *testing.T) {
	cases := map[string]float64{
		"6.0000MHz": 6,
		"6.4 MHz":   6.4,
		"":          0,
		"n/a":       0,
	}
	for input, want := range cases {
		if got := parseMHz(input); got != want {
			t.Fatalf("parseMHz(%q) = %v, want %v", input, got, want)
		}
	}
}
