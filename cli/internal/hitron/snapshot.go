package hitron

import (
	"context"
	"fmt"
)

// Snapshot reads the current state of whichever family the client was
// detected as. It is safe to call repeatedly; each call is a new read.
func (c *Client) Snapshot(ctx context.Context) (*Snapshot, error) {
	switch c.Kind {
	case KindCoda6X:
		return c.snapshotCoda6X(ctx)
	case KindCoda57:
		return c.snapshotCoda57(ctx)
	case KindMoca:
		return c.snapshotMoca(ctx)
	default:
		return nil, fmt.Errorf("unknown device family")
	}
}
