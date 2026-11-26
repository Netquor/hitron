// Package hitron is a small, dependency-free Go client for the three Hitron
// device families this repository documents:
//
//   - CODA6X cable gateways (CODA-60 and friends): the /1/Device/... JSON API
//   - CODA-57 legacy gateways: the /data/*.asp JSON API
//   - HT-EM5 MoCA bridges: the /ms/<type>/<reg> hex-word RPC
//
// It is a deliberately independent implementation of the Python clients in
// src/hitron, not a port of the exporter: the CLI needs detection, live
// values, and the three reset actions, and nothing else.
package hitron

import (
	"fmt"
	"time"
)

// Kind is the device family the detector settled on.
type Kind string

const (
	KindUnknown Kind = "unknown"
	KindCoda6X  Kind = "coda6x"
	KindCoda57  Kind = "coda57"
	KindMoca    Kind = "moca"
)

// Label is the human-facing family name.
func (k Kind) Label() string {
	switch k {
	case KindCoda6X:
		return "CODA-60 / CODA6X"
	case KindCoda57:
		return "CODA-57"
	case KindMoca:
		return "HT-EM5 (MoCA)"
	default:
		return "unknown"
	}
}

// Credentials are optional: every code path works anonymously if the device
// allows it, which is how the analyzed CODA6X firmware ships.
type Credentials struct {
	Username string
	Password string
	AppToken string // Ht-App-Auth, base64("appid:appuser")
}

// Empty reports whether no credential material was supplied at all.
func (c Credentials) Empty() bool {
	return c.Username == "" && c.Password == "" && c.AppToken == ""
}

// Complete reports whether a username and password pair was supplied.
func (c Credentials) Complete() bool {
	return c.Username != "" && c.Password != ""
}

// Channel is one SC-QAM downstream or upstream channel.
type Channel struct {
	ID           string  `json:"id"`
	PortID       string  `json:"port_id,omitempty"`
	Frequency    float64 `json:"frequency_hz"`
	Modulation   string  `json:"modulation,omitempty"`
	Mode         string  `json:"mode,omitempty"` // ATDMA / TDMA / SCDMA, upstream only
	Bandwidth    float64 `json:"bandwidth_hz,omitempty"`
	Power        float64 `json:"power_dbmv"`
	HasPower     bool    `json:"-"`
	SNR          float64 `json:"snr_db,omitempty"`
	HasSNR       bool    `json:"-"`
	Corrected    uint64  `json:"corrected,omitempty"`
	Uncorrect    uint64  `json:"uncorrectable,omitempty"`
	HasCodewords bool    `json:"-"`
}

// OFDMChannel is one downstream OFDM channel (DOCSIS 3.1).
type OFDMChannel struct {
	Index       string  `json:"index"`
	Frequency   float64 `json:"frequency_hz"`
	SNR         float64 `json:"snr_db,omitempty"`
	HasSNR      bool    `json:"-"`
	PLCPower    float64 `json:"plc_power_dbmv"`
	HasPLCPower bool    `json:"-"`
	Octets      uint64  `json:"octets,omitempty"`
	Corrected   uint64  `json:"corrected,omitempty"`
	Uncorrect   uint64  `json:"uncorrectable,omitempty"`
	HasCounters bool    `json:"-"`
	PLCLock     bool    `json:"plc_lock"`
	NCPLock     bool    `json:"ncp_lock"`
	MDC1Lock    bool    `json:"mdc1_lock"`
	FFTType     string  `json:"fft_type,omitempty"`
}

// OFDMAChannel is one upstream OFDMA channel (DOCSIS 3.1).
type OFDMAChannel struct {
	Index      string  `json:"index"`
	State      string  `json:"state,omitempty"`
	Disabled   bool    `json:"disabled"`
	Frequency  float64 `json:"frequency_hz"`
	Bandwidth  float64 `json:"bandwidth_mhz,omitempty"`
	Power      float64 `json:"power_dbmv"`
	HasPower   bool    `json:"-"`
	Power16    float64 `json:"power_1_6_dbmv"`
	HasPower16 bool    `json:"-"`
	DigAtten   float64 `json:"digital_attenuation_db"`
}

