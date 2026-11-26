package hitron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const versionJSON = `{"errCode":"000","errMsg":"","modelName":"CODA-60","vendorName":"Hitron","hwVersion":"1.1","swVersion":"8.5.0.0.4b1","serialNumber":"C6X5A1B2C3D4E"}`

func fakeCoda6X() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/1/Device/")
		switch path {
		case "CM/Version":
			fmt.Fprint(w, versionJSON)
		case "Router/SysInfo":
			fmt.Fprint(w, `{"errCode":"000","errMsg":"","rfMac":"C0:3F:0E:5A:1B:2C","systemUptime":"134h:54m:33s"}`)
		case "CM/SysInfo":
			fmt.Fprint(w, `{"errCode":"000","errMsg":"","ntAccess":"Allowed","ip":"2600:1700::1 / 73.15.42.18","subMask":"255.255.252.0","gw":"73.15.40.1","lease":"241823","Configname":"midco.cfg"}`)
		case "CM/DocsisProvision":
			fmt.Fprint(w, `{"errCode":"000","errMsg":"","hwInit":"Completed","findDownstream":"Completed","ranging":"Completed","dhcp":"Completed","timeOfday":"Completed","downloadCfg":"Completed","registration":"Completed","bpiStatus":"Enabled"}`)
		case "CM/DsInfo":
			fmt.Fprint(w, `{"errCode":"000","Freq_List":[
				{"portId":1,"frequency":"555000000","modulation":"QAM256","signalStrength":"3.4","snr":"41.2","channelId":"1","correcteds":"120","uncorrect":"0"},
				{"portId":2,"frequency":"561000000","modulation":"QAM256","signalStrength":"-inf","snr":"n/a","channelId":"2","correcteds":"0","uncorrect":"0"}
			]}`)
		case "CM/UsInfo":
			fmt.Fprint(w, `{"errCode":"000","Freq_List":[
				{"portId":"1","frequency":"17600000","modtype":"ATDMA","signalStrength":"44.5","bandwidth":"6400000","channelId":"1","scdmaMode":"ATDMA"}
			]}`)
		case "CM/DsOfdm":
			fmt.Fprint(w, `{"errCode":"000","Freq_List":[
				{"index":1,"receive":null,"plcpower":null,"SNR":null,"Subcarr0freqFreq":null},
				{"index":4,"receive":"OPERATE","ffttype":"4K","Subcarr0freqFreq":"750000000","plclock":"1","ncplock":"1","mdc1lock":"1","plcpower":"1.8","SNR":"42.1","correcteds":"0","uncorrect":"0"}
			]}`)
		case "CM/UsOfdm":
			fmt.Fprint(w, `{"errCode":"000","Freq_List":[
				{"uschindex":"5","state":"OPERATE","frequency":"45000000","channelBw":"40","repPower":"45.5","repPower1_6":"44.8","digAtten":"0"},
				{"uschindex":null,"state":null,"frequency":null}
			]}`)
		case "CM/Log":
			fmt.Fprint(w, `{"errCode":"000","Log_List":[{"index":"1","time":"Thu Nov 26 03:12:44 2026","type":"Information","priority":"5","event":"MDD message timeout"}]}`)
		case "Advanced/AdvancedSwitch":
			fmt.Fprint(w, `{"errCode":"000","advSwitch_List":[{"portId":1,"power":"On","neg":"Auto","speed":"1Gbps","duplex":"Full","linked":"Up"}]}`)
		case "Users/Login":
			_ = r.ParseForm()
			var model map[string]string
			_ = json.Unmarshal([]byte(r.Form.Get("model")), &model)
			if model["username"] != "mso" {
				fmt.Fprint(w, `{"errCode":"000","result":"Error_Username_Wrong"}`)
				return
			}
			fmt.Fprint(w, `{"errCode":"000","result":"success"}`)
		case "CM/Reboot":
			_ = r.ParseForm()
			fmt.Fprintf(w, `{"errCode":"000","errMsg":"","echo":%s}`, r.Form.Get("model"))
		default:
			http.NotFound(w, r)
		}
	}
}

