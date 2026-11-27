package hitron

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// ErrAuthRequired is returned when a device answers with a login gate. It is
// a sentinel so callers can tell "we need credentials" from "the device is
// broken" without string matching. Most paths return *AuthRequiredError, which
// wraps this value.
var ErrAuthRequired = errors.New("device requires authentication")

const maxBody = 32 << 20 // 32 MiB is far more than any console response

// Client is one HTTP session against one device. The same client is reused
// between snapshots so the browser cookies (PHP session, CSRF token) survive.
type Client struct {
	Base    *url.URL
	HTTP    *http.Client
	Kind    Kind
	Creds   Credentials
	Timeout time.Duration

	// DefaultCreds is set when detection fell back to the HT-EM5 factory
	// admin/admin pair. Reads may use it; writes must not, so the caller can
	// tell "the user gave credentials" from "the detector guessed".
	DefaultCreds bool

	csrf string
}

// NewClient normalizes a host and prepares an HTTP session. host may be a bare
// IP, an IP with a port ("127.0.0.1:8080"), a hostname, or a full URL.
func NewClient(host string, creds Credentials, timeout time.Duration) (*Client, error) {
	base, err := normalizeBase(host)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	jar, _ := cookiejar.New(nil)
	return &Client{
		Base:    base,
		HTTP:    &http.Client{Timeout: timeout, Jar: jar},
		Creds:   creds,
		Timeout: timeout,
	}, nil
}

func normalizeBase(host string) (*url.URL, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("no host given")
	}
	if !strings.Contains(host, "://") {
		// Ports and IPv6 literals both keep working here.
		host = "http://" + host
	}
	parsed, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("invalid host %q: %w", host, err)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("invalid host %q", host)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q (use http or https)", parsed.Scheme)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	if parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid host %q", host)
	}
	return parsed, nil
}

// Addr is the host:port shown in the UI.
func (c *Client) Addr() string {
	host := c.Base.Hostname()
	if port := c.Base.Port(); port != "" && port != "80" && port != "443" {
		return net.JoinHostPort(host, port)
	}
	return host
}

func (c *Client) url(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return c.Base.String() + path
}

type requestOptions struct {
	method      string
	path        string
	body        []byte
	contentType string
	basic       bool
	accept      string
	headers     map[string]string
}

// do performs one request and returns the status and body. It never treats a
// 4xx as an error: the three families disagree about what a 400 means.
func (c *Client) do(ctx context.Context, opts requestOptions) (*http.Response, []byte, error) {
	var body io.Reader
	if opts.body != nil {
		body = bytes.NewReader(opts.body)
	}
	req, err := http.NewRequestWithContext(ctx, opts.method, c.url(opts.path), body)
	if err != nil {
		return nil, nil, err
	}
	if opts.contentType != "" {
		req.Header.Set("Content-Type", opts.contentType)
	}
	if opts.accept != "" {
		req.Header.Set("Accept", opts.accept)
	}
	for key, value := range opts.headers {
		req.Header.Set(key, value)
	}
	if c.Creds.AppToken != "" {
		req.Header.Set("Ht-App-Auth", c.Creds.AppToken)
	}
	if opts.basic && c.Creds.Username != "" {
		req.SetBasicAuth(c.Creds.Username, c.Creds.Password)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return resp, nil, err
	}
	if c.HTTP.Jar != nil {
		for _, cookie := range c.HTTP.Jar.Cookies(c.Base) {
			if cookie.Name == "csrf_token" {
				c.csrf = cookie.Value
			}
		}
	}
	return resp, data, nil
}

