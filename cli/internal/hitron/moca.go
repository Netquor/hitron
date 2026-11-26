package hitron

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HT-EM5 register map. The values are object ids inside the device's tiny
// /ms/<type>/<reg> RPC, not REST paths.
const (
	mocaLocalInfo = 0x15
	mocaNetInfo   = 0x16
	mocaFrameInfo = 0x14
	mocaMiscPHY   = 0x24
	mocaMiscM25   = 0x7F
)

var mocaNoSuffix = map[uint32]bool{
	mocaLocalInfo: true,
	mocaNetInfo:   true,
	mocaFrameInfo: true,
	mocaMiscPHY:   true,
	mocaMiscM25:   true,
}

var mocaRoomNames = []string{
	"Master", "LivingRoom", "TVRoom", "GameRoom", "Office", "Den",
	"Room1", "Room2", "Room3", "Room4", "Room5", "Kids1", "Kids2",
	"Bedroom1", "Bedroom2", "Office1", "Office2",
}

var mocaBandNames = []string{"D-Ext", "D-Low", "D-High"}

var mocaChipNames = []string{"MXL370x", "MXL371x", "UNKNOWN"}

// decodeASCIIWords turns the HW_VERSION register into text. Depending on the
// firmware build it answers either a plain string or ASCII packed into hex
// words, so accept both and drop NUL padding.
func decodeASCIIWords(words []string) string {
	var plain, packed strings.Builder
	for _, word := range words {
		trimmed := strings.TrimSpace(word)
		if strings.HasPrefix(trimmed, "0x") || strings.HasPrefix(trimmed, "0X") {
			digits := trimmed[2:]
			if len(digits)%2 == 1 {
				digits = "0" + digits
			}
			for index := 0; index < len(digits); index += 2 {
				var value uint64
				if _, err := fmt.Sscanf(digits[index:index+2], "%02x", &value); err == nil && value != 0 {
					packed.WriteByte(byte(value))
				}
			}
			continue
		}
		plain.WriteString(trimmed)
	}
	if packed.Len() > 0 {
		return packed.String()
	}
	return plain.String()
}

