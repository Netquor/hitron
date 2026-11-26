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
