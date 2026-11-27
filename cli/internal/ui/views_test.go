package ui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

func press(m model, key string) (model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(model), cmd
}

func newViewModel() model {
	return model{
		ctx: context.Background(), history: NewHistory(), width: 100, height: 30, portID: -1,
		snap: &hitron.Snapshot{}, auto: true,
		src: TUIOptions{
			Channels: func(context.Context) ([]hitron.SpectrumChannel, error) {
				return []hitron.SpectrumChannel{{PortID: 5, FreqMHz: 100}, {PortID: 6, FreqMHz: 200}}, nil
			},
			Sweep: func(context.Context, float64) (*hitron.SpectrumSweep, error) {
				return &hitron.SpectrumSweep{}, nil
			},
			Constellation: func(context.Context, int) (*hitron.Constellation, error) {
				return &hitron.Constellation{}, nil
			},
		},
	}
}

func TestViewKeysSwitchModeAndQueueBehindInFlightRead(t *testing.T) {
	m := newViewModel()
	m, cmd := press(m, "2")
	if m.mode != viewSpectrum || cmd == nil || !m.loading {
		t.Fatalf("2: mode=%v loading=%v cmd=%v", m.mode, m.loading, cmd != nil)
	}
	// A second request while the first is in flight must wait: the analyzer
	// is one shared tuner.
	m, cmd = press(m, "3")
	if m.mode != viewConstellation || cmd != nil || !m.dirty {
		t.Fatalf("3 while loading: mode=%v dirty=%v cmd=%v", m.mode, m.dirty, cmd != nil)
	}
	// The first capture lands; the queued one runs.
	next, cmd := m.Update(specMsg{channelsTried: true, channels: []hitron.SpectrumChannel{{PortID: 5, FreqMHz: 100}, {PortID: 6, FreqMHz: 200}}, center: 100})
	m = next.(model)
	if cmd == nil || !m.loading || m.dirty {
		t.Fatalf("after read: loading=%v dirty=%v cmd=%v", m.loading, m.dirty, cmd != nil)
	}
	if m.centerMHz != 100 || m.portID != 5 || !m.channelsLoaded {
		t.Fatalf("channel state not applied: %+v", m)
	}
}

func TestChannelStepAndTune(t *testing.T) {
	m := newViewModel()
	m.mode = viewSpectrum
	m.channels = []hitron.SpectrumChannel{{PortID: 5, FreqMHz: 100}, {PortID: 6, FreqMHz: 200}}
	m.channelsLoaded, m.centerMHz, m.portID = true, 100, 5
	m, _ = press(m, "]")
	if m.centerMHz != 200 || m.portID != 6 {
		t.Fatalf("] -> center %v port %v", m.centerMHz, m.portID)
	}
	m.loading = false
	m, _ = press(m, ".")
	if m.centerMHz != 201 {
		t.Fatalf(". -> center %v", m.centerMHz)
	}
	// Channel and tune keys do nothing on the summary.
	m.mode, m.loading = viewSummary, false
	before := m.centerMHz
	m, _ = press(m, "]")
	if m.centerMHz != before {
		t.Fatal("] must not move the center on the summary view")
	}
}
