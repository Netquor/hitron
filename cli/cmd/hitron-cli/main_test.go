package main

import (
	"testing"
	"time"
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
