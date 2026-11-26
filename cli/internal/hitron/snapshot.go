package hitron

import (
	"context"
	"fmt"
)

// Snapshot reads the current state of whichever family the client was
// detected as. It is safe to call repeatedly; each call is a new read.
func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	var snap *Snapshot
	var err error
	switch c.Kind {
	case KindCoda6X:
		snap, err = c.snapshotCoda6X(ctx)
	case KindCoda57:
		snap, err = c.snapshotCoda57(ctx)
	case KindMoca:
		snap, err = c.snapshotMoca(ctx)
	default:
		return nil, fmt.Errorf("unknown device family")
	}
	if err == nil && snap != nil {
		snap.CMTSMac = CMTSMacFromLogs(snap.Logs)
	}
	return snap, err
}
