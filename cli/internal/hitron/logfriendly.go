package hitron

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Severity is a normalized DOCSIS event priority.
type Severity string

const (
	SevCritical Severity = "critical"
	SevError    Severity = "error"
	SevWarning  Severity = "warning"
	SevNotice   Severity = "notice"
	SevInfo     Severity = "info"
)

// FriendlyEvent is a log line boiled down for reading: a short summary with
// the repeated CM-MAC/CMTS-MAC/CM-QOS/CM-VER trailer removed, a normalized
// severity and a parsed time.
type FriendlyEvent struct {
	When       string // e.g. "Nov 26 09:19", empty when it cannot be parsed
	ClockUnset bool   // the modem logged this before it had the time of day
	Severity   Severity
	Summary    string // short text; changes are written "from → to"
	Message    string // the full message minus the MAC/QoS trailer
}

// cmStatusNames are the CM-STATUS event type codes from the DOCSIS MULPI
// specification (CM-SP-MULPIv3.1, CM-STATUS Event Type Codes).
var cmStatusNames = map[int]string{
	1:  "Secondary channel MDD timeout",
	2:  "QAM/FEC lock failure",
	3:  "Sequence out of range",
	4:  "Secondary channel MDD recovered",
	5:  "QAM/FEC lock recovered",
	6:  "T4 timeout",
	7:  "T3 retries exceeded",
	8:  "Ranging recovered after T3 retries",
	9:  "Running on battery backup",
	10: "Back on A/C power",
	11: "MAC removal",
	16: "DS OFDM profile failure",
	17: "Primary downstream changed",
	18: "DPD mismatch",
	20: "NCP profile failure",
	21: "PLC failure",
	22: "NCP profile recovered",
	23: "PLC recovered",
	24: "OFDM profile recovered",
	25: "OFDMA profile failure",
	26: "MAP storage overflow",
	27: "MAP storage almost full",
}

var (
	macTrailer    = regexp.MustCompile(`;?\s*CM-MAC=.*$`)
	profileChange = regexp.MustCompile(`(?i)\b(US|DS)\s+profile assignment change.*?Chan(?:nel)? ID:\s*(\d+).*?Previous Profile:\s*([\d ,]+?)\s*;.*?New Profile:\s*([\d ,]+?)\s*\.?\s*$`)
	cmStatus      = regexp.MustCompile(`(?i)CM-STATUS message sent\.\s*Event Type Code:\s*(\d+)(?:;\s*Chan ID:\s*([^;]*?))?\s*(?:;|$)(?:.*?OFDM/OFDMA Profile ID:\s*([^.;]*?)\s*\.?\s*$)?`)
	mimoEvent     = regexp.MustCompile(`(?i)MIMO Event MIMO:\s*Stored MIMO=(-?\d+)\s+post cfg file MIMO=(-?\d+)`)
	rebootReason  = regexp.MustCompile(`(?i)CM Reboot Reason\s*:\s*([A-Za-z0-9_]+)`)
	dhcpEvent     = regexp.MustCompile(`(?i)^DHCP\s+(Renew|Rebind|Discover|Request|Failed)`)
	spaces        = regexp.MustCompile(`\s+`)
)

var rebootReasons = map[string]string{
	"POWER_ON":          "power on",
	"SW_UPGRADE_REBOOT": "software upgrade",
}

func severityOf(priority, typ string) Severity {
	for _, raw := range []string{priority, typ} {
		text := strings.ToLower(strings.TrimSpace(raw))
		switch text {
		case "emergency", "alert", "critical":
			return SevCritical
		case "error":
			return SevError
		case "warning", "warn":
			return SevWarning
		case "notice":
			return SevNotice
		case "info", "information", "informational", "debug":
			return SevInfo
		}
		// DOCSIS event priorities as numbers: 1 emergency .. 8 debug.
		if n, err := strconv.Atoi(text); err == nil && n >= 1 && n <= 8 {
			switch {
			case n <= 3:
				return SevCritical
			case n == 4:
				return SevError
			case n == 5:
				return SevWarning
			case n == 6:
				return SevNotice
			default:
				return SevInfo
			}
		}
	}
	return SevInfo
}

