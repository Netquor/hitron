package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

// minCardsWidth is the narrowest terminal that gets the big-number cards; below
// it the compact text summary is used instead.
const minCardsWidth = 84

type card struct {
	label  string
	status string // short colored word next to the label, may be empty
	value  string // text drawn in the big font
	unit   string
	paint  func(string) string
	sub    string
}

func padVisible(text string, width int) string {
	if w := lipgloss.Width(text); w < width {
		return text + strings.Repeat(" ", width-w)
	}
	return text
}

func statusWord(ok bool, good, bad string) string {
	if ok {
		return Green("● " + good)
	}
	return Red("● " + bad)
}

// countUncorrectable sums downstream uncorrectable and corrected codewords.
func countCodewords(s *hitron.Snapshot) (uncorrect, corrected uint64) {
	for _, ch := range s.DS {
		uncorrect += ch.Uncorrect
		corrected += ch.Corrected
	}
	for _, ch := range s.OFDM {
		uncorrect += ch.Uncorrect
		corrected += ch.Corrected
	}
	return uncorrect, corrected
}

func buildCards(s *hitron.Snapshot, h *History) []card {
	var cards []card
	if s.DSHasReadings() {
		ok, low, high := s.DSInSpec()
		avg, _ := s.DSAverage()
		cards = append(cards, card{
			label: "DS POWER", status: statusWord(ok, "in spec", "out of spec"),
			value: fmt.Sprintf("%+.1f", avg), unit: "dBmV", paint: specColor(-6, 15)(avg),
			sub: fmt.Sprintf("%+.1f to %+.1f", low, high),
		})
	}
	if s.USHasReadings() {
		ok, low, high := s.USInSpec()
		avg, _ := s.USAverage()
		cards = append(cards, card{
			label: "US POWER", status: statusWord(ok, "in spec", "out of spec"),
			value: fmt.Sprintf("%.1f", avg), unit: "dBmV", paint: specColor(35, 48)(avg),
			sub: fmt.Sprintf("%.1f to %.1f", low, high),
		})
	}
	if snr, ok := h.DSSNR.last(); ok {
		cards = append(cards, card{
			label: "DS SNR", value: fmt.Sprintf("%.1f", snr), unit: "dB",
			paint: floorColor(33)(snr), sub: "weakest channel",
		})
	}
	unc, cor := countCodewords(s)
	value, unit := fmt.Sprintf("%d", unc), "uncorrected"
	if unc > 99999 {
		value, unit = fmt.Sprintf("%d", unc/1000), "k uncorrected"
	}
	paint := Green
	status := statusWord(true, "clean", "")
	if unc > 0 {
		paint, status = Red, statusWord(false, "errors", "errors")
	}
	cards = append(cards, card{
		label: "CODEWORDS", status: status, value: value, unit: unit, paint: paint,
		sub: formatCount(cor) + " corrected",
	})
	return cards
}

func (c card) lines(width int) [5]string {
	label := Dim(c.label)
	if c.status != "" {
		label += "  " + c.status
	}
	big := bigText(c.value, c.paint)
	var out [5]string
	out[0] = padVisible(truncate(label, width), width)
	for i := 0; i < 3; i++ {
		line := big[i]
		if i == 1 && c.unit != "" {
			line += " " + MutedText(c.unit)
		}
		out[i+1] = padVisible(line, width)
	}
	out[4] = padVisible(truncate(MutedText(c.sub), width), width)
	return out
}

// Cards lays the headline readings out side by side, each with a three-row
// number. It returns nil when width is too narrow for them.
func Cards(s *hitron.Snapshot, h *History, width int) []string {
	if width < minCardsWidth || s == nil {
		return nil
	}
	cards := buildCards(s, h)
	if len(cards) == 0 {
		return nil
	}
	const gutter = 5 // " │ " plus a space either side of the divider
	colW := (width - 2 - gutter*(len(cards)-1)) / len(cards)
	divider := "  " + fg(CurrentTheme().Border, "│") + "  "
	rows := make([]string, 5)
	for _, c := range cards {
		ls := c.lines(colW)
		for i := range rows {
			if rows[i] != "" {
				rows[i] += divider
			}
			rows[i] += ls[i]
		}
	}
	for i := range rows {
		rows[i] = " " + rows[i]
	}
	return rows
}
