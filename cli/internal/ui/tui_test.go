package ui

import (
	"testing"

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
		{268, 1, 273},   // a fine-tuned centre lands on the next channel
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

func TestNextChannelCarriesTheAnalyserPort(t *testing.T) {
	channels := []hitron.SpectrumChannel{
		{FreqMHz: 267, PortID: 0},
		{FreqMHz: 291, PortID: 25},
	}
	got, _ := nextChannel(channels, 267, 1)
	if got.PortID != 25 {
		t.Fatalf("port = %d, want 25", got.PortID)
	}
}

func TestWithStatusKeepsTheFooterInPlace(t *testing.T) {
	lines := []string{"header", "body", "", "  q quit  h help"}
	got := withStatus(lines, "  FEC reset: 3.0 1/1 cleared, OFDM 2/3 cleared")
	if len(got) != len(lines) {
		t.Fatalf("status changed the frame height: %d -> %d", len(lines), len(got))
	}
	if got[2] != "  FEC reset: 3.0 1/1 cleared, OFDM 2/3 cleared" {
		t.Fatalf("status line = %q", got[2])
	}
	if got[3] != lines[3] {
		t.Fatalf("footer moved: %q", got[3])
	}
}

func TestWithStatusAppendsWhenTheSlotIsTaken(t *testing.T) {
	lines := []string{"header", "body", "  channels 267 [273]", "  q quit"}
	got := withStatus(lines, "  FEC reset failed: boom")
	if len(got) != len(lines)+1 || got[len(got)-1] != "  FEC reset failed: boom" {
		t.Fatalf("status = %v", got)
	}
}
