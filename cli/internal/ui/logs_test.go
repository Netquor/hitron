package ui

import (
	"strings"
	"testing"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

func TestLogsFriendlyTable(t *testing.T) {
	old := Color
	Color = false
	defer func() { Color = old }()
	tail := ";CM-MAC=b0:f5:30:da:e6:a0;CMTS-MAC=00:90:f0:78:00:00;CM-QOS=1.1;CM-VER=4.0;"
	snap := &hitron.Snapshot{Logs: []hitron.LogEntry{
		{Time: "11/26/26 09:19:38", Type: "67061601", Priority: "notice", Event: "US profile assignment change.  US Chan ID: 42; Previous Profile: 9; New Profile: 6." + tail},
		{Time: "12/31/69 18:00:41", Type: "90000006", Priority: "notice", Event: "CM Reboot Reason : POWER_ON" + tail},
		{Time: "11/18/26 05:31:58", Type: "82000200", Priority: "critical", Event: "No Ranging Response received - T3 time-out" + tail},
	}}
	var b strings.Builder
	Logs(&b, snap, 10)
	out := b.String()
	t.Log("\n" + out)
	for _, want := range []string{"US ch 42 profile 9 → 6", "Reboot · power on", "Ranging timed out (T3)", "CRIT", "no clock"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "CM-MAC") || strings.Contains(out, "67061601") {
		t.Error("raw trailer or event id leaked into the table")
	}
}