var timeLayouts = []string{
	"01/02/06 15:04:05",
	"2006-01-02, 15:04:05",
	"2006-01-02 15:04:05",
	"Mon Jan 2 15:04:05 2006",
}

func friendlyTime(raw string, now time.Time) (when string, unset bool) {
	raw = strings.TrimSpace(raw)
	for _, layout := range timeLayouts {
		t, err := time.Parse(layout, raw)
		if err != nil {
			continue
		}
		// Before the modem has the time of day it logs the epoch (shown as
		// 12/31/69 or 1/1/70 depending on the time zone).
		if t.Year() <= 1970 {
			return "", true
		}
		if t.Year() != now.Year() {
			return t.Format("Jan 02 2006 15:04"), false
		}
		return t.Format("Jan 02 15:04"), false
	}
	return raw, false
}

// Friendly decodes one log entry.
func (e LogEntry) Friendly() FriendlyEvent {
	return e.friendlyAt(time.Now())
}

func (e LogEntry) friendlyAt(now time.Time) FriendlyEvent {
	message := strings.TrimSpace(macTrailer.ReplaceAllString(e.Event, ""))
	message = strings.TrimRight(spaces.ReplaceAllString(message, " "), "; ")
	when, unset := friendlyTime(e.Time, now)
	return FriendlyEvent{
		When:       when,
		ClockUnset: unset,
		Severity:   severityOf(e.Priority, e.Type),
		Summary:    summarize(message),
		Message:    message,
	}
}

func summarize(msg string) string {
	if m := profileChange.FindStringSubmatch(msg); m != nil {
		return fmt.Sprintf("%s ch %s profile %s → %s", strings.ToUpper(m[1]), m[2], strings.TrimSpace(m[3]), strings.TrimSpace(m[4]))
	}
	if m := cmStatus.FindStringSubmatch(msg); m != nil {
		code, _ := strconv.Atoi(m[1])
		name, ok := cmStatusNames[code]
		if !ok {
			name = fmt.Sprintf("CM-STATUS code %d", code)
		}
		out := name
		if ch := strings.TrimSpace(m[2]); ch != "" && !strings.EqualFold(ch, "N/A") {
			out += " · ch " + ch
		}
		if p := strings.TrimSpace(m[3]); p != "" && !strings.EqualFold(p, "N/A") {
			out += " · profile " + p
		}
		return out
	}
	if m := rebootReason.FindStringSubmatch(msg); m != nil {
		reason, ok := rebootReasons[strings.ToUpper(m[1])]
		if !ok {
			reason = strings.ToLower(strings.ReplaceAll(m[1], "_", " "))
		}
		return "Reboot · " + reason
	}
	if m := mimoEvent.FindStringSubmatch(msg); m != nil {
		return fmt.Sprintf("MIMO setting: stored %s, config file %s", m[1], m[2])
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "t3 time-out") || strings.Contains(lower, "t3 timeout"):
		return "Ranging timed out (T3)"
	case strings.Contains(lower, "t4 time out") || strings.Contains(lower, "t4 time-out") || strings.Contains(lower, "t4 timeout"):
		return "Maintenance timed out (T4)"
	case strings.Contains(lower, "lost mdd timeout"):
		return "Lost downstream MDD (timeout)"
	case strings.Contains(lower, "sync timing synchronization failure"):
		return "SYNC timing failure"
	case strings.Contains(lower, "rng-rsp") && strings.Contains(lower, "commanded power") && strings.Contains(lower, "drw"):
		return "Ranging: CCAP power command beyond the DRW"
	case strings.Contains(lower, "reg-rsp-mp") && strings.Contains(lower, "p1.6hi"):
		return "Registration: P1.6hi differs from CCAP value"
	case strings.Contains(lower, "sw download init"):
		return "Firmware download started"
	case strings.Contains(lower, "sw download successful"):
		return "Firmware download finished"
	}
	if m := dhcpEvent.FindStringSubmatch(msg); m != nil {
		out := "DHCP " + strings.ToLower(m[1])
		if strings.Contains(lower, "tftp file") && strings.Contains(lower, "modified") {
			out += " · config file path changed"
		}
		return out
	}
	return msg
}
