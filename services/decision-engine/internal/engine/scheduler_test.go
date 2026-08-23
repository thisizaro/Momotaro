//go:build integration

package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
)

func schedulerTestConfig(dlqTopic string) SchedulerConfig {
	return SchedulerConfig{CallTimeout: 2 * time.Second, PollInterval: 50 * time.Millisecond, DLQTopic: dlqTopic}
}

// seedScheduled parks a record in a waiting state with the given due_at, as
// if HandleMessage had already classified and scheduled it.
func seedScheduled(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID string, state commonv1.RecordState, pendingAction commonv1.ActionType, dueAt time.Time) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO record_state (record_id, current_state, attempt_count, pending_action, due_at)
		VALUES ($1, $2, 0, $3, $4)`,
		recordID, state.String(), pendingAction.String(), dueAt)
	if err != nil {
		t.Fatalf("seed record_state: %v", err)
	}
}

func TestSchedulerClaimsDueRetryAndRecordsSuccess(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, time.Now().Add(-time.Minute))

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS, CostPaise: 20}}
	sched := NewScheduler(pool, executor, dlqProducer, clock.New(), schedulerTestConfig(dlqTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	var state string
	var attemptCount int
	if err := pool.QueryRow(ctx, `SELECT current_state, attempt_count FROM record_state WHERE record_id=$1`, recordID).Scan(&state, &attemptCount); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
		t.Errorf("current_state = %q, want RECOVERED", state)
	}
	if attemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1", attemptCount)
	}
	// Not asserting executor.calls here: claimDue is deliberately a
	// system-wide query with no per-test scoping (that is its actual job in
	// production), so running alongside other integration/e2e tests
	// against the same shared Postgres can have it also claim an unrelated
	// due record. The state and audit assertions below are scoped to this
	// record_id and are the real proof.

	var entryCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_entry WHERE record_id=$1`, recordID).Scan(&entryCount); err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	if entryCount != 2 {
		t.Errorf("audit_entry rows = %d, want 2 (claim transition + outcome transition)", entryCount)
	}
}

func TestSchedulerIgnoresNotYetDueRecords(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, time.Now().Add(time.Hour))

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS}}
	sched := NewScheduler(pool, executor, dlqProducer, clock.New(), schedulerTestConfig(dlqTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Scoped to this record_id specifically, not a global call count: see
	// the comment in TestSchedulerClaimsDueRetryAndRecordsSuccess for why.
	var state string
	var dueAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT current_state, due_at FROM record_state WHERE record_id=$1`, recordID).Scan(&state, &dueAt); err != nil {
		t.Fatalf("query: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String() {
		t.Errorf("current_state = %q, want unchanged RETRY_SCHEDULED", state)
	}
	if dueAt == nil || !dueAt.After(time.Now()) {
		t.Errorf("due_at = %v, want unchanged and still in the future", dueAt)
	}
}

func TestSchedulerParksNudgeAsPendingAwaitingDelayedOutcome(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, time.Now().Add(-time.Minute))

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_PENDING}}
	sched := NewScheduler(pool, executor, dlqProducer, clock.New(), schedulerTestConfig(dlqTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	var state string
	var dueAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT current_state, due_at FROM record_state WHERE record_id=$1`, recordID).Scan(&state, &dueAt); err != nil {
		t.Fatalf("query: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_NUDGED.String() {
		t.Errorf("current_state = %q, want NUDGED", state)
	}
	if dueAt != nil {
		t.Errorf("due_at = %v, want NULL: Phase 1 has no delayed-outcome callback to schedule against yet", *dueAt)
	}
}

func TestSchedulerEscalatesOnExecuteFailure(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, time.Now().Add(-time.Minute))

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_FAILURE, FailureCode: "HARD_DECLINE"}}
	sched := NewScheduler(pool, executor, dlqProducer, clock.New(), schedulerTestConfig(dlqTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id=$1`, recordID).Scan(&state); err != nil {
		t.Fatalf("query: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_ESCALATED.String() {
		t.Errorf("current_state = %q, want ESCALATED", state)
	}
}

func TestSchedulerDeadLettersAfterExecuteRetriesExhausted(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, time.Now().Add(-time.Minute))

	executor := &fakeExecutor{err: errors.New("executor unavailable")}
	sched := NewScheduler(pool, executor, dlqProducer, clock.New(), schedulerTestConfig(dlqTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// At least maxExecuteAttempts, not exactly: this fake always errors, so
	// an unrelated due record claimed alongside this one (see the comment
	// in TestSchedulerClaimsDueRetryAndRecordsSuccess) would also retry
	// against it and add to the count. The dead letter check below is what
	// actually proves this record's bounded retry ran out.
	if executor.calls < maxExecuteAttempts {
		t.Errorf("executor called %d times, want at least maxExecuteAttempts=%d", executor.calls, maxExecuteAttempts)
	}

	dl := waitForDeadLetter(t, dlqTopic, recordID, 10*time.Second)
	if dl.RecordID != recordID {
		t.Errorf("dead letter RecordID = %q, want %q", dl.RecordID, recordID)
	}

	// The claim transition (Scheduled -> Retrying) already happened and is
	// not undone: a permanently failing execute is a processing failure
	// reported via the DLQ, distinct from Escalated (docs/ARCHITECTURE.md
	// section 8b), so the record is left exactly where Execute found it.
	var state string
	if err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id=$1`, recordID).Scan(&state); err != nil {
		t.Fatalf("query: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_RETRYING.String() {
		t.Errorf("current_state = %q, want RETRYING (left as claimed, not silently marked terminal)", state)
	}
}

func TestSchedulerNeverDoubleClaimsTheSameRecord(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic := testDLQ(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, time.Now().Add(-time.Minute))

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS}}
	sched := NewScheduler(pool, executor, dlqProducer, clock.New(), schedulerTestConfig(dlqTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if err := sched.tick(ctx); err != nil {
		t.Fatalf("second tick: %v", err)
	}

	// Scoped to this record_id, not executor.calls globally (see the
	// comment in TestSchedulerClaimsDueRetryAndRecordsSuccess): due_at is
	// cleared on claim, so a second tick reclaiming this same record would
	// show up as attempt_count advancing past 1, or a second audit_entry
	// pair for it.
	var attemptCount int
	if err := pool.QueryRow(ctx, `SELECT attempt_count FROM record_state WHERE record_id=$1`, recordID).Scan(&attemptCount); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if attemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1: a second tick must not reclaim a terminal record", attemptCount)
	}
	var entryCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_entry WHERE record_id=$1`, recordID).Scan(&entryCount); err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	if entryCount != 2 {
		t.Errorf("audit_entry rows = %d, want 2 (one claim + one outcome, not doubled by a second tick)", entryCount)
	}
}
