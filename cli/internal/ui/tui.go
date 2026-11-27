package ui

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

// FetchFunc polls the device once.
type FetchFunc func(context.Context) (*hitron.Snapshot, error)

// RebootFunc sends a reboot using the given login and returns a short result
// message. It is only ever called after the confirmation dialog.
type RebootFunc func(ctx context.Context, username, password string) (string, error)

// TUIOptions configures the live dashboard.
type TUIOptions struct {
	Fetch    FetchFunc
	Interval time.Duration
	Host     string

	// Reboot enables the guarded reboot dialog. nil hides it entirely.
	Reboot RebootFunc
	// Username and Password, when both are set, are used for the reboot and
	// the dialog skips asking for them. The confirmation step is never skipped.
	Username, Password string

	// NoLogin means the device accepts a local reboot without credentials
	// (CODA6X at its LAN address), so the dialog does not ask for any. A login
	// passed in Username/Password is still handed to Reboot as a fallback.
	NoLogin bool

	// Spectrum and constellation readers (CODA6X). Any may be nil; the view
	// then says it is unavailable instead of failing.
	Channels      func(context.Context) ([]hitron.SpectrumChannel, error)
	Sweep         func(context.Context, float64) (*hitron.SpectrumSweep, error)
	RealTime      func(context.Context) ([]hitron.RealTimeFrame, error)
	Constellation func(context.Context, int) (*hitron.Constellation, error)

	// ResetFEC clears the downstream counters on the device. nil means the
	// family has no confirmed endpoint, so z only zeroes the display.
	ResetFEC func(context.Context) (hitron.FECReset, error)

	NoMouse bool
	// NoBackground turns the themed gradient background off.
	NoBackground bool
}

type dialogStage int

const (
	dlgNone dialogStage = iota
	dlgUser
	dlgPass
	dlgConfirm
	dlgRunning
	dlgDone
)

const confirmWord = "reboot"

type tickMsg struct{}

type snapMsg struct {
	snap *hitron.Snapshot
	err  error
	at   time.Time
}

// specMsg and consMsg carry one analyser capture. channels is set only when
// the analyser channel list was fetched by this command.
type specMsg struct {
	channels      []hitron.SpectrumChannel
	channelsTried bool
	channelsErr   error
	center        float64
	sweep         *hitron.SpectrumSweep
	sweepErr      error
	frames        []hitron.RealTimeFrame
}

type consMsg struct {
	channels      []hitron.SpectrumChannel
	channelsTried bool
	channelsErr   error
	port          int
	cons          *hitron.Constellation
	err           error
}

type rebootMsg struct {
	text string
	err  error
}

// fecMsg carries the outcome of the device-side counter clear.
type fecMsg struct {
	result hitron.FECReset
	err    error
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

	src  TUIOptions
	mode viewMode
	// dirty means the user changed view or channel while a capture was in
	// flight; one more is issued as soon as it lands.
	dirty bool

	channels       []hitron.SpectrumChannel
	channelsErr    error
	channelsLoaded bool
	centerMHz      float64
	sweep          *hitron.SpectrumSweep
	sweepErr       error
	realTime       []hitron.RealTimeFrame
	portID         int
	cons           *hitron.Constellation
	consErr        error

	// fecBase is the local fallback for families with no FEC endpoint: z
	// zeroes the display instead of writing to the device.
	fecBase  map[string][2]uint64
	fecSince time.Time

	// fecRunning and fecStatus track the real counter clear on the device.
	fecRunning bool
	fecStatus  string
	fecFailed  bool

	reboot            RebootFunc
	noLogin           bool
	bg                bool
	preUser, prePass  string
	dlg               dialogStage
	fUser, fPass, fOK []rune
	dlgText           string
	dlgErr            error
}

