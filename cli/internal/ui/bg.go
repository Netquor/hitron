package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Dark top and bottom colours of the vertical background gradient, per theme.
var bgColors = map[string][2]string{
	"neon":      {"#07081A", "#1B0A30"},
	"synthwave": {"#14072A", "#33104F"},
	"dracula":   {"#191A23", "#2C2745"},
	"nord":      {"#1C212B", "#2E3647"},
	"gruvbox":   {"#1A1B1C", "#33302C"},
	"matrix":    {"#000802", "#00260C"},
}

// TrueColor reports whether the terminal advertises 24-bit colour. The
// gradient is skipped elsewhere because 256 colours would band visibly.
func TrueColor() bool {
	c := strings.ToLower(os.Getenv("COLORTERM"))
	return Color && (strings.Contains(c, "truecolor") || strings.Contains(c, "24bit"))
}

// paintBackground fills the whole screen with the theme's gradient. Styled
// segments end in a reset, which would drop the background, so the escape is
// re-issued after every reset.
func paintBackground(view string, width, height int) string {
	cols, ok := bgColors[CurrentTheme().Name]
	if !ok || width <= 0 || height <= 0 {
		return view
	}
	lines := strings.Split(view, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	r1, g1, b1 := parseHex(cols[0])
	r2, g2, b2 := parseHex(cols[1])
	for i, line := range lines {
		t := 0.0
		if height > 1 {
			t = float64(i) / float64(height-1)
		}
		esc := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", lerp(r1, r2, t), lerp(g1, g2, t), lerp(b1, b2, t))
		line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+esc)
		line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+esc)
		if pad := width - lipgloss.Width(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		lines[i] = esc + line + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}
