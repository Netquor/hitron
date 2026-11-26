package ui

import (
	"strings"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

func fecKeyDS(id string) string   { return "ds:" + strings.TrimSpace(id) }
func fecKeyOFDM(id string) string { return "ofdm:" + strings.TrimSpace(id) }

// fecBaseline captures the current corrected/uncorrectable counters per
// downstream channel (SC-QAM and OFDM).
func fecBaseline(s *hitron.Snapshot) map[string][2]uint64 {
	base := map[string][2]uint64{}
	for _, ch := range s.DS {
		base[fecKeyDS(ch.ID)] = [2]uint64{ch.Corrected, ch.Uncorrect}
	}
	for _, ch := range s.OFDM {
		base[fecKeyOFDM(ch.Index)] = [2]uint64{ch.Corrected, ch.Uncorrect}
	}
	return base
}

func sub(now, was uint64) uint64 {
	if now < was {
		// The counter went backwards, so the modem re-locked or rebooted;
		// everything it reports now happened after the baseline.
		return now
	}
	return now - was
}

// applyFECBaseline returns a copy of s whose downstream counters are the
// change since base. s itself is not modified.
func applyFECBaseline(s *hitron.Snapshot, base map[string][2]uint64) *hitron.Snapshot {
	out := *s
	out.DS = append([]hitron.Channel(nil), s.DS...)
	for i := range out.DS {
		if b, ok := base[fecKeyDS(out.DS[i].ID)]; ok {
			out.DS[i].Corrected = sub(out.DS[i].Corrected, b[0])
			out.DS[i].Uncorrect = sub(out.DS[i].Uncorrect, b[1])
		}
	}
	out.OFDM = append([]hitron.OFDMChannel(nil), s.OFDM...)
	for i := range out.OFDM {
		if b, ok := base[fecKeyOFDM(out.OFDM[i].Index)]; ok {
			out.OFDM[i].Corrected = sub(out.OFDM[i].Corrected, b[0])
			out.OFDM[i].Uncorrect = sub(out.OFDM[i].Uncorrect, b[1])
		}
	}
	return &out
}