// RunTUI runs the live dashboard until the user quits or ctx is cancelled.
func RunTUI(ctx context.Context, o TUIOptions) error {
	if !IsTerminal() {
		return fmt.Errorf("stdin is not a terminal; use --once for a one-shot report")
	}
	interval := o.Interval
	if interval < time.Second {
		interval = time.Second
	}
	m := model{
		src: o, portID: -1,
		ctx: ctx, fetch: o.Fetch, interval: interval, host: o.Host,
		history: NewHistory(), auto: true, showGraphs: true,
		width: 100, height: 32, loading: true,
		bg: !o.NoBackground && TrueColor(), reboot: o.Reboot, noLogin: o.NoLogin, preUser: o.Username, prePass: o.Password,
	}
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	if !o.NoMouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	_, err := tea.NewProgram(m, opts...).Run()
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

// fecResetCmd runs the device-side clear off the draw loop.
func (m model) fecResetCmd() tea.Cmd {
	reset := m.src.ResetFEC
	return func() tea.Msg {
		result, err := reset(m.ctx)
		return fecMsg{result: result, err: err}
	}
}

func (m model) tickCmd() tea.Cmd {
	return tea.Tick(m.interval, func(time.Time) tea.Msg { return tickMsg{} })
}

func wipe(r []rune) {
	for i := range r {
		r[i] = 0
	}
}

func (m *model) closeDialog() {
	wipe(m.fUser)
	wipe(m.fPass)
	m.fUser, m.fPass, m.fOK = nil, nil, nil
	m.dlg = dlgNone
	m.dlgText, m.dlgErr = "", nil
}

// openDialog starts the reboot flow. With a login already supplied it goes
// straight to the confirmation; the confirmation itself is always required.
func (m model) openDialog() model {
	if m.reboot == nil || m.snap == nil || m.dlg != dlgNone {
		return m
	}
	m.fUser, m.fPass, m.fOK = nil, nil, nil
	m.dlgText, m.dlgErr = "", nil
	if m.noLogin || (m.preUser != "" && m.prePass != "") {
		m.fUser, m.fPass = []rune(m.preUser), []rune(m.prePass)
		m.dlg = dlgConfirm
	} else {
		m.fUser = []rune(m.preUser)
		m.dlg = dlgUser
	}
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case snapMsg:
		if msg.err != nil {
			m.err = msg.err
			return m.afterRead()
		}
		m.snap, m.err, m.lastOK = msg.snap, nil, msg.at
		m.history.Push(msg.snap)
		return m.afterRead()
	case specMsg:
		return m.applySpec(msg).afterRead()
	case consMsg:
		return m.applyCons(msg).afterRead()
	case rebootMsg:
		wipe(m.fPass)
		m.fPass = nil
		m.dlg, m.dlgText, m.dlgErr = dlgDone, msg.text, msg.err
	case fecMsg:
		m.fecRunning = false
		if msg.err != nil {
			m.fecStatus, m.fecFailed = "FEC reset failed: "+msg.err.Error(), true
			return m, nil
		}
		m.fecStatus, m.fecFailed = msg.result.String(), false
		// Re-read so the tables show the cleared counters. A poll that was
		// already in flight will land on its own.
		if m.loading {
			return m, nil
		}
		m.loading = true
		return m, m.fetchCmd()
	case tickMsg:
		// Always re-arm; only poll when auto-refresh is on, nothing is in
		// flight and no dialog is open, so a slow modem can't stack requests
		// and a reboot isn't racing a poll. A FEC clear holds the same rule.
		if m.auto && !m.loading && !m.fecRunning && m.dlg == dlgNone {
			next, cmd := m.issue()
			return next, tea.Batch(cmd, m.tickCmd())
		}
		return m, m.tickCmd()
	case tea.MouseMsg:
		return m.mouse(msg)
	case tea.KeyMsg:
		if m.dlg != dlgNone {
			return m.dialogKey(msg)
		}
		return m.key(msg)
	}
	return m, nil
}

func (m model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.dlg != dlgNone || m.showHelp || m.mode != viewSummary {
		return m, nil
	}
	switch {
	case msg.Button == tea.MouseButtonWheelUp:
		if m.scroll > 0 {
			m.scroll--
		}
	case msg.Button == tea.MouseButtonWheelDown:
		m.scroll++
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft:
		// Clicking the button only opens the confirmation dialog.
		if m.reboot != nil && m.snap != nil && msg.Y == m.height-1 {
			if start, end := m.rebootButtonRange(); msg.X >= start && msg.X < end {
				m = m.openDialog()
			}
		}
	}
	return m, nil
}

