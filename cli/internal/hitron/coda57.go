package hitron

import (
	"context"
	"fmt"
	"strings"
	"time"
)

var coda57Endpoints = []string{
	"dsinfo",
	"dsofdminfo",
	"usinfo",
	"usofdminfo",
	"getCMInit",
	"getLinkStatus",
	"getCmDocsisWan",
	"getSysInfo",
}

// asObjects accepts either a single object or an array of objects, because the
// legacy .asp endpoints disagree.
func asObjects(value any) []map[string]any {
	switch payload := value.(type) {
	case map[string]any:
		return []map[string]any{payload}
	case []any:
		items := make([]map[string]any, 0, len(payload))
		for _, entry := range payload {
			if item, ok := entry.(map[string]any); ok {
				items = append(items, item)
			}
		}
		return items
	default:
		return nil
	}
}

func (c *Client) snapshotCoda57(ctx context.Context) (*Snapshot, error) {
	snap := &Snapshot{Kind: KindCoda57, Host: c.Addr(), At: time.Now()}
	if err := c.primeCoda57(ctx); err != nil {
		snap.Warnings = append(snap.Warnings, fmt.Sprintf("session prime failed: %v", err))
	}
	results := make(map[string]any, len(coda57Endpoints))
	for _, endpoint := range coda57Endpoints {
		value, err := c.getJSON(ctx, "/data/"+endpoint+".asp")
		if err != nil {
			if isAuthErr(err) && len(results) == 0 {
				return nil, err
			}
			snap.Warnings = joinWarnings(snap.Warnings, "%s: %v", endpoint, err)
			continue
		}
		results[endpoint] = value
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no CODA-57 endpoint answered")
	}

	sysinfo := firstObject(results["getSysInfo"])
	snap.Model = firstNonEmpty(str(sysinfo["modelName"]), str(sysinfo["ModelName"]), "CODA-57")
	snap.Vendor = firstNonEmpty(str(sysinfo["vendorName"]), str(sysinfo["VendorName"]), str(sysinfo["vendor"]))
	snap.Firmware = firstNonEmpty(str(sysinfo["swVersion"]), str(sysinfo["softwareVersion"]), str(sysinfo["version"]))
	snap.Hardware = firstNonEmpty(str(sysinfo["hwVersion"]), str(sysinfo["hardwareVersion"]))
	snap.Serial = firstNonEmpty(str(sysinfo["serialNumber"]), str(sysinfo["serial"]))
	snap.RFMac = firstNonEmpty(str(sysinfo["rfMac"]), str(sysinfo["mac"]))
	snap.WANIP = firstIPv4(str(sysinfo["ip"]), str(sysinfo["wanIP"]))
	snap.Gateway = firstNonEmpty(str(sysinfo["gw"]), str(sysinfo["gateway"]))
	snap.Netmask = firstNonEmpty(str(sysinfo["subMask"]), str(sysinfo["netmask"]))
	snap.Lease = str(sysinfo["lease"])
	snap.ConfigName = firstNonEmpty(str(sysinfo["Configname"]), str(sysinfo["configName"]))
	snap.AccessType = firstNonEmpty(str(sysinfo["ntAccess"]), str(sysinfo["accessType"]))
	if seconds, ok := uptime(sysinfo["systemUptime"]); ok {
		snap.UptimeSeconds = seconds
		snap.HasUptime = true
	}

	for _, item := range asObjects(results["dsinfo"]) {
		channel := Channel{
			ID:         str(item["channelId"]),
			PortID:     str(item["portId"]),
			Modulation: firstNonEmpty(str(item["modulation"]), str(item["modtype"])),
		}
		channel.Frequency, _ = num(item["frequency"])
		channel.Power, channel.HasPower = signal(item["signalStrength"])
		channel.SNR, channel.HasSNR = signal(item["snr"])
		channel.Corrected, _ = counter(item["correcteds"])
		channel.Uncorrect, _ = counter(item["uncorrect"])
		if channel.ID != "" || channel.Frequency != 0 {
			snap.DS = append(snap.DS, channel)
		}
	}
	for _, item := range asObjects(results["usinfo"]) {
		channel := Channel{
			ID:         str(item["channelId"]),
			PortID:     str(item["portId"]),
			Modulation: str(item["modtype"]),
			Mode:       str(item["scdmaMode"]),
		}
		channel.Frequency, _ = num(item["frequency"])
		channel.Power, channel.HasPower = signal(item["signalStrength"])
		channel.Bandwidth, _ = num(item["bandwidth"])
		if channel.ID != "" || channel.Frequency != 0 {
			snap.US = append(snap.US, channel)
		}
	}
	for _, item := range asObjects(results["dsofdminfo"]) {
		power, hasPower := signal(item["plcpower"])
		snr, hasSNR := signal(item["SNR"])
		channel := OFDMChannel{
			Index:       str(item["index"]),
			FFTType:     str(item["ffttype"]),
			PLCPower:    power,
			HasPLCPower: hasPower,
			SNR:         snr,
			HasSNR:      hasSNR,
			PLCLock:     boolFlag(item["plclock"]),
			NCPLock:     boolFlag(item["ncplock"]),
			MDC1Lock:    boolFlag(item["mdc1lock"]),
		}
		channel.Frequency, _ = num(item["Subcarr0freqFreq"])
		channel.Corrected, _ = counter(item["correcteds"])
		channel.Uncorrect, _ = counter(item["uncorrect"])
		channel.Octets, _ = counter(item["dsoctets"])
		if channel.Index != "" || channel.Frequency != 0 || hasPower {
			snap.OFDM = append(snap.OFDM, channel)
		}
	}
	for _, item := range asObjects(results["usofdminfo"]) {
		index := str(item["uschindex"])
		state := str(item["state"])
		channel := OFDMAChannel{
			Index:    index,
			State:    state,
			Disabled: strings.EqualFold(state, "disabled"),
		}
		channel.Frequency, _ = num(item["frequency"])
		channel.Bandwidth, _ = num(item["channelBw"])
		channel.Power, channel.HasPower = signal(item["repPower"])
		channel.Power16, channel.HasPower16 = signal(item["repPower1_6"])
		channel.DigAtten, _ = num(item["digAtten"])
		if channel.Index != "" || channel.Frequency != 0 {
			snap.OFDMA = append(snap.OFDMA, channel)
		}
	}

	return snap, nil
}

func firstObject(value any) map[string]any {
	items := asObjects(value)
	if len(items) == 0 {
		return nil
	}
	return items[0]
}

func boolFlag(value any) bool {
	ok, _ := flag(value)
	return ok
}
