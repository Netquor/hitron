package ui

import "github.com/Netquor/hitron/cli/internal/hitron"

// historyCap is the number of samples each series retains. At the default
// 5s refresh that is ~50 minutes of trend; the window is trimmed to the
// terminal width at draw time, so a large cap costs only a little memory.
const historyCap = 600

// series is a bounded ring of readings, oldest first.
type series struct {
	buf []float64
}

func (s *series) push(v float64) {
	s.buf = append(s.buf, v)
	if len(s.buf) > historyCap {
		s.buf = s.buf[len(s.buf)-historyCap:]
	}
}

func (s *series) values() []float64 { return s.buf }

func (s *series) last() (float64, bool) {
	if len(s.buf) == 0 {
		return 0, false
	}
	return s.buf[len(s.buf)-1], true
}

// History accumulates one aggregate reading per refresh so the TUI can graph
// signal trends over time. It is presentation state owned by the TUI loop;
// the one-shot report paths never touch it.
type History struct {
	DSPower series // mean downstream power, dBmV (matches the header average)
	DSSNR   series // worst (minimum) downstream SNR, dB
	USPower series // mean upstream power, dBmV (matches the header average)
	Samples int
}

// NewHistory returns an empty history.
func NewHistory() *History { return &History{} }

// Push folds one snapshot into each series. A missing reading is skipped for
// that series only, so a device that reports power but no SNR still graphs
// what it does report. A nil snapshot (a failed refresh) is ignored, which
// keeps the x-axis a record of successful polls.
func (h *History) Push(s *hitron.Snapshot) {
	if s == nil {
		return
	}
	h.Samples++
	if v, ok := s.DSAverage(); ok {
		h.DSPower.push(v)
	}
	if v, ok := worstDSSNR(s); ok {
		h.DSSNR.push(v)
	}
	if v, ok := s.USAverage(); ok {
		h.USPower.push(v)
	}
}

// worstDSSNR tracks the lowest SNR across SC-QAM and OFDM downstream channels,
// since the weakest channel is what degrades first.
func worstDSSNR(s *hitron.Snapshot) (float64, bool) {
	min, ok := 0.0, false
	for _, ch := range s.DS {
		if ch.HasSNR && (!ok || ch.SNR < min) {
			min, ok = ch.SNR, true
		}
	}
	for _, ch := range s.OFDM {
		if ch.HasSNR && (!ok || ch.SNR < min) {
			min, ok = ch.SNR, true
		}
	}
	return min, ok
}