func (m model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		return m, tea.Quit
	case "r", " ":
		if !m.loading && !m.fecRunning {
			return m.issue()
		}
	case "1":
		m.mode, m.scroll = viewSummary, 0
		return m.refreshOrQueue()
	case "2", "s":
		m.mode = viewSpectrum
		return m.refreshOrQueue()
	case "3", "c":
		m.mode = viewConstellation
		return m.refreshOrQueue()
	case "[", "]", "p":
		if m.mode == viewSummary {
			return m, nil
		}
		delta := -1
		if msg.String() != "[" {
			delta = 1
		}
		return m.stepChannel(delta).refreshOrQueue()
	case ",", ".":
		if m.mode != viewSpectrum {
			return m, nil
		}
		delta := -1.0
		if msg.String() == "." {
			delta = 1
		}
		m.centerMHz = math.Max(49, math.Min(1788, m.centerMHz+delta))
		return m.refreshOrQueue()
	case "a":
		m.auto = !m.auto
	case "g":
		m.showGraphs = !m.showGraphs
	case "t":
		SaveTheme(NextTheme().Name)
	case "b":
		m.bg = !m.bg
	case "z":
		if m.snap == nil || m.fecRunning {
			return m, nil
		}
		if m.src.ResetFEC != nil {
			m.fecRunning = true
			m.fecFailed = false
			m.fecStatus = "FEC reset: clearing 3.0 and OFDM counters…"
			return m, m.fecResetCmd()
		}
		// No confirmed device endpoint: zero the display locally.
		if m.fecBase != nil {
			m.fecBase, m.fecSince = nil, time.Time{}
		} else {
			m.fecBase, m.fecSince = fecBaseline(m.snap), time.Now()
		}
	case "x":
		m = m.openDialog()
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
	return m, nil
}

func (m *model) field() *[]rune {
	switch m.dlg {
	case dlgUser:
		return &m.fUser
	case dlgPass:
		return &m.fPass
	default:
		return &m.fOK
	}
}

func (m model) dialogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.dlg {
	case dlgRunning:
		return m, nil
	case dlgDone:
		m.closeDialog()
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.closeDialog()
	case tea.KeyEnter:
		return m.dialogEnter()
	case tea.KeyBackspace:
		f := m.field()
		if n := len(*f); n > 0 {
			(*f)[n-1] = 0
			*f = (*f)[:n-1]
		}
	case tea.KeySpace:
		f := m.field()
		*f = append(*f, ' ')
	case tea.KeyRunes:
		f := m.field()
		*f = append(*f, msg.Runes...)
	}
	return m, nil
}

func (m model) dialogEnter() (tea.Model, tea.Cmd) {
	switch m.dlg {
	case dlgUser:
		if len(m.fUser) > 0 {
			m.dlg = dlgPass
		}
	case dlgPass:
		if len(m.fPass) > 0 {
			m.dlg = dlgConfirm
		}
	case dlgConfirm:
		if strings.TrimSpace(string(m.fOK)) != confirmWord {
			return m, nil
		}
		user, pass := string(m.fUser), string(m.fPass)
		m.dlg = dlgRunning
		return m, func() tea.Msg {
			text, err := m.reboot(m.ctx, user, pass)
			return rebootMsg{text: text, err: err}
		}
	}
	return m, nil
}

// ---- views -----------------------------------------------------------------

func (m model) View() string {
	out := m.render()
	if m.bg {
		return paintBackground(out, m.width, m.height)
	}
	return out
}

