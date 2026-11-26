package ui

import (
	"fmt"
	"io"
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
		{"Uptime", uptime},
		{"WAN IP", s.WANIP},
		{"Gateway", s.Gateway},
		{"Netmask", s.Netmask},
		{"Lease", s.Lease},
		{"Config", s.ConfigName},
		{"Access", s.AccessType},
	}
	if s.Kind == hitron.KindMoca {
		pairs[7][0] = "Device IP"
		pairs[0][1] = s.Model
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
		state := Red("out of spec")
		if ok {
			state = Green("in spec")
		}
		lines = append(lines, "  "+Dim(pad("Downstream", 11))+"  "+
			fmt.Sprintf("%+.1f to %+.1f dBmV", low, high)+"  "+Dim("(-6 to +15)")+"  "+state)
	}
	if ok, low, high := s.USInSpec(); s.USHasReadings() {
		state := Red("out of spec")
		if ok {
			state = Green("in spec")
		}
		lines = append(lines, "  "+Dim(pad("Upstream", 11))+"  "+
			fmt.Sprintf("%.1f to %.1f dBmV", low, high)+"  "+Dim("(35 to 48)")+"  "+state)
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

// Channels prints every downstream and upstream channel.
func Channels(w io.Writer, s *hitron.Snapshot) {
	fmt.Fprintln(w, "  "+Bold("Downstream")+"  "+Dim(fmt.Sprintf("%d SC-QAM", len(s.DS))))
	if len(s.DS) == 0 {
		fmt.Fprintln(w, "  "+Dim("No SC-QAM channels reported."))
	} else {
		rows := make([][]string, 0, len(s.DS))
		for _, ch := range s.DS {
			corrected := "—"
			if ch.Corrected != 0 || ch.Uncorrect != 0 {
				corrected = formatCount(ch.Corrected)
			}
			rows = append(rows, []string{
				emptyDash(ch.ID),
				freq(ch.Frequency),
				powerCell(ch.Power, ch.HasPower, -6, 15),
				snrCell(ch.SNR, ch.HasSNR),
				emptyDash(ch.Modulation),
				corrected,
				formatCount(ch.Uncorrect),
			})
		}
		writeLines(w, columns(
			[]string{"Ch", "Frequency", "Power", "SNR", "Modulation", "Corrected", "Uncorrect"},
			rows,
			[]int{4, 11, 8, 7, 10, 10, 10},
		))
	}

	if len(s.OFDM) > 0 {
		fmt.Fprintln(w, "\n  "+Bold("Downstream OFDM")+"  "+Dim(fmt.Sprintf("%d", len(s.OFDM))))
		rows := make([][]string, 0, len(s.OFDM))
		for _, ch := range s.OFDM {
			locks := lockText(ch.PLCLock, ch.NCPLock, ch.MDC1Lock)
			rows = append(rows, []string{
				emptyDash(ch.Index),
				freq(ch.Frequency),
				plcPowerCell(ch.PLCPower, ch.HasPLCPower),
				snrCell(ch.SNR, ch.HasSNR),
				emptyDash(ch.FFTType),
				locks,
				formatCount(ch.Corrected),
			})
		}
		writeLines(w, columns(
			[]string{"Idx", "Frequency", "PLC pwr", "SNR", "FFT", "Locks", "Corrected"},
			rows,
			[]int{4, 11, 8, 7, 6, 12, 10},
		))
	}

	fmt.Fprintln(w, "\n  "+Bold("Upstream")+"  "+Dim(fmt.Sprintf("%d SC-QAM", len(s.US))))
	if len(s.US) == 0 {
		fmt.Fprintln(w, "  "+Dim("No upstream channels reported."))
	} else {
		rows := make([][]string, 0, len(s.US))
		for _, ch := range s.US {
			mode := ch.Mode
			if mode == "" {
				mode = ch.Modulation
			}
			rows = append(rows, []string{
				emptyDash(ch.ID),
				freq(ch.Frequency),
				emptyDash(mode),
				bandwidth(ch.Bandwidth),
				powerCell(ch.Power, ch.HasPower, 35, 48),
			})
		}
		writeLines(w, columns(
			[]string{"Ch", "Frequency", "Mode", "Bandwidth", "Power"},
			rows,
			[]int{4, 11, 9, 11, 8},
		))
	}

	if len(s.OFDMA) > 0 {
		fmt.Fprintln(w, "\n  "+Bold("Upstream OFDMA")+"  "+Dim(fmt.Sprintf("%d", len(s.OFDMA))))
		rows := make([][]string, 0, len(s.OFDMA))
		for _, ch := range s.OFDMA {
			rows = append(rows, []string{
				emptyDash(ch.Index),
				emptyDash(ch.State),
				freq(ch.Frequency),
				bandwidthMHz(ch.Bandwidth),
				ofdmaPowerCell(ch.Power16, ch.HasPower16, !ch.Disabled),
				fmt.Sprintf("%.1f", ch.DigAtten),
			})
		}
		writeLines(w, columns(
			[]string{"Idx", "State", "Frequency", "BW MHz", "Power (+6)", "Atten"},
			rows,
			[]int{4, 11, 11, 8, 11, 6},
		))
	}
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
		priority := entry.Priority
		if entry.Type != "" {
			priority = entry.Type
		}
		rows = append(rows, []string{entry.Time, emptyDash(priority), entry.Event})
	}
	writeLines(w, columns(
		[]string{"Time", "Level", "Event"},
		rows,
		[]int{24, 11, 80},
	))
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

func bandwidthMHz(mhz float64) string {
	if mhz == 0 {
		return Dim("—")
	}
	return fmt.Sprintf("%.1f", mhz)
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
