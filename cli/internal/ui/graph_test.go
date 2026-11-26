package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
	if got != "▁█   " {
		t.Fatalf("sparkline = %q, want %q", got, "▁█   ")
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

func TestViewWithAndWithoutGraphs(t *testing.T) {
	prev := Color
	Color = false
	defer func() { Color = prev }()

	s := snap(4, 38, 40)
	h := NewHistory()
	h.Push(s)
	m := model{snap: s, history: h, showGraphs: true, auto: true, interval: 5e9, width: 120, height: 60}
	with := m.View()
	m.showGraphs = false
	without := m.View()
	if !strings.Contains(with, "Trends") || !strings.Contains(with, "DS SNR") {
		t.Fatalf("graphs missing from view:\n%s", with)
	}
	if strings.Contains(without, "Trends") {
		t.Fatalf("graphs shown when disabled")
	}
}

func TestUpdateKeysAndRefreshGuard(t *testing.T) {
	m := model{history: NewHistory(), auto: true, showGraphs: true, interval: 5e9, loading: true}
	// a tick while a poll is in flight must not start another
	next, _ := m.Update(tickMsg{})
	if !next.(model).loading {
		t.Fatalf("loading cleared by tick")
	}
	next, _ = next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if next.(model).showGraphs {
		t.Fatalf("g did not toggle graphs")
	}
	// a failed poll keeps the previous snapshot and surfaces the error
	good := snap(4, 38, 40)
	next, _ = next.Update(snapMsg{snap: good})
	next, _ = next.Update(snapMsg{err: fmt.Errorf("boom")})
	got := next.(model)
	if got.snap != good || got.err == nil || got.history.Samples != 1 {
		t.Fatalf("failed poll should keep data, set err, not record: %+v", got)
	}
}

func TestChannelsTypedAndHidesDisabledOFDMA(t *testing.T) {
	prev := Color
	Color = false
	defer func() { Color = prev }()

	s := &hitron.Snapshot{
		DS:   []hitron.Channel{{ID: "2", Frequency: 567e6, Power: 3, HasPower: true}, {ID: "1", Frequency: 561e6, Power: 3, HasPower: true}},
		OFDM: []hitron.OFDMChannel{{Index: "4", Frequency: 750e6, FFTType: "4K"}},
		US:   []hitron.Channel{{ID: "1", Frequency: 20.4e6, Power: 32, HasPower: true, Mode: "ATDMA"}},
		OFDMA: []hitron.OFDMAChannel{
			{Index: "0", State: "OPERATE", Frequency: 22e6, Power16: 34, HasPower16: true},
			{Index: "2", State: "DISABLED", Disabled: true},
			{Index: "3", State: "DISABLED", Disabled: true},
		},
	}
	var b strings.Builder
	Channels(&b, s)
	out := b.String()
	for _, want := range []string{"3 channels · 2 SC-QAM · 1 OFDM", "2 channels · 1 SC-QAM · 1 OFDMA · 2 disabled hidden", "OFDMA", "+40.0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DISABLED") {
		t.Fatalf("disabled OFDMA rows should not be shown:\n%s", out)
	}
	if strings.Index(out, "561.0 MHz") > strings.Index(out, "567.0 MHz") {
		t.Fatalf("SC-QAM channels should be sorted by channel ID:\n%s", out)
	}
}

func TestPaintBackgroundKeepsWidthAndSurvivesResets(t *testing.T) {
	out := paintBackground("a\x1b[31mred\x1b[0m tail\nb", 10, 3)
	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != 10 {
			t.Errorf("line %d width %d, want 10", i, w)
		}
	}
	if strings.Count(lines[0], "\x1b[48;2;") < 2 {
		t.Error("background must be re-issued after a reset")
	}
}