func (m model) render() string {
	switch {
	case m.dlg != dlgNone:
		return m.dialogView()
	case m.showHelp:
		return m.helpView()
	case m.mode == viewSpectrum:
		return m.spectrumView()
	case m.mode == viewConstellation:
		return m.constellationView()
	case m.snap == nil:
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
	return strings.Join(append(out, footer), "\n")
}

func (m model) statusText() string {
	switch {
	case m.err != nil:
		return Red("✗ error")
	case m.fecRunning:
		return Yellow("● FEC reset")
	case m.loading:
		return Yellow("● polling")
	case m.auto:
		return Green("● live") + " " + MutedText(m.interval.String())
	default:
		return MutedText("‖ paused")
	}
}

// current is the snapshot as displayed: the raw reading, or with the FEC
// counters shown relative to the baseline when z is active.
func (m model) current() *hitron.Snapshot {
	if m.fecBase != nil {
		return applyFECBaseline(m.snap, m.fecBase)
	}
	return m.snap
}

func rule(width int) string {
	return fg(CurrentTheme().Border, strings.Repeat("─", width))
}

// topLine is the one-line title bar shared by every view.
func (m model) topLine() string {
	th := CurrentTheme()
	model, detail := m.host, ""
	if m.snap != nil {
		model = m.snap.Model
		if model == "" {
			model = m.snap.Kind.Label()
		}
		detail = "  " + m.snap.Host + "  ·  " + m.snap.Kind.Label()
	}
	left := " " + AccentBold("hitron") + MutedText("  ·  ") + Bold(model) + MutedText(detail)
	right := MutedText(time.Now().Format("15:04:05")) + "  " + m.statusText() + "  " + MutedText(th.Name) + " "
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		// Narrow terminal: drop the host and family so the status stays visible.
		left = " " + AccentBold("hitron") + MutedText("  ·  ") + Bold(model)
		gap = m.width - lipgloss.Width(left) - lipgloss.Width(right)
	}
	line := truncate(left, m.width)
	if gap >= 2 {
		line = left + strings.Repeat(" ", gap) + right
	}
	return line
}

func (m model) headerLines() []string {
	line := m.topLine()
	lines := []string{line, rule(m.width)}
	switch {
	case m.err != nil:
		age := ""
		if !m.lastOK.IsZero() {
			age = " (showing data from " + m.lastOK.Format("15:04:05") + ")"
		}
		lines = append(lines, truncate(" "+Red("error: ")+m.err.Error()+MutedText(age), m.width))
	default:
		if cards := Cards(m.current(), m.history, m.width); cards != nil {
			lines = append(lines, "")
			lines = append(lines, cards...)
			lines = append(lines, "", rule(m.width))
		} else {
			lines = append(lines, specLine(m.snap)...)
		}
	}
	if m.fecStatus != "" {
		text := " " + m.fecStatus
		if m.fecFailed {
			text = " " + Red(m.fecStatus)
		} else {
			text = " " + Green(m.fecStatus)
		}
		lines = append(lines, truncate(text, m.width))
	}
	return lines
}

func keycap(key, label string) string { return AccentBold(key) + " " + MutedText(label) }

func (m model) rebootButton() string {
	if !Color {
		return "[ Reboot x ]"
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(CurrentTheme().Bad)).
		Foreground(lipgloss.Color("#0B0B0F")).
		Bold(true).Padding(0, 1).Render("Reboot x")
}

// rebootButtonRange is the [start, end) column span of the footer button; the
// mouse handler uses the same numbers the footer is drawn with.
func (m model) rebootButtonRange() (int, int) {
	w := lipgloss.Width(m.rebootButton())
	start := m.width - w - 1
	return start, start + w
}

func (m model) footerLine() string {
	hints := []string{
		keycap("q", "quit"), keycap("r", "refresh"), keycap("a", "auto"), keycap("1 2 3", "views"),
		keycap("g", "graphs"), keycap("z", "reset FEC"), keycap("t", "theme"), keycap("h", "help"),
	}
	left := " " + strings.Join(hints, "  ")
	if m.reboot == nil {
		return truncate(left, m.width)
	}
	start, _ := m.rebootButtonRange()
	if start < 2 {
		return truncate(left, m.width)
	}
	left = truncate(left, start-1)
	return left + strings.Repeat(" ", start-lipgloss.Width(left)) + m.rebootButton()
}

func panelBox(width int) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(CurrentTheme().Border)).
		Padding(0, 1).
		Width(width)
}

func panelHeading(title string) string { return AccentBold("◆ " + title) }

