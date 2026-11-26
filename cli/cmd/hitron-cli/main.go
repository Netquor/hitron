package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Netquor/hitron/cli/internal/hitron"
	"github.com/Netquor/hitron/cli/internal/ui"
)

// version is replaced at build time with -X main.version=...
var version = "0.1.1"

type options struct {
	host     string
	user     string
	password string
	token    string
	timeout  time.Duration
	interval time.Duration
	limit    int

	json     bool
	noColor  bool
	confirm  bool
	once     bool
	noBanner bool
	noMouse  bool
	theme    string

	command string
	topic   string
	hostSet bool
}

var commands = map[string]bool{
	"stats": true, "status": true,
	"monitor": true, "tui": true,
	"detect": true, "system": true, "channels": true, "logs": true, "moca": true,
	"reboot": true, "factory-reset": true, "hard-reset": true,
	"help": true, "version": true,
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	opts, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hitron-cli: "+err.Error())
		fmt.Fprintln(os.Stderr, "Try 'hitron-cli help' for usage.")
		return 2
	}
	if opts.command == "version" {
		fmt.Println("hitron-cli " + version)
		return 0
	}
	if opts.command == "help" {
		if opts.json {
			payload := map[string]string{"topic": opts.topic, "help": helpText(opts.topic)}
			writeJSON(payload)
		} else {
			fmt.Println(helpText(opts.topic))
		}
		return 0
	}

	ui.Color = !opts.noColor && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	if err := chooseTheme(opts.theme); err != nil {
		fmt.Fprintln(os.Stderr, "hitron-cli: "+err.Error())
		return 2
	}
	if !opts.json && !opts.noBanner {
		fmt.Fprintln(os.Stderr, ui.Banner(version))
		fmt.Fprintln(os.Stderr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := connect(ctx, opts)
	if err != nil {
		return fail(err)
	}

	switch opts.command {
	case "reboot", "factory-reset", "hard-reset":
		return runAction(ctx, client, opts)
	case "monitor", "tui":
		if opts.once || opts.json || !ui.IsTerminal() {
			return runSnapshot(ctx, client, opts)
		}
		return runMonitor(ctx, client, opts)
	default:
		return runSnapshot(ctx, client, opts)
	}
}

func fail(err error) int {
	var auth *hitron.AuthRequiredError
	switch {
	case errors.As(err, &auth):
		fmt.Fprintln(os.Stderr, "hitron-cli: "+auth.Error())
		fmt.Fprintln(os.Stderr, "Pass --user and --password (or set HITRON_USERNAME/HITRON_PASSWORD).")
	case errors.Is(err, hitron.ErrUnsupported):
		fmt.Fprintln(os.Stderr, "hitron-cli: unsupported: "+err.Error())
	default:
		fmt.Fprintln(os.Stderr, "hitron-cli: "+err.Error())
	}
	return 1
}

func connect(ctx context.Context, opts options) (*hitron.Client, error) {
	creds := hitron.Credentials{
		Username: opts.user,
		Password: opts.password,
		AppToken: opts.token,
	}
	client, err := hitron.Detect(ctx, opts.host, creds, opts.timeout)
	if err == nil {
		return client, nil
	}
	var auth *hitron.AuthRequiredError
	if !errors.As(err, &auth) || !ui.IsTerminal() || creds.Complete() {
		return nil, err
	}
	if creds.AppToken != "" {
		return nil, err
	}

	fmt.Fprintln(os.Stderr, "This device asks for a login.")
	username, promptErr := ui.Prompt("Username", "admin")
	if promptErr != nil {
		return nil, err
	}
	password, promptErr := ui.ReadPassword("Password: ")
	if promptErr != nil {
		return nil, err
	}
	creds = hitron.Credentials{Username: username, Password: password}
	client, err = hitron.Detect(ctx, opts.host, creds, opts.timeout)
	if err != nil {
		return nil, err
	}
	if err := client.Login(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

func snapshotWithTimeout(ctx context.Context, client *hitron.Client, timeout time.Duration) (*hitron.Snapshot, error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout*3)
	defer cancel()
	return client.Snapshot(callCtx)
}

func runSnapshot(ctx context.Context, client *hitron.Client, opts options) int {
	snap, err := snapshotWithTimeout(ctx, client, opts.timeout)
	if err != nil {
		return fail(err)
	}
	if opts.json {
		writeJSON(snap)
		return 0
	}
	switch opts.command {
	case "detect":
		fmt.Println(ui.Title(snap))
		fmt.Println()
		ui.Identity(os.Stdout, snap)
		fmt.Println()
		ui.Spec(os.Stdout, snap)
	case "system":
		fmt.Println(ui.Title(snap))
		fmt.Println()
		ui.System(os.Stdout, snap)
		ui.Warnings(os.Stdout, snap)
	case "channels":
		fmt.Println(ui.Title(snap))
		fmt.Println()
		ui.Channels(os.Stdout, snap)
		ui.Warnings(os.Stdout, snap)
	case "logs":
		fmt.Println(ui.Title(snap))
		fmt.Println()
		ui.Logs(os.Stdout, snap, opts.limit)
		ui.Warnings(os.Stdout, snap)
	case "moca":
		if snap.MoCA == nil {
			fmt.Fprintln(os.Stderr, "hitron-cli: this device is not an HT-EM5; no MoCA data")
			return 1
		}
		fmt.Println(ui.Title(snap))
		fmt.Println()
		ui.Moca(os.Stdout, snap)
		ui.Warnings(os.Stdout, snap)
	default:
		ui.All(os.Stdout, snap, opts.limit)
	}
	return 0
}

// chooseTheme picks the palette: --theme, then HITRON_THEME, then the one the
// `t` key saved last time. A bad name from the flag or environment is an error;
// a stale saved name is ignored.
func chooseTheme(flagValue string) error {
	for _, name := range []string{flagValue, os.Getenv("HITRON_THEME")} {
		if name == "" {
			continue
		}
		if !ui.SetTheme(name) {
			return fmt.Errorf("unknown theme %q (themes: %s)", name, strings.Join(ui.ThemeNames(), ", "))
		}
		return nil
	}
	if saved := ui.SavedTheme(); saved != "" {
		ui.SetTheme(saved)
	}
	return nil
}

func runMonitor(ctx context.Context, client *hitron.Client, opts options) int {
	fetch := func(runCtx context.Context) (*hitron.Snapshot, error) {
		return snapshotWithTimeout(runCtx, client, opts.timeout)
	}
	tui := ui.TUIOptions{
		Fetch:    fetch,
		Interval: opts.interval,
		Host:     client.Addr(),
		NoMouse:  opts.noMouse,
	}
	// Reboot is offered where the CLI's own reboot command works. The CODA-57
	// has no confirmed write endpoint, so it gets no button at all.
	if client.Kind != hitron.KindCoda57 {
		tui.Reboot = func(runCtx context.Context, user, pass string) (string, error) {
			// Work on a copy so the polling client's credentials never change
			// underneath an in-flight read.
			act := *client
			act.Creds = hitron.Credentials{Username: user, Password: pass}
			act.DefaultCreds = false
			if err := act.Login(runCtx); err != nil {
				return "", err
			}
			callCtx, cancel := context.WithTimeout(runCtx, opts.timeout*2)
			defer cancel()
			result, err := act.Reboot(callCtx)
			if err != nil {
				return "", err
			}
			if code := fmt.Sprint(result["errCode"]); code != "" && code != "000" && code != "<nil>" {
				return "", fmt.Errorf("device answered errCode %s %v", code, result["errMsg"])
			}
			return fmt.Sprintf("Reboot sent to %s (%s)", client.Addr(), client.Kind.Label()), nil
		}
		// A login given on the command line or in the environment is reused;
		// the confirmation still has to be typed.
		if !client.DefaultCreds && opts.user != "" && opts.password != "" {
			tui.Username, tui.Password = opts.user, opts.password
		}
	}
	if err := ui.RunTUI(ctx, tui); err != nil {
		return fail(err)
	}
	return 0
}

func runAction(ctx context.Context, client *hitron.Client, opts options) int {
	action := opts.command
	if (!client.Creds.Complete() && client.Creds.AppToken == "") || client.DefaultCreds {
		fmt.Fprintf(os.Stderr, "hitron-cli: %s needs explicit credentials; pass --user and --password (the HT-EM5 factory default is used for reads only)\n", action)
		return 2
	}
	if client.Kind == hitron.KindCoda57 {
		return fail(fmt.Errorf("%s: %w (no confirmed write endpoint)", action, hitron.ErrUnsupported))
	}
	if !opts.confirm {
		verb := map[string]string{
			"reboot":        "Reboot",
			"factory-reset": "Factory reset",
			"hard-reset":    "Hard reset and reboot",
		}[action]
		ok, err := ui.Confirm(fmt.Sprintf("%s %s at %s? This acts on the real device.", verb, client.Kind.Label(), client.Addr()))
		if err != nil {
			return fail(err)
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "aborted")
			return 1
		}
	}

	if err := client.Login(ctx); err != nil {
		return fail(err)
	}
	callCtx, cancel := context.WithTimeout(ctx, opts.timeout*2)
	defer cancel()
	var result map[string]any
	var err error
	switch action {
	case "reboot":
		result, err = client.Reboot(callCtx)
	case "factory-reset":
		result, err = client.FactoryReset(callCtx)
	case "hard-reset":
		result, err = client.HardReset(callCtx)
	}
	if err != nil {
		return fail(err)
	}
	if opts.json {
		writeJSON(map[string]any{
			"action": action,
			"host":   client.Addr(),
			"device": client.Kind.Label(),
			"result": result,
		})
		return 0
	}
	fmt.Printf("%s sent to %s (%s)\n", action, client.Addr(), client.Kind.Label())
	if code := fmt.Sprint(result["errCode"]); code != "" && code != "000" && code != "<nil>" {
		fmt.Printf("device answered errCode %s %v\n", code, result["errMsg"])
	}
	return 0
}

func writeJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, "hitron-cli: "+err.Error())
	}
}

