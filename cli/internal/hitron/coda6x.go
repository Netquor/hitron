package hitron

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall"
	"time"
)

var coda6xEndpoints = []string{
	"CM/Version",
	"Router/SysInfo",
	"CM/SysInfo",
	"CM/DocsisProvision",
	"CM/DsInfo",
	"CM/UsInfo",
	"CM/DsOfdm",
	"CM/UsOfdm",
	"CM/Log",
	"Advanced/AdvancedSwitch",
}

type endpointResult struct {
	path string
	data map[string]any
	err  error
}

// Snapshot reads every endpoint the summary needs. The firmware's PHP-FPM pool
// is small, so requests are bounded to two in flight, exactly like the Python
// client.
func (c *Client) snapshotCoda6X(ctx context.Context) (*Snapshot, error) {
	snap := &Snapshot{Kind: KindCoda6X, Host: c.Addr(), At: time.Now()}
	results := make(map[string]map[string]any, len(coda6xEndpoints))

	sem := make(chan struct{}, 2)
	out := make(chan endpointResult, len(coda6xEndpoints))
	var wg sync.WaitGroup
	for _, path := range coda6xEndpoints {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			value, err := c.getJSON(ctx, "/1/Device/"+path)
			if err != nil {
				out <- endpointResult{path: path, err: err}
				return
			}
			object, ok := value.(map[string]any)
			if !ok {
				out <- endpointResult{path: path, err: fmt.Errorf("unexpected %T", value)}
				return
			}
			out <- endpointResult{path: path, data: object}
		}(path)
	}
	wg.Wait()
	close(out)

	for result := range out {
		if result.err != nil {
			if isAuthErr(result.err) && len(results) == 0 {
				return nil, result.err
			}
			snap.Warnings = joinWarnings(snap.Warnings, "%s: %v", result.path, result.err)
			continue
		}
		results[result.path] = result.data
	}
	if _, ok := results["CM/Version"]; !ok && len(results) == 0 {
		return nil, fmt.Errorf("no CODA6X endpoint answered")
	}

	version := results["CM/Version"]
	router := results["Router/SysInfo"]
	sysinfo := results["CM/SysInfo"]

	snap.Model = firstNonEmpty(str(version["modelName"]), str(router["modelName"]))
	if snap.Model == "" {
		snap.Model = "CODA6X"
	}
	snap.Vendor = firstNonEmpty(str(version["vendorName"]), str(router["vendorName"]))
	snap.Firmware = firstNonEmpty(str(version["swVersion"]), str(router["swVersion"]))
	snap.Hardware = firstNonEmpty(str(version["hwVersion"]), str(router["hwVersion"]))
	snap.Serial = firstNonEmpty(str(version["serialNumber"]), str(router["serialNumber"]))
	snap.RFMac = str(router["rfMac"])

	if seconds, ok := uptime(router["systemUptime"]); ok {
		snap.UptimeSeconds = seconds
		snap.HasUptime = true
	}

	snap.AccessType = firstNonEmpty(str(sysinfo["ntAccess"]))
	snap.WANIP = firstIPv4(str(sysinfo["ip"]))
	snap.Netmask = firstNonEmpty(str(sysinfo["subMask"]))
	snap.Gateway = firstNonEmpty(str(sysinfo["gw"]))
	snap.Lease = firstNonEmpty(str(sysinfo["lease"]))
	snap.ConfigName = firstNonEmpty(str(sysinfo["Configname"]), str(sysinfo["configName"]))

	parseProvision(snap, results["CM/DocsisProvision"])
	parseCodaDS(snap, results["CM/DsInfo"])
	parseCodaUS(snap, results["CM/UsInfo"])
	parseCodaOFDM(snap, results["CM/DsOfdm"])
	parseCodaOFDMA(snap, results["CM/UsOfdm"])
	parseCodaLogs(snap, results["CM/Log"])
	parseCodaEthernet(snap, results["Advanced/AdvancedSwitch"])
	return snap, nil
}

