package ui

import "strings"

// bigGlyphs is a three-row heavy box-drawing digit font used for the headline
// readings. Terminal fonts are the user's own, so this is drawn from standard
// box-drawing characters that every monospace font carries.
var bigGlyphs = map[rune][3]string{
	'0': {"┏━┓", "┃ ┃", "┗━┛"},
	'1': {"╺┓ ", " ┃ ", "╺┻╸"},
	'2': {"╺━┓", "┏━┛", "┗━╸"},
	'3': {"╺━┓", " ━┫", "╺━┛"},
	'4': {"╻ ╻", "┗━┫", "  ╹"},
	'5': {"┏━╸", "┗━┓", "╺━┛"},
	'6': {"┏━╸", "┣━┓", "┗━┛"},
	'7': {"╺━┓", "  ┃", "  ╹"},
	'8': {"┏━┓", "┣━┫", "┗━┛"},
	'9': {"┏━┓", "┗━┫", "╺━┛"},
	'.': {" ", " ", "▪"},
	'+': {"   ", "╺╋╸", "   "},
	'-': {"   ", "╺━╸", "   "},
	' ': {" ", " ", " "},
}

// bigText renders s in the big font as three rows. Unknown runes are blank.
func bigText(s string, paint func(string) string) [3]string {
	var rows [3]strings.Builder
	for i, r := range s {
		g, ok := bigGlyphs[r]
		if !ok {
			g = bigGlyphs[' ']
		}
		for row := 0; row < 3; row++ {
			if i > 0 {
				rows[row].WriteString(" ")
			}
			rows[row].WriteString(g[row])
		}
	}
	var out [3]string
	for row := range rows {
		out[row] = rows[row].String()
		if paint != nil {
			out[row] = paint(out[row])
		}
	}
	return out
}
