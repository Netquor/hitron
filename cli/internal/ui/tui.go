package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

// RunTUI draws a live report until the user quits or the context is
// cancelled. fetch is called once immediately and then on every tick.
func RunTUI(ctx context.Context, fetch func(context.Context) (*hitron.Snapshot, error), interval time.Duration, host string) error {
	if !IsTerminal() {
		return fmt.Errorf("stdin is not a terminal; use --once for a one-shot report")
	}
	if interval < time.Second {
		interval = time.Second
	}

	raw, err := EnterRaw()
	if err != nil {
		return err
	}
	defer raw.Restore()

	fmt.Fprint(os.Stdout, "\x1b[?1049h\x1b[?25l")
	defer fmt.Fprint(os.Stdout, "\x1b[?25h\x1b[?1049l")

	keys := make(chan byte, 8)
	go func() {
		for {
			key, err := ReadKey()
			if err != nil {
				close(keys)
				return
			}
			select {
			case keys <- key:
			case <-ctx.Done():
				return
			}
		}
	}()

	var snapshot *hitron.Snapshot
	var fetchErr error
	history := NewHistory()
	refresh := func() {
		next, err := fetch(ctx)
		if err != nil {
			fetchErr = err
			return
		}
		snapshot = next
		history.Push(next)
		fetchErr = nil
	}

	refresh()
	auto := true
	showHelp := false
	showGraphs := true
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	draw := func() {
		width, height := Size()
		var lines []string
		if showHelp {
			lines = HelpOverlay(width)
			lines = append(lines, "", "  "+Dim("h or q to close"))
		} else if snapshot == nil {
			lines = []string{
				"",
				"  " + Bold("hitron-cli") + "  " + Dim(host),
				"",
				"  " + Red("no data yet") + "  " + Dim(interval.String()),
			}
			if fetchErr != nil {
				lines = append(lines, "  "+Red("error: ")+fetchErr.Error())
			}
			lines = append(lines, "", "  "+Dim("r retry  q quit"))
		} else {
			errorText := ""
			if fetchErr != nil {
				errorText = fetchErr.Error()
			}
			graphs := history
			if !showGraphs {
				graphs = nil
			}
			lines = Frame(snapshot, graphs, width, height, interval, auto, errorText)
		}
		var builder strings.Builder
		builder.WriteString("\x1b[H")
		for _, line := range lines {
			builder.WriteString(line)
			builder.WriteString("\x1b[K\r\n")
		}
		builder.WriteString("\x1b[J")
		fmt.Fprint(os.Stdout, builder.String())
	}

	for {
		draw()
		select {
		case <-ctx.Done():
			return nil
		case key, ok := <-keys:
			if !ok {
				return nil
			}
			switch key {
			case 'q', 'Q', 3: // 3 is Ctrl-C in raw mode
				return nil
			case 'r', 'R', ' ':
				refresh()
			case 'a', 'A':
				auto = !auto
			case 'g', 'G':
				showGraphs = !showGraphs
			case 'h', 'H', '?':
				showHelp = !showHelp
			}
		case <-ticker.C:
			if auto {
				refresh()
			}
		}
	}
}
