package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var leaseParts = regexp.MustCompile(`(?i)\b([DHMS])\s*:\s*(\d+)`)

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// humanDuration turns a number of seconds into "5 days", "1 day 12 h",
// "6 h 30 m" or "45 s": the two largest non-zero units at most.
func humanDuration(total int) string {
	if total <= 0 {
		return "0 s"
	}
	days, rest := total/86400, total%86400
	hours, rest := rest/3600, rest%3600
	mins, secs := rest/60, rest%60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%s %d h", plural(days, "day"), hours)
	case days > 0:
		return plural(days, "day")
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%d h %d m", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%d h", hours)
	case mins > 0 && secs > 0 && mins < 10:
		return fmt.Sprintf("%d m %d s", mins, secs)
	case mins > 0:
		return fmt.Sprintf("%d m", mins)
	default:
		return fmt.Sprintf("%d s", secs)
	}
}

// friendlyLease cleans the modem's lease text. It understands "D: 05 H: 00 M:
// 00 S: 00" and a plain number of seconds; anything else is returned as-is.
func friendlyLease(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	if matches := leaseParts.FindAllStringSubmatch(raw, -1); len(matches) > 0 {
		total := 0
		for _, m := range matches {
			n, _ := strconv.Atoi(m[2])
			switch strings.ToUpper(m[1]) {
			case "D":
				total += n * 86400
			case "H":
				total += n * 3600
			case "M":
				total += n * 60
			case "S":
				total += n
			}
		}
		return humanDuration(total)
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return humanDuration(n)
	}
	return raw
}