func isAuthStatus(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

func looksLikeHTML(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '<'
}

func (c *Client) authError(detail string) error {
	return &AuthRequiredError{Kind: c.Kind, Host: c.Addr(), Detail: detail}
}

// getJSON reads a JSON object or array and unwraps the firmware's error
// envelope. An HTML response on an API path is a login page, not a payload.
func (c *Client) getJSON(ctx context.Context, path string) (any, error) {
	resp, body, err := c.do(ctx, requestOptions{method: http.MethodGet, path: path, accept: "application/json, */*"})
	if err != nil {
		return nil, err
	}
	if isAuthStatus(resp.StatusCode) {
		return nil, c.authError(fmt.Sprintf("HTTP %d on %s", resp.StatusCode, path))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	if looksLikeHTML(body) {
		return nil, c.authError("device answered with a login page")
	}
	value, err := decodeJSON(body)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	if object, ok := value.(map[string]any); ok {
		code := str(object["errCode"])
		if code != "" && code != "000" {
			return nil, fmt.Errorf("%s: device error %s %s", path, code, str(object["errMsg"]))
		}
	}
	return value, nil
}

func decodeJSON(body []byte) (any, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty response")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return value, nil
}

// postModel posts the firmware's form-encoded {"model": <json>} body.
func (c *Client) postModel(ctx context.Context, path string, model any) (map[string]any, error) {
	payload, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	form := url.Values{"model": {string(payload)}}
	resp, body, err := c.do(ctx, requestOptions{
		method:      http.MethodPost,
		path:        path,
		body:        []byte(form.Encode()),
		contentType: "application/x-www-form-urlencoded",
		accept:      "application/json, */*",
	})
	if err != nil {
		return nil, err
	}
	if isAuthStatus(resp.StatusCode) {
		return nil, c.authError(fmt.Sprintf("HTTP %d on %s", resp.StatusCode, path))
	}
	if looksLikeHTML(body) {
		return nil, c.authError("device answered with a login page")
	}
	value, err := decodeJSON(body)
	if err != nil {
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("POST %s: HTTP %d", path, resp.StatusCode)
		}
		return nil, fmt.Errorf("POST %s: %w", path, err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("POST %s: unexpected %T response", path, value)
	}
	code := str(object["errCode"])
	if code != "" && code != "000" {
		return nil, fmt.Errorf("%s: device error %s %s", path, code, str(object["errMsg"]))
	}
	return object, nil
}

// Login authenticates against the family's confirmed login flow. It is only
// needed for writes: reads on the analyzed firmware are anonymous.
func (c *Client) Login(ctx context.Context) error {
	switch c.Kind {
	case KindCoda6X:
		return c.loginCoda6X(ctx)
	case KindCoda57:
		// The legacy API's login page is primed with a GET; no credential
		// POST has been confirmed against a real unit.
		return c.primeCoda57(ctx)
	case KindMoca:
		if !c.Creds.Complete() {
			return fmt.Errorf("HT-EM5 needs a username and password (default admin/admin)")
		}
		// HTTP Basic is sent on every request; prove it works with one read.
		if _, err := c.rpc(ctx, 1, 0x302, "GET", nil, nil); err != nil {
			return fmt.Errorf("HT-EM5 login failed: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown device; run detect first")
	}
}

func (c *Client) loginCoda6X(ctx context.Context) error {
	if !c.Creds.Complete() && c.Creds.AppToken == "" {
		return fmt.Errorf("CODA6X writes need --username and --password (or an app token)")
	}
	if c.Creds.AppToken != "" && !c.Creds.Complete() {
		// App-token auth is checked by protected routes themselves; there is
		// no separate login call for it.
		return nil
	}
	payload, _ := json.Marshal(map[string]string{
		"username": c.Creds.Username,
		"password": c.Creds.Password,
	})
	object, err := c.postModel(ctx, "/1/Device/Users/Login", json.RawMessage(payload))
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	if result := str(object["result"]); result != "" &&
		!strings.EqualFold(result, "success") {
		return fmt.Errorf("login failed: %s", result)
	}
	return nil
}

// primeCoda57 GETs /login.html so the device can set its PHPSESSID cookie,
// matching the confirmed behavior of the legacy client.
func (c *Client) primeCoda57(ctx context.Context) error {
	_, _, err := c.do(ctx, requestOptions{method: http.MethodGet, path: "/login.html", accept: "text/html,*/*"})
	if err != nil {
		return err
	}
	return nil
}

// coda57Probe is a cheap read used only to check credentials on an HT-EM5
// client that was constructed by hand. It is deliberately private and unused
// outside Login.
func (c *Client) coda57Probe(ctx context.Context) (any, error) {
	return c.getJSON(ctx, "/data/getSysInfo.asp")
}

func joinWarnings(warnings []string, format string, args ...any) []string {
	return append(warnings, fmt.Sprintf(format, args...))
}