// deviceLines lays the identity block out in two columns when there is room.
func (m model) deviceLines(inner int) []string {
	var pairs [][2]string
	for _, p := range identityPairs(m.snap) {
		if strings.TrimSpace(p[1]) != "" {
			pairs = append(pairs, p)
		}
	}
	if inner < 90 || len(pairs) < 6 {
		return kvLines(pairs)
	}
	half := (len(pairs) + 1) / 2
	left, right := kvLines(pairs[:half]), kvLines(pairs[half:])
	col := inner / 2
	lines := make([]string, 0, len(left))
	for i, l := range left {
		row := pad(l, col)
		if i < len(right) {
			row += right[i]
		}
		lines = append(lines, row)
	}
	return lines
}

// section is a titled block with a rule instead of a box: less chrome, more
// room for the data.
func section(width int, title, meta string, lines []string) []string {
	head := fg(CurrentTheme().Border, "── ") + AccentBold(title)
	if meta != "" {
		head += "  " + MutedText(meta)
	}
	fill := width - lipgloss.Width(head) - 1
	if fill > 0 {
		head += " " + fg(CurrentTheme().Border, strings.Repeat("─", fill))
	}
	out := []string{head}
	for _, l := range lines {
		out = append(out, truncate(" "+l, width))
	}
	return append(out, "")
}

func (m model) bodyView() string {
	width := m.width
	if width < 24 {
		width = 24
	}
	view := m.current()
	capture := func(fn func(*strings.Builder)) []string {
		var b strings.Builder
		fn(&b)
		return strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	}

	var out []string
	if m.showGraphs {
		out = append(out, section(width, "Trends", fmt.Sprintf("%d polls", m.history.Samples), Graphs(m.history, width)[1:])...)
	}
	out = append(out, section(width, "Device", "", m.deviceLines(width-2))...)
	if len(m.snap.Steps) > 0 {
		meta := fmt.Sprintf("%d/%d steps", m.snap.StepsOK(), len(m.snap.Steps))
		out = append(out, section(width, "DOCSIS", meta, provisionLines(m.snap, width-2))...)
	}
	meta := ""
	if m.fecBase != nil {
		meta = "codewords since " + m.fecSince.Format("15:04:05") + " (z to restore)"
	}
	out = append(out, section(width, "Channels", meta, capture(func(b *strings.Builder) { Channels(b, view) }))...)
	if m.snap.MoCA != nil {
		out = append(out, section(width, "MoCA", "", capture(func(b *strings.Builder) { Moca(b, m.snap) }))...)
	}
	if len(m.snap.Logs) > 0 {
		out = append(out, section(width, "Logs", "newest first", capture(func(b *strings.Builder) { Logs(b, m.snap, 8) }))...)
	}
	return strings.Join(out, "\n")
}

func (m model) waitingView() string {
	lines := []string{
		"",
		"  " + Gradient("hitron-cli", CurrentTheme().Accent, CurrentTheme().Accent2) + "  " + MutedText(m.host),
		"",
	}
	if m.err != nil {
		lines = append(lines,
			"  "+Red("no data yet"),
			"  "+Red("error: ")+m.err.Error(),
			"", "  "+keycap("r", "retry")+"  "+keycap("q", "quit"))
	} else {
		lines = append(lines, "  "+MutedText("connecting…"), "", "  "+keycap("q", "quit"))
	}
	return strings.Join(lines, "\n")
}