// ProvisionStep is one DOCSIS provisioning milestone.
type ProvisionStep struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
}

// LogEntry is one DOCSIS event log line.
type LogEntry struct {
	Time     string `json:"time"`
	Priority string `json:"priority,omitempty"`
	Type     string `json:"type,omitempty"`
	Event    string `json:"event"`
}

// EthPort is one switch port on a CODA6X.
type EthPort struct {
	ID     string `json:"id"`
	Speed  string `json:"speed,omitempty"`
	Duplex string `json:"duplex,omitempty"`
	Link   string `json:"link,omitempty"`
	Power  string `json:"power,omitempty"`
}

// MocaNode is one node in the MoCA network.
type MocaNode struct {
	NodeID  int    `json:"node_id"`
	MAC     string `json:"mac"`
	Version string `json:"moca_version"`
	NC      bool   `json:"network_coordinator"`
	Self    bool   `json:"self"`
}

// MocaBand is one MoCA band, as advertised by the HT-EM5 config.
type MocaBand struct {
	Name      string `json:"name"`
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Privacy   bool   `json:"privacy"`
}

// MocaStatus is the decoded HT-EM5 view.
type MocaStatus struct {
	LinkUp         bool       `json:"link_up"`
	NetworkVersion string     `json:"network_version,omitempty"`
	DeviceVersion  string     `json:"device_version,omitempty"`
	BeaconMHz      int        `json:"beacon_mhz,omitempty"`
	LOFMHz         int        `json:"lof_mhz,omitempty"`
	TXPowerIndex   int        `json:"tx_power_index,omitempty"`
	AdapterName    string     `json:"adapter_name,omitempty"`
	Nodes          []MocaNode `json:"nodes,omitempty"`
	EthTxGood      uint64     `json:"ethernet_tx_good,omitempty"`
	EthRxGood      uint64     `json:"ethernet_rx_good,omitempty"`
	Bands          []MocaBand `json:"bands,omitempty"`
}

