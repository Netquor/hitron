package hitron

import (
	"testing"
	"time"
	"unicode/utf8"
)

func day(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// TestPWODPasswordMatchesTheFirmware pins the implementation to vectors from
// work/pwod.py, which was written from libpodalgorithm and self-tested there.
func TestPWODPasswordMatchesTheFirmware(t *testing.T) {
	cases := []struct {
		seed string
		date string
		want string
	}{
		{"", "2026-11-26", "u29a6p81fi4x383q"},
		{"", "2024-02-29", "7Ea8LlrvCoprgb7x"},
		{"", "2026-12-31", "tz0sz34gr26rz57u"},
		{"12345678", "2026-11-26", "8bci6yucguxoE22s"},
		{"0123456789abcdefg", "2026-01-01", "Seg0O14dF979bbn9"},
		{"0123456789abcdef", "2026-01-01", "a7oxMseuqfiJq04f"},
		{"0123456789abcde", "2026-01-01", "si70lZezuHfiDvjh"},
		{"café", "2026-01-01", "fglglt4e96w48oZg"},
	}
	for _, c := range cases {
		got, err := PWODPassword(c.seed, day(t, c.date))
		if err != nil {
			t.Fatalf("%q/%s: %v", c.seed, c.date, err)
		}
		if got != c.want {
			t.Errorf("PWODPassword(%q, %s) = %q, want %q", c.seed, c.date, got, c.want)
		}
		if len(got) != 16 {
			t.Errorf("%q/%s is %d characters, want 16", c.seed, c.date, len(got))
		}
	}
}

func TestPWODCharacterMapCoversEveryByte(t *testing.T) {
	// One row per 16 consecutive byte values, produced by the reference
	// charset_map in work/pwod.py.
	want := []string{
		"0123456789012345",
		"6789012345678901",
		"2345678901234567",
		"0123456789ABCDEF",
		"GABCDEFGHIJKLMNO",
		"PQRSTUVWXYZnLpJr",
		"Habcdefghijklmno",
		"pqrstuvwxyztuvwx",
		"yzabcdefghijklmn",
		"opqrstuvwxyzabcd",
		"efghijklmnopqrst",
		"uvwxyzabcdefghij",
		"klmnopqrstuvwxyz",
		"abcdefghijklmnop",
		"qrstuvwxyzabcdef",
		"ghijklmnopqrstuv",
	}
	for row, expected := range want {
		digest := make([]byte, 16)
		for i := range digest {
			digest[i] = byte(row*16 + i)
		}
		if got := pwodCharset(digest); got != expected {
			t.Errorf("bytes %d-%d: got %q, want %q", row*16, row*16+15, got, expected)
		}
	}
}

func TestPWODSeedCodec(t *testing.T) {
	cases := []struct {
		seed string
		enc  string
	}{
		{"12345678", "i19WhrZqaiiNNVC+oWsv3Q=="},
		{"0123456789abcdef", "OnNpi82eBdOs0w/V5J1vlg=="},
		{"0123456789abcdefg", "OnNpi82eBdOs0w/V5J1vlma3rYX7Bihpfo9T83T3UEQ="},
		{"0123456789abcde", "ZdEjjSkf0d+71GM6uL6UYg=="},
		{"", ""},
	}
	for _, c := range cases {
		got, err := PWODEncryptSeed(c.seed)
		if err != nil {
			t.Fatalf("%q: %v", c.seed, err)
		}
		if got != c.enc {
			t.Errorf("PWODEncryptSeed(%q) = %q, want %q", c.seed, got, c.enc)
		}
	}
}

func TestPWODSeedDecrypt(t *testing.T) {
	cases := []struct {
		enc  string
		want string
	}{
		{"i19WhrZqaiiNNVC+oWsv3Q==", "12345678"},
		{"OnNpi82eBdOs0w/V5J1vlma3rYX7Bihpfo9T83T3UEQ=", "0123456789abcdefg"},
		{"ZdEjjSkf0d+71GM6uL6UYg==", "0123456789abcde"},
		// Whitespace around the stored value comes from a config dump.
		{"  i19WhrZqaiiNNVC+oWsv3Q==\n", "12345678"},
	}
	for _, c := range cases {
		got, err := PWODDecryptSeed(c.enc)
		if err != nil {
			t.Fatalf("%q: %v", c.enc, err)
		}
		if got != c.want {
			t.Errorf("PWODDecryptSeed(%q) = %q, want %q", c.enc, got, c.want)
		}
	}
}

// TestPWODDecryptKeepsTheAlignedSeedQuirk documents firmware behaviour rather
// than endorsing it: a block-aligned seed is stored without padding, and the
// strlen-style unpad still reads its last byte as a pad length.
func TestPWODDecryptKeepsTheAlignedSeedQuirk(t *testing.T) {
	got, err := PWODDecryptSeed("OnNpi82eBdOs0w/V5J1vlg==")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("aligned seed decoded to %q, want the firmware's empty result", got)
	}
}

func TestPWODDecryptRejectsBadBase64(t *testing.T) {
	if _, err := PWODDecryptSeed("not base64!"); err == nil {
		t.Fatal("invalid base64 should fail")
	}
}

func TestPWODSeedMustBeBytes(t *testing.T) {
	if _, err := PWODPassword("caf\u2603", day(t, "2026-01-01")); err == nil {
		t.Fatal("a rune above U+00FF should fail")
	}
	if _, err := PWODEncryptSeed("caf\u2603"); err == nil {
		t.Fatal("a rune above U+00FF should fail to encrypt")
	}
}

func TestPWODUsesOnlyTheDate(t *testing.T) {
	date := day(t, "2026-11-26")
	same := date.Add(18 * time.Hour)
	first, err := PWODPassword("12345678", date)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PWODPassword("12345678", same)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("time of day changed the password: %q vs %q", first, second)
	}
}

func TestPWODRoundTrip(t *testing.T) {
	for _, seed := range []string{"12345678", "0123456789abcde", "s"} {
		encrypted, err := PWODEncryptSeed(seed)
		if err != nil {
			t.Fatal(err)
		}
		got, err := PWODDecryptSeed(encrypted)
		if err != nil {
			t.Fatal(err)
		}
		if got != seed {
			t.Errorf("round trip %q = %q", seed, got)
		}
	}
}

func TestLatin1StringPreservesBytes(t *testing.T) {
	raw := []byte{'a', 0xe9, 'z'}
	got := latin1String(raw)
	if got != "aéz" {
		t.Fatalf("latin1String = %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatal("latin1String produced invalid UTF-8")
	}
}
