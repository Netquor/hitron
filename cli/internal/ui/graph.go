package ui

import (
	"fmt"
	"strings"
)

// blocks are the eight Unicode lower-block glyphs, lowest to highest.
var blocks = []rune("▁▂▃▄▅▆▇█")

// sparkline renders vals as one row of block glyphs, scaled between lo and hi
// and colored per point by color. A short history fills left to right so the
// graph sits next to its label; once it reaches width it scrolls, newest
// reading at the right edge.
//
// Scaling against a fixed range (the spec envelope) instead of the data's own
// min/max is deliberate: auto-zooming makes a perfectly stable line look like
// violent noise, which is the wrong signal on a monitoring screen.
func sparkline(vals []float64, width int, lo, hi float64, color func(v float64) func(string) string) string {
	if width <= 0 {
		return ""
	}
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	var b strings.Builder
	span := hi - lo
	for _, v := range vals {
		idx := 0
		if span > 0 {
			frac := (v - lo) / span
			if frac < 0 {
				frac = 0
			}
			if frac > 1 {
				frac = 1
			}
			idx = int(frac*float64(len(blocks)-1) + 0.5)
		}
		glyph := string(blocks[idx])
		if color != nil {
			glyph = color(v)(glyph)
		}
		b.WriteString(glyph)
	}
	b.WriteString(strings.Repeat(" ", width-len(vals)))
	return b.String()
}

func minMax(vals []float64) (lo, hi float64) {
	lo, hi = vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return lo, hi
}

// specColor colors a point green inside [low, high], amber within 1.5 of an
// edge, red outside — the same bands powerCell uses in the tables, so the
// graph and the numbers never disagree.
func specColor(low, high float64) func(v float64) func(string) string {
	return func(v float64) func(string) string {
		switch {
		case v < low || v > high:
			return Red
		case v <= low+1.5 || v >= high-1.5:
			return Yellow
		default:
			return Green
		}
	}
}

// floorColor colors a point against a one-sided minimum (SNR): red below the
// floor, amber within 3 dB above it, green otherwise.
func floorColor(floor float64) func(v float64) func(string) string {
	return func(v float64) func(string) string {
		switch {
		case v < floor:
			return Red
		case v < floor+3:
			return Yellow
		default:
			return Green
		}
	}
}

// graphRow is one labelled trend line:
//
//	label   sparkline   now  (min..max)
func graphRow(label string, ser *series, width int, lo, hi float64, color func(float64) func(string) string, unit, format string) string {
	vals := ser.values()
	if len(vals) == 0 {
		return "  " + Dim(pad(label, 14)) + " " + Dim("no readings")
	}
	now, _ := ser.last()
	wmin, wmax := minMax(vals)
	trailer := fmt.Sprintf(format+"%s  %s", now, unit, Dim(fmt.Sprintf("("+format+".."+format+")", wmin, wmax)))
	return "  " + Dim(pad(label, 14)) + " " + sparkline(vals, width, lo, hi, color) + "  " + trailer
}

// Graphs renders the trend block. Fixed ranges: downstream power uses the
// spec envelope padded a little; SNR is plotted 20..45 dB, the range cable
// plant actually lives in; upstream is plotted 28..52 so readings below the 35 floor stay visible.
func Graphs(h *History, width int) []string {
	// Reserve room for the label (14), gutters, and the numeric trailer.
	graphWidth := width - 14 - 4 - 28
	if graphWidth > 60 {
		graphWidth = 60
	}
	if graphWidth < 10 {
		graphWidth = 10
	}
	return []string{
		"  " + Bold("Trends") + "  " + Dim(fmt.Sprintf("%d polls", h.Samples)),
		graphRow("DS power", &h.DSPower, graphWidth, -8, 17, specColor(-6, 15), " dBmV", "%+.1f"),
		graphRow("DS SNR (min)", &h.DSSNR, graphWidth, 20, 45, floorColor(33), " dB", "%.1f"),
		graphRow("US power", &h.USPower, graphWidth, 28, 52, specColor(35, 48), " dBmV", "%+.1f"),
	}
}
