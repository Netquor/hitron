package hitron

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// The firmware is wildly inconsistent about types: nearly every number is a
// quoted string, counters arrive as "34 * 2e32   1203911172", flags are
// "YES"/"1"/"OPERATE", and an unconnected channel can report "-inf". These
// helpers are the only place that mess is interpreted.

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asSlice(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	return nil
}

func str(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(strings.Trim(value, `"`))
	case float64:
		if value == math.Trunc(value) && math.Abs(value) < 1e15 {
			return strconv.FormatInt(int64(value), 10)
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	case json.Number:
		return value.String()
	case bool:
		if value {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

func num(v any) (float64, bool) {
	text := strings.ToLower(str(v))
	if text == "" {
		return 0, false
	}
	if text == "-inf" || text == "-infinity" {
		// The exporter maps this sentinel to -99 dBmV so downstream math keeps
		// working; the CLI keeps the same convention and flags it in warnings.
		return -99, true
	}
	if text == "n/a" || text == "na" || text == "--" {
		return 0, false
	}
	cleaned := strings.TrimSuffix(text, "dbmv")
	cleaned = strings.TrimSuffix(cleaned, "db")
	cleaned = strings.TrimSpace(cleaned)
	value, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, false
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

var lastInteger = regexp.MustCompile(`(\d[\d,]*)\s*$`)

// counter parses the two counter spellings seen in the field: a plain decimal
// (sometimes comma-grouped) and the CODA6X "34 * 2e32   1203911172" form,
// where the trailing word is the actual value.
func counter(v any) (uint64, bool) {
	if f, ok := v.(float64); ok && f >= 0 {
		return uint64(f), true
	}
	text := str(v)
	if text == "" {
		return 0, false
	}
	match := lastInteger.FindStringSubmatch(text)
	if match == nil {
		return 0, false
	}
	value, err := strconv.ParseUint(strings.ReplaceAll(match[1], ",", ""), 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func flag(v any) (bool, bool) {
	text := strings.ToLower(str(v))
	switch text {
	case "", "n/a":
		return false, false
	case "1", "on", "yes", "true", "success", "up", "operate", "operating",
		"locked", "allowed", "enabled", "enable", "ok", "operational",
		"completed", "complete", "registered", "bound", "authorized",
		"auth:authorized":
		return true, true
	case "0", "off", "no", "false", "fail", "failed", "down", "none",
		"disabled", "disable", "error":
		return false, true
	}
	return false, false
}

var (
	uptimeHM     = regexp.MustCompile(`(?i)(\d+)\s*h[:\s]\s*(\d+)\s*m[:\s]\s*(\d+)\s*s`)
	uptimeDays   = regexp.MustCompile(`(?i)(\d+)\s*days?[,\s]+(\d+):(\d+):(\d+)`)
	uptimeColon  = regexp.MustCompile(`^(\d+):(\d+):(\d+)$`)
	uptimeDigits = regexp.MustCompile(`^(\d+)$`)
)

// uptime parses "134h:54m:33s", "2 days, 03:04:05", "03:04:05", or a bare
// number of seconds.
func uptime(v any) (int64, bool) {
	text := strings.TrimSpace(str(v))
	if text == "" {
		return 0, false
	}
	if m := uptimeHM.FindStringSubmatch(text); m != nil {
		h, _ := strconv.ParseInt(m[1], 10, 64)
		min, _ := strconv.ParseInt(m[2], 10, 64)
		sec, _ := strconv.ParseInt(m[3], 10, 64)
		return h*3600 + min*60 + sec, true
	}
	if m := uptimeDays.FindStringSubmatch(text); m != nil {
		days, _ := strconv.ParseInt(m[1], 10, 64)
		h, _ := strconv.ParseInt(m[2], 10, 64)
		min, _ := strconv.ParseInt(m[3], 10, 64)
		sec, _ := strconv.ParseInt(m[4], 10, 64)
		return days*86400 + h*3600 + min*60 + sec, true
	}
	if m := uptimeColon.FindStringSubmatch(text); m != nil {
		h, _ := strconv.ParseInt(m[1], 10, 64)
		min, _ := strconv.ParseInt(m[2], 10, 64)
		sec, _ := strconv.ParseInt(m[3], 10, 64)
		return h*3600 + min*60 + sec, true
	}
	if m := uptimeDigits.FindStringSubmatch(text); m != nil {
		value, _ := strconv.ParseInt(m[1], 10, 64)
		return value, true
	}
	return 0, false
}

// h2i decodes the MoCA API's "0x1234abcd" words.
func h2i(value string) uint64 {
	text := strings.TrimSpace(value)
	text = strings.TrimPrefix(strings.ToLower(text), "0x")
	parsed, err := strconv.ParseUint(text, 16, 32)
	if err != nil {
		return 0
	}
	return parsed & 0xFFFFFFFF
}

func mocaVersion(word uint64) string {
	return strconv.FormatUint((word>>4)&0xF, 10) + "." + strconv.FormatUint(word&0xF, 10)
}

func hexMAC(hi, lo uint64) string {
	bytes := []uint64{
		(hi >> 24) & 0xFF, (hi >> 16) & 0xFF, (hi >> 8) & 0xFF, hi & 0xFF,
		(lo >> 24) & 0xFF, (lo >> 16) & 0xFF,
	}
	out := make([]string, 0, len(bytes))
	const hexdigits = "0123456789abcdef"
	for _, b := range bytes {
		out = append(out, string([]byte{hexdigits[b>>4], hexdigits[b&0xF]}))
	}
	return strings.Join(out, ":")
}

func int2ip(value uint64) string {
	return strconv.FormatUint((value>>24)&0xFF, 10) + "." +
		strconv.FormatUint((value>>16)&0xFF, 10) + "." +
		strconv.FormatUint((value>>8)&0xFF, 10) + "." +
		strconv.FormatUint(value&0xFF, 10)
}

func byte2ascii(value uint64) string {
	var b strings.Builder
	for i := 3; i >= 0; i-- {
		c := byte((value >> (uint(i) * 8)) & 0xFF)
		if c < 0x20 || c >= 0x7F {
			break
		}
		b.WriteByte(c)
	}
	return b.String()
}

// signal is num for an RF reading. A "-inf" is a channel with no reading, not
// a -99 dBmV measurement, so it must not drag a spec check out of range.
func signal(v any) (float64, bool) {
	text := strings.ToLower(str(v))
	if text == "-inf" || text == "-infinity" {
		return 0, false
	}
	return num(v)
}

// firstNonEmpty returns the first string that is not blank.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// firstIPv4 picks an IPv4 address out of fields that sometimes carry an IPv6
// address and an IPv4 address separated by a slash ("2600:... / 73.15.42.18").
func firstIPv4(values ...string) string {
	for _, value := range values {
		for _, token := range strings.FieldsFunc(value, func(r rune) bool {
			return r == '/' || r == ',' || r == ' ' || r == '\t'
		}) {
			token = strings.TrimSpace(token)
			parts := strings.Split(token, ".")
			if len(parts) != 4 {
				continue
			}
			allDigits := true
			for _, part := range parts {
				if part == "" {
					allDigits = false
					break
				}
				if _, err := strconv.ParseUint(part, 10, 8); err != nil {
					allDigits = false
					break
				}
			}
			if allDigits {
				return token
			}
		}
	}
	return ""
}
