package ui

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

// Banner is the startup wordmark. It is printed once, before any command, and
// suppressed for --json so machine output stays clean.
func Banner(version string) string {
	lines := []string{
		"  +-------------------------------------------------------+",
		"  |  h i t r o n - c l i                                  |",
		"  |                                                       |",
		"  |    ####   [==========]                    coax ------+ |",
		"  |           [    ()    ]                               | |",
		"  +-------------------------------------------------------+",
		"  " + Dim("CODA6X · CODA-57 · HT-EM5    v"+version),
	}
	return strings.Join(lines, "\n")
}

func writeLines(w io.Writer, lines []string) {
	for _, line := range lines {
		fmt.Fprintln(w, line)
	}
}

// Title is the "kind at host" line shown above every report.
func Title(s *hitron.Snapshot) string {
	model := s.Model
	if model == "" {
		model = s.Kind.Label()
	}
	return Bold(model) + "  " + Dim("at "+s.Host+"  ·  "+s.Kind.Label())
}

func kvLines(pairs [][2]string) []string {
	width := 0
	for _, pair := range pairs {
		if len(pair[0]) > width {
			width = len(pair[0])
		}
	}
	lines := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if strings.TrimSpace(pair[1]) == "" {
			continue
		}
		lines = append(lines, "  "+Dim(pad(pair[0], width))+"  "+pair[1])
	}
	return lines
}

func identityPairs(s *hitron.Snapshot) [][2]string {
	uptime := "—"
	if s.HasUptime {
		uptime = formatUptime(s.UptimeSeconds)
	}
	pairs := [][2]string{
		{"Model", s.Model},
		{"Vendor", s.Vendor},
		{"Firmware", s.Firmware},
		{"Hardware", s.Hardware},
		{"Serial", s.Serial},
		{"RF MAC", s.RFMac},
		{"CMTS MAC", s.CMTSMac},
		{"Uptime", uptime},
		{"WAN IP", s.WANIP},
		{"Gateway", s.Gateway},
		{"Netmask", s.Netmask},
		{"Lease", s.Lease},
		{"Config file", s.ConfigName},
		{"Access", s.AccessType},
	}
	if s.Kind == hitron.KindMoca {
		for i := range pairs {
			if pairs[i][0] == "WAN IP" {
				pairs[i][0] = "Device IP"
			}
		}
	}
	return pairs
}

// Identity prints the identity and WAN block.
func Identity(w io.Writer, s *hitron.Snapshot) {
	writeLines(w, kvLines(identityPairs(s)))
}

func specLine(s *hitron.Snapshot) []string {
	lines := make([]string, 0, 2)
	if ok, low, high := s.DSInSpec(); s.DSHasReadings() {
		state := PillBad("OUT OF SPEC")
		if ok {
			state = PillGood("IN SPEC")
		}
		avg, _ := s.DSAverage()
		lines = append(lines, "  "+Dim(pad("Downstream", 11))+"  "+
			fmt.Sprintf("%+.1f to %+.1f dBmV", low, high)+"  "+fmt.Sprintf("avg %+.1f", avg)+"  "+Dim("(-6 to +15)")+"  "+state)
	}
	if ok, low, high := s.USInSpec(); s.USHasReadings() {
		state := PillBad("OUT OF SPEC")
		if ok {
			state = PillGood("IN SPEC")
		}
		avg, _ := s.USAverage()
		lines = append(lines, "  "+Dim(pad("Upstream", 11))+"  "+
			fmt.Sprintf("%.1f to %.1f dBmV", low, high)+"  "+fmt.Sprintf("avg %.1f", avg)+"  "+Dim("(35 to 48)")+"  "+state)
	}
	return lines
}

// Spec prints the in-spec summary.
func Spec(w io.Writer, s *hitron.Snapshot) {
	lines := specLine(s)
	if len(lines) == 0 {
		lines = []string{"  " + Dim("No RF readings reported.")}
	}
	writeLines(w, lines)
}

