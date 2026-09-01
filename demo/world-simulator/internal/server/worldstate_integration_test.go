//go:build integration

// GetWorldState exercises real Redis rather than a mock, per
// docs/ENGINEERING.md section 1 ("do not mock what you own"). Needs the
// docker-compose stack up.

package server

import (
	"context"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
)

func TestGetWorldStateReportsQueuedEntriesWithoutDraining(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	ctx := context.Background()

	q := newQueue(redisClient)
	now := time.Now()
	due := now.Add(90 * time.Second)
	if err := q.schedule(ctx, delayedOutcome{RecordID: "rec-pending", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS}, due); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	s := New(pool, redisClient, clock.New(), noScale, nil, "")
	resp, err := s.GetWorldState(ctx, &worldsimv1.GetWorldStateRequest{})
	if err != nil {
		t.Fatalf("GetWorldState: %v", err)
	}
	if len(resp.GetPending()) != 1 {
		t.Fatalf("Pending = %d entries, want 1: %+v", len(resp.GetPending()), resp.GetPending())
	}
	got := resp.GetPending()[0]
	if got.GetRecordId() != "rec-pending" {
		t.Errorf("RecordId = %q, want rec-pending", got.GetRecordId())
	}
	if got.GetAttemptNumber() != 1 {
		t.Errorf("AttemptNumber = %d, want 1", got.GetAttemptNumber())
	}
	if got.GetOutcome() != commonv1.Outcome_OUTCOME_SUCCESS {
		t.Errorf("Outcome = %v, want SUCCESS", got.GetOutcome())
	}
	if !got.GetDueAt().AsTime().Equal(due.Truncate(time.Second)) {
		t.Errorf("DueAt = %v, want %v", got.GetDueAt().AsTime(), due)
	}

	// Read-only: the entry must still be there afterwards.
	second, err := s.GetWorldState(ctx, &worldsimv1.GetWorldStateRequest{})
	if err != nil {
		t.Fatalf("second GetWorldState: %v", err)
	}
	if len(second.GetPending()) != 1 {
		t.Errorf("second GetWorldState returned %d entries, want 1 (GetWorldState must not drain the queue)", len(second.GetPending()))
	}
}

func TestGetWorldStateOnEmptyQueueReturnsNoPending(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	s := New(pool, redisClient, clock.New(), noScale, nil, "")

	resp, err := s.GetWorldState(context.Background(), &worldsimv1.GetWorldStateRequest{})
	if err != nil {
		t.Fatalf("GetWorldState: %v", err)
	}
	if len(resp.GetPending()) != 0 {
		t.Errorf("Pending = %+v, want empty", resp.GetPending())
	}
}