func parseArgs(argv []string) (options, error) {
	opts := options{
		host:     firstNonEmpty(os.Getenv("HITRON_HOST"), os.Getenv("HITRON_DEVICE"), "192.168.100.1"),
		user:     firstNonEmpty(os.Getenv("HITRON_USERNAME"), os.Getenv("HITRON_USER")),
		password: firstNonEmpty(os.Getenv("HITRON_PASSWORD"), os.Getenv("HITRON_PASS")),
		token:    os.Getenv("HITRON_APP_TOKEN"),
		timeout:  10 * time.Second,
		interval: 5 * time.Second,
		limit:    20,
	}
	if value := os.Getenv("HITRON_TIMEOUT"); value != "" {
		if seconds, err := strconv.ParseFloat(value, 64); err == nil {
			opts.timeout = time.Duration(seconds * float64(time.Second))
		}
	}
	if value := os.Getenv("HITRON_INTERVAL"); value != "" {
		if parsed, err := parseInterval(value); err == nil {
			opts.interval = parsed
		}
	}

	positional := []string{}
	for index := 0; index < len(argv); index++ {
		arg := argv[index]
		if arg == "--" {
			positional = append(positional, argv[index+1:]...)
			break
		}
		if strings.HasPrefix(arg, "--") {
			name, value, hasValue := splitFlag(arg)
			takeValue := func() (string, error) {
				if hasValue {
					return value, nil
				}
				index++
				if index >= len(argv) {
					return "", fmt.Errorf("%s needs a value", name)
				}
				return argv[index], nil
			}
			switch name {
			case "--host", "--device":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.host = got
				opts.hostSet = true
			case "--user", "--username":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.user = got
			case "--password":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.password = got
			case "--token":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.token = got
			case "--timeout":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				parsed, err := parseInterval(got)
				if err != nil {
					return opts, fmt.Errorf("--timeout: %w", err)
				}
				opts.timeout = parsed
			case "--interval":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				parsed, err := parseInterval(got)
				if err != nil {
					return opts, fmt.Errorf("--interval: %w", err)
				}
				opts.interval = parsed
			case "--limit":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				parsed, err := strconv.Atoi(got)
				if err != nil || parsed < 0 {
					return opts, fmt.Errorf("--limit needs a non-negative number")
				}
				opts.limit = parsed
			case "--json":
				opts.json = true
			case "--no-color":
				opts.noColor = true
			case "--confirm":
				opts.confirm = true
			case "--once":
				opts.once = true
			case "--no-banner":
				opts.noBanner = true
			case "--no-mouse":
				opts.noMouse = true
			case "--theme":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.theme = got
			case "--help":
				opts.command = "help"
			case "--version":
				opts.command = "version"
			default:
				return opts, fmt.Errorf("unknown flag %s", name)
			}
			continue
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			switch arg {
			case "-h":
				opts.command = "help"
			case "-V":
				opts.command = "version"
			case "-H":
				index++
				if index >= len(argv) {
					return opts, fmt.Errorf("-H needs a host")
				}
				opts.host = argv[index]
				opts.hostSet = true
			case "-u":
				index++
				if index >= len(argv) {
					return opts, fmt.Errorf("-u needs a username")
				}
				opts.user = argv[index]
			case "-p":
				index++
				if index >= len(argv) {
					return opts, fmt.Errorf("-p needs a password")
				}
				opts.password = argv[index]
			case "-y":
				opts.confirm = true
			default:
				return opts, fmt.Errorf("unknown flag %s", arg)
			}
			continue
		}
		positional = append(positional, arg)
	}

	for _, arg := range positional {
		if opts.command == "" && commands[arg] {
			opts.command = arg
			continue
		}
		if opts.command == "help" && opts.topic == "" {
			opts.topic = arg
			continue
		}
		if !opts.hostSet {
			opts.host = arg
			opts.hostSet = true
			continue
		}
		return opts, fmt.Errorf("unexpected argument %q", arg)
	}
	if opts.command == "" {
		opts.command = "stats"
	}
	if opts.command == "status" {
		opts.command = "stats"
	}
	if opts.command == "tui" {
		opts.command = "monitor"
	}
	if opts.command == "help" && opts.topic == "" {
		opts.topic = "overview"
	}
	if opts.token == "" && opts.user == "" && opts.password != "" {
		return opts, fmt.Errorf("--password needs --user")
	}
	if opts.json && opts.command == "monitor" {
		opts.once = true
	}
	return opts, nil
}

