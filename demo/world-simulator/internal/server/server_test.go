//go:build integration

// SimulateOutcome exercises real Postgres and real Redis rather than a
// mock, per docs/ENGINEERING.md section 1 ("do not mock what you own").
// Needs the docker-compose stack up. Run with `make test-integration`.

package server

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func noScale(d time.Duration) time.Duration { return d }

func TestSimulateOutcomeRetrySuccessIsImmediate(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	ctx := context.Background()
	recordID := seedRecordWithGroundTruth(ctx, t, pool, "BANK_TIMEOUT", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 0.80, 0.05, 300)

	s := New(pool, redisClient, clock.New(), noScale, nil, "")
	s.rng = fixedRand(0.10) // < 0.80, correct action (RETRY), succeeds

	resp, err := s.SimulateOutcome(ctx, &worldsimv1.SimulateOutcomeRequest{
		RecordId: recordID, ActionType: commonv1.ActionType_ACTION_TYPE_RETRY, AttemptNumber: 1,
	})
	if err != nil {
		t.Fatalf("SimulateOutcome: %v", err)
	}
	if resp.Outcome != commonv1.Outcome_OUTCOME_SUCCESS {
		t.Errorf("Outcome = %v, want SUCCESS", resp.Outcome)
	}
	if !resp.Immediate {
		t.Error("Immediate = false, want true for a retry")
	}
	if resp.FailureCode != "" {
		t.Errorf("FailureCode = %q, want empty on success", resp.FailureCode)
	}
}

func TestSimulateOutcomeRetryFailureCarriesTheRecordsOriginalFailureCode(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	ctx := context.Background()
	recordID := seedRecordWithGroundTruth(ctx, t, pool, "BANK_TIMEOUT", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 0.80, 0.05, 300)

	s := New(pool, redisClient, clock.New(), noScale, nil, "")
	s.rng = fixedRand(0.90) // >= 0.80, correct action, fails

	resp, err := s.SimulateOutcome(ctx, &worldsimv1.SimulateOutcomeRequest{
		RecordId: recordID, ActionType: commonv1.ActionType_ACTION_TYPE_RETRY, AttemptNumber: 2,
	})
	if err != nil {
		t.Fatalf("SimulateOutcome: %v", err)
	}
	if resp.Outcome != commonv1.Outcome_OUTCOME_FAILURE {
		t.Errorf("Outcome = %v, want FAILURE", resp.Outcome)
	}
	if !resp.Immediate {
		t.Error("Immediate = false, want true for a retry")
	}
	if resp.FailureCode != "BANK_TIMEOUT" {
		t.Errorf("FailureCode = %q, want the record's original BANK_TIMEOUT", resp.FailureCode)
	}
}

func TestSimulateOutcomeWrongActionUsesWrongActionProbability(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	ctx := context.Background()
	// HARD_DECLINE's correct action is NUDGE_METHOD_UPDATE (bucket.go); a
	// RETRY here is the wrong action.
	recordID := seedRecordWithGroundTruth(ctx, t, pool, "CARD_EXPIRED", "ROOT_CAUSE_BUCKET_HARD_DECLINE", 0.80, 0.05, 300)

	s := New(pool, redisClient, clock.New(), noScale, nil, "")
	s.rng = fixedRand(0.10) // would succeed against RecoveryProbability 0.80, must not against WrongActionProbability 0.05

	resp, err := s.SimulateOutcome(ctx, &worldsimv1.SimulateOutcomeRequest{
		RecordId: recordID, ActionType: commonv1.ActionType_ACTION_TYPE_RETRY, AttemptNumber: 1,
	})
	if err != nil {
		t.Fatalf("SimulateOutcome: %v", err)
	}
	if resp.Outcome != commonv1.Outcome_OUTCOME_FAILURE {
		t.Errorf("Outcome = %v, want FAILURE (wrong action, 0.10 >= WrongActionProbability 0.05)", resp.Outcome)
	}
}