func listItems(value any, key string) []map[string]any {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	raw, ok := object[key]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	items := make([]map[string]any, 0, len(list))
	for _, entry := range list {
		if item, ok := entry.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items
}

func parseProvision(snap *Snapshot, data map[string]any) {
	steps := []struct {
		key  string
		name string
	}{
		{"hwInit", "Hardware initialization"},
		{"findDownstream", "Downstream lock"},
		{"ranging", "Ranging"},
		{"dhcp", "DHCP"},
		{"timeOfday", "Time of day"},
		{"timeofday", "Time of day"},
		{"downloadCfg", "Configuration file"},
		{"registration", "CMTS registration"},
		{"bpiStatus", "BPI security"},
	}
	seen := make(map[string]bool)
	for _, step := range steps {
		raw, ok := data[step.key]
		if !ok || seen[step.name] {
			continue
		}
		seen[step.name] = true
		ok, _ = flag(raw)
		snap.Steps = append(snap.Steps, ProvisionStep{Name: step.name, OK: ok})
	}
}

func parseCodaDS(snap *Snapshot, data map[string]any) {
	for _, item := range listItems(data, "Freq_List") {
		channel := Channel{
			ID:         str(item["channelId"]),
			PortID:     str(item["portId"]),
			Modulation: str(item["modulation"]),
		}
		channel.Frequency, _ = num(item["frequency"])
		channel.Power, channel.HasPower = signal(item["signalStrength"])
		channel.SNR, channel.HasSNR = signal(item["snr"])
		channel.Corrected, _ = counter(item["correcteds"])
		channel.Uncorrect, _ = counter(item["uncorrect"])
		if channel.ID == "" && channel.Frequency == 0 {
			continue
		}
		snap.DS = append(snap.DS, channel)
	}
}

func parseCodaUS(snap *Snapshot, data map[string]any) {
	for _, item := range listItems(data, "Freq_List") {
		channel := Channel{
			ID:         str(item["channelId"]),
			PortID:     str(item["portId"]),
			Modulation: str(item["modtype"]),
			Mode:       str(item["scdmaMode"]),
		}
		channel.Frequency, _ = num(item["frequency"])
		channel.Power, channel.HasPower = signal(item["signalStrength"])
		channel.Bandwidth, _ = num(item["bandwidth"])
		if channel.ID == "" && channel.Frequency == 0 {
			continue
		}
		snap.US = append(snap.US, channel)
	}
}

func parseCodaOFDM(snap *Snapshot, data map[string]any) {
	for _, item := range listItems(data, "Freq_List") {
		power, hasPower := signal(item["plcpower"])
		snr, hasSNR := signal(item["SNR"])
		channel := OFDMChannel{
			Index:       str(item["index"]),
			FFTType:     str(item["ffttype"]),
			PLCPower:    power,
			HasPLCPower: hasPower,
			SNR:         snr,
			HasSNR:      hasSNR,
		}
		channel.Frequency, _ = num(item["Subcarr0freqFreq"])
		channel.PLCLock, _ = flag(item["plclock"])
		channel.NCPLock, _ = flag(item["ncplock"])
		channel.MDC1Lock, _ = flag(item["mdc1lock"])
		channel.Corrected, _ = counter(item["correcteds"])
		channel.Uncorrect, _ = counter(item["uncorrect"])
		channel.Octets, _ = counter(item["dsoctets"])
		if channel.Frequency == 0 && !hasPower && !hasSNR {
			// Unused OFDM slots are returned with an index and nothing else.
			continue
		}
		snap.OFDM = append(snap.OFDM, channel)
	}
}

func parseCodaOFDMA(snap *Snapshot, data map[string]any) {
	for _, item := range listItems(data, "Freq_List") {
		index := str(item["uschindex"])
		power, hasPower := signal(item["repPower"])
		power16, hasPower16 := signal(item["repPower1_6"])
		channel := OFDMAChannel{
			Index:    index,
			State:    str(item["state"]),
			Disabled: strings.EqualFold(str(item["state"]), "disabled"),
		}
		channel.Frequency, _ = num(item["frequency"])
		channel.Bandwidth, _ = num(item["channelBw"])
		channel.Power, channel.HasPower = power, hasPower
		channel.Power16, channel.HasPower16 = power16, hasPower16
		channel.DigAtten, _ = num(item["digAtten"])
		if index == "" && channel.Frequency == 0 {
			continue
		}
		snap.OFDMA = append(snap.OFDMA, channel)
	}
}

func parseCodaLogs(snap *Snapshot, data map[string]any) {
	for _, item := range listItems(data, "Log_List") {
		snap.Logs = append(snap.Logs, LogEntry{
			Time:     str(item["time"]),
			Priority: str(item["priority"]),
			Type:     str(item["type"]),
			Event:    str(item["event"]),
		})
	}
}

func parseCodaEthernet(snap *Snapshot, data map[string]any) {
	for _, item := range listItems(data, "advSwitch_List") {
		snap.Ethernet = append(snap.Ethernet, EthPort{
			ID:     str(item["portId"]),
			Speed:  str(item["speed"]),
			Duplex: str(item["duplex"]),
			Link:   str(item["linked"]),
			Power:  str(item["power"]),
		})
	}
}

// Action performs a CODA6X write. The caller has already authenticated and
// shown the user a confirmation prompt.
func (c *Client) coda6xAction(ctx context.Context, model map[string]int) (map[string]any, error) {
	return c.postModel(ctx, "/1/Device/CM/Reboot", model)
}

// rebootBody builds the full ModelInfo body for /1/Device/CM/Reboot.
//
// doRebootAction() reads every flag in turn before it checks which one is set,
// and on this build a missing key is a PHP fatal, not a warning. A body of
// only {"restoreReset":1} therefore dies with `Undefined array key "reboot"`
// and never reaches its branch, so all three keys must always be present with
// the wanted one set to 1.
func rebootBody(set string) map[string]int {
	model := map[string]int{"reboot": 0, "factoryReset": 0, "restoreReset": 0}
	model[set] = 1
	return model
}

// Reboot asks the gateway to reboot through the TR-181 flag. Measured on
// 8.7.1.0.1b4 the call answers errCode 000 and the unit never reboots: nothing
// on the box consumes Device.DeviceInfo.X_HITRON_COM_MGMT.Reboot. HardReset is
// the only measured-working reboot, and it resets the configuration first.
func (c *Client) Reboot(ctx context.Context) (map[string]any, error) {
	switch c.Kind {
	case KindCoda6X:
		return c.coda6xAction(ctx, rebootBody("reboot"))
	case KindMoca:
		if _, err := c.rpc(ctx, 1, 0xb00, "PUT", []any{1}, nil); err != nil {
			return nil, err
		}
		return map[string]any{"result": "reboot requested"}, nil
	case KindCoda57:
		return nil, fmt.Errorf("CODA-57: %w (no confirmed reboot endpoint)", ErrUnsupported)
	default:
		return nil, fmt.Errorf("unknown device")
	}
}

// FactoryReset is the flag-based factory reset (CODA6X only). It uses the same
// X_HITRON_COM_MGMT.FactoryReset flag as Reboot uses for reboot, so expect the
// same inert behaviour on 8.7.1.0.1b4; this variant has not been measured.
func (c *Client) FactoryReset(ctx context.Context) (map[string]any, error) {
	if c.Kind != KindCoda6X {
		return nil, fmt.Errorf("factory reset on %s: %w (CODA6X only)", c.Kind.Label(), ErrUnsupported)
	}
	return c.coda6xAction(ctx, rebootBody("factoryReset"))
}

// HardReset is the restoreReset variant, and the only reboot that actually
// works on 8.7.1.0.1b4. The PHP handler returns, sleeps 2 s, then execs
// `ncpu_exec -e ht_reset_cmd.sh reboot` followed by `reboot`, so the socket
// dies instead of answering: the connection close *is* the success signal.
// Measured 2026-11-26: empty reply, ping down 5 s later, back after ~2 min
// with fresh counters and a "CM Reboot Reason: POWER_ON" log entry. It clears
// the local configuration on the way, so it is a factory reset, not a reboot.
func (c *Client) HardReset(ctx context.Context) (map[string]any, error) {
	if c.Kind != KindCoda6X {
		return nil, fmt.Errorf("hard reset on %s: %w (CODA6X only)", c.Kind.Label(), ErrUnsupported)
	}
	result, err := c.coda6xAction(ctx, rebootBody("restoreReset"))
	if err != nil && isConnectionClosed(err) {
		return map[string]any{
			"result": "reboot requested; the device closed the connection",
		}, nil
	}
	return result, err
}

// isConnectionClosed reports whether err is the transport-level EOF the
// firmware produces when it reboots before finishing the response.
func isConnectionClosed(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}
