package ui

import (
	"testing"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

func TestFECBaseline(t *testing.T) {
	s := &hitron.Snapshot{
		DS:   []hitron.Channel{{ID: "1", Corrected: 14, Uncorrect: 640, HasCodewords: true}},
		OFDM: []hitron.OFDMChannel{{Index: "0", Corrected: 1000, Uncorrect: 2, HasCounters: true}},
	}
	base := fecBaseline(s)
	later := &hitron.Snapshot{
		DS:   []hitron.Channel{{ID: "1", Corrected: 20, Uncorrect: 640, HasCodewords: true}},
		OFDM: []hitron.OFDMChannel{{Index: "0", Corrected: 5, Uncorrect: 0, HasCounters: true}},
	}
	got := applyFECBaseline(later, base)
	if got.DS[0].Corrected != 6 || got.DS[0].Uncorrect != 0 {
		t.Errorf("ds = %+v", got.DS[0])
	}
	if got.OFDM[0].Corrected != 5 || got.OFDM[0].Uncorrect != 0 {
		t.Errorf("ofdm after counter reset = %+v", got.OFDM[0])
	}
	if later.DS[0].Corrected != 20 {
		t.Error("input snapshot was modified")
	}
}
