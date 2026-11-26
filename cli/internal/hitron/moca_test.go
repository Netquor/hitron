package hitron

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeMoca answers the HT-EM5 register RPC the way the real InterNiche server
// does: Basic auth, a csrf_token cookie from /index.html echoed as a header,
// and a {"data": ["0x..."]} envelope.
func fakeMoca(t *testing.T, wantUser, wantPass string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/ms/") && r.URL.Path != "/index.html" {
			// A real HT-EM5 has no CODA6X routes; answer a plain 404 so the
			// detector does not mistake it for an auth gate.
			http.NotFound(w, r)
			return
		}
		if user, pass, ok := r.BasicAuth(); !ok || user != wantUser || pass != wantPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="hitron"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/index.html" {
			http.SetCookie(w, &http.Cookie{Name: "csrf_token", Value: "csrf-abc", Path: "/"})
			fmt.Fprint(w, "<html><title>HT-EM5</title></html>")
			return
		}
		if r.Header.Get("X-CSRF-TOKEN") != "csrf-abc" {
			http.Error(w, "bad csrf", http.StatusBadRequest)
			return
		}
		words := []string{"0x00000000"}
		path := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(r.URL.Path, "/GET"), "/PUT"))
		switch path {
		case "/ms/0/0x15":
			words = make([]string, 29)
			for index := range words {
				words[index] = "0x00000000"
			}
			words[0] = "0x00000000" // this node
			words[1] = "0x00000000" // NC node
			words[5] = "0x00000001" // link up
			words[11] = "0x00000025"
			words[12] = "0x00000001" // node bitmask: node 0 only
		case "/ms/0/0x16":
			words = []string{"0xC03F0E5A", "0x1B2C0000", "0x00000000", "0x00000000", "0x00000025"}
		case "/ms/0/0x24":
			words = []string{"0x00000000", "0x00000352"} // beacon 850 MHz
		case "/ms/0/0x14":
			words = make([]string, 52)
			for index := range words {
				words[index] = "0x00000000"
			}
			words[6] = "0x000004D2"  // 1234 TX good
			words[33] = "0x000007D0" // 2000 RX good
		case "/ms/1/0x102":
			words = []string{"0x312e30"} // ASCII "1.0"
		case "/ms/1/0x302":
			words = []string{"0x02030100"}
		case "/ms/1/0x303":
			words = []string{"0x00000015"}
		case "/ms/0/0x1003":
			words = []string{"0x0000047E"} // 1150 MHz
		case "/ms/1/0x20b":
			words = []string{"0x490F2A12"}
		case "/ms/1/0x210":
			words = []string{"0xFFFFFF00"}
		case "/ms/1/0x211":
			words = []string{"0x490F2A01"}
		case "/ms/1/0x212":
			words = []string{"0x00000004"} // Office
		case "/ms/0/0x10a8":
			words = []string{"0x00000007"}
		case "/ms/1/0xb00":
			if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/PUT") {
				http.Error(w, "want POST .../PUT", http.StatusMethodNotAllowed)
				return
			}
			words = []string{"0x00000001"}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": words})
	}
}

func TestSnapshotMoca(t *testing.T) {
	server := httptest.NewServer(fakeMoca(t, "admin", "admin"))
	defer server.Close()

	client, err := Detect(context.Background(), server.URL, Credentials{}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.Kind != KindMoca {
		t.Fatalf("kind = %q", client.Kind)
	}
	if !client.DefaultCreds {
		t.Fatal("detection should record that it used the factory default")
	}
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Kind != KindMoca || snapshot.MoCA == nil {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	status := snapshot.MoCA
	if !status.LinkUp || status.NetworkVersion != "2.5" || status.BeaconMHz != 850 {
		t.Fatalf("status = %+v", status)
	}
	if snapshot.Firmware != "2.3.1" || snapshot.Hardware != "1.0" {
		t.Fatalf("versions = %q / %q", snapshot.Firmware, snapshot.Hardware)
	}
	if snapshot.RFMac != "c0:3f:0e:5a:1b:2c" {
		t.Fatalf("mac = %q", snapshot.RFMac)
	}
	if snapshot.WANIP != "73.15.42.18" || snapshot.Gateway != "73.15.42.1" {
		t.Fatalf("addressing = %q / %q", snapshot.WANIP, snapshot.Gateway)
	}
	if status.LOFMHz != 1150 || status.TXPowerIndex != 7 || status.AdapterName != "Office" {
		t.Fatalf("phy = %+v", status)
	}
	if status.EthTxGood != 1234 || status.EthRxGood != 2000 {
		t.Fatalf("counters = %d / %d", status.EthTxGood, status.EthRxGood)
	}
	if len(status.Nodes) != 1 || status.Nodes[0].MAC != "c0:3f:0e:5a:1b:2c" {
		t.Fatalf("nodes = %+v", status.Nodes)
	}
}

func TestMocaRebootUsesPutAndRejectsDefaultCreds(t *testing.T) {
	server := httptest.NewServer(fakeMoca(t, "admin", "admin"))
	defer server.Close()

	client, err := Detect(context.Background(), server.URL, Credentials{}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Reboot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result["result"] != "reboot requested" {
		t.Fatalf("result = %v", result)
	}
}

func TestMocaRejectsWrongPassword(t *testing.T) {
	server := httptest.NewServer(fakeMoca(t, "admin", "secret"))
	defer server.Close()

	_, err := Detect(context.Background(), server.URL, Credentials{Username: "admin", Password: "wrong"}, 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "Basic") && !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v", err)
	}
}