// Snapshot is one read of a device. Any field may be zero if the device did
// not expose it or that endpoint failed; Warnings records what was lost.
type Snapshot struct {
	Kind Kind      `json:"kind"`
	Host string    `json:"host"`
	At   time.Time `json:"at"`

	Model    string `json:"model,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
	Firmware string `json:"firmware,omitempty"`
	Hardware string `json:"hardware,omitempty"`
	Serial   string `json:"serial,omitempty"`
	RFMac    string `json:"rf_mac,omitempty"`
	// CMTSMac is read from the DOCSIS event log; the device API has no field
	// for it, so it is empty until a log line names one.
	CMTSMac string `json:"cmts_mac,omitempty"`

	UptimeSeconds int64 `json:"uptime_seconds,omitempty"`
	HasUptime     bool  `json:"-"`

	WANIP      string `json:"wan_ip,omitempty"`
	Gateway    string `json:"gateway,omitempty"`
	Netmask    string `json:"netmask,omitempty"`
	Lease      string `json:"lease,omitempty"`
	ConfigName string `json:"config_name,omitempty"`
	AccessType string `json:"access_type,omitempty"`

	DS       []Channel       `json:"downstream,omitempty"`
	US       []Channel       `json:"upstream,omitempty"`
	OFDM     []OFDMChannel   `json:"downstream_ofdm,omitempty"`
	OFDMA    []OFDMAChannel  `json:"upstream_ofdma,omitempty"`
	Steps    []ProvisionStep `json:"provisioning,omitempty"`
	Logs     []LogEntry      `json:"logs,omitempty"`
	Ethernet []EthPort       `json:"ethernet,omitempty"`
	MoCA     *MocaStatus     `json:"moca,omitempty"`

	Warnings []string `json:"warnings,omitempty"`
}

// StepsOK counts successful provisioning milestones.
func (s *Snapshot) StepsOK() int {
	ok := 0
	for _, step := range s.Steps {
		if step.OK {
			ok++
		}
	}
	return ok
}

// DSInSpec / USInSpec apply the Midco envelope used by the CODA-57 client:
// downstream -6..15 dBmV, upstream 35..48 dBmV, with the 6 dB DOCSIS 3.0
// correction applied to OFDMA repPower1_6.
func (s *Snapshot) DSInSpec() (ok bool, min, max float64) {
	min, max = 0, 0
	first := true
	for _, ch := range s.DS {
		if !ch.HasPower {
			continue
		}
		if first || ch.Power < min {
			min = ch.Power
		}
		if first || ch.Power > max {
			max = ch.Power
		}
		first = false
	}
	for _, ch := range s.OFDM {
		if !ch.HasPLCPower {
			continue
		}
		if first || ch.PLCPower < min {
			min = ch.PLCPower
		}
		if first || ch.PLCPower > max {
			max = ch.PLCPower
		}
		first = false
	}
	if first {
		return false, 0, 0
	}
	return min >= -6.0 && max <= 15.0, min, max
}

// USInSpec is the upstream half of DSInSpec.
func (s *Snapshot) USInSpec() (ok bool, min, max float64) {
	min, max = 0, 0
	first := true
	for _, ch := range s.US {
		if !ch.HasPower {
			continue
		}
		if first || ch.Power < min {
			min = ch.Power
		}
		if first || ch.Power > max {
			max = ch.Power
		}
		first = false
	}
	for _, ch := range s.OFDMA {
		if ch.Disabled || !ch.HasPower16 {
			continue
		}
		value := ch.Power16 + 6.0
		if first || value < min {
			min = value
		}
		if first || value > max {
			max = value
		}
		first = false
	}
	if first {
		return false, 0, 0
	}
	return min >= 35.0 && max <= 48.0, min, max
}

// DSAverage is the mean downstream power in dBmV over the same channels
// DSInSpec checks (SC-QAM power plus OFDM PLC power). It is the arithmetic
// mean of the dBmV readings, not a power-domain average.
func (s *Snapshot) DSAverage() (avg float64, ok bool) {
	sum, n := 0.0, 0
	for _, ch := range s.DS {
		if ch.HasPower {
			sum += ch.Power
			n++
		}
	}
	for _, ch := range s.OFDM {
		if ch.HasPLCPower {
			sum += ch.PLCPower
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

// USAverage is the upstream half of DSAverage. Enabled OFDMA channels use
// repPower1_6 + 6 dB, the same 6.4 MHz-equivalent USInSpec compares.
func (s *Snapshot) USAverage() (avg float64, ok bool) {
	sum, n := 0.0, 0
	for _, ch := range s.US {
		if ch.HasPower {
			sum += ch.Power
			n++
		}
	}
	for _, ch := range s.OFDMA {
		if !ch.Disabled && ch.HasPower16 {
			sum += ch.Power16 + 6.0
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

// DSHasReadings reports whether any downstream power was reported. A genuine
// 0.0 dBmV reading is common, so callers cannot test min/max against zero.
func (s *Snapshot) DSHasReadings() bool {
	for _, ch := range s.DS {
		if ch.HasPower {
			return true
		}
	}
	for _, ch := range s.OFDM {
		if ch.HasPLCPower {
			return true
		}
	}
	return false
}

// USHasReadings is the upstream half of DSHasReadings.
func (s *Snapshot) USHasReadings() bool {
	for _, ch := range s.US {
		if ch.HasPower {
			return true
		}
	}
	for _, ch := range s.OFDMA {
		if !ch.Disabled && ch.HasPower16 {
			return true
		}
	}
	return false
}

// CountChannels is a convenience for the UI.
func (s *Snapshot) CountChannels() int {
	return len(s.DS) + len(s.US) + len(s.OFDM) + len(s.OFDMA)
}

// AuthRequiredError means the device answered with a login gate. The caller
// should collect credentials and try again; nothing was written.
type AuthRequiredError struct {
	Kind   Kind
	Host   string
	Detail string
}

func (e *AuthRequiredError) Error() string {
	detail := e.Detail
	if detail == "" {
		detail = "device requires authentication"
	}
	return fmt.Sprintf("%s at %s: %s", e.Kind.Label(), e.Host, detail)
}

// ErrUnsupported is returned for actions a family has no confirmed write
// endpoint for. It is never silently ignored.
var ErrUnsupported = fmt.Errorf("action not supported on this device")
