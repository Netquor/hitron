package ui

import (
	"context"
	"errors"
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var errSpectrumUnavailable = errors.New("spectrum reader is unavailable on this device")

// activeCmd starts a read for whichever view is showing.
func (m model) activeCmd() tea.Cmd {
	switch m.mode {
	case viewSpectrum:
		return m.spectrumCmd()
	case viewConstellation:
		return m.constellationCmd()
	default:
		return m.fetchCmd()
	}
}

// issue starts a read for the active view and marks one as in flight. The
// analyser is one shared hardware tuner, so only one capture runs at a time.
// A FEC clear owns the device until it finishes, so reads wait for it.
func (m model) issue() (model, tea.Cmd) {
	if m.fecRunning {
		return m, nil
	}
	m.loading = true
	return m, m.activeCmd()
}

// refreshOrQueue issues a read now, or queues one behind the read in flight.
func (m model) refreshOrQueue() (model, tea.Cmd) {
	if m.loading {
		m.dirty = true
		return m, nil
	}
	return m.issue()
}

// afterRead clears the in-flight flag and runs any queued read.
func (m model) afterRead() (model, tea.Cmd) {
	m.loading = false
	if m.dirty {
		m.dirty = false
		return m.issue()
	}
	return m, nil
}

func loadChannels(ctx context.Context, o TUIOptions) (msgChannels specMsg) {
	msgChannels.channelsTried = true
	if o.Channels == nil {
		msgChannels.channelsErr = errSpectrumUnavailable
		return msgChannels
	}
	msgChannels.channels, msgChannels.channelsErr = o.Channels(ctx)
	return msgChannels
}

func (m model) spectrumCmd() tea.Cmd {
	ctx, src := m.ctx, m.src
	needChannels, center := !m.channelsLoaded, m.centerMHz
	return func() tea.Msg {
		var msg specMsg
		if needChannels {
			loaded := loadChannels(ctx, src)
			msg.channels, msg.channelsTried, msg.channelsErr = loaded.channels, true, loaded.channelsErr
		}
		if center == 0 {
			center = 645
			if len(msg.channels) > 0 {
				center = msg.channels[0].FreqMHz
			}
		}
		msg.center = center
		if src.Sweep == nil {
			msg.sweepErr = errSpectrumUnavailable
			return msg
		}
		msg.sweep, msg.sweepErr = src.Sweep(ctx, center)
		if msg.sweepErr == nil && src.RealTime != nil {
			if frames, err := src.RealTime(ctx); err == nil {
				msg.frames = frames
			}
		}
		return msg
	}
}

func (m model) constellationCmd() tea.Cmd {
	ctx, src := m.ctx, m.src
	needChannels, port := !m.channelsLoaded, m.portID
	return func() tea.Msg {
		var msg consMsg
		if needChannels {
			loaded := loadChannels(ctx, src)
			msg.channels, msg.channelsTried, msg.channelsErr = loaded.channels, true, loaded.channelsErr
		}
		if port < 0 {
			port = 24
			if len(msg.channels) > 0 {
				port = msg.channels[0].PortID
			}
		}
		msg.port = port
		if src.Constellation == nil {
			msg.err = errors.New("constellation reader is unavailable on this device")
			return msg
		}
		msg.cons, msg.err = src.Constellation(ctx, port)
		return msg
	}
}

func (m model) applySpec(msg specMsg) model {
	if msg.channelsTried {
		m.channelsLoaded = true
		m.channelsErr = msg.channelsErr
		if msg.channelsErr == nil {
			m.channels = msg.channels
			if m.portID < 0 && len(msg.channels) > 0 {
				m.portID = msg.channels[0].PortID
			}
		}
	}
	if m.centerMHz == 0 {
		m.centerMHz = msg.center
	}
	m.sweepErr = msg.sweepErr
	if msg.sweepErr == nil {
		m.sweep = msg.sweep
		if msg.frames != nil {
			m.realTime = msg.frames
		}
	}
	return m
}

func (m model) applyCons(msg consMsg) model {
	if msg.channelsTried {
		m.channelsLoaded = true
		m.channelsErr = msg.channelsErr
		if msg.channelsErr == nil {
			m.channels = msg.channels
		}
	}
	if m.portID < 0 {
		m.portID = msg.port
	}
	m.consErr = msg.err
	if msg.err == nil {
		m.cons = msg.cons
	}
	return m
}

// stepChannel moves the shared selection to the previous or next analyser
// channel. Without a channel list it falls back to 6 MHz jumps.
func (m model) stepChannel(delta int) model {
	if len(m.channels) == 0 {
		m.centerMHz = math.Max(49, math.Min(1788, m.centerMHz+float64(delta)*6))
		return m
	}
	if next, ok := nextChannel(m.channels, m.centerMHz, delta); ok {
		m.centerMHz, m.portID = next.FreqMHz, next.PortID
	}
	return m
}

func (m model) spectrumView() string {
	lines := SpectrumFrame(m.topLine(), m.centerMHz, m.sweep, m.channels, m.realTime,
		m.width, m.height, errorText(joinedError(m.sweepErr, m.channelsErr)))
	return strings.Join(lines, "\n")
}

func (m model) constellationView() string {
	lines := ConstellationFrame(m.topLine(), m.cons, m.channels,
		m.width, m.height, errorText(joinedError(m.consErr, m.channelsErr)))
	return strings.Join(lines, "\n")
}
