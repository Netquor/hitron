package hitron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Every payload in this file is SYNTHETIC. The CODA 4x8x family is
// experimental and has not been verified on live hardware; these tests only
// pin the hypothesis that it serves the CODA6X data model from the site root.

// fakeCoda4x8x serves the synthetic CODA6X payloads from the site root and
// answers 404 under /1/Device. Non-GET requests are counted in writes.
func fakeCoda4x8x(writes *int32) http.HandlerFunc {
	inner := fakeCoda6X()
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/1/Device/") {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.URL.Path != "/Users/Login" {
			atomic.AddInt32(writes, 1)
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/1/Device" + r.URL.Path
		inner(w, r2)
	}
}

func TestDetectPicksCoda4x8xForRootOnlyServer(t *testing.T) {
	var writes int32
	server := httptest.NewServer(fakeCoda4x8x(&writes))
	defer server.Close()

	client, err := Detect(context.Background(), server.URL, Credentials{}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.Kind != KindCoda4x8x {
		t.Fatalf("kind = %s, want %s", client.Kind, KindCoda4x8x)
	}
	if !strings.Contains(client.Kind.Label(), "CODA 4x8x") || !strings.Contains(client.Kind.Label(), "experimental") {
		t.Fatalf("label = %q", client.Kind.Label())
	}
	if n := atomic.LoadInt32(&writes); n != 0 {
		t.Fatalf("detection wrote %d times", n)
	}
}

// A 401 on every path must never make Detect POST credentials to the site
// root (the CODA6X path under /1/Device keeps its own, existing login).
func TestDetectNeverPostsCredentialsToAnUnknownGate(t *testing.T) {
	var posts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !strings.HasPrefix(r.URL.Path, "/1/Device/") {
			atomic.AddInt32(&posts, 1)
		}
		http.Error(w, "login", http.StatusUnauthorized)
	}))
	defer server.Close()
	_, _ = Detect(context.Background(), server.URL, Credentials{Username: "u", Password: "p"}, 3*time.Second)
	if n := atomic.LoadInt32(&posts); n != 0 {
		t.Fatalf("Detect sent %d non-GET requests (root credential POST) to an unidentified device", n)
	}
}

func TestProbeCoda4x8xRejectsErrCodeOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"errCode":"000"}`)
	}))
	defer server.Close()
	c, _ := NewClient(server.URL, Credentials{}, 3*time.Second)
	c.Kind = KindCoda4x8x
	if err := probeCoda4x8x(context.Background(), c); err == nil {
		t.Fatal("an errCode alone must not match")
	}
}

func TestDetectStillPicksCoda6XUnderDevicePrefix(t *testing.T) {
	server := httptest.NewServer(fakeCoda6X())
	defer server.Close()

	client, err := Detect(context.Background(), server.URL, Credentials{}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.Kind != KindCoda6X {
		t.Fatalf("kind = %s, want %s", client.Kind, KindCoda6X)
	}
}

func TestDetectCoda4x8xRejectsHTMLAnd404(t *testing.T) {
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body>router login</body></html>")
	}))
	defer html.Close()
	notFound := httptest.NewServer(http.NotFoundHandler())
	defer notFound.Close()

	for name, url := range map[string]string{"html": html.URL, "404": notFound.URL} {
		c, err := NewClient(url, Credentials{}, 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		c.Kind = KindCoda4x8x
		if err := probeCoda4x8x(context.Background(), c); err == nil {
			t.Fatalf("%s: probe matched", name)
		}
		if _, err := Detect(context.Background(), url, Credentials{Username: "u", Password: "p"}, 3*time.Second); err == nil {
			t.Fatalf("%s: Detect matched a non-device", name)
		} else if auth := (*AuthRequiredError)(nil); errors.As(err, &auth) && auth.Kind == KindCoda4x8x {
			t.Fatalf("%s: an HTML page must not be reported as a 4x8x", name)
		}
	}
}

func TestDetectCoda4x8xRejectsJSONWithoutVersionFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"hello":"world"}`)
	}))
	defer server.Close()
	c, _ := NewClient(server.URL, Credentials{}, 3*time.Second)
	c.Kind = KindCoda4x8x
	if err := probeCoda4x8x(context.Background(), c); err == nil {
		t.Fatal("arbitrary JSON must not match")
	}
}

func TestDetectErrorListsAllFamilies(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	_, err := Detect(context.Background(), server.URL, Credentials{}, 3*time.Second)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"CODA6X", "CODA 4x8x", "CODA-57", "HT-EM5"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %s", err, want)
		}
	}
}

