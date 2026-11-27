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
	// intervalSet is true when --interval or HITRON_INTERVAL chose the value,
	// so a per-model default does not override it.
	intervalSet bool
	limit       int

	json     bool
	noColor  bool
	confirm  bool
	once     bool
	noBanner bool
	noMouse  bool
	noBg     bool
	theme    string

	// pwod runs offline. seed is the plaintext seed, or encryptedSeed holds
	// the stored base64 form; encryptSeed/decryptSeed select the codec verbs.
	// The *Set flags keep an explicitly empty value distinct from an absent
	// flag, which matters because "" is a valid seed and a valid encoding.
	seed          string
	seedSet       bool
	encryptedSeed string
	encryptedSet  bool
	encryptSeed   string
	encryptSet    bool
	decryptSeed   string
	decryptSet    bool
	date          string
	days          int
	utc           bool

	command string
	topic   string
	hostSet bool
}

var commands = map[string]bool{
	"stats": true, "status": true,
	"monitor": true, "tui": true,
	"detect": true, "system": true, "channels": true, "logs": true, "moca": true,
	"reboot": true, "factory-reset": true, "hard-reset": true, "fec-reset": true,
	"pwod": true, "pod": true,
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
	// pwod is arithmetic on a seed, not a request: it runs before the banner,
	// the theme, and connect so it works with no device and no terminal.
	if opts.command == "pwod" {
		return runPWOD(opts)
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
	case "fec-reset":
		return runFECReset(ctx, client, opts)
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
	// The CODA-57 and CODA-60 answer local reads quickly, so they refresh every
	// second unless the user picked an interval. The experimental CODA 4x8x is
	// deliberately left at the default: an unverified device is not polled fast.
	if !opts.intervalSet && (client.Kind == hitron.KindCoda6X || client.Kind == hitron.KindCoda57) {
		opts.interval = time.Second
	}
	fetch := func(runCtx context.Context) (*hitron.Snapshot, error) {
		return snapshotWithTimeout(runCtx, client, opts.timeout)
	}
	// Analyzer reads share one hardware tuner on the modem, so each gets its
	// own generous timeout and the TUI never runs two at once.
	analyzer := func(runCtx context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(runCtx, opts.timeout*2)
	}
	tui := ui.TUIOptions{
		Channels: func(runCtx context.Context) ([]hitron.SpectrumChannel, error) {
			callCtx, cancel := analyzer(runCtx)
			defer cancel()
			return client.SpectrumChannels(callCtx)
		},
		Sweep: func(runCtx context.Context, centerMHz float64) (*hitron.SpectrumSweep, error) {
			callCtx, cancel := analyzer(runCtx)
			defer cancel()
			return client.SpectrumSweep(callCtx, centerMHz)
		},
		RealTime: func(runCtx context.Context) ([]hitron.RealTimeFrame, error) {
			callCtx, cancel := analyzer(runCtx)
			defer cancel()
			return client.RealTimeSpectrum(callCtx)
		},
		Constellation: func(runCtx context.Context, portID int) (*hitron.Constellation, error) {
			callCtx, cancel := analyzer(runCtx)
			defer cancel()
			return client.Constellation(callCtx, portID)
		},
		Fetch:        fetch,
		Interval:     opts.interval,
		Host:         client.Addr(),
		NoMouse:      opts.noMouse,
		NoBackground: opts.noBg,
	}
	if client.Kind == hitron.KindCoda6X {
		// The 4x8x gets no ResetFEC: the TUI answers z with the read-only
		// message itself, before any status suggests a clear is running.
		tui.ResetFEC = func(runCtx context.Context) (hitron.FECReset, error) {
			callCtx, cancel := context.WithTimeout(runCtx, opts.timeout*3)
			defer cancel()
			return client.ResetFEC(callCtx)
		}
	}
	// Reboot is offered where the CLI's own reboot command works. The CODA-57
	// has no confirmed write endpoint, so it gets no button at all.
	if client.Kind != hitron.KindCoda57 && client.Kind != hitron.KindCoda4x8x {
		// On the CODA6X the reboot call is accepted from the LAN with no
		// login, and customers are usually never given one, so the dialog
		// asks for none. A login supplied with --user/--password is only used
		// if the device refuses the anonymous call.
		tui.NoLogin = client.Kind == hitron.KindCoda6X
		tui.Reboot = func(runCtx context.Context, user, pass string) (string, error) {
			// Work on a copy so the polling client's credentials never change
			// underneath an in-flight read.
			act := *client
			act.DefaultCreds = false
			callCtx, cancel := context.WithTimeout(runCtx, opts.timeout*2)
			defer cancel()
			send := func() (map[string]any, error) { return act.Reboot(callCtx) }
			var result map[string]any
			var err error
			if user == "" && pass == "" {
				result, err = send()
				if err != nil {
					return "", fmt.Errorf("%w (the device wants a login; run with --user and --password)", err)
				}
			} else {
				act.Creds = hitron.Credentials{Username: user, Password: pass}
				if tui.NoLogin {
					// Try anonymously first; log in only if refused.
					if result, err = send(); err != nil {
						result = nil
					}
				}
				if result == nil {
					if err := act.Login(runCtx); err != nil {
						return "", err
					}
					if result, err = send(); err != nil {
						return "", err
					}
				}
			}
			if code := fmt.Sprint(result["errCode"]); code != "" && code != "000" && code != "<nil>" {
				return "", fmt.Errorf("device answered errCode %s %v", code, result["errMsg"])
			}
			return fmt.Sprintf("Reboot sent to %s (%s)", client.Addr(), client.Kind.Label()), nil
		}
		// A login given on the command line or in the environment is kept as
		// a fallback; the confirmation still has to be typed.
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
	if client.Kind == hitron.KindCoda4x8x {
		return fail(fmt.Errorf("%s: %w", action, hitron.ErrCoda4x8xReadOnly))
	}
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
	if note, ok := result["result"].(string); ok && note != "" {
		fmt.Println(note)
	}
	if code := fmt.Sprint(result["errCode"]); code != "" && code != "000" && code != "<nil>" {
		fmt.Printf("device answered errCode %s %v\n", code, result["errMsg"])
	}
	return 0
}

// runPWOD computes the CODA6X password of the day. It is arithmetic on a seed,
// so it runs before connect and needs no host, credentials, or terminal.
func runPWOD(opts options) int {
	if opts.days < 1 {
		opts.days = 1
	}
	if err := checkPWOD(opts); err != nil {
		fmt.Fprintln(os.Stderr, "hitron-cli: "+err.Error())
		return 2
	}
	if opts.encryptSet {
		encrypted, err := hitron.PWODEncryptSeed(opts.encryptSeed)
		if err != nil {
			return fail(err)
		}
		if opts.json {
			writeJSON(map[string]any{"seed": opts.encryptSeed, "encrypted_seed": encrypted})
		} else {
			fmt.Println(encrypted)
		}
		return 0
	}
	if opts.decryptSet {
		seed, err := hitron.PWODDecryptSeed(opts.decryptSeed)
		if err != nil {
			return fail(err)
		}
		if opts.json {
			writeJSON(map[string]any{"encrypted_seed": opts.decryptSeed, "seed": seed})
		} else {
			fmt.Println(seed)
		}
		return 0
	}

	seed, encrypted := opts.seed, ""
	if opts.encryptedSet {
		decoded, err := hitron.PWODDecryptSeed(opts.encryptedSeed)
		if err != nil {
			return fail(err)
		}
		seed, encrypted = decoded, opts.encryptedSeed
	}
	start, err := pwodStart(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hitron-cli: "+err.Error())
		return 2
	}

	type entry struct {
		Date     string `json:"date"`
		Password string `json:"password"`
	}
	entries := make([]entry, 0, opts.days)
	for i := 0; i < opts.days; i++ {
		date := start.AddDate(0, 0, i)
		password, err := hitron.PWODPassword(seed, date)
		if err != nil {
			return fail(err)
		}
		entries = append(entries, entry{Date: date.Format("2006-01-02"), Password: password})
	}
	if opts.json {
		payload := map[string]any{"seed": seed, "passwords": entries}
		if encrypted != "" {
			payload["encrypted_seed"] = encrypted
		}
		writeJSON(payload)
		return 0
	}
	for _, entry := range entries {
		fmt.Printf("%s  %s\n", entry.Date, entry.Password)
	}
	return 0
}

func checkPWOD(opts options) error {
	if opts.encryptSet && opts.decryptSet {
		return fmt.Errorf("--encrypt-seed and --decrypt-seed are mutually exclusive")
	}
	if opts.seedSet && opts.encryptedSet {
		return fmt.Errorf("--seed and --encrypted-seed are mutually exclusive")
	}
	codec := opts.encryptSet || opts.decryptSet
	if codec && (opts.seedSet || opts.encryptedSet || opts.date != "" || opts.utc || opts.days != 1) {
		return fmt.Errorf("seed codec flags cannot be combined with --seed, --encrypted-seed, --date, --utc or --days")
	}
	return nil
}

// pwodStart is the first day to print: an explicit date, otherwise today on
// the host clock the modem is expected to share.
func pwodStart(opts options) (time.Time, error) {
	if opts.date != "" {
		parsed, err := time.Parse("2006-01-02", opts.date)
		if err != nil {
			return time.Time{}, fmt.Errorf("--date must be YYYY-MM-DD")
		}
		return parsed, nil
	}
	if opts.utc {
		return time.Now().UTC(), nil
	}
	return time.Now(), nil
}

func runFECReset(ctx context.Context, client *hitron.Client, opts options) int {
	callCtx, cancel := context.WithTimeout(ctx, opts.timeout*3)
	defer cancel()
	result, err := client.ResetFEC(callCtx)
	if err != nil {
		return fail(err)
	}
	stillCounting := result.OFDMNotZero
	if stillCounting == nil {
		stillCounting = []string{}
	}
	if opts.json {
		writeJSON(map[string]any{
			"action": "fec-reset",
			"host":   client.Addr(),
			"device": client.Kind.Label(),
			"docsis": map[string]int{
				"busy":    result.DOCSISBusy,
				"cleared": result.DOCSISCleared,
			},
			"ofdm": map[string]any{
				"busy":           result.OFDMBusy,
				"cleared":        result.OFDMCleared,
				"still_counting": stillCounting,
			},
			"summary": result.String(),
		})
		return 0
	}
	fmt.Printf("FEC reset on %s (%s)\n", client.Addr(), client.Kind.Label())
	fmt.Println("  " + result.Detail())
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
		days:     1,
	}
	if value := os.Getenv("HITRON_TIMEOUT"); value != "" {
		if seconds, err := strconv.ParseFloat(value, 64); err == nil {
			opts.timeout = time.Duration(seconds * float64(time.Second))
		}
	}
	if value := os.Getenv("HITRON_INTERVAL"); value != "" {
		if parsed, err := parseInterval(value); err == nil {
			opts.interval = parsed
			opts.intervalSet = true
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
				opts.intervalSet = true
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
			case "--no-bg":
				opts.noBg = true
			case "--theme":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.theme = got
			case "--seed":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.seed, opts.seedSet = got, true
			case "--encrypted-seed":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.encryptedSeed, opts.encryptedSet = got, true
			case "--encrypt-seed":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.encryptSeed, opts.encryptSet = got, true
			case "--decrypt-seed":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.decryptSeed, opts.decryptSet = got, true
			case "--date":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				opts.date = got
			case "--days":
				got, err := takeValue()
				if err != nil {
					return opts, err
				}
				parsed, err := strconv.Atoi(got)
				if err != nil || parsed < 1 {
					return opts, fmt.Errorf("--days needs a positive number")
				}
				opts.days = parsed
			case "--utc":
				opts.utc = true
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
	if opts.command == "pod" {
		opts.command = "pwod"
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
  --interval 5s     live refresh interval (default 1s on CODA-57 / CODA-60)
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
  z        reset the downstream FEC counters (3.0 + OFDM; CODA6X)
  t        next theme (remembered for next time)
  b        toggle the gradient background (24-bit color terminals only)
  j / k    scroll (the mouse wheel works too)
  x        reboot, after a confirmation (also the Reboot button)
  space    refresh now
  h        help overlay
  1        summary
  2 / s    live spectrum (6 MHz capture)
  3 / c    constellation (I/Q)
  [ ]      previous / next analyzer channel
  , .      fine-tune the spectrum center by 1 MHz

Themes: neon (default), synthwave, dracula, nord, gruvbox, matrix.
Pick one with --theme NAME or HITRON_THEME; the t key saves your choice.

Reboot: x (or a click on the Reboot button) opens a dialog that asks for the
login (not on the CODA6X, which needs none from the LAN), then makes you
type the word reboot. Nothing is sent before that. Factory reset and hard reset are not in
the TUI; they stay separate commands. The CODA-57 has no reboot button.

Flags:
  --theme NAME   color theme
  --no-mouse     do not capture the mouse (lets you select text normally)
  --no-bg        no gradient background (it is also skipped unless the terminal
                 reports 24-bit color via COLORTERM; b toggles it live)

The TUI needs a real terminal. Use --once to get the same report as text, or
--json for a snapshot.

The spectrum and constellation views exist only on the CODA6X family. Both read
the same single hardware tuner, so captures are sequential: one 6 MHz window
per refresh. Inactive constellation ports answer 200 with an empty cloud.

z issues PUT /1/Device/CM/DsInfo and PUT /1/Device/CM/DsOfdm, then re-reads the
counters. The status line reports how many of the channels that were counting
before now read zero, so it never claims more than the device did. The 3.0 stop
clears every channel in one call; the OFDM stop is unreliable (one PUT often
zeroes a single channel), so it is retried eight times and the best result is
reported. On a family with no confirmed FEC endpoint, z only zeroes the
displayed counts.

hitron-cli fec-reset runs the same clear without the TUI, for scripts.

Examples:
  hitron-cli monitor --host 192.168.100.1 --interval 3s
  hitron-cli monitor --theme dracula
  hitron-cli monitor --once --no-color`
	case "fec-reset":
		return `hitron-cli fec-reset — clear the downstream FEC counters

Usage:
  hitron-cli fec-reset [host] [--json]

CODA6X only. Sends one bare PUT /1/Device/CM/DsInfo (DOCSIS 3.0 counters) and
then repeats a bare PUT /1/Device/CM/DsOfdm (OFDM counters), re-reading both.
No body, no session, and no y/N question: it zeroes counters and changes
nothing else.

The report is relative to the channels that were counting before the clear, so
an already-clean counter is never claimed as cleared. The 3.0 stop clears every
busy channel in one call. The OFDM stop is unreliable on 8.7.1.0.1b4 - one PUT
zeroes zero to two of the live channels and the handler ignores the body - so
it is retried up to eight times and the best observation is kept.

--json prints the counts, including the OFDM channels still counting.

Examples:
  hitron-cli fec-reset
  hitron-cli fec-reset 192.168.100.1 --json | jq '.ofdm'`
	case "pwod", "pod":
		return `hitron-cli pwod — password of the day (CODA6X)

Usage:
  hitron-cli pwod [--seed SEED | --encrypted-seed BASE64] [--date YYYY-MM-DD]
                  [--utc] [--days N] [--json]
  hitron-cli pwod --encrypt-seed SEED
  hitron-cli pwod --decrypt-seed BASE64

Runs offline: it computes the value htx_pod_main writes to the "Default Admin"
(mso) account after local midnight. No host, no credentials, no request.

The algorithm is SHA1("Pa55w0RolHiTr0N" + YYYY + MM + DD + seed), with the
first 16 bytes mapped to [0-9A-Za-z]. The seed is the plaintext value from
DOCSIS TLV43 sub-option 242; the device stores it as base64(AES-128-ECB(seed))
in uci general.podCfg.defseed. Without a seed the date-only value is still
computed, but a provisioned modem is not using it.

The modem applies its own local date. If its clock or timezone differs from
this host, use --utc or --date to name the day it will use.

--encrypted-seed decrypts the stored form with the firmware's fixed key and
--encrypt-seed prints that form for a plaintext seed. NUL truncation and the
firmware's strlen-based unpad are reproduced, so an already-aligned seed can
decode short; pass --seed when the plaintext is available.

Flags:
  --seed SEED            plaintext seed (default: empty)
  --encrypted-seed B64   stored seed (general.podCfg.defseed)
  --date YYYY-MM-DD      first day to print (default: today)
  --utc                  take today from UTC instead of local time
  --days N               print N consecutive days (default: 1)
  --json                 date/password pairs as JSON

Examples:
  hitron-cli pwod
  hitron-cli pwod --seed 12345678
  hitron-cli pwod --seed 12345678 --date 2026-11-26 --days 7
  hitron-cli pwod --encrypted-seed 'i19WhrZqaiiNNVC+oWsv3Q==' --json`
	case "detect":
		return `hitron-cli detect — identify the device

Usage:
  hitron-cli detect [host] [flags]

Detection order:
  1. CODA6X     GET /1/Device/CM/Version
  2. CODA 4x8x  GET /CM/Version (experimental, read-only; never logs in)
  3. CODA-57    GET /data/getSysInfo.asp
  4. HT-EM5     GET /index.html then POST /ms/1/0x302/GET

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
		return destructiveHelp("reboot", `CODA6X: POST /1/Device/CM/Reboot
  {"reboot":1,"factoryReset":0,"restoreReset":0}
HT-EM5: PUT /ms/1/0xb00 {"data":[1]}
CODA-57: no confirmed reboot endpoint.

The body always carries all three flags: the handler reads each one before
it picks a branch, so a partial body is a PHP 8 fatal, not errCode 001.
Measured on 8.7.1.0.1b4 this call answers errCode 000 and the unit does not
reboot; nothing on the box acts on the flag. Use hard-reset.`)
	case "factory-reset":
		return destructiveHelp("factory-reset", `CODA6X: POST /1/Device/CM/Reboot
  {"reboot":0,"factoryReset":1,"restoreReset":0}

Destroys the device configuration. CODA6X only. Not measured: it sets the
same TR-181 flag as reboot, which is inert on 8.7.1.0.1b4.`)
	case "hard-reset":
		return destructiveHelp("hard-reset", `CODA6X: POST /1/Device/CM/Reboot
  {"reboot":0,"factoryReset":0,"restoreReset":1}

The firmware handler execs reboot directly, so the socket closes with no
reply; that empty reply is success, not an error. Measured on 8.7.1.0.1b4:
the unit is down ~5 s after the call and back ~2 min later, with its local
configuration reset. CODA6X only.`)
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
right before it acts, so the action is one traceable operation.

The password of the day (hitron-cli help pwod) is a separate offline
computation over the ISP seed; it is not a web login.`
	case "environment", "env":
		return `Environment

  HITRON_HOST        default gateway (192.168.100.1)
  HITRON_DEVICE      alias for HITRON_HOST
  HITRON_USERNAME    username
  HITRON_PASSWORD    password
  HITRON_APP_TOKEN   CODA6X Ht-App-Auth token
  HITRON_TIMEOUT     request timeout in seconds (10)
  HITRON_INTERVAL    live refresh interval (5s; 1s on CODA-57 / CODA-60)
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
  hitron-cli fec-reset --json
  hitron-cli pwod --seed 12345678 --date 2026-11-26 --days 7
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

Read commands never write. The live TUI has two writes:

  z            reset the downstream FEC counters (CODA6X; counters only)
  x            reboot, only after typing the confirmation word

fec-reset is the command-line form of z: bare PUTs to CM/DsInfo and CM/DsOfdm,
no session and no prompt, and --json for scripts. It zeroes counters and
changes nothing else.

The destructive commands are separate verbs:

  reboot         ask the modem to reboot (inert on 8.7.1.0.1b4)
  factory-reset  clear configuration and reboot (not measured)
  hard-reset     immediate restoreReset + reboot (CODA6X, measured working)

Each one asks a y/N question unless --confirm is given. It also requires
--user and --password (or HITRON_USERNAME/HITRON_PASSWORD), because the
firmware's write routes need a real session. --confirm removes only the
question, never the credential requirement.

The CLI refuses CODA-57 writes: no write endpoint is confirmed for the legacy
.asp family, and guessing one against a live modem is not acceptable.

pwod is not a write and not a read: it computes a value from a seed on this
host and never contacts the device.`
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
  fec-reset        clear the downstream FEC counters (CODA6X)
  pwod             password of the day (CODA6X, offline)
  help [topic]     this text, or a topic
  version          print the version

Global flags:
  -H, --host HOST        gateway address, default 192.168.100.1
  -u, --user NAME        username for authenticated devices and writes
  -p, --password PASS    password
      --token TOKEN      CODA6X Ht-App-Auth app token
      --timeout 10s      per-request timeout
      --interval 5s      TUI refresh interval (1s default on CODA-57 / CODA-60)
      --limit 20         log lines
      --once             one report, no TUI
      --json             machine-readable output
      --no-color         plain text
      --confirm          skip the y/N question on a destructive command
      --no-banner        suppress the startup art
      --theme NAME       TUI color theme (neon, synthwave, dracula, nord, gruvbox, matrix)
      --no-mouse         do not capture the mouse in the TUI
      --no-bg            no gradient background in the TUI
  -h, --help             this text
  -V, --version          version

Help topics:
  stats monitor detect system channels logs moca
  reboot factory-reset hard-reset fec-reset pwod
  auth environment examples safety exit-codes

Examples:
  hitron-cli 192.168.100.1
  hitron-cli stats --once
  hitron-cli channels --json
  hitron-cli monitor --interval 3s
  hitron-cli fec-reset --json
  hitron-cli pwod --seed 12345678
  hitron-cli help auth
`
}
