package hitron

import (
	"testing"
)

func TestNumHandlesFirmwareSpellings(t *testing.T) {
	cases := []struct {
		in    any
		want  float64
		valid bool
	}{
		{"3.4", 3.4, true},
		{" 3.4 dBmV ", 3.4, true},
		{`"8.098"`, 8.098, true},
		{"-inf", -99, true},
		{"n/a", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := num(tc.in)
		if ok != tc.valid || (ok && got != tc.want) {
			t.Fatalf("num(%v) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.valid)
		}
	}
	if _, ok := signal("-inf"); ok {
		t.Fatal("signal(-inf) must be an absent reading, not -99 dBmV")
	}
}

func TestCounterParsesBothEncodings(t *testing.T) {
	cases := map[any]uint64{
		"34 * 2e32   1203911172": 1203911172,
		"1,203,911,172":          1203911172,
		"120":                    120,
	}
	for in, want := range cases {
		got, ok := counter(in)
		if !ok || got != want {
			t.Fatalf("counter(%v) = %v, %v; want %v", in, got, ok, want)
		}
	}
}

func TestUptimeForms(t *testing.T) {
	cases := map[any]int64{
		"134h:54m:33s":     134*3600 + 54*60 + 33,
		"2 days, 03:04:05": 2*86400 + 3*3600 + 4*60 + 5,
		"03:04:05":         3*3600 + 4*60 + 5,
		"586431":           586431,
	}
	for in, want := range cases {
		got, ok := uptime(in)
		if !ok || got != want {
			t.Fatalf("uptime(%v) = %v, %v; want %v", in, got, ok, want)
		}
	}
}

func TestFlagAcceptsProvisioningWords(t *testing.T) {
	for _, value := range []string{"Completed", "Completed", "Authorized", "Enabled", "Up"} {
		if ok, valid := flag(value); !ok || !valid {
			t.Fatalf("flag(%q) should be true", value)
		}
	}
	for _, value := range []string{"Failed", "Down", "Disabled", "No"} {
		if ok, valid := flag(value); ok || !valid {
			t.Fatalf("flag(%q) should be false", value)
		}
	}
}

func TestFirstIPv4PicksV4FromMixedField(t *testing.T) {
	got := firstIPv4("2600:1700:1a2b:3c4d::5a1b / 73.15.42.18")
	if got != "73.15.42.18" {
		t.Fatalf("firstIPv4 = %q", got)
	}
}

func TestMocaDecoders(t *testing.T) {
	if got := mocaVersion(0x25); got != "2.5" {
		t.Fatalf("mocaVersion = %q", got)
	}
	if got := hexMAC(0xC03F0E5A, 0x1B2C0000); got != "c0:3f:0e:5a:1b:2c" {
		t.Fatalf("hexMAC = %q", got)
	}
	if got := int2ip(0x490F2A12); got != "73.15.42.18" {
		t.Fatalf("int2ip = %q", got)
	}
}

func TestSpecChecksSkipDeadChannels(t *testing.T) {
	snapshot := &Snapshot{
		DS: []Channel{
			{Power: 3.4, HasPower: true},
			{Power: -99, HasPower: false},
		},
		US: []Channel{{Power: 44.5, HasPower: true}},
	}
	if ok, low, high := snapshot.DSInSpec(); !ok || low != 3.4 || high != 3.4 {
		t.Fatalf("DSInSpec = %v, %v, %v", ok, low, high)
	}
	if ok, low, high := snapshot.USInSpec(); !ok || low != 44.5 || high != 44.5 {
		t.Fatalf("USInSpec = %v, %v, %v", ok, low, high)
	}
}