func (m model) helpView() string {
	rows := [][2]string{
		{"q / ctrl+c", "quit"},
		{"r / space", "refresh now"},
		{"a", "toggle automatic refresh"},
		{"1 / 2 / 3", "summary / live spectrum / constellation (also s, c)"},
		{"[ ]  p", "previous / next analyser channel"},
		{", .", "fine-tune the spectrum centre by 1 MHz"},
		{"g", "toggle trend graphs"},
		{"b", "toggle the gradient background (needs a 24-bit colour terminal)"},
		{"z", "reset the downstream FEC counters (3.0 + OFDM; CODA6X)"},
		{"t", "next theme (remembered for next time)"},
		{"j / k, ↓ / ↑", "scroll (the mouse wheel works too)"},
		{"x", "reboot the device, after a confirmation"},
		{"h / ?", "toggle this help"},
	}
	var b strings.Builder
	b.WriteString(panelHeading("Keys") + "\n")
	for _, r := range rows {
		b.WriteString("  " + AccentBold(pad(r[0], 14)) + r[1] + "\n")
	}
	b.WriteString("\n" + panelHeading("Themes") + "\n  ")
	for i, t := range Themes {
		mark := fg(t.Accent, "●")
		name := t.Name
		if t.Name == CurrentTheme().Name {
			name = Bold(name)
		} else {
			name = MutedText(name)
		}
		if i > 0 {
			b.WriteString("   ")
		}
		b.WriteString(mark + " " + name)
	}
	b.WriteString("\n\n" + panelHeading("Notes") + "\n")
	notes := []string{
		"  · Reads never log in on the analyzed firmware.",
		"  · Reboot asks you to type the word “reboot”; clicking the button only opens that dialog.",
		"  · Factory reset and hard reset stay separate commands.",
		"  · One hardware tuner serves every capture; requests stay sequential.",
		"  · Inactive constellation ports answer empty; that is not an error.",
		"  · On the CODA6X, z clears the real 3.0 and OFDM FEC counters; other families zero only the display.",
		"  · With the mouse on, select text by holding Option (or Shift); --no-mouse turns it off.",
	}
	for i, note := range notes {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(MutedText(note))
	}
	return panelBox(0).Render(b.String())
}

func (m model) dialogView() string {
	th := CurrentTheme()
	kind := ""
	if m.snap != nil {
		kind = m.snap.Kind.Label() + " "
	}
	var lines []string
	lines = append(lines,
		fg(th.Bad, "Reboot ")+Bold(kind)+MutedText("at "+m.host),
		MutedText("This restarts the real device; it goes offline for a couple of minutes."),
		"")

	cursor := AccentText("▌")
	switch m.dlg {
	case dlgUser:
		lines = append(lines, "Username  "+string(m.fUser)+cursor, "", MutedText("Enter next · Esc cancels"))
	case dlgPass:
		lines = append(lines,
			"Username  "+string(m.fUser),
			"Password  "+strings.Repeat("•", len(m.fPass))+cursor,
			"", MutedText("Enter next · Esc cancels"))
	case dlgConfirm:
		switch {
		case m.noLogin && m.preUser == "":
			lines = append(lines, MutedText("No username or password needed at this address."), "")
		case m.preUser != "" && m.prePass != "":
			lines = append(lines, MutedText("Using the login from --user / the environment: "+string(m.fUser)), "")
		}
		typed := string(m.fOK)
		button := MutedText("[ Reboot now ]")
		if strings.TrimSpace(typed) == confirmWord {
			button = pill("REBOOT NOW (Enter)", th.Bad)
		}
		lines = append(lines,
			"Type "+Bold(confirmWord)+" to confirm:",
			"  > "+typed+cursor,
			"", button, "",
			MutedText("Nothing is sent until you confirm · Esc cancels"))
	case dlgRunning:
		lines = append(lines, Yellow("Sending the reboot request…"))
	case dlgDone:
		if m.dlgErr != nil {
			lines = append(lines, Red("✗ "+m.dlgErr.Error()))
		} else {
			lines = append(lines, Green("✓ "+m.dlgText),
				MutedText("The dashboard reconnects on its own once the device is back."))
		}
		lines = append(lines, "", MutedText("Press any key to close"))
	}

	box := lipgloss.NewStyle().
		Border(lipgloss.ThickBorder()).
		BorderForeground(lipgloss.Color(th.Bad)).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// provisionLines lays the provisioning milestones out in rows that fit width,
// so a failing step is never pushed off the right edge.
func provisionLines(s *hitron.Snapshot, width int) []string {
	var lines []string
	row, rowLen := "", 0
	for _, step := range s.Steps {
		mark := Green("✓")
		if !step.OK {
			mark = Red("✗")
		}
		item := mark + " " + step.Name
		itemLen := 2 + len([]rune(step.Name))
		if row != "" && rowLen+3+itemLen > width-2 {
			lines = append(lines, "  "+row)
			row, rowLen = "", 0
		}
		if row != "" {
			row += Dim("  ·  ")
			rowLen += 5
		}
		row += item
		rowLen += itemLen
	}
	if row != "" {
		lines = append(lines, "  "+row)
	}
	return lines
}