func splitFlag(arg string) (name, value string, hasValue bool) {
	if index := strings.IndexByte(arg, '='); index >= 0 {
		return arg[:index], arg[index+1:], true
	}
	return arg, "", false
}

func parseInterval(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds <= 0 {
			return 0, fmt.Errorf("must be positive")
		}
		return time.Duration(seconds * float64(time.Second)), nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("use seconds or a duration like 5s, 500ms, 1m")
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("must be positive")
	}
	return parsed, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func helpText(topic string) string {
	topic = strings.ToLower(strings.TrimSpace(topic))
	switch topic {
	case "", "overview", "all":
		return overviewHelp()
	case "stats", "status":
		return `hitron-cli stats — one-shot or live report

Usage:
  hitron-cli stats [host] [flags]
  hitron-cli [host]                # stats + live TUI when run in a terminal

Shows identity, RF spec check, provisioning, every downstream/upstream
channel, MoCA (on an HT-EM5), logs, and switch ports.

Flags:
  --once            print one report and exit (no TUI)
  --json            machine-readable snapshot
  --interval 5s     live refresh interval
  --limit 20        log lines in the one-shot report
  --host 10.0.0.1   gateway address

Examples:
  hitron-cli 192.168.100.1
  hitron-cli stats --host 192.168.100.1 --once
  hitron-cli stats --json > snapshot.json`
	case "monitor", "tui", "watch":
		return `hitron-cli monitor — live terminal UI

Usage:
  hitron-cli monitor [host] [flags]

Keys:
  q        quit
  r        refresh now
  a        toggle automatic refresh
  g        toggle trend graphs
  t        next theme (remembered for next time)
  j / k    scroll (the mouse wheel works too)
  x        reboot, after a confirmation (also the Reboot button)
  space    refresh now
  h        help overlay

Themes: neon (default), synthwave, dracula, nord, gruvbox, matrix.
Pick one with --theme NAME or HITRON_THEME; the t key saves your choice.

Reboot: x (or a click on the Reboot button) opens a dialog that asks for the
login unless --user/--password were given, then makes you type the word
reboot. Nothing is sent before that. Factory reset and hard reset are not in
the TUI; they stay separate commands. The CODA-57 has no reboot button.

Flags:
  --theme NAME   color theme
  --no-mouse     do not capture the mouse (lets you select text normally)

The TUI needs a real terminal. Use --once to get the same report as text, or
--json for a snapshot.

Examples:
  hitron-cli monitor --host 192.168.100.1 --interval 3s
  hitron-cli monitor --theme dracula
  hitron-cli monitor --once --no-color`
	case "detect":
		return `hitron-cli detect — identify the device

Usage:
  hitron-cli detect [host] [flags]

Detection order:
  1. CODA6X   GET /1/Device/CM/Version
  2. CODA-57  GET /data/getSysInfo.asp
  3. HT-EM5   GET /index.html then POST /ms/1/0x302/GET

A 401 or a login page on the first matching path means the device needs
credentials; the CLI prompts once when stdin is a terminal. CODA6X and
CODA-57 reads are anonymous on the analyzed firmware. HT-EM5 uses HTTP Basic
and defaults to admin/admin only when no credentials were supplied.

Examples:
  hitron-cli detect 192.168.100.1
  hitron-cli detect --host 127.0.0.1:8080 --json`
	case "system":
		return `hitron-cli system — identity, provisioning, switch ports

Usage:
  hitron-cli system [host] [flags]

Prints model, firmware, serial, RF MAC, uptime, WAN addressing, the Midco RF
spec check, DOCSIS provisioning milestones, and Ethernet port state.

  --json    full snapshot instead of the formatted section`
	case "channels":
		return `hitron-cli channels — RF channel detail

Usage:
  hitron-cli channels [host] [flags]

Prints one downstream table (SC-QAM and OFDM) and one upstream table (SC-QAM
and OFDMA), each row typed and sorted by channel. Disabled OFDMA channels are
hidden and counted in the heading.
Power is colored against the Midco envelope: downstream -6 to +15 dBmV,
upstream 35 to 48 dBmV (OFDMA uses repPower1_6 + 6 dB).

  --json    full snapshot instead of the formatted section`
	case "logs":
		return `hitron-cli logs — DOCSIS event log

Usage:
  hitron-cli logs [host] [flags]

  --limit 20   number of entries (0 means all)
  --json       full snapshot instead of the formatted section`
	case "moca":
		return `hitron-cli moca — HT-EM5 link, bands, and nodes

Usage:
  hitron-cli moca [host] [flags]

Only meaningful on an HT-EM5. Prints link state, MoCA versions, adapter
name, beacon/LOF, TX power index, Ethernet frame counters, per-band
supported/enabled/privacy state, and every node with its MAC and role.`
	case "reboot":
		return destructiveHelp("reboot", "CODA6X: POST /1/Device/CM/Reboot {\"reboot\":1}\nHT-EM5: PUT /ms/1/0xb00 {\"data\":[1]}\nCODA-57: no confirmed reboot endpoint.")
	case "factory-reset":
		return destructiveHelp("factory-reset", "CODA6X: POST /1/Device/CM/Reboot {\"factoryReset\":1}.\nDestroys the device configuration. CODA6X only.")
	case "hard-reset":
		return destructiveHelp("hard-reset", "CODA6X: POST /1/Device/CM/Reboot {\"restoreReset\":1}.\nThe firmware handler execs reboot directly; the most immediate variant.\nCODA6X only.")
	case "auth":
		return `Authentication

CODA6X
  Reads answer anonymously on the analyzed firmware. Writes need a session:
  the CLI POSTs /1/Device/Users/Login with the firmware's model JSON and keeps
  the cookie. An app token can be sent instead with --token (Ht-App-Auth).

CODA-57
  The legacy .asp API primes a PHPSESSID cookie from GET /login.html. No
  credential POST has been confirmed against real hardware, so the CLI does
  not guess one; it reports the gate instead.

HT-EM5
  HTTP Basic on every request, default admin/admin on the factory image. The
  CLI fetches /index.html first for the csrf_token cookie, then echoes it as
  X-CSRF-TOKEN.

Flags:
  -u, --user NAME       username
  -p, --password PASS   password
      --token TOKEN     Ht-App-Auth value (CODA6X)

Environment:
  HITRON_USERNAME, HITRON_PASSWORD, HITRON_APP_TOKEN

Read routes never log in silently. A write subcommand calls Login explicitly
right before it acts, so the action is one traceable operation.`
	case "environment", "env":
		return `Environment

  HITRON_HOST        default gateway (192.168.100.1)
  HITRON_DEVICE      alias for HITRON_HOST
  HITRON_USERNAME    username
  HITRON_PASSWORD    password
  HITRON_APP_TOKEN   CODA6X Ht-App-Auth token
  HITRON_TIMEOUT     request timeout in seconds (10)
  HITRON_INTERVAL    live refresh interval (5s)
  HITRON_THEME       color theme (neon, synthwave, dracula, nord, gruvbox, matrix)
  NO_COLOR           disable ANSI color

Flags always override the environment.`
	case "exit-codes", "exit":
		return `Exit codes

  0   success
  1   device, network, or action error (including an aborted confirmation)
  2   usage error (bad flag, missing credential for a write)

Warnings about individual endpoints do not change the exit code when the rest
of the snapshot succeeded; they are printed under "Partial data" and in the
snapshot's warnings array.`
	case "examples":
		return `Examples

  hitron-cli 192.168.100.1
  hitron-cli stats --host 192.168.100.1 --once --no-color
  hitron-cli monitor --interval 3s
  hitron-cli detect 192.168.100.1
  hitron-cli channels --json | jq '.downstream[] | {id, power_dbmv}'
  hitron-cli logs --limit 50
  hitron-cli moca 192.168.0.212
  hitron-cli system --user admin --password admin
  hitron-cli reboot 192.168.100.1 --user admin --password admin
  hitron-cli factory-reset 192.168.100.1 --user admin --password admin --confirm
  hitron-cli hard-reset 192.168.100.1 --user admin --password admin --confirm`
	case "safety", "writes":
		return `Writes and safety

Read commands never write. The three destructive commands are separate verbs:

  reboot         ask the modem to reboot
  factory-reset  clear configuration and reboot
  hard-reset     immediate restoreReset + reboot (CODA6X)

Each one asks a y/N question unless --confirm is given. It also requires
--user and --password (or HITRON_USERNAME/HITRON_PASSWORD), because the
firmware's write routes need a real session. --confirm removes only the
question, never the credential requirement.

The CLI refuses CODA-57 writes: no write endpoint is confirmed for the legacy
.asp family, and guessing one against a live modem is not acceptable.`
	default:
		return "No help topic " + strconv.Quote(topic) + ".\n\n" + overviewHelp()
	}
}

