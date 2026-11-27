package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Netquor/hitron/cli/internal/hitron"
)

func TestParseHostAndCommand(t *testing.T) {
	opts, err := parseArgs([]string{"192.168.1.1", "channels", "--no-color"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "channels" || opts.host != "192.168.1.1" || !opts.noColor {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseRejectsSecondHost(t *testing.T) {
	if _, err := parseArgs([]string{"--host", "10.0.0.1", "192.168.1.1"}); err == nil {
		t.Fatal("two hosts should be a usage error")
	}
}

func TestParseDurations(t *testing.T) {
	opts, err := parseArgs([]string{"monitor", "--interval", "3s", "--timeout", "2.5"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.interval != 3*time.Second || opts.timeout != 2500*time.Millisecond {
		t.Fatalf("interval/timeout = %v/%v", opts.interval, opts.timeout)
	}
}

func TestParseAliases(t *testing.T) {
	opts, err := parseArgs([]string{"status", "-H", "192.168.0.1", "-u", "a", "-p", "b", "-y"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "stats" || !opts.confirm || opts.user != "a" || opts.password != "b" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseRejectsUnknownFlag(t *testing.T) {
	if _, err := parseArgs([]string{"--nope"}); err == nil {
		t.Fatal("unknown flag should fail")
	}
}

func TestParseHelpTopic(t *testing.T) {
	opts, err := parseArgs([]string{"help", "auth"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "help" || opts.topic != "auth" {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseJSONForcesOneShotMonitor(t *testing.T) {
	opts, err := parseArgs([]string{"monitor", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.once {
		t.Fatal("--json monitor must not try to open a TUI")
	}
}

func TestParseFECReset(t *testing.T) {
	opts, err := parseArgs([]string{"fec-reset", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "fec-reset" || !opts.json {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.user != "" || opts.password != "" {
		t.Fatalf("fec-reset must not require credentials, opts = %+v", opts)
	}
}

func TestParsePWOD(t *testing.T) {
	opts, err := parseArgs([]string{"pwod", "--seed", "12345678", "--date", "2026-11-26", "--days", "3", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "pwod" || opts.seed != "12345678" || opts.date != "2026-11-26" || opts.days != 3 || !opts.json || !opts.seedSet {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.user != "" || opts.password != "" {
		t.Fatalf("pwod must not require credentials, opts = %+v", opts)
	}
	alias, err := parseArgs([]string{"pod", "--seed", "x"})
	if err != nil || alias.command != "pwod" {
		t.Fatalf("pod alias = %+v, %v", alias, err)
	}
}

func TestParsePWODCodec(t *testing.T) {
	opts, err := parseArgs([]string{"pwod", "--encrypted-seed", "i19WhrZqaiiNNVC+oWsv3Q=="})
	if err != nil {
		t.Fatal(err)
	}
	if opts.encryptedSeed != "i19WhrZqaiiNNVC+oWsv3Q==" || opts.seed != "" || !opts.encryptedSet {
		t.Fatalf("opts = %+v", opts)
	}
	if _, err := parseArgs([]string{"pwod", "--days", "0"}); err == nil {
		t.Fatal("--days 0 should be a usage error")
	}
}

func TestRunPWODJSON(t *testing.T) {
	body := captureStdout(t, func() {
		if code := runPWOD(options{seed: "12345678", date: "2026-11-26", days: 2, json: true}); code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	})
	var payload struct {
		Seed      string `json:"seed"`
		Passwords []struct {
			Date     string `json:"date"`
			Password string `json:"password"`
		} `json:"passwords"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("bad JSON %q: %v", body, err)
	}
	if payload.Seed != "12345678" || len(payload.Passwords) != 2 {
		t.Fatalf("payload = %+v (%s)", payload, body)
	}
	if payload.Passwords[0].Date != "2026-11-26" || payload.Passwords[0].Password != "8bci6yucguxoE22s" {
		t.Fatalf("first day = %+v", payload.Passwords[0])
	}
	if payload.Passwords[1].Date != "2026-11-27" {
		t.Fatalf("second day = %+v", payload.Passwords[1])
	}
}

func TestRunPWODPlainText(t *testing.T) {
	body := captureStdout(t, func() {
		if code := runPWOD(options{date: "2026-11-26", days: 1}); code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	})
	if body != "2026-11-26  u29a6p81fi4x383q\n" {
		t.Fatalf("output = %q", body)
	}
}

func TestRunPWODSeedCodec(t *testing.T) {
	body := captureStdout(t, func() {
		if code := runPWOD(options{encryptSeed: "12345678", encryptSet: true}); code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	})
	if body != "i19WhrZqaiiNNVC+oWsv3Q==\n" {
		t.Fatalf("encrypt output = %q", body)
	}
	body = captureStdout(t, func() {
		if code := runPWOD(options{decryptSeed: "i19WhrZqaiiNNVC+oWsv3Q==", decryptSet: true}); code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	})
	if body != "12345678\n" {
		t.Fatalf("decrypt output = %q", body)
	}
	// An empty seed is a valid input, not an absent flag.
	body = captureStdout(t, func() {
		if code := runPWOD(options{encryptSeed: "", encryptSet: true}); code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	})
	if body != "\n" {
		t.Fatalf("empty encrypt output = %q", body)
	}
}

func TestRunPWODRejectsMixedInputs(t *testing.T) {
	if code := runPWOD(options{seed: "a", seedSet: true, encryptedSeed: "b", encryptedSet: true}); code != 2 {
		t.Fatalf("exit code = %d, want a usage error", code)
	}
	if code := runPWOD(options{encryptSeed: "a", encryptSet: true, seed: "b", seedSet: true}); code != 2 {
		t.Fatalf("exit code = %d, want a usage error", code)
	}
	if code := runPWOD(options{date: "2026-13-01"}); code != 2 {
		t.Fatalf("exit code = %d for a bad date", code)
	}
}

// TestRunFECResetJSON exercises the verb end to end against a fake device: no
// credentials, one PUT per counter family, and a JSON body a script can read.
func TestRunFECResetJSON(t *testing.T) {
	dsBusy, ofdmBusy := true, true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/1/Device/CM/DsInfo":
			if r.Method == http.MethodPut {
				dsBusy = false
				_, _ = w.Write([]byte(`{"errCode":"000","errMsg":""}`))
				return
			}
			count := 0
			if dsBusy {
				count = 12
			}
			_, _ = w.Write([]byte(`{"errCode":"000","Freq_List":[{"channelId":"1","frequency":"267000000","correcteds":"` + strconv.Itoa(count) + `","uncorrect":"0"}]}`))
		case "/1/Device/CM/DsOfdm":
			if r.Method == http.MethodPut {
				ofdmBusy = false
				_, _ = w.Write([]byte(`{"errCode":"000","errMsg":""}`))
				return
			}
			count := 0
			if ofdmBusy {
				count = 7
			}
			_, _ = w.Write([]byte(`{"errCode":"000","Freq_List":[{"index":1,"Subcarr0freqFreq":"296600000","correcteds":"` + strconv.Itoa(count) + `","uncorrect":"0"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := hitron.NewClient(server.URL, hitron.Credentials{}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client.Kind = hitron.KindCoda6X

	body := captureStdout(t, func() {
		if code := runFECReset(context.Background(), client, options{timeout: 2 * time.Second, json: true}); code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	})

	var payload struct {
		Action string `json:"action"`
		DOCSIS struct {
			Busy    int `json:"busy"`
			Cleared int `json:"cleared"`
		} `json:"docsis"`
		OFDM struct {
			Busy          int      `json:"busy"`
			Cleared       int      `json:"cleared"`
			StillCounting []string `json:"still_counting"`
		} `json:"ofdm"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("bad JSON %q: %v", body, err)
	}
	if payload.Action != "fec-reset" || payload.DOCSIS.Cleared != 1 || payload.OFDM.Cleared != 1 {
		t.Fatalf("payload = %+v (%s)", payload, body)
	}
	if payload.OFDM.StillCounting == nil || len(payload.OFDM.StillCounting) != 0 {
		t.Fatalf("still_counting should be an empty array, got %#v", payload.OFDM.StillCounting)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	defer func() { os.Stdout = original }()
	fn()
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