// rpc performs one register read or write. The body is the browser's compact
// JSON under an x-www-form-urlencoded content type, which this firmware
// insists on; there is no real form encoding involved.
func (c *Client) rpc(ctx context.Context, typeID int, reg uint32, method string, data, data2 []any) ([]string, error) {
	path := fmt.Sprintf("/ms/%d/0x%x", typeID, reg)
	if !mocaNoSuffix[reg] {
		path += "/" + method
	}
	body := map[string]any{"data": []any{}}
	if data != nil {
		body["data"] = data
	}
	if data2 != nil {
		body["data2"] = data2
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	base := c.Base.String()
	headers := map[string]string{
		"X-CSRF-TOKEN":    c.csrf,
		"Origin":          base,
		"Referer":         base + "/index.html",
		"Accept-Language": "en-US,en;q=0.9",
		"User-Agent":      "hitron-cli/1.0 (+https://github.com/Netquor/hitron)",
	}
	resp, responseBody, err := c.do(ctx, requestOptions{
		method:      http.MethodPost,
		path:        path,
		body:        payload,
		contentType: "application/x-www-form-urlencoded",
		accept:      "text/html, */*",
		basic:       true,
		headers:     headers,
	})
	if err != nil {
		return nil, err
	}
	if isAuthStatus(resp.StatusCode) {
		return nil, c.authError("HT-EM5 Basic auth rejected")
	}
	if resp.StatusCode == http.StatusBadRequest {
		return nil, fmt.Errorf("%s answered 400: %s", path, strings.TrimSpace(string(responseBody)))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	if looksLikeHTML(responseBody) {
		return nil, c.authError("HT-EM5 answered with a login page")
	}
	value, err := decodeJSON(responseBody)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: unexpected %T response", path, value)
	}
	raw, ok := object["data"].([]any)
	if !ok {
		return nil, fmt.Errorf("%s: response has no data array", path)
	}
	words := make([]string, 0, len(raw))
	for _, entry := range raw {
		words = append(words, str(entry))
	}
	return words, nil
}

func h2iList(words []string) []uint64 {
	values := make([]uint64, len(words))
	for index, word := range words {
		values[index] = h2i(word)
	}
	return values
}

func wordAt(words []uint64, index int) uint64 {
	if index < 0 || index >= len(words) {
		return 0
	}
	return words[index]
}

// Snapshot for an HT-EM5. The device serializes RPCs, so this reads one
// register at a time; that is also what its embedded TCP stack tolerates.
func (c *Client) snapshotMoca(ctx context.Context) (*Snapshot, error) {
	snap := &Snapshot{Kind: KindMoca, Host: c.Addr(), At: time.Now(), Model: "HT-EM5"}

	localWords, err := c.rpc(ctx, 0, mocaLocalInfo, "GET", nil, nil)
	if err != nil {
		return nil, err
	}
	local := h2iList(localWords)

	readU := func(typeID int, reg uint32, data ...any) ([]uint64, error) {
		if data == nil {
			data = []any{}
		}
		words, err := c.rpc(ctx, typeID, reg, "GET", data, nil)
		if err != nil {
			return nil, err
		}
		return h2iList(words), nil
	}

	myNode := int(wordAt(local, 0))
	ncNode := int(wordAt(local, 1) & 0xFF)
	status := &MocaStatus{
		LinkUp:         wordAt(local, 5) != 0,
		NetworkVersion: mocaVersion(wordAt(local, 11) & 0xFF),
	}
	snap.MoCA = status

	netInfo, err := readU(0, mocaNetInfo, myNode)
	if err == nil {
		snap.RFMac = hexMAC(wordAt(netInfo, 0), wordAt(netInfo, 1))
		status.DeviceVersion = mocaVersion(wordAt(netInfo, 4) & 0xFF)
	} else {
		snap.Warnings = joinWarnings(snap.Warnings, "local node info: %v", err)
	}

	misc, err := readU(0, mocaMiscPHY)
	if err == nil {
		status.BeaconMHz = int(wordAt(misc, 1))
	} else {
		snap.Warnings = joinWarnings(snap.Warnings, "PHY info: %v", err)
	}

	hwVersion, err := c.rpc(ctx, 1, 0x102, "GET", nil, nil)
	if err == nil && len(hwVersion) > 0 {
		snap.Hardware = strings.ToUpper(decodeASCIIWords(hwVersion))
	}

	swVersion, err := readU(1, 0x302)
	if err == nil && len(swVersion) > 0 {
		word := wordAt(swVersion, 0)
		snap.Firmware = fmt.Sprintf("%d.%d.%d", (word>>24)&0xFF, (word>>16)&0xFF, (word>>8)&0xFF)
	}

	chipID, err := readU(1, 0x303)
	chipName := "UNKNOWN"
	if err == nil && len(chipID) > 0 {
		index := int(wordAt(chipID, 0)) - 0x15
		if index < 0 {
			index = 0
		}
		if index >= len(mocaChipNames) {
			index = len(mocaChipNames) - 1
		}
		chipName = mocaChipNames[index]
	}
	if chipName == "UNKNOWN" {
		snap.Warnings = append(snap.Warnings, "unrecognized MoCA chip id")
	}

	if lof, err := readU(0, 0x1003); err == nil {
		status.LOFMHz = int(wordAt(lof, 0))
	}
	if ip, err := readU(1, 0x20B); err == nil {
		snap.WANIP = int2ip(wordAt(ip, 0))
	}
	if mask, err := readU(1, 0x210); err == nil {
		snap.Netmask = int2ip(wordAt(mask, 0))
	}
	if gateway, err := readU(1, 0x211); err == nil {
		snap.Gateway = int2ip(wordAt(gateway, 0))
	}
	if adapter, err := readU(1, 0x212); err == nil {
		index := int(wordAt(adapter, 0))
		if index >= 0 && index < len(mocaRoomNames) {
			status.AdapterName = mocaRoomNames[index]
		} else {
			status.AdapterName = strconv.Itoa(index)
		}
	}
	if power, err := readU(0, 0x10A8); err == nil {
		status.TXPowerIndex = int(wordAt(power, 0))
	}

	bandMask, _ := readU(0, 0x1009)
	securityMask, _ := readU(0, 0x1059)
	hwCfg, _ := readU(0, 0x10F5)
	if len(bandMask) > 0 {
		mask := wordAt(bandMask, 0)
		secure := wordAt(securityMask, 0)
		hardware := wordAt(hwCfg, 0)
		for index, name := range mocaBandNames {
			status.Bands = append(status.Bands, MocaBand{
				Name:      name,
				Supported: (hardware>>(31-uint(index)))&1 == 1,
				Enabled:   (mask>>uint(index))&1 == 1,
				Privacy:   (secure>>uint(index))&1 == 1,
			})
		}
	}

	if frame, err := readU(0, mocaFrameInfo, 0); err == nil {
		status.EthTxGood = wordAt(frame, 6)
		status.EthRxGood = wordAt(frame, 33)
	} else {
		snap.Warnings = joinWarnings(snap.Warnings, "ethernet counters: %v", err)
	}

	nodeMask := wordAt(local, 12)
	for nodeID := 0; nodeID < 16; nodeID++ {
		if (nodeMask>>uint(nodeID))&1 == 0 {
			continue
		}
		info, err := readU(0, mocaNetInfo, nodeID)
		if err != nil {
			snap.Warnings = joinWarnings(snap.Warnings, "node %d: %v", nodeID, err)
			continue
		}
		status.Nodes = append(status.Nodes, MocaNode{
			NodeID:  nodeID,
			MAC:     hexMAC(wordAt(info, 0), wordAt(info, 1)),
			Version: mocaVersion(wordAt(info, 4) & 0xFF),
			NC:      nodeID == ncNode,
			Self:    nodeID == myNode,
		})
	}
	return snap, nil
}
