package ui

import (
	"math"
	"strings"
	"testing"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

func withoutColor(t *testing.T) {
	t.Helper()
	previous := Color
	Color = false
	t.Cleanup(func() { Color = previous })
}

func sampleSweep() *hitron.SpectrumSweep {
	points := make([]hitron.SpectrumPoint, 0, 300)
	for index := 0; index < 300; index++ {
		freq := 270 + float64(index)*0.02
		power := 4 + 3*math.Sin(float64(index)/20)
		points = append(points, hitron.SpectrumPoint{FreqMHz: freq, PowerDBmV: power})
	}
	return &hitron.SpectrumSweep{CenterMHz: 273, FromMHz: 270, ToMHz: 275.98, Points: points}
}

func isBraille(char rune) bool { return char >= 0x2800 && char <= 0x28ff }

func TestSpectrumChartPutsThePeakInTheTopRow(t *testing.T) {
	withoutColor(t)
	points := []hitron.SpectrumPoint{
		{FreqMHz: 270, PowerDBmV: -10},
		{FreqMHz: 276, PowerDBmV: 10},
	}
	lines := spectrumChart(points, 20, 6, false)
	if len(lines) != 6 {
		t.Fatalf("lines = %d", len(lines))
	}

	top := []rune(lines[0])
	if len(top) != 22 {
		t.Fatalf("top row is %d cells: %q", len(top), lines[0])
	}
	last := -1
	for index, char := range top {
		if char != ' ' {
			last = index
		}
		if char != ' ' && index < 10 {
			t.Fatalf("the top row should only hold the rising edge, found a dot at %d: %q", index, lines[0])
		}
	}
	if last != 21 {
		t.Fatalf("the peak should reach the last column, got %d: %q", last, lines[0])
	}
	if !isBraille(top[21]) {
		t.Fatalf("the peak is missing from the top row: %q", lines[0])
	}
	if bits := top[21] - 0x2800; bits&0x1b == 0 {
		t.Fatalf("the peak should use a dot in the top half of its cell: %q", lines[0])
	}

	bottom := []rune(lines[len(lines)-1])
	first := -1
	for index, char := range bottom {
		if char != ' ' {
			first = index
			break
		}
	}
	if first != 2 {
		t.Fatalf("the floor sample should sit in the first column, got %d: %q", first, lines[len(lines)-1])
	}
	if !isBraille(bottom[2]) {
		t.Fatalf("the floor sample is missing: %q", lines[len(lines)-1])
	}
}

func TestSpectrumChartKeepsEveryRowTheSameWidth(t *testing.T) {
	withoutColor(t)
	lines := spectrumChart(sampleSweep().Points, 40, 10, true)
	// 10 traced rows, then the axis rule and the frequency labels.
	if len(lines) != 12 {
		t.Fatalf("lines = %d", len(lines))
	}
	for _, line := range lines[:10] {
		if got := len([]rune(line)); got != 2+7+40 {
			t.Fatalf("row is %d cells, want %d: %q", got, 2+7+40, line)
		}
	}
}

func TestAxisLabelsLineUpWithTheChart(t *testing.T) {
	label := axisLabels(270, 276, 20)
	if len([]rune(label)) != 29 {
		t.Fatalf("label width = %d: %q", len([]rune(label)), label)
	}
	for _, want := range []string{"270.0", "273.0 MHz", "276.0"} {
		if !strings.Contains(label, want) {
			t.Fatalf("label %q is missing %q", label, want)
		}
	}
}

func TestConstellationChartPlotsBothQuadrants(t *testing.T) {
	points := []hitron.IQPoint{
		{I: -5, Q: 5}, {I: 5, Q: 5}, {I: -5, Q: -5}, {I: 5, Q: -5},
	}
	lines := constellationChart(points, 40, 20)
	if len(lines) != 20+2 {
		t.Fatalf("lines = %d", len(lines))
	}
	firstPointRow, lastPointRow := -1, -1
	for index, line := range lines[:20] {
		if strings.ContainsAny(line, "·•●") {
			if firstPointRow < 0 {
				firstPointRow = index
			}
			lastPointRow = index
		}
	}
	if firstPointRow < 0 || lastPointRow < 0 {
		t.Fatalf("no points plotted:\n%s", strings.Join(lines, "\n"))
	}
	if firstPointRow >= 10 || lastPointRow <= 9 {
		t.Fatalf("points should sit on both sides of the zero axis: first=%d last=%d", firstPointRow, lastPointRow)
	}
}

func TestSpectrumFrameShowsStatsAndFitsHeight(t *testing.T) {
	withoutColor(t)
	lines := SpectrumFrame("header", 273, sampleSweep(),
		[]hitron.SpectrumChannel{{FreqMHz: 267, PortID: 0}, {FreqMHz: 273, PortID: 26}},
		[]hitron.RealTimeFrame{{Time: "11:37", Channels: []hitron.RealTimeChannel{{FreqMHz: 267, PowerDBmV: 4.4, MERdB: 43.4}}}},
		100, 24, "")
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"spectrum", "273.0 MHz", "300 pts", "min", "max", "avg", "RTData 11:37", "[273]"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("frame is missing %q:\n%s", want, joined)
		}
	}
	if len(lines) > 24 {
		t.Fatalf("frame has %d lines for a 24-row terminal", len(lines))
	}
}

func TestSpectrumFrameExplainsAnEmptyCapture(t *testing.T) {
	withoutColor(t)
	sweep := &hitron.SpectrumSweep{CenterMHz: 645, FromMHz: 642, ToMHz: 648}
	lines := SpectrumFrame("header", 645, sweep, nil, nil, 100, 30, "")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "no samples") {
		t.Fatalf("empty capture is not explained:\n%s", joined)
	}
}

func TestConstellationFrameExplainsEmptyPort(t *testing.T) {
	withoutColor(t)
	lines := ConstellationFrame("header", &hitron.Constellation{PortID: 7}, nil, 100, 30, "")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "no constellation points") {
		t.Fatalf("empty port is not explained:\n%s", joined)
	}
}

func TestConstellationFrameShowsMetadata(t *testing.T) {
	withoutColor(t)
	cons := &hitron.Constellation{
		PortID: 25, Modulation: "256 QAM", BandwidthMHz: 6, PowerDBmV: 3.7, MERdB: 44.6,
		Points: []hitron.IQPoint{{I: 1, Q: 1}, {I: -1, Q: -1}},
	}
	lines := ConstellationFrame("header", cons, []hitron.SpectrumChannel{{FreqMHz: 291, PortID: 25}}, 100, 30, "")
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"constellation", "port 25", "291.0 MHz", "256 QAM", "MER 44.6 dB", "+3.7 dBmV"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("frame is missing %q:\n%s", want, joined)
		}
	}
}

func TestPackTokensWrapsInsteadOfOverflowing(t *testing.T) {
	tokens := []string{"267 +4.4/43", "291 +3.6/45", "273 +4.2/45", "279 +4.0/43"}
	lines := packTokens(tokens, 30)
	if len(lines) < 2 {
		t.Fatalf("tokens did not wrap: %v", lines)
	}
	for _, line := range lines {
		if len(line) > 30 {
			t.Fatalf("line is %d wide: %q", len(line), line)
		}
	}
}

func TestChannelStripMarksTheSelection(t *testing.T) {
	channels := []hitron.SpectrumChannel{{FreqMHz: 267, PortID: 0}, {FreqMHz: 291, PortID: 25}}
	strip := channelStrip(channels, 291)
	if !strings.Contains(strip, "[291]") || !strings.Contains(strip, "267") {
		t.Fatalf("strip = %q", strip)
	}
}