// Provision prints the DOCSIS milestones.
func Provision(w io.Writer, s *hitron.Snapshot) {
	if len(s.Steps) == 0 {
		return
	}
	fmt.Fprintln(w, "  "+Dim(fmt.Sprintf("Provisioning  %d/%d", s.StepsOK(), len(s.Steps))))
	var parts []string
	for _, step := range s.Steps {
		mark := Green("✓")
		if !step.OK {
			mark = Red("✗")
		}
		parts = append(parts, mark+" "+step.Name)
	}
	writeLines(w, []string{"  " + strings.Join(parts, Dim("  ·  "))})
}

// chanOrder sorts channel IDs numerically; IDs that are not numbers go last.
func chanOrder(id string) int {
	n, err := strconv.Atoi(strings.TrimSpace(id))
	if err != nil {
		return 1 << 30
	}
	return n
}

// codewordCell renders corrected/uncorrectable codeword counters as one
// "14/640" cell. Any uncorrectable codeword is red; corrected counts are
// green while small, yellow when large and red when very large. They are
// cumulative since the modem last (re)locked, so only the uncorrectables are
// treated as a real fault at any size.
func codewordCell(has bool, corrected, uncorrect uint64) string {
	if !has && corrected == 0 && uncorrect == 0 {
		return Dim("—")
	}
	c := Green(formatCount(corrected))
	switch {
	case corrected >= 1_000_000:
		c = Red(formatCount(corrected))
	case corrected >= 100_000:
		c = Yellow(formatCount(corrected))
	}
	u := Green(formatCount(uncorrect))
	if uncorrect > 0 {
		u = Red(formatCount(uncorrect))
	}
	// The modem reports no total-codeword counter, so the percentage is the
	// uncorrectable share of the codewords that were errored at all.
	pct := Green("0.00%")
	if uncorrect > 0 {
		share := 100 * float64(uncorrect) / float64(corrected+uncorrect)
		text := fmt.Sprintf("%.2f%%", share)
		if share < 0.01 {
			text = "<0.01%"
		}
		pct = Red(text)
	}
	return c + Dim("/") + u + " " + pct
}

func typeCell(label string) string {
	if label == "SC-QAM" {
		return label
	}
	return Cyan(label)
}

func countSummary(parts ...string) string {
	return Dim(strings.Join(parts, " · "))
}

