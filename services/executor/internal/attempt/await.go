package attempt

import (
	"context"
	"fmt"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
)

const (
	// pollInterval is how often a duplicate re-reads the row while the
	// original caller is still between its claim and its outcome write.
	pollInterval = 5 * time.Millisecond

	// awaitBudget bounds that wait. Deliberately short: the window it covers
	// is one in-process port call, so anything longer means the original
	// caller died between claiming the slot and recording the answer, leaving
	// a row nothing will ever resolve. Waiting on the caller's full RPC
	// deadline for that is a hang dressed up as patience, and it produces a
	// far less useful error than saying what actually happened.
	awaitBudget = 500 * time.Millisecond
)

// ErrAbandonedClaim means the slot is claimed but unresolved and stayed that
// way, which in practice means the process holding it died mid-attempt. The
// safe answer is to report it rather than guess: re-running the action could
// double-charge, and inventing an outcome would put a fiction in the trail.
var ErrAbandonedClaim = fmt.Errorf("attempt slot is claimed but was never resolved")

// Await answers a duplicate request from the row the original already
// claimed, polling briefly in case the original is still mid-flight.
//
// Polling via the injected clock rather than time.Sleep so this is testable
// without a real wait (docs/ENGINEERING.md sections 1 and 2).
func (s *Store) Await(ctx context.Context, clk clock.Clock, recordID string, attemptNumber int32) (Recorded, error) {
	deadline := clk.Now().Add(awaitBudget)
	for {
		rec, resolved, err := s.Load(ctx, recordID, attemptNumber)
		if err != nil {
			return Recorded{}, err
		}
		if resolved {
			return rec, nil
		}
		if !clk.Now().Before(deadline) {
			return Recorded{}, fmt.Errorf("%w: record %s attempt %d", ErrAbandonedClaim, recordID, attemptNumber)
		}
		select {
		case <-ctx.Done():
			return Recorded{}, ctx.Err()
		case <-clk.After(pollInterval):
		}
	}
}
