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
