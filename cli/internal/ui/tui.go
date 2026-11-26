package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

// FetchFunc polls the device once.
type FetchFunc func(context.Context) (*hitron.Snapshot, error)

var (
	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)
	headerStyle = lipgloss.NewStyle().Bold(true)
	mutedStyle  = lipgloss.NewStyle().Faint(true)
	panelTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
)

type tickMsg struct{}

type snapMsg struct {
	snap *hitron.Snapshot
	err  error
	at   time.Time
}

type model struct {
	ctx      context.Context
	fetch    FetchFunc
	interval time.Duration
	host     string

	snap    *hitron.Snapshot
	err     error
	history *History
	lastOK  time.Time

	loading    bool
	auto       bool
	showGraphs bool
	showHelp   bool

	width, height int
	scroll        int
}

// RunTUI runs the live dashboard until the user quits or ctx is cancelled.
// fetch is called once immediately and then every interval.
func RunTUI(ctx context.Context, fetch FetchFunc, interval time.Duration, host string) error {
	if !IsTerminal() {
		return fmt.Errorf("stdin is not a terminal; use --once for a one-shot report")
	}
	if interval < time.Second {
		interval = time.Second
	}
	m := model{
		ctx: ctx, fetch: fetch, interval: interval, host: host,
		history: NewHistory(), auto: true, showGraphs: true,
		width: 100, height: 32, loading: true,
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if err == tea.ErrProgramKilled && ctx.Err() != nil {
		return nil
	}
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.fetchCmd(), m.tickCmd())
}

func (m model) fetchCmd() tea.Cmd {
	return func() tea.Msg {
		snap, err := m.fetch(m.ctx)
		return snapMsg{snap: snap, err: err, at: time.Now()}
	}
}

func (m model) tickCmd() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case snapMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.snap, m.err, m.lastOK = msg.snap, nil, msg.at
		m.history.Push(msg.snap)
	case tickMsg:
		// Always re-arm; only poll when auto-refresh is on and nothing is in
		// flight, so a slow modem can't stack up requests.
		if m.auto && !m.loading {
			m.loading = true
			return m, tea.Batch(m.fetchCmd(), m.tickCmd())
		}
		return m, m.tickCmd()
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			if m.showHelp && msg.String() == "esc" {
				m.showHelp = false
				return m, nil
			}
			return m, tea.Quit
		case "r", " ":
			if !m.loading {
				m.loading = true
				return m, m.fetchCmd()
			}
		case "a":
			m.auto = !m.auto
		case "g":
			m.showGraphs = !m.showGraphs
		case "h", "?":
			m.showHelp = !m.showHelp
		case "j", "down":
			m.scroll++
		case "k", "up":
			if m.scroll > 0 {
				m.scroll--
			}
		case "home":
			m.scroll = 0
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.showHelp {
		return m.helpView()
	}
	if m.snap == nil {
		return m.waitingView()
	}

	header := m.headerLines()
	footer := m.footerLine()
	bodyHeight := m.height - len(header) - 1
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	body := strings.Split(m.bodyView(), "\n")
	maxScroll := len(body) - bodyHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scroll > maxScroll {
		m.scroll = maxScroll
	}
	end := m.scroll + bodyHeight
	if end > len(body) {
		end = len(body)
	}
	visible := body[m.scroll:end]
	for len(visible) < bodyHeight {
		visible = append(visible, "")
	}

	out := append(append([]string{}, header...), visible...)
	if maxScroll > 0 {
		footer += mutedStyle.Render(fmt.Sprintf("   j/k scroll %d/%d", m.scroll, maxScroll))
	}
	return strings.Join(append(out, footer), "\n")
}

func (m model) headerLines() []string {
	state := "paused"
	if m.auto {
		state = "auto " + m.interval.String()
	}
	if m.loading {
		state += " · polling"
	}
	lines := []string{
		truncate(headerStyle.Render("hitron-cli")+"  "+Title(m.snap)+"  "+
			mutedStyle.Render(time.Now().Format("15:04:05")+"  "+state), m.width),
	}
	if m.err != nil {
		age := ""
		if !m.lastOK.IsZero() {
			age = " (showing data from " + m.lastOK.Format("15:04:05") + ")"
		}
		lines = append(lines, truncate(Red("error: ")+m.err.Error()+Dim(age), m.width))
	} else {
		lines = append(lines, specLine(m.snap)...)
	}
	return lines
}

func (m model) footerLine() string {
	keys := "q quit  r refresh  a auto  g graphs  j/k scroll  h help"
	if m.err != nil {
		keys = strings.Replace(keys, "r refresh", "r retry", 1)
	}
	return " " + mutedStyle.Render(keys)
}

func (m model) bodyView() string {
	inner := m.width - 4 // border (2) + padding (2)
	if inner < 20 {
		inner = 20
	}
	panel := func(title string, lines []string) string {
		clipped := make([]string, 0, len(lines)+1)
		clipped = append(clipped, panelTitle.Render(title))
		for _, l := range lines {
			clipped = append(clipped, truncate(l, inner))
		}
		return panelStyle.Width(m.width - 2).Render(strings.Join(clipped, "\n"))
	}
	capture := func(fn func(*strings.Builder)) []string {
		var b strings.Builder
		fn(&b)
		return strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	}

	var panels []string
	if m.showGraphs {
		panels = append(panels, panel("Trends", Graphs(m.history, inner)[1:]))
	}
	if len(m.snap.Steps) > 0 {
		panels = append(panels, panel("DOCSIS", capture(func(b *strings.Builder) { Provision(b, m.snap) })))
	}
	panels = append(panels, panel("Channels", capture(func(b *strings.Builder) { Channels(b, m.snap) })))
	if m.snap.MoCA != nil {
		panels = append(panels, panel("MoCA", capture(func(b *strings.Builder) { Moca(b, m.snap) })))
	}
	if len(m.snap.Logs) > 0 {
		panels = append(panels, panel("Logs", capture(func(b *strings.Builder) { Logs(b, m.snap, 6) })))
	}
	return lipgloss.JoinVertical(lipgloss.Left, panels...)
}

func (m model) waitingView() string {
	lines := []string{
		"",
		"  " + headerStyle.Render("hitron-cli") + "  " + mutedStyle.Render(m.host),
		"",
	}
	if m.err != nil {
		lines = append(lines,
			"  "+Red("no data yet"),
			"  "+Red("error: ")+m.err.Error(),
			"", "  "+mutedStyle.Render("r retry  q quit"))
	} else {
		lines = append(lines, "  "+mutedStyle.Render("connecting…"), "", "  "+mutedStyle.Render("q quit"))
	}
	return strings.Join(lines, "\n")
}

func (m model) helpView() string {
	rows := [][2]string{
		{"q / ctrl+c", "quit"},
		{"r / space", "refresh now"},
		{"a", "toggle automatic refresh"},
		{"g", "toggle trend graphs"},
		{"j / k, ↓ / ↑", "scroll"},
		{"h / ?", "toggle this help"},
	}
	var b strings.Builder
	b.WriteString(panelTitle.Render("Keys") + "\n")
	for _, r := range rows {
		b.WriteString("  " + pad(r[0], 14) + r[1] + "\n")
	}
	b.WriteString("\n" + panelTitle.Render("Notes") + "\n")
	b.WriteString(mutedStyle.Render("  · Reads never log in on the analyzed firmware.\n"))
	b.WriteString(mutedStyle.Render("  · Destructive actions are separate commands, never keys.\n"))
	b.WriteString(mutedStyle.Render("  · Use --once for a one-shot report, --json for machine output."))
	return panelStyle.Render(b.String())
}