func TestSnapshotCoda6X(t *testing.T) {
	server := httptest.NewServer(fakeCoda6X())
	defer server.Close()

	client, err := NewClient(server.URL, Credentials{}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client.Kind = KindCoda6X
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Model != "CODA-60" || snapshot.Firmware != "8.5.0.0.4b1" {
		t.Fatalf("identity = %+v", snapshot)
	}
	if len(snapshot.DS) != 2 || len(snapshot.US) != 1 {
		t.Fatalf("channels = %d DS, %d US", len(snapshot.DS), len(snapshot.US))
	}
	if snapshot.DS[1].HasPower {
		t.Fatal("-inf must not count as a power reading")
	}
	if len(snapshot.OFDM) != 1 || len(snapshot.OFDMA) != 1 {
		t.Fatalf("OFDM/OFDMA = %d/%d; empty slots must be dropped", len(snapshot.OFDM), len(snapshot.OFDMA))
	}
	if snapshot.StepsOK() != 8 {
		t.Fatalf("steps ok = %d", snapshot.StepsOK())
	}
	if snapshot.WANIP != "73.15.42.18" {
		t.Fatalf("WANIP = %q", snapshot.WANIP)
	}
	if snapshot.UptimeSeconds != 134*3600+54*60+33 {
		t.Fatalf("uptime = %d", snapshot.UptimeSeconds)
	}
	if len(snapshot.Logs) != 1 || len(snapshot.Ethernet) != 1 {
		t.Fatalf("logs/ethernet = %d/%d", len(snapshot.Logs), len(snapshot.Ethernet))
	}
	if len(snapshot.Warnings) != 0 {
		t.Fatalf("warnings = %v", snapshot.Warnings)
	}
}

func TestSnapshotKeepsGoingWhenOneEndpointFails(t *testing.T) {
	base := fakeCoda6X()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "CM/Log") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		base(w, r)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{}, 3*time.Second)
	client.Kind = KindCoda6X
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Warnings) != 1 || !strings.Contains(snapshot.Warnings[0], "CM/Log") {
		t.Fatalf("warnings = %v", snapshot.Warnings)
	}
	if len(snapshot.DS) == 0 {
		t.Fatal("the rest of the snapshot should survive")
	}
}

func TestDetectCoda6X(t *testing.T) {
	server := httptest.NewServer(fakeCoda6X())
	defer server.Close()

	client, err := Detect(context.Background(), server.URL, Credentials{}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.Kind != KindCoda6X {
		t.Fatalf("kind = %q", client.Kind)
	}
}

func TestDetectReportsAuthGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := Detect(context.Background(), server.URL, Credentials{}, time.Second)
	var auth *AuthRequiredError
	if !errors.As(err, &auth) {
		t.Fatalf("want AuthRequiredError, got %v", err)
	}
	if auth.Kind != KindCoda6X {
		t.Fatalf("kind = %q", auth.Kind)
	}
}

func TestLoginAndReboot(t *testing.T) {
	server := httptest.NewServer(fakeCoda6X())
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{Username: "mso", Password: "msopassword"}, 3*time.Second)
	client.Kind = KindCoda6X
	ctx := context.Background()
	if err := client.Login(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := client.Reboot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	echo, _ := result["echo"].(map[string]any)
	if fmt.Sprint(echo["reboot"]) != "1" {
		t.Fatalf("reboot body = %v", result["echo"])
	}
	// doRebootAction() reads all three flags before it picks one, and a missing
	// key is a PHP fatal on this build, so every body must carry all three.
	for _, key := range []string{"factoryReset", "restoreReset"} {
		if _, ok := echo[key]; !ok {
			t.Fatalf("body for reboot is missing %q: %v", key, result["echo"])
		}
	}
}

func TestHardResetTreatsADroppedConnectionAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/CM/Reboot") {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			conn.Close() // the firmware reboots before it finishes answering
			return
		}
		fakeCoda6X()(w, r)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{Username: "mso", Password: "msopassword"}, 3*time.Second)
	client.Kind = KindCoda6X
	if err := client.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := client.HardReset(context.Background())
	if err != nil {
		t.Fatalf("HardReset reported %v; a dropped connection is the success signal", err)
	}
	if got := fmt.Sprint(result["result"]); !strings.Contains(got, "closed the connection") {
		t.Fatalf("result = %v", result)
	}
}

func TestLoginRejectsWrongUser(t *testing.T) {
	server := httptest.NewServer(fakeCoda6X())
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{Username: "nobody", Password: "wrong"}, 3*time.Second)
	client.Kind = KindCoda6X
	err := client.Login(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Error_Username_Wrong") {
		t.Fatalf("login error = %v", err)
	}
}

func TestCoda57ActionUnsupported(t *testing.T) {
	client, _ := NewClient("127.0.0.1", Credentials{}, time.Second)
	client.Kind = KindCoda57
	_, err := client.Reboot(context.Background())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"192.168.100.1", "http://192.168.100.1"},
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"http://192.168.0.1/", "http://192.168.0.1"},
		{"https://example.test/base/", "https://example.test/base"},
	}
	for _, tc := range cases {
		parsed, err := normalizeBase(tc.in)
		if err != nil {
			t.Fatalf("normalizeBase(%q): %v", tc.in, err)
		}
		if parsed.String() != tc.want {
			t.Fatalf("normalizeBase(%q) = %q, want %q", tc.in, parsed.String(), tc.want)
		}
	}
	if _, err := normalizeBase("ftp://host"); err == nil {
		t.Fatal("ftp should be rejected")
	}
}

func TestCoda6XLoginSendsModelForm(t *testing.T) {
	var got url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		got = r.PostForm
		fmt.Fprint(w, `{"errCode":"000","result":"success"}`)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{Username: "admin", Password: "pw"}, 2*time.Second)
	client.Kind = KindCoda6X
	if err := client.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	var model map[string]string
	if err := json.Unmarshal([]byte(got.Get("model")), &model); err != nil {
		t.Fatal(err)
	}
	if model["username"] != "admin" || model["password"] != "pw" {
		t.Fatalf("login body = %v", model)
	}
}
