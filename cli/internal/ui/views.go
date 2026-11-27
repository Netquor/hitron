package ui

import (
	"fmt"
	"math"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

type viewMode int

const (
	viewSummary viewMode = iota
	viewSpectrum
	viewConstellation
)

// nextChannel picks the analyzer channel delta steps away from centerMHz. An
// exact match advances from that channel; a fine-tuned center lands on the
// first channel in the direction of travel, wrapping at both ends.
func nextChannel(channels []hitron.SpectrumChannel, centerMHz float64, delta int) (hitron.SpectrumChannel, bool) {
	if len(channels) == 0 {
		return hitron.SpectrumChannel{}, false
	}
	index := -1
	for i, channel := range channels {
		if math.Abs(channel.FreqMHz-centerMHz) < 0.5 {
			index = i
			break
		}
	}
	switch {
	case index >= 0:
		index = (index + delta + len(channels)) % len(channels)
	case delta > 0:
		index = 0
		for i, channel := range channels {
			if channel.FreqMHz > centerMHz {
				index = i
				break
			}
		}
	default:
		index = len(channels) - 1
		for i := len(channels) - 1; i >= 0; i-- {
			if channels[i].FreqMHz < centerMHz {
				index = i
				break
			}
		}
	}
	return channels[index], true
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func joinedError(primary, secondary error) error {
	switch {
	case primary == nil:
		return secondary
	case secondary == nil:
		return primary
	default:
		return fmt.Errorf("%v; %v", primary, secondary)
	}
}
