package ui

import (
	"strings"
	"testing"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

func snap(dsPower, dsSNR, usPower float64) *hitron.Snapshot {
	return &hitron.Snapshot{
		DS: []hitron.Channel{
			{Power: dsPower, HasPower: true, SNR: dsSNR, HasSNR: true},
			{Power: dsPower + 2, HasPower: true, SNR: dsSNR + 4, HasSNR: true},
		},
		US: []hitron.Channel{{Power: usPower, HasPower: true}},
	}
}

func TestHistoryAggregates(t *testing.T) {
	h := NewHistory()
	h.Push(snap(4, 38, 40))
	if v, _ := h.DSPower.last(); v != 5 { // mean of 4 and 6
		t.Fatalf("mean DS power = %v, want 5", v)
	}
	if v, _ := h.DSSNR.last(); v != 38 { // worst of 38 and 42
		t.Fatalf("worst SNR = %v, want 38", v)
	}
	if v, _ := h.USPower.last(); v != 40 {
		t.Fatalf("US power = %v, want 40", v)
	}
}

func TestHistoryIgnoresNilAndMissing(t *testing.T) {
	h := NewHistory()
	h.Push(nil)
	if h.Samples != 0 {
		t.Fatalf("nil snapshot counted as a sample")
	}
	h.Push(&hitron.Snapshot{DS: []hitron.Channel{{Power: 3, HasPower: true}}})
	if len(h.DSSNR.values()) != 0 {
		t.Fatalf("SNR series should stay empty when no SNR reported")
	}
	if len(h.DSPower.values()) != 1 {
		t.Fatalf("power series should still record")
	}
}

func TestHistoryCapped(t *testing.T) {
	h := NewHistory()
	for i := 0; i < historyCap+50; i++ {
		h.Push(snap(4, 38, 40))
	}
	if got := len(h.DSPower.values()); got != historyCap {
		t.Fatalf("series len = %d, want cap %d", got, historyCap)
	}
}

func TestSparklineRightAlignedAndScaled(t *testing.T) {
	prev := Color
	Color = false
	defer func() { Color = prev }()

	got := sparkline([]float64{0, 10}, 5, 0, 10, nil)
	if got != "   ▁█" {
		t.Fatalf("sparkline = %q, want %q", got, "   ▁█")
	}
	// clamps out-of-range values instead of indexing past the glyph table
	got = sparkline([]float64{-99, 99}, 2, 0, 10, nil)
	if got != "▁█" {
		t.Fatalf("clamped sparkline = %q", got)
	}
	// more samples than width keeps the newest
	got = sparkline([]float64{0, 0, 0, 10}, 2, 0, 10, nil)
	if got != "▁█" {
		t.Fatalf("trimmed sparkline = %q", got)
	}
}

func TestFrameWithAndWithoutGraphs(t *testing.T) {
	prev := Color
	Color = false
	defer func() { Color = prev }()

	s := snap(4, 38, 40)
	h := NewHistory()
	h.Push(s)
	with := strings.Join(Frame(s, h, 120, 50, 5e9, true, ""), "\n")
	without := strings.Join(Frame(s, nil, 120, 50, 5e9, true, ""), "\n")
	if !strings.Contains(with, "Trends") || !strings.Contains(with, "DS SNR") {
		t.Fatalf("graphs missing from frame:\n%s", with)
	}
	if strings.Contains(without, "Trends") {
		t.Fatalf("graphs shown when disabled")
	}
}
