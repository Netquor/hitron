package ui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

func TestNextChannelWrapsAndFollowsTheDirectionOfTravel(t *testing.T) {
	channels := []hitron.SpectrumChannel{
		{FreqMHz: 267, PortID: 0},
		{FreqMHz: 273, PortID: 26},
		{FreqMHz: 279, PortID: 27},
	}
	cases := []struct {
		center float64
		delta  int
		want   float64
	}{
		{267, 1, 273},  // exact match steps forward
		{267, -1, 279}, // and wraps backwards
		{273, 1, 279},
		{279, 1, 267},   // wraps forwards
		{268, 1, 273},   // a fine-tuned center lands on the next channel
		{268, -1, 267},  // or the previous one
		{300, 1, 267},   // past the last channel, wrap
		{260, -1, 279},  // before the first channel, wrap
		{268.4, 1, 273}, // inside the match tolerance
	}
	for _, tc := range cases {
		got, ok := nextChannel(channels, tc.center, tc.delta)
		if !ok {
			t.Fatalf("nextChannel(%v, %d) reported no channel", tc.center, tc.delta)
		}
		if got.FreqMHz != tc.want {
			t.Fatalf("nextChannel(%v, %d) = %v, want %v", tc.center, tc.delta, got.FreqMHz, tc.want)
		}
	}

	if _, ok := nextChannel(nil, 267, 1); ok {
		t.Fatal("nextChannel on an empty list should report false")
	}
}

func TestNextChannelCarriesTheAnalyzerPort(t *testing.T) {
	channels := []hitron.SpectrumChannel{
		{FreqMHz: 267, PortID: 0},
		{FreqMHz: 291, PortID: 25},
	}
	got, _ := nextChannel(channels, 267, 1)
	if got.PortID != 25 {
		t.Fatalf("port = %d, want 25", got.PortID)
	}
}

func TestZRunsTheDeviceFECReset(t *testing.T) {
	var called bool
	m := model{
		snap: &hitron.Snapshot{},
		src: TUIOptions{ResetFEC: func(context.Context) (hitron.FECReset, error) {
			called = true
			return hitron.FECReset{DOCSISBusy: 1, DOCSISCleared: 1}, nil
		}},
	}
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = next.(model)
	if called {
		t.Fatal("the reset must run off the draw loop")
	}
	if !m.fecRunning || cmd == nil {
		t.Fatalf("z did not start a reset: running=%v cmd=%v", m.fecRunning, cmd)
	}
	if m.fecBase != nil {
		t.Fatal("a device reset must not fall back to the display baseline")
	}
	outcome, ok := cmd().(fecMsg)
	if !ok || outcome.err != nil || !called {
		t.Fatalf("outcome = %+v, called = %v", outcome, called)
	}
}

func TestZFallsBackToTheDisplayWhenThereIsNoEndpoint(t *testing.T) {
	m := model{snap: &hitron.Snapshot{DS: []hitron.Channel{{ID: "1", Corrected: 3}}}}
	next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = next.(model)
	if cmd != nil || m.fecRunning {
		t.Fatal("without an endpoint z must not write")
	}
	if m.fecBase == nil {
		t.Fatal("z should still zero the displayed counts")
	}
}

func TestFECMsgReportsWhatCleared(t *testing.T) {
	m := model{fetch: func(context.Context) (*hitron.Snapshot, error) {
		return &hitron.Snapshot{}, nil
	}}
	out, cmd := m.Update(fecMsg{result: hitron.FECReset{DOCSISBusy: 1, DOCSISCleared: 1, OFDMBusy: 2, OFDMCleared: 1, OFDMNotZero: []string{"4"}}})
	m = out.(model)
	if m.fecRunning || m.fecFailed {
		t.Fatalf("state = running %v failed %v", m.fecRunning, m.fecFailed)
	}
	want := "FEC reset: 3.0 1/1 cleared, OFDM 1/2 cleared (OFDM 4 still counting)"
	if m.fecStatus != want {
		t.Fatalf("status = %q, want %q", m.fecStatus, want)
	}
	if cmd == nil || !m.loading {
		t.Fatal("a successful reset should re-read the snapshot")
	}
}

func TestZAndXAreReadOnlyOnTheCoda4x8x(t *testing.T) {
	for _, key := range []rune{'z', 'x'} {
		m := model{snap: &hitron.Snapshot{Kind: hitron.KindCoda4x8x}}
		next, cmd := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		m = next.(model)
		if cmd != nil || m.fecRunning || !m.fecFailed {
			t.Fatalf("%c: cmd=%v running=%v failed=%v", key, cmd, m.fecRunning, m.fecFailed)
		}
		if !strings.Contains(m.fecStatus, "read-only") || strings.Contains(m.fecStatus, "clearing") {
			t.Fatalf("%c: status = %q", key, m.fecStatus)
		}
	}
}
