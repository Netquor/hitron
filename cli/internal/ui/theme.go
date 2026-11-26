package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme is a named palette. Every color in the UI comes from the active one:
// Good/Warn/Bad carry meaning (in range / near the edge / out of range), the
// rest is decoration.
type Theme struct {
	Name    string
	Accent  string // titles, key caps, start of the wordmark gradient
	Accent2 string // end of the wordmark gradient
	Border  string // panel borders
	Good    string
	Warn    string
	Bad     string
	Muted   string
}

// Themes is the built-in list, in the order the `t` key cycles through them.
var Themes = []Theme{
	{"neon", "#00E5FF", "#FF4DFF", "#4F5BD5", "#3DFF9A", "#FFD84D", "#FF4D6D", "#8089B3"},
	{"synthwave", "#FF71CE", "#01CDFE", "#B967FF", "#05FFA1", "#FFFB96", "#FF3864", "#9A8CC2"},
	{"dracula", "#BD93F9", "#FF79C6", "#6272A4", "#50FA7B", "#F1FA8C", "#FF5555", "#7D86B1"},
	{"nord", "#88C0D0", "#B48EAD", "#5E81AC", "#A3BE8C", "#EBCB8B", "#BF616A", "#7B88A1"},
	{"gruvbox", "#83A598", "#D3869B", "#7C6F64", "#B8BB26", "#FABD2F", "#FB4934", "#928374"},
	{"matrix", "#00FF41", "#008F11", "#006B1A", "#00FF41", "#C8FF00", "#FF3B3B", "#3F7F4F"},
}

var activeTheme = 0

// CurrentTheme returns the active palette.
func CurrentTheme() Theme { return Themes[activeTheme] }

// ThemeNames lists the built-in theme names.
func ThemeNames() []string {
	names := make([]string, len(Themes))
	for i, t := range Themes {
		names[i] = t.Name
	}
	return names
}

// SetTheme selects a theme by name and reports whether it exists.
func SetTheme(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, t := range Themes {
		if t.Name == name {
			activeTheme = i
			return true
		}
	}
	return false
}

// NextTheme advances to the next palette and returns it.
func NextTheme() Theme {
	activeTheme = (activeTheme + 1) % len(Themes)
	return Themes[activeTheme]
}

// themeFile is where the chosen theme is remembered between runs.
func themeFile() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "hitron-cli", "theme")
}

// SavedTheme returns the theme remembered from a previous run, or "".
func SavedTheme() string {
	path := themeFile()
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SaveTheme remembers name for the next run. It is best effort: a read-only
// home directory must not break the dashboard.
func SaveTheme(name string) {
	path := themeFile()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(name+"\n"), 0o644)
}

// fg paints text in a hex color, or returns it untouched when color is off.
func fg(hex, text string) string {
	if !Color || text == "" {
		return text
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render(text)
}

// AccentText colors text with the theme accent.
func AccentText(text string) string { return fg(CurrentTheme().Accent, text) }

// AccentBold is AccentText in bold, used for titles and key caps.
func AccentBold(text string) string {
	if !Color || text == "" {
		return text
	}
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(CurrentTheme().Accent)).Render(text)
}

// MutedText colors secondary text with the theme's muted tone.
func MutedText(text string) string { return fg(CurrentTheme().Muted, text) }

// pill is a filled label such as IN SPEC. Without color it degrades to the
// plain lowercase words the one-shot report always used.
func pill(text, bg string) string {
	if !Color {
		return strings.ToLower(text)
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(bg)).
		Foreground(lipgloss.Color("#0B0B0F")).
		Bold(true).
		Padding(0, 1).
		Render(text)
}

// PillGood, PillWarn and PillBad are status pills in the theme's meaning colors.
func PillGood(text string) string { return pill(text, CurrentTheme().Good) }
func PillWarn(text string) string { return pill(text, CurrentTheme().Warn) }
func PillBad(text string) string  { return pill(text, CurrentTheme().Bad) }

// Gradient paints text with a left-to-right blend between two hex colors.
func Gradient(text, from, to string) string {
	if !Color || text == "" {
		return text
	}
	runes := []rune(text)
	r1, g1, b1 := parseHex(from)
	r2, g2, b2 := parseHex(to)
	var out strings.Builder
	for i, r := range runes {
		t := 0.0
		if len(runes) > 1 {
			t = float64(i) / float64(len(runes)-1)
		}
		hex := fmt.Sprintf("#%02X%02X%02X", lerp(r1, r2, t), lerp(g1, g2, t), lerp(b1, b2, t))
		out.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(hex)).Render(string(r)))
	}
	return out.String()
}

func lerp(a, b int, t float64) int { return int(float64(a) + (float64(b)-float64(a))*t + 0.5) }

func parseHex(hex string) (r, g, b int) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 255, 255, 255
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 255, 255, 255
	}
	return int(v >> 16 & 0xFF), int(v >> 8 & 0xFF), int(v & 0xFF)
}
