package hitron

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Detect identifies the device family and returns a client pointed at it.
//
// Order matters. The CODA6X has a JSON route on a firmware-exempt path, so it
// answers instantly and unambiguously. The CODA-57 is a different URL family
// on the same devices. The HT-EM5 answers only its /ms/<type>/<reg> RPC, which
// needs Basic auth, so it is tried last with the documented default
// admin/admin unless the caller supplied credentials.
func Detect(ctx context.Context, host string, creds Credentials, timeout time.Duration) (*Client, error) {
	c, err := NewClient(host, creds, timeout)
	if err != nil {
		return nil, err
	}

	c.Kind = KindCoda6X
	if err := probeCoda6X(ctx, c); err == nil {
		c.Kind = KindCoda6X
		return c, nil
	} else if isAuthErr(err) {
		c.Kind = KindCoda6X
		if creds.Complete() {
			if loginErr := c.loginCoda6X(ctx); loginErr == nil {
				if retryErr := probeCoda6X(ctx, c); retryErr == nil {
					return c, nil
				}
			}
		}
		return nil, err
	}

	c.Kind = KindCoda57
	if err := probeCoda57(ctx, c); err == nil {
		c.Kind = KindCoda57
		return c, nil
	} else if isAuthErr(err) {
		c.Kind = KindCoda57
		return nil, err
	}

	// HT-EM5: Basic auth. Use the factory default only if the caller gave no
	// credentials at all, and never persist it back into their options.
	mocaCreds := creds
	defaulted := false
	if !mocaCreds.Complete() && mocaCreds.AppToken == "" {
		mocaCreds = Credentials{Username: "admin", Password: "admin"}
		defaulted = true
	}
	mc, err := NewClient(host, mocaCreds, timeout)
	if err != nil {
		return nil, err
	}
	mc.DefaultCreds = defaulted
	mc.Kind = KindMoca
	if err := probeMoca(ctx, mc); err == nil {
		mc.Kind = KindMoca
		return mc, nil
	} else if isAuthErr(err) {
		mc.Kind = KindMoca
		return nil, err
	}

	return nil, fmt.Errorf("no Hitron device answered at %s (tried CODA6X, CODA-57, and HT-EM5)", c.Addr())
}

func isAuthErr(err error) bool {
	var auth *AuthRequiredError
	return errors.As(err, &auth) || errors.Is(err, ErrAuthRequired)
}

func probeCoda6X(ctx context.Context, c *Client) error {
	value, err := c.getJSON(ctx, "/1/Device/CM/Version")
	if err != nil {
		return err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("not a CODA6X version object")
	}
	for _, key := range []string{"modelName", "swVersion", "hwVersion", "serialNumber", "deploymentName"} {
		if strings.TrimSpace(str(object[key])) != "" {
			return nil
		}
	}
	// The mock and some builds return null for the descriptive fields but keep
	// the standard envelope; the route existing at all is the signal.
	if code := str(object["errCode"]); code == "000" {
		return nil
	}
	return fmt.Errorf("not a CODA6X version object")
}

func probeCoda57(ctx context.Context, c *Client) error {
	if err := c.primeCoda57(ctx); err != nil {
		return err
	}
	value, err := c.getJSON(ctx, "/data/getSysInfo.asp")
	if err != nil {
		return err
	}
	switch payload := value.(type) {
	case map[string]any:
		if _, ok := payload["rfMac"]; ok {
			return nil
		}
		if _, ok := payload["systemUptime"]; ok {
			return nil
		}
	case []any:
		if len(payload) > 0 {
			if item, ok := payload[0].(map[string]any); ok {
				if _, ok := item["rfMac"]; ok {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("not a CODA-57 sysinfo payload")
}

func probeMoca(ctx context.Context, c *Client) error {
	// Load index.html first, the same way the browser does, so the server can
	// hand out its csrf_token cookie.
	resp, body, err := c.do(ctx, requestOptions{
		method: http.MethodGet, path: "/index.html", accept: "text/html,*/*", basic: true,
	})
	if err != nil {
		return err
	}
	if isAuthStatus(resp.StatusCode) {
		return c.authError("HT-EM5 answered 401; Basic auth is required")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET /index.html: HTTP %d", resp.StatusCode)
	}
	if len(body) == 0 {
		return fmt.Errorf("empty index.html")
	}
	if _, err := c.rpc(ctx, 1, 0x302, "GET", nil, nil); err != nil {
		return err
	}
	return nil
}
