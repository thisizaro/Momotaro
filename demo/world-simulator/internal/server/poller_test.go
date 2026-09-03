//go:build integration

// Poller exercises real Redis (see queue_test.go's testRedis) against a
// fake DecisionEngineServiceClient: the cross-service gRPC boundary is
// faked the same way services/decision-engine/internal/engine's own
// tests fake the Executor client (testhelpers_test.go's fakeExecutor),
// not a network call to a real decision-engine binary.

package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	decisionenginev1 "github.com/thisizaro/Momotaro/proto/gen/decisionengine/v1"
	"google.golang.org/grpc"
)

// fakeDecisionEngine records every ReportDelayedOutcome call it receives.
// failuresBeforeSuccess makes the first N calls fail, to exercise
// deliver's bounded retry.
type fakeDecisionEngine struct {
	mu                    sync.Mutex
	calls                 []*decisionenginev1.ReportDelayedOutcomeRequest
	failuresBeforeSuccess int
	callCount             int
}

func (f *fakeDecisionEngine) ReportDelayedOutcome(ctx context.Context, in *decisionenginev1.ReportDelayedOutcomeRequest, opts ...grpc.CallOption) (*decisionenginev1.ReportDelayedOutcomeResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if f.callCount <= f.failuresBeforeSuccess {
		return nil, fmt.Errorf("simulated transient failure %d", f.callCount)
	}
	f.calls = append(f.calls, in)
	return &decisionenginev1.ReportDelayedOutcomeResponse{Applied: true, ResultingState: commonv1.RecordState_RECORD_STATE_RECOVERED}, nil
}

// ReportDowntimeEvent is unused by World Simulator (it only ever calls
// ReportDelayedOutcome); present so fakeDecisionEngine still satisfies
// decisionenginev1.DecisionEngineServiceClient after
// docs/PHASE5_5_IMPLEMENTATION.md Unit Y added this RPC to the same service.
func (f *fakeDecisionEngine) ReportDowntimeEvent(ctx context.Context, in *decisionenginev1.ReportDowntimeEventRequest, opts ...grpc.CallOption) (*decisionenginev1.ReportDowntimeEventResponse, error) {
	return &decisionenginev1.ReportDowntimeEventResponse{Applied: true}, nil
}

// GetAgentConfig is here only so fakeDecisionEngine satisfies
// decisionenginev1.DecisionEngineServiceClient (Unit AM added the method to
// that interface). World Simulator never calls this RPC, only the
// Gateway does, so an empty response is fine: nothing in this package's
// tests reads it.
func (f *fakeDecisionEngine) GetAgentConfig(ctx context.Context, in *decisionenginev1.GetAgentConfigRequest, opts ...grpc.CallOption) (*decisionenginev1.GetAgentConfigResponse, error) {
	return &decisionenginev1.GetAgentConfigResponse{}, nil
}

func (f *fakeDecisionEngine) callsMade() []*decisionenginev1.ReportDelayedOutcomeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*decisionenginev1.ReportDelayedOutcomeRequest(nil), f.calls...)
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitForCallCount advances fake in small steps, interleaved with a real
// (short) sleep to let the goroutine under test actually reach its next
// clock.After wait, until the fake decision engine has recorded want
// calls. Robust against scheduling jitter in a way a fixed-count
// sleep-then-Advance loop is not: it keeps nudging the clock rather than
// assuming a exact number of steps lines up with the goroutine's progress.
func waitForCallCount(t *testing.T, de *fakeDecisionEngine, want int, fake *clock.Fake, step time.Duration) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		de.mu.Lock()
		got := de.callCount
		de.mu.Unlock()
		if got >= want {
			return
		}
		fake.Advance(step)
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("callCount did not reach %d within the deadline", want)
}

func TestPollerTickDeliversDueEntries(t *testing.T) {
	redisClient := testRedis(t)
	q := newQueue(redisClient)
	ctx := context.Background()
	now := time.Now()

	if err := q.schedule(ctx, delayedOutcome{RecordID: "rec-1", AttemptNumber: 2, Outcome: commonv1.Outcome_OUTCOME_SUCCESS}, now.Add(-time.Second)); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	de := &fakeDecisionEngine{}
	p := NewPoller(redisClient, de, clock.New(), time.Second, time.Second, discardLog())
	if err := p.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	calls := de.callsMade()
	if len(calls) != 1 {
		t.Fatalf("len(calls) = %d, want 1", len(calls))
	}
	if calls[0].RecordId != "rec-1" || calls[0].AttemptNumber != 2 || calls[0].Outcome != commonv1.Outcome_OUTCOME_SUCCESS {
		t.Errorf("call = %+v, want the scheduled rec-1/2/SUCCESS", calls[0])
	}
}

