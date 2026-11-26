package hitron

import (
	"regexp"
	"strings"
)

// DOCSIS event-log messages end with "CM-MAC=..;CMTS-MAC=..;CM-QOS=..;CM-VER=..;".
// The device API has no CMTS MAC field, so the log is the only place it shows.
var cmtsMacPattern = regexp.MustCompile(`(?i)CMTS-MAC=((?:[0-9a-f]{2}:){5}[0-9a-f]{2})`)

// CMTSMacFromLogs returns the CMTS MAC from the newest log entry that names a
// real one, upper-cased, or "" when no entry does. Entries logged before the
// modem finds a CMTS carry an all-zero MAC, which is skipped. Logs are newest
// first, as the firmware lists them.
func CMTSMacFromLogs(logs []LogEntry) string {
	for _, entry := range logs {
		m := cmtsMacPattern.FindStringSubmatch(entry.Event)
		if m == nil {
			continue
		}
		mac := strings.ToUpper(m[1])
		if mac == "00:00:00:00:00:00" {
			continue
		}
		return mac
	}
	return ""
}