// Channels prints one downstream and one upstream table. Each row is typed:
// SC-QAM (incl. ATDMA upstream), OFDM (downstream) or OFDMA (upstream). Disabled
// OFDMA channels carry no signal and are left out, with a count in the
// heading.
func Channels(w io.Writer, s *hitron.Snapshot) {
	ds := append([]hitron.Channel(nil), s.DS...)
	sort.SliceStable(ds, func(i, j int) bool { return chanOrder(ds[i].ID) < chanOrder(ds[j].ID) })
	ofdm := append([]hitron.OFDMChannel(nil), s.OFDM...)
	sort.SliceStable(ofdm, func(i, j int) bool { return chanOrder(ofdm[i].Index) < chanOrder(ofdm[j].Index) })

	total := len(ds) + len(ofdm)
	if total == 0 {
		fmt.Fprintln(w, "  "+Bold("Downstream"))
		fmt.Fprintln(w, "  "+Dim("No downstream channels reported."))
	} else {
		fmt.Fprintln(w, "  "+Bold("Downstream")+"  "+countSummary(
			fmt.Sprintf("%d channels", total),
			fmt.Sprintf("%d SC-QAM", len(ds)),
			fmt.Sprintf("%d OFDM", len(ofdm))))
		rows := make([][]string, 0, total)
		for _, ch := range ds {
			cw := codewordCell(ch.HasCodewords, ch.Corrected, ch.Uncorrect)
			rows = append(rows, []string{
				typeCell("SC-QAM"), emptyDash(ch.ID), freq(ch.Frequency),
				powerCell(ch.Power, ch.HasPower, -6, 15), snrCell(ch.SNR, ch.HasSNR),
				emptyDash(ch.Modulation), Dim("—"), cw,
			})
		}
		for _, ch := range ofdm {
			cw := codewordCell(ch.HasCounters, ch.Corrected, ch.Uncorrect)
			rows = append(rows, []string{
				typeCell("OFDM"), emptyDash(ch.Index), freq(ch.Frequency),
				plcPowerCell(ch.PLCPower, ch.HasPLCPower), snrCell(ch.SNR, ch.HasSNR),
				emptyDash(ch.FFTType), lockText(ch.PLCLock, ch.NCPLock, ch.MDC1Lock), cw,
			})
		}
		writeLines(w, columns(
			[]string{"Type", "Ch", "Frequency", "Power", "SNR", "Mod/FFT", "Locks", "CCWE/UCCWE"},
			rows,
			[]int{7, 4, 11, 7, 6, 8, 12, 25},
		))
	}

	us := append([]hitron.Channel(nil), s.US...)
	sort.SliceStable(us, func(i, j int) bool { return chanOrder(us[i].ID) < chanOrder(us[j].ID) })
	var ofdma []hitron.OFDMAChannel
	disabled := 0
	for _, ch := range s.OFDMA {
		if ch.Disabled {
			disabled++
			continue
		}
		ofdma = append(ofdma, ch)
	}
	sort.SliceStable(ofdma, func(i, j int) bool { return chanOrder(ofdma[i].Index) < chanOrder(ofdma[j].Index) })

	fmt.Fprintln(w)
	total = len(us) + len(ofdma)
	if total == 0 {
		fmt.Fprintln(w, "  "+Bold("Upstream"))
		fmt.Fprintln(w, "  "+Dim("No upstream channels reported."))
		return
	}
	summary := []string{
		fmt.Sprintf("%d channels", total),
		fmt.Sprintf("%d SC-QAM", len(us)),
		fmt.Sprintf("%d OFDMA", len(ofdma)),
	}
	if disabled > 0 {
		summary = append(summary, fmt.Sprintf("%d disabled hidden", disabled))
	}
	fmt.Fprintln(w, "  "+Bold("Upstream")+"  "+countSummary(summary...))
	rows := make([][]string, 0, total)
	for _, ch := range us {
		mode := ch.Mode
		if mode == "" {
			mode = ch.Modulation
		}
		rows = append(rows, []string{
			typeCell("SC-QAM"), emptyDash(ch.ID), freq(ch.Frequency), bandwidth(ch.Bandwidth),
			powerCell(ch.Power, ch.HasPower, 35, 48), emptyDash(mode), Dim("—"),
		})
	}
	for _, ch := range ofdma {
		rows = append(rows, []string{
			typeCell("OFDMA"), emptyDash(ch.Index), freq(ch.Frequency), bandwidthMHzCell(ch.Bandwidth),
			ofdmaPowerCell(ch.Power16, ch.HasPower16, true), emptyDash(ch.State), fmt.Sprintf("%.1f", ch.DigAtten),
		})
	}
	writeLines(w, columns(
		[]string{"Type", "Ch", "Frequency", "Bandwidth", "Power", "Mode", "Atten"},
		rows,
		[]int{7, 4, 11, 10, 7, 9, 6},
	))
}

// System prints identity, provisioning, and switch ports.
func System(w io.Writer, s *hitron.Snapshot) {
	Identity(w, s)
	fmt.Fprintln(w)
	Spec(w, s)
	if len(s.Steps) > 0 {
		fmt.Fprintln(w)
		Provision(w, s)
	}
	if len(s.Ethernet) > 0 {
		fmt.Fprintln(w, "\n  "+Bold("Ethernet"))
		rows := make([][]string, 0, len(s.Ethernet))
		for _, port := range s.Ethernet {
			rows = append(rows, []string{
				emptyDash(port.ID), emptyDash(port.Link), emptyDash(port.Speed),
				emptyDash(port.Duplex), emptyDash(port.Power),
			})
		}
		writeLines(w, columns(
			[]string{"Port", "Link", "Speed", "Duplex", "Power"},
			rows,
			[]int{4, 5, 8, 7, 6},
		))
	}
}

