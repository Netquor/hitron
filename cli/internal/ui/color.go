package ui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Color is off for --no-color, NO_COLOR, and non-terminal output.
var Color = true

func paint(code, text string) string {
	if !Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func Bold(text string) string { return paint("1", text) }
func Dim(text string) string  { return paint("2", text) }

// Green, Yellow and Red carry meaning (good, near the edge, bad) and Cyan is
// the accent. All four follow the active theme.
func Green(text string) string  { return fg(CurrentTheme().Good, text) }
func Red(text string) string    { return fg(CurrentTheme().Bad, text) }
func Yellow(text string) string { return fg(CurrentTheme().Warn, text) }
func Cyan(text string) string   { return fg(CurrentTheme().Accent, text) }

// Power colors a dBmV reading against the Midco envelope. The two outer bands
// are amber so a drifting channel is visible before it fails the check.
func Power(value float64, inSpec bool, nearEdge bool) string {
	text := fmt.Sprintf("%+.1f", value)
	if !Color {
		return text
	}
	switch {
	case !inSpec:
		return Red(text)
	case nearEdge:
		return Yellow(text)
	default:
		return Green(text)
	}
}

// Status renders a textual state with a color, never an emoji.
func Status(ok bool) string {
	if ok {
		return Green("ok")
	}
	return Red("fail")
}

// initially keeps gofmt from folding the imports away when Color is unused in
// a trimmed build.
var _ = os.Stdout

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func visibleLen(text string) int {
	return len([]rune(ansiPattern.ReplaceAllString(text, "")))
}

func truncate(text string, width int) string {
	plain := []rune(ansiPattern.ReplaceAllString(text, ""))
	if width <= 0 {
		return ""
	}
	if len(plain) <= width {
		return text
	}
	if width == 1 {
		return string(plain[:1])
	}
	return string(plain[:width-1]) + "…"
}

func pad(text string, width int) string {
	if width <= 0 {
		return text
	}
	visible := visibleLen(text)
	if visible >= width {
		return truncate(text, width)
	}
	return text + strings.Repeat(" ", width-visible)
}

func columns(header []string, rows [][]string, widths []int) []string {
	lines := make([]string, 0, len(rows)+2)
	head := make([]string, len(header))
	for index, title := range header {
		head[index] = pad(title, widths[index])
	}
	lines = append(lines, Dim(strings.TrimRight(strings.Join(head, "  "), " ")))
	rule := make([]string, len(widths))
	for index, width := range widths {
		rule[index] = strings.Repeat("─", width)
	}
	lines = append(lines, Dim(strings.TrimRight(strings.Join(rule, "  "), " ")))
	for _, row := range rows {
		cells := make([]string, len(row))
		for index, cell := range row {
			if index >= len(widths) {
				break
			}
			cells[index] = pad(cell, widths[index])
		}
		lines = append(lines, strings.TrimRight(strings.Join(cells, "  "), " "))
	}
	return lines
}