func TestSnapshotCoda4x8xReadsRootPaths(t *testing.T) {
	var writes int32
	var seen int32
	base := fakeCoda4x8x(&writes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/1/Device") {
			atomic.AddInt32(&seen, 1)
		}
		base(w, r)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{}, 3*time.Second)
	client.Kind = KindCoda4x8x
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Kind != KindCoda4x8x {
		t.Fatalf("snapshot kind = %s", snapshot.Kind)
	}
	if len(snapshot.DS) != 2 || len(snapshot.US) != 1 || len(snapshot.OFDM) != 1 || len(snapshot.OFDMA) != 1 {
		t.Fatalf("channels = %d/%d/%d/%d", len(snapshot.DS), len(snapshot.US), len(snapshot.OFDM), len(snapshot.OFDMA))
	}
	if len(snapshot.Warnings) != 0 {
		t.Fatalf("warnings = %v", snapshot.Warnings)
	}
	if atomic.LoadInt32(&seen) != 0 {
		t.Fatal("the 4x8x client must never request /1/Device paths")
	}
	if atomic.LoadInt32(&writes) != 0 {
		t.Fatal("a snapshot must not write")
	}
}

func TestCoda4x8xActionsAreUnsupportedAndNeverReachTheDevice(t *testing.T) {
	var writes int32
	var hits int32
	base := fakeCoda4x8x(&writes)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		base(w, r)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{Username: "u", Password: "p"}, 3*time.Second)
	client.Kind = KindCoda4x8x
	ctx := context.Background()

	calls := map[string]func() error{
		"reboot":        func() error { _, err := client.Reboot(ctx); return err },
		"factory-reset": func() error { _, err := client.FactoryReset(ctx); return err },
		"hard-reset":    func() error { _, err := client.HardReset(ctx); return err },
		"coda6xAction": func() error {
			_, err := client.coda6xAction(ctx, rebootBody("reboot"))
			return err
		},
		"fec-reset": func() error { _, err := client.ResetFEC(ctx); return err },
		"putClear":  func() error { return client.putClear(ctx, fecDOCSISPath) },
		"channels":  func() error { _, err := client.SpectrumChannels(ctx); return err },
		"sweep":     func() error { _, err := client.SpectrumSweep(ctx, 645); return err },
		"realtime":  func() error { _, err := client.RealTimeSpectrum(ctx); return err },
		"const":     func() error { _, err := client.Constellation(ctx, 24); return err },
	}
	for name, call := range calls {
		err := call()
		if !errors.Is(err, ErrCoda4x8xReadOnly) || !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s: err = %v, want the typed read-only error", name, err)
		}
		if !strings.Contains(err.Error(), "not supported on CODA 4x8x (experimental, read-only)") {
			t.Fatalf("%s: message = %q", name, err)
		}
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatalf("unsupported actions made %d requests", hits)
	}
}

func TestCoda4x8xLoginSendsModelFormToRoot(t *testing.T) {
	var path, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		path = r.URL.Path
		body = r.Form.Get("model")
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"errCode":"000","result":"success"}`)
	}))
	defer server.Close()

	client, _ := NewClient(server.URL, Credentials{Username: "user", Password: "pass"}, 3*time.Second)
	client.Kind = KindCoda4x8x
	if err := client.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if path != "/Users/Login" {
		t.Fatalf("login path = %q", path)
	}
	var model map[string]string
	if err := json.Unmarshal([]byte(body), &model); err != nil {
		t.Fatalf("model body %q: %v", body, err)
	}
	if model["username"] != "user" || model["password"] != "pass" || len(model) != 2 {
		t.Fatalf("model = %v", model)
	}

	noCreds, _ := NewClient(server.URL, Credentials{}, 3*time.Second)
	noCreds.Kind = KindCoda4x8x
	if err := noCreds.Login(context.Background()); err == nil {
		t.Fatal("login without credentials must fail before any request")
	}
}

func TestAPIRoot(t *testing.T) {
	c, _ := NewClient("127.0.0.1", Credentials{}, time.Second)
	c.Kind = KindCoda6X
	if got := c.api("/CM/DsInfo"); got != "/1/Device/CM/DsInfo" {
		t.Fatalf("coda6x api = %q", got)
	}
	c.Kind = KindCoda4x8x
	if got := c.api("/CM/DsInfo"); got != "/CM/DsInfo" {
		t.Fatalf("coda4x8x api = %q", got)
	}
}