// Logs prints the event log, newest first. The firmware lists newest first
// already; this only applies the limit.
func Logs(w io.Writer, s *hitron.Snapshot, limit int) {
	if len(s.Logs) == 0 {
		fmt.Fprintln(w, "  "+Dim("No log entries reported."))
		return
	}
	if limit <= 0 || limit > len(s.Logs) {
		limit = len(s.Logs)
	}
	rows := make([][]string, 0, limit)
	for _, entry := range s.Logs[:limit] {
		ev := entry.Friendly()
		when := ev.When
		if ev.ClockUnset {
			when = Dim("no clock")
		} else if when == "" {
			when = Dim("—")
		}
		rows = append(rows, []string{when, severityCell(ev.Severity), eventText(ev)})
	}
	writeLines(w, columns(
		[]string{"Time", "Level", "Event"},
		rows,
		[]int{13, 5, 80},
	))
}

func severityCell(sev hitron.Severity) string {
	switch sev {
	case hitron.SevCritical:
		return Red("CRIT")
	case hitron.SevError:
		return Red("ERR")
	case hitron.SevWarning:
		return Yellow("WARN")
	case hitron.SevNotice:
		return Dim("note")
	default:
		return Dim("info")
	}
}

// eventText colours the summary by severity and highlights the arrow in
// "from → to" changes.
func eventText(ev hitron.FriendlyEvent) string {
	text := strings.ReplaceAll(ev.Summary, " → ", " "+AccentBold("→")+" ")
	if ev.Severity == hitron.SevCritical || ev.Severity == hitron.SevError {
		return Red(ev.Summary)
	}
	return text
}

// Moca prints the HT-EM5 view.
func Moca(w io.Writer, s *hitron.Snapshot) {
	if s.MoCA == nil {
		fmt.Fprintln(w, "  "+Dim("No MoCA data (this is not an HT-EM5)."))
		return
	}
	status := s.MoCA
	link := Red("down")
	if status.LinkUp {
		link = Green("up")
	}
	pairs := [][2]string{
		{"Link", link},
		{"Network", status.NetworkVersion},
		{"Device", status.DeviceVersion},
		{"Adapter", status.AdapterName},
		{"Beacon", fmt.Sprintf("%d MHz", status.BeaconMHz)},
		{"LOF", fmt.Sprintf("%d MHz", status.LOFMHz)},
		{"TX power", fmt.Sprintf("index %d", status.TXPowerIndex)},
		{"Ethernet TX", formatCount(status.EthTxGood) + " good frames"},
		{"Ethernet RX", formatCount(status.EthRxGood) + " good frames"},
	}
	writeLines(w, kvLines(pairs))

	if len(status.Bands) > 0 {
		fmt.Fprintln(w, "\n  "+Bold("Bands"))
		rows := make([][]string, 0, len(status.Bands))
		for _, band := range status.Bands {
			rows = append(rows, []string{
				band.Name,
				yesNo(band.Supported),
				yesNo(band.Enabled),
				yesNo(band.Privacy),
			})
		}
		writeLines(w, columns([]string{"Band", "Supported", "Enabled", "Privacy"}, rows, []int{8, 10, 8, 8}))
	}

	if len(status.Nodes) > 0 {
		fmt.Fprintln(w, "\n  "+Bold("Nodes")+"  "+Dim(fmt.Sprintf("%d", len(status.Nodes))))
		rows := make([][]string, 0, len(status.Nodes))
		for _, node := range status.Nodes {
			role := ""
			if node.NC {
				role = "NC"
			}
			if node.Self {
				if role != "" {
					role += ", self"
				} else {
					role = "self"
				}
			}
			rows = append(rows, []string{fmt.Sprintf("%d", node.NodeID), node.MAC, node.Version, role})
		}
		writeLines(w, columns([]string{"Node", "MAC", "MoCA", "Role"}, rows, []int{4, 17, 6, 10}))
	}
}