func TestSimulateOutcomeNudgeIsPendingAndSchedulesTheDelayedOutcome(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	ctx := context.Background()
	recordID := seedRecordWithGroundTruth(ctx, t, pool, "CARD_EXPIRED", "ROOT_CAUSE_BUCKET_HARD_DECLINE", 0.15, 0.02, 3600)

	start := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	fake := clock.NewFake(start)
	s := New(pool, redisClient, fake, noScale, nil, "")
	s.rng = fixedRand(0.10) // < 0.15 RecoveryProbability, correct action, succeeds

	resp, err := s.SimulateOutcome(ctx, &worldsimv1.SimulateOutcomeRequest{
		RecordId: recordID, ActionType: commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, AttemptNumber: 1,
	})
	if err != nil {
		t.Fatalf("SimulateOutcome: %v", err)
	}
	if resp.Outcome != commonv1.Outcome_OUTCOME_PENDING {
		t.Errorf("Outcome = %v, want PENDING", resp.Outcome)
	}
	if resp.Immediate {
		t.Error("Immediate = true, want false for a nudge with a nonzero delay")
	}
	wantResolvesAt := start.Add(3600 * time.Second)
	if !resp.ResolvesAt.AsTime().Equal(wantResolvesAt) {
		t.Errorf("ResolvesAt = %v, want %v", resp.ResolvesAt.AsTime(), wantResolvesAt)
	}

	q := newQueue(redisClient)
	delivered, malformed, err := q.due(ctx, wantResolvesAt)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(malformed) != 0 {
		t.Fatalf("malformed = %v, want none", malformed)
	}
	if len(delivered) != 1 {
		t.Fatalf("len(delivered) = %d, want 1 (the scheduled nudge outcome)", len(delivered))
	}
	if delivered[0].RecordID != recordID || delivered[0].AttemptNumber != 1 || delivered[0].Outcome != commonv1.Outcome_OUTCOME_SUCCESS {
		t.Errorf("delivered[0] = %+v, want the rolled SUCCESS for %s attempt 1", delivered[0], recordID)
	}
}

func TestSimulateOutcomeNudgeWithZeroDelayResolvesImmediately(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	ctx := context.Background()
	recordID := seedRecordWithGroundTruth(ctx, t, pool, "PAYMENT_RISK_CHECK_FAILED", "ROOT_CAUSE_BUCKET_HARD_DECLINE", 0.15, 0.02, 0)

	s := New(pool, redisClient, clock.New(), noScale, nil, "")
	s.rng = fixedRand(0.01)

	// NUDGE_METHOD_UPDATE, not ESCALATE: isNudge must actually be true here
	// or this test would pass for the wrong reason (it did, once, when
	// this used ESCALATE -- adversarial verification below caught that the
	// zero-delay check itself was gone but this test stayed green, because
	// isNudge(ESCALATE) is false regardless of the check it claims to
	// cover).
	resp, err := s.SimulateOutcome(ctx, &worldsimv1.SimulateOutcomeRequest{
		RecordId: recordID, ActionType: commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, AttemptNumber: 1,
	})
	if err != nil {
		t.Fatalf("SimulateOutcome: %v", err)
	}
	if !resp.Immediate {
		t.Error("Immediate = false, want true when the profile's response delay is zero")
	}
	if resp.Outcome != commonv1.Outcome_OUTCOME_SUCCESS {
		t.Errorf("Outcome = %v, want SUCCESS (0.01 < RecoveryProbability 0.15)", resp.Outcome)
	}
}

func TestSimulateOutcomeScalesTheResponseDelay(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	ctx := context.Background()
	recordID := seedRecordWithGroundTruth(ctx, t, pool, "PAYMENT_CANCELLED", "ROOT_CAUSE_BUCKET_ABANDONMENT", 0.25, 0.05, 3600)

	start := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	fake := clock.NewFake(start)
	half := func(d time.Duration) time.Duration { return d / 2 }
	s := New(pool, redisClient, fake, half, nil, "")
	s.rng = fixedRand(0.10)

	resp, err := s.SimulateOutcome(ctx, &worldsimv1.SimulateOutcomeRequest{
		RecordId: recordID, ActionType: commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, AttemptNumber: 1,
	})
	if err != nil {
		t.Fatalf("SimulateOutcome: %v", err)
	}
	want := start.Add(1800 * time.Second) // 3600s scaled by 1/2
	if !resp.ResolvesAt.AsTime().Equal(want) {
		t.Errorf("ResolvesAt = %v, want %v (3600s halved)", resp.ResolvesAt.AsTime(), want)
	}
}

func TestSimulateOutcomeUnknownRecordNotFound(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	s := New(pool, redisClient, clock.New(), noScale, nil, "")

	_, err := s.SimulateOutcome(context.Background(), &worldsimv1.SimulateOutcomeRequest{
		RecordId: uuid.NewString(), ActionType: commonv1.ActionType_ACTION_TYPE_RETRY,
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("SimulateOutcome for unknown record: err = %v, want NotFound", err)
	}
}

func TestSimulateOutcomeMissingRecordID(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	s := New(pool, redisClient, clock.New(), noScale, nil, "")

	_, err := s.SimulateOutcome(context.Background(), &worldsimv1.SimulateOutcomeRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SimulateOutcome with no record_id: err = %v, want InvalidArgument", err)
	}
}
