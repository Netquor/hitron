package hitron

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	fecDOCSISPath = "/1/Device/CM/DsInfo"
	fecOFDMPath   = "/1/Device/CM/DsOfdm"
)

// FECReset reports what a downstream-FEC counter clear actually cleared.
//
// The firmware splits the counters over two endpoints: PUT CM/DsInfo clears the
// DOCSIS 3.0 counters (ModelInfo::doClearDsFec writes
// Docsis.ClearAllDownstreamFECCounter), and PUT CM/DsOfdm clears the OFDM ones.
// Both answer {"errCode":"000"} and neither needs a session.
//
// The counts are relative to the channels that were counting before the clear,
// because channels that were already clean prove nothing: on an idle unit most
// 3.0 counters sit at 0/0 and always read back that way.
type FECReset struct {
	DOCSISBusy    int      // 3.0 channels with a non-zero counter before the clear
	DOCSISCleared int      // ...that read zero afterwards
	OFDMBusy      int      // OFDM channels with a non-zero counter before the clear
	OFDMCleared   int      // ...that read zero afterwards
	OFDMNotZero   []string // OFDM channels still counting at the best attempt
}

// String is the one-line summary the TUI prints.
func (r FECReset) String() string {
	return "FEC reset: " + r.Detail()
}

// Detail is the summary without the "FEC reset:" prefix, for callers that
// already printed a header.
func (r FECReset) Detail() string {
	var parts []string
	if r.DOCSISBusy == 0 {
		parts = append(parts, "3.0 already clean")
	} else {
		parts = append(parts, fmt.Sprintf("3.0 %d/%d cleared", r.DOCSISCleared, r.DOCSISBusy))
	}
	if r.OFDMBusy == 0 {
		parts = append(parts, "OFDM already clean")
	} else {
		parts = append(parts, fmt.Sprintf("OFDM %d/%d cleared", r.OFDMCleared, r.OFDMBusy))
	}
	out := strings.Join(parts, ", ")
	if len(r.OFDMNotZero) > 0 {
		out += fmt.Sprintf(" (OFDM %s still counting)", strings.Join(r.OFDMNotZero, ","))
	}
	return out
}

// fecRead retries a read that failed transiently. The OFDM route occasionally
// answers HTTP 500 for a single request while the RDKB bridge is applying a
// clear; the same request succeeds immediately afterwards. Auth failures are
// real and are not retried.
func (c *Client) fecRead(ctx context.Context, path string) (any, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(150 * time.Millisecond):
			}
		}
		value, err := c.getJSON(ctx, path)
		if err == nil {
			return value, nil
		}
		var auth *AuthRequiredError
		if errors.As(err, &auth) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// fecDOCSISBusy maps the 3.0 channels that have a non-zero corrected or
// uncorrected count.
func (c *Client) fecDOCSISBusy(ctx context.Context) (map[string]bool, error) {
	value, err := c.fecRead(ctx, fecDOCSISPath)
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if object, ok := value.(map[string]any); ok {
		parseCodaDS(&snap, object)
	}
	busy := map[string]bool{}
	for _, ch := range snap.DS {
		if ch.Corrected != 0 || ch.Uncorrect != 0 {
			busy[ch.ID] = true
		}
	}
	return busy, nil
}

// fecOFDMBusy does the same for the OFDM channels. "NA" rows are inactive
// receivers and never appear in the snapshot, so they cannot become "busy".
func (c *Client) fecOFDMBusy(ctx context.Context) (map[string]bool, error) {
	value, err := c.fecRead(ctx, fecOFDMPath)
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if object, ok := value.(map[string]any); ok {
		parseCodaOFDM(&snap, object)
	}
	busy := map[string]bool{}
	for _, ch := range snap.OFDM {
		if ch.Corrected != 0 || ch.Uncorrect != 0 {
			busy[ch.Index] = true
		}
	}
	return busy, nil
}

// ResetFEC clears the DOCSIS 3.0 and OFDM downstream FEC counters together and
// reports how many of the channels that were counting before now read zero.
//
// Measured on 8.7.1.0.1b4 (2026-11-26): the 3.0 stop clears every busy channel
// in one call. The OFDM stop is unreliable - one PUT zeroes between zero and two
// of the live channels, which ones vary per call, and the handler ignores the
// body and query string, so there is no way to target a channel. This repeats
// the OFDM clear a bounded number of times and keeps the best observation, so
// the caller sees what actually happened instead of a claim.
//
// The vendor's own "Reset FEC Counters" button only calls the 3.0 endpoint, so
// it misses OFDM entirely.
func (c *Client) ResetFEC(ctx context.Context) (FECReset, error) {
	if c.Kind != KindCoda6X {
		return FECReset{}, fmt.Errorf("FEC reset on %s: %w (CODA6X only)", c.Kind.Label(), ErrUnsupported)
	}

	busyDS, err := c.fecDOCSISBusy(ctx)
	if err != nil {
		return FECReset{}, err
	}
	busyOFDM, err := c.fecOFDMBusy(ctx)
	if err != nil {
		return FECReset{}, err
	}
	result := FECReset{DOCSISBusy: len(busyDS), OFDMBusy: len(busyOFDM)}

	if err := c.putClear(ctx, fecDOCSISPath); err != nil {
		return FECReset{}, err
	}
	afterDS, err := c.fecDOCSISBusy(ctx)
	if err != nil {
		return FECReset{}, err
	}
	for id := range busyDS {
		if !afterDS[id] {
			result.DOCSISCleared++
		}
	}

	const attempts = 8
	best := -1
	for attempt := 0; attempt < attempts; attempt++ {
		if err := c.putClear(ctx, fecOFDMPath); err != nil {
			return FECReset{}, err
		}
		afterOFDM, err := c.fecOFDMBusy(ctx)
		if err != nil {
			return FECReset{}, err
		}
		cleared := 0
		var still []string
		for id := range busyOFDM {
			if afterOFDM[id] {
				still = append(still, id)
			} else {
				cleared++
			}
		}
		sort.Strings(still)
		if cleared > best {
			best = cleared
			result.OFDMCleared, result.OFDMNotZero = cleared, still
		}
		if len(still) == 0 {
			break
		}
	}
	return result, nil
}

// putClear issues a bare PUT. The handlers ignore the body, and the vendor page
// sends none, so there is no model envelope to build.
func (c *Client) putClear(ctx context.Context, path string) error {
	resp, body, err := c.do(ctx, requestOptions{method: http.MethodPut, path: path, accept: "application/json, */*"})
	if err != nil {
		return err
	}
	if isAuthStatus(resp.StatusCode) {
		return c.authError(fmt.Sprintf("HTTP %d on %s", resp.StatusCode, path))
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PUT %s: HTTP %d %s", path, resp.StatusCode, firstLine(string(body)))
	}
	if looksLikeHTML(body) {
		return c.authError("device answered with a login page")
	}
	value, err := decodeJSON(body)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", path, err)
	}
	if object, ok := value.(map[string]any); ok {
		if code := str(object["errCode"]); code != "" && code != "000" {
			return fmt.Errorf("%s: device error %s %s", path, code, str(object["errMsg"]))
		}
	}
	return nil
}

// firstLine trims an error body to something that fits on one line.
func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if index := strings.IndexAny(text, "\r\n"); index >= 0 {
		text = text[:index]
	}
	if len(text) > 120 {
		text = text[:120] + "…"
	}
	return strings.TrimSpace(text)
}