// Warnings prints the endpoints that failed.
func Warnings(w io.Writer, s *hitron.Snapshot) {
	if len(s.Warnings) == 0 {
		return
	}
	fmt.Fprintln(w, "\n  "+Yellow("Partial data"))
	for _, warning := range s.Warnings {
		fmt.Fprintln(w, "  "+Dim("· ")+warning)
	}
}

// All prints the full one-shot report.
func All(w io.Writer, s *hitron.Snapshot, logLimit int) {
	fmt.Fprintln(w, Title(s))
	fmt.Fprintln(w)
	Identity(w, s)
	fmt.Fprintln(w)
	Spec(w, s)
	if len(s.Steps) > 0 {
		fmt.Fprintln(w)
		Provision(w, s)
	}
	fmt.Fprintln(w)
	Channels(w, s)
	if s.MoCA != nil {
		fmt.Fprintln(w, "\n  "+Bold("MoCA"))
		Moca(w, s)
	}
	if len(s.Logs) > 0 {
		fmt.Fprintln(w, "\n  "+Bold("Logs")+"  "+Dim(fmt.Sprintf("showing %d of %d", minInt(logLimit, len(s.Logs)), len(s.Logs))))
		Logs(w, s, logLimit)
	}
	if len(s.Ethernet) > 0 {
		fmt.Fprintln(w, "\n  "+Bold("Ethernet"))
		rows := make([][]string, 0, len(s.Ethernet))
		for _, port := range s.Ethernet {
			rows = append(rows, []string{
				emptyDash(port.ID), emptyDash(port.Link), emptyDash(port.Speed),
				emptyDash(port.Duplex), emptyDash(port.Power),
			})
		}
		writeLines(w, columns([]string{"Port", "Link", "Speed", "Duplex", "Power"}, rows, []int{4, 5, 8, 7, 6}))
	}
	Warnings(w, s)
}

func powerCell(power float64, has bool, low, high float64) string {
	if !has {
		return Dim("—")
	}
	inSpec := power >= low && power <= high
	nearEdge := (power >= low && power <= low+1.5) || (power <= high && power >= high-1.5)
	return Power(power, inSpec, nearEdge)
}

func plcPowerCell(power float64, has bool) string {
	return powerCell(power, has, -6, 15)
}

func ofdmaPowerCell(power16 float64, has, enabled bool) string {
	if !enabled || !has {
		return Dim("—")
	}
	return powerCell(power16+6, true, 35, 48)
}

func snrCell(snr float64, has bool) string {
	if !has {
		return Dim("—")
	}
	return fmt.Sprintf("%.1f", snr)
}

func lockText(plc, ncp, mdc bool) string {
	var parts []string
	if plc {
		parts = append(parts, "PLC")
	}
	if ncp {
		parts = append(parts, "NCP")
	}
	if mdc {
		parts = append(parts, "MDC1")
	}
	if len(parts) == 0 {
		return Dim("—")
	}
	return strings.Join(parts, ",")
}

func freq(hz float64) string {
	if hz == 0 {
		return Dim("—")
	}
	return fmt.Sprintf("%.1f MHz", hz/1e6)
}

func bandwidth(hz float64) string {
	if hz == 0 {
		return Dim("—")
	}
	return fmt.Sprintf("%.1f MHz", hz/1e6)
}

func bandwidthMHzCell(mhz float64) string {
	if mhz == 0 {
		return Dim("—")
	}
	return fmt.Sprintf("%.1f MHz", mhz)
}

func formatUptime(seconds int64) string {
	if seconds <= 0 {
		return "—"
	}
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm %ds", minutes, seconds%60)
	}
}

func formatCount(value uint64) string {
	text := fmt.Sprintf("%d", value)
	if len(text) <= 3 {
		return text
	}
	parts := []string{}
	for len(text) > 3 {
		parts = append([]string{text[len(text)-3:]}, parts...)
		text = text[:len(text)-3]
	}
	parts = append([]string{text}, parts...)
	return strings.Join(parts, ",")
}

func emptyDash(text string) string {
	if strings.TrimSpace(text) == "" {
		return Dim("—")
	}
	return text
}

func yesNo(value bool) string {
	if value {
		return Green("yes")
	}
	return Dim("no")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