func TestPollerTickIgnoresNotYetDueEntries(t *testing.T) {
	redisClient := testRedis(t)
	q := newQueue(redisClient)
	ctx := context.Background()
	now := time.Now()

	if err := q.schedule(ctx, delayedOutcome{RecordID: "rec-future", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS}, now.Add(time.Hour)); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	de := &fakeDecisionEngine{}
	p := NewPoller(redisClient, de, clock.New(), time.Second, time.Second, discardLog())
	if err := p.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(de.callsMade()) != 0 {
		t.Errorf("calls = %v, want none (not yet due)", de.callsMade())
	}
}

func TestPollerDeliverRetriesOnTransientFailureThenSucceeds(t *testing.T) {
	redisClient := testRedis(t)
	q := newQueue(redisClient)
	ctx := context.Background()
	now := time.Now()

	if err := q.schedule(ctx, delayedOutcome{RecordID: "rec-flaky", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS}, now.Add(-time.Second)); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	de := &fakeDecisionEngine{failuresBeforeSuccess: 2}
	fake := clock.NewFake(now)
	p := NewPoller(redisClient, de, fake, time.Second, time.Second, discardLog())

	done := make(chan error, 1)
	go func() { done <- p.tick(ctx) }()

	// deliver's retry loop waits on p.clock.After(deliverRetryDelay)
	// between attempts (docs/ENGINEERING.md section 2: no real sleep).
	waitForCallCount(t, de, 3, fake, deliverRetryDelay)

	if err := <-done; err != nil {
		t.Fatalf("tick: %v", err)
	}
	if de.callCount != 3 {
		t.Errorf("callCount = %d, want 3 (2 failures then a success)", de.callCount)
	}
	if len(de.callsMade()) != 1 {
		t.Errorf("len(calls) = %d, want 1 delivered call", len(de.callsMade()))
	}
}

func TestPollerDeliverGivesUpAfterMaxAttempts(t *testing.T) {
	redisClient := testRedis(t)
	q := newQueue(redisClient)
	ctx := context.Background()
	now := time.Now()

	if err := q.schedule(ctx, delayedOutcome{RecordID: "rec-dead", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_FAILURE, FailureCode: "BANK_TIMEOUT"}, now.Add(-time.Second)); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	de := &fakeDecisionEngine{failuresBeforeSuccess: maxDeliverAttempts + 10}
	fake := clock.NewFake(now)
	p := NewPoller(redisClient, de, fake, time.Second, time.Second, discardLog())

	done := make(chan error, 1)
	go func() { done <- p.tick(ctx) }()

	waitForCallCount(t, de, maxDeliverAttempts, fake, deliverRetryDelay)

	if err := <-done; err != nil {
		t.Fatalf("tick: %v", err)
	}
	if de.callCount != maxDeliverAttempts {
		t.Errorf("callCount = %d, want exactly maxDeliverAttempts (%d), no further retries after giving up", de.callCount, maxDeliverAttempts)
	}
	if len(de.callsMade()) != 0 {
		t.Errorf("len(calls) = %d, want 0: every attempt failed, nothing should count as delivered", len(de.callsMade()))
	}
}

func TestPollerTickLogsMalformedMembersButStillDeliversTheRest(t *testing.T) {
	redisClient := testRedis(t)
	q := newQueue(redisClient)
	ctx := context.Background()
	now := time.Now()

	if err := redisClient.ZAdd(ctx, delayedOutcomesKey, redis.Z{
		Score: float64(now.Add(-time.Second).Unix()), Member: "not-enough-fields",
	}).Err(); err != nil {
		t.Fatalf("seed malformed member: %v", err)
	}
	if err := q.schedule(ctx, delayedOutcome{RecordID: "rec-good", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS}, now.Add(-time.Second)); err != nil {
		t.Fatalf("schedule good: %v", err)
	}

	de := &fakeDecisionEngine{}
	p := NewPoller(redisClient, de, clock.New(), time.Second, time.Second, discardLog())
	if err := p.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	calls := de.callsMade()
	if len(calls) != 1 || calls[0].RecordId != "rec-good" {
		t.Errorf("calls = %+v, want exactly the well-formed rec-good entry", calls)
	}
}