func destructiveHelp(command, detail string) string {
	verb := map[string]string{
		"reboot":        "Reboot",
		"factory-reset": "Factory reset",
		"hard-reset":    "Hard reset and reboot",
	}[command]
	return fmt.Sprintf(`hitron-cli %s — destructive

Usage:
  hitron-cli %s [host] --user NAME --password PASS [--confirm]

%s

Without --confirm the CLI asks:
  %s <device> at <host>? This acts on the real device. [y/N]

--confirm skips that question only. Credentials are still required, and the
CLI calls Login immediately before the action.

Exit status is 1 if the user answers no.`, command, command, detail, verb)
}

func overviewHelp() string {
	return `hitron-cli — talk to Hitron cable gateways and MoCA bridges

Usage:
  hitron-cli [command] [host] [flags]

Commands:
  stats            one-shot report, or live TUI in a terminal (default)
  monitor          force the live TUI
  detect           identify CODA6X / CODA-57 / HT-EM5
  system           identity, provisioning, Ethernet ports
  channels         downstream and upstream RF detail
  logs             DOCSIS event log
  moca             HT-EM5 link, bands, nodes
  reboot           reboot (prompts, or --confirm)
  factory-reset    factory reset + reboot (CODA6X)
  hard-reset       immediate restoreReset + reboot (CODA6X)
  help [topic]     this text, or a topic
  version          print the version

Global flags:
  -H, --host HOST        gateway address, default 192.168.100.1
  -u, --user NAME        username for authenticated devices and writes
  -p, --password PASS    password
      --token TOKEN      CODA6X Ht-App-Auth app token
      --timeout 10s      per-request timeout
      --interval 5s      TUI refresh interval
      --limit 20         log lines
      --once             one report, no TUI
      --json             machine-readable output
      --no-color         plain text
      --confirm          skip the y/N question on a destructive command
      --no-banner        suppress the startup art
      --theme NAME       TUI color theme (neon, synthwave, dracula, nord, gruvbox, matrix)
      --no-mouse         do not capture the mouse in the TUI
  -h, --help             this text
  -V, --version          version

Help topics:
  stats monitor detect system channels logs moca
  reboot factory-reset hard-reset
  auth environment examples safety exit-codes

Examples:
  hitron-cli 192.168.100.1
  hitron-cli stats --once
  hitron-cli channels --json
  hitron-cli monitor --interval 3s
  hitron-cli help auth
`
}
