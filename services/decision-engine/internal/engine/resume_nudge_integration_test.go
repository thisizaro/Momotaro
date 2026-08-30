//go:build integration

// Phase 5 Unit A: ReportDelayedOutcome's engine-side logic
// (Scheduler.ResumeNudge), proven against real Postgres. The gRPC layer
// itself (services/decision-engine/internal/server) is tested separately
// with a fake, since it has no logic of its own beyond request validation
// and translating this method's result to and from the proto shapes.

package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func TestResumeNudgeAppliesSuccessOutcome(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Time{})
	if _, err := pool.Exec(ctx, `UPDATE record_state SET attempt_count=1, due_at=NULL WHERE record_id=$1`, recordID); err != nil {
		t.Fatalf("seed attempt_count: %v", err)
	}

	sched := NewScheduler(pool, &fakeClassifier{}, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	applied, state, err := sched.ResumeNudge(ctx, recordID, 1, commonv1.Outcome_OUTCOME_SUCCESS, "")
	if err != nil {
		t.Fatalf("ResumeNudge: %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}
	if state != commonv1.RecordState_RECORD_STATE_RECOVERED {
		t.Errorf("resultingState = %v, want RECOVERED", state)
	}

	var dbState string
	if err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id=$1`, recordID).Scan(&dbState); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if dbState != commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
		t.Errorf("record_state.current_state = %q, want RECOVERED", dbState)
	}

	var reason string
	if err := pool.QueryRow(ctx, `SELECT reason FROM audit_entry WHERE record_id=$1 AND from_state=$2 AND to_state=$3`,
		recordID, commonv1.RecordState_RECORD_STATE_NUDGED.String(), commonv1.RecordState_RECORD_STATE_RECOVERED.String(),
	).Scan(&reason); err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	if reason != "action succeeded" {
		t.Errorf("audit_entry.reason = %q, want %q", reason, "action succeeded")
	}

	// Phase 5 Unit F: applyResumedOutcome's transaction is the one that
	// actually recovered the record, an amount that only exists because
	// World Simulator's delayed-outcome callback arrived, so
	// recovered_delta_paise must carry it here even though nothing in this
	// call was synchronous with the original nudge send.
	evt := waitForAuditEvent(t, auditTopic, recordID, 5*time.Second)
	if evt.FromState != commonv1.RecordState_RECORD_STATE_NUDGED.String() {
		t.Errorf("audit event FromState = %q, want NUDGED", evt.FromState)
	}
	if evt.ToState != commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
		t.Errorf("audit event ToState = %q, want RECOVERED", evt.ToState)
	}
	if evt.RecoveredDeltaPaise != 10000 {
		t.Errorf("audit event RecoveredDeltaPaise = %d, want 10000", evt.RecoveredDeltaPaise)
	}
}

// A fresh record has retry budget and positive-EV priors left
// (TRANSIENT_BANK, attempt 2), so a failed nudge outcome must re-score
// back into Scoring and land on RETRY_SCHEDULED, exactly the re-entry
// docs/ARCHITECTURE.md section 7 describes for a synchronous execute
// failure -- proving ResumeNudge takes the same path, not an escalate-only
// shortcut, for the async case.
func TestResumeNudgeReSchedulesRatherThanEscalatingOnFailureOutcome(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Time{})
	if _, err := pool.Exec(ctx, `UPDATE record_state SET attempt_count=1, due_at=NULL WHERE record_id=$1`, recordID); err != nil {
		t.Fatalf("seed attempt_count: %v", err)
	}

	sched := NewScheduler(pool, &fakeClassifier{}, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	applied, state, err := sched.ResumeNudge(ctx, recordID, 1, commonv1.Outcome_OUTCOME_FAILURE, "CUSTOMER_UNREACHABLE")
	if err != nil {
		t.Fatalf("ResumeNudge: %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}
	if state != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED {
		t.Errorf("resultingState = %v, want RETRY_SCHEDULED (re-scored, not escalated)", state)
	}

	rows, err := pool.Query(ctx, `SELECT from_state, to_state FROM audit_entry WHERE record_id=$1 ORDER BY id`, recordID)
	if err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	defer rows.Close()
	var got [][2]string
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, [2]string{from, to})
	}
	want := [][2]string{
		{commonv1.RecordState_RECORD_STATE_NUDGED.String(), commonv1.RecordState_RECORD_STATE_SCORING.String()},
		{commonv1.RecordState_RECORD_STATE_SCORING.String(), commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String()},
	}
	if len(got) != len(want) {
		t.Fatalf("trail = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("trail[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// attempt_number is what stops a delayed report that arrives after the
// record has already moved on (re-scored, a fresh attempt sent) from
// being misapplied to the wrong attempt -- exactly what
// decisionengine.proto's own comment on the field warns about.
func TestResumeNudgeDiscardsStaleAttemptNumber(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Time{})
	if _, err := pool.Exec(ctx, `UPDATE record_state SET attempt_count=2, due_at=NULL WHERE record_id=$1`, recordID); err != nil {
		t.Fatalf("seed attempt_count: %v", err)
	}

	sched := NewScheduler(pool, &fakeClassifier{}, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	applied, state, err := sched.ResumeNudge(ctx, recordID, 1, commonv1.Outcome_OUTCOME_SUCCESS, "")
	if err != nil {
		t.Fatalf("ResumeNudge: %v", err)
	}
	if applied {
		t.Error("applied = true, want false: attempt_number 1 is stale, the record is waiting on attempt 2")
	}
	if state != commonv1.RecordState_RECORD_STATE_NUDGED {
		t.Errorf("resultingState = %v, want the record's actual current state NUDGED", state)
	}

	var dbState string
	if err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id=$1`, recordID).Scan(&dbState); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if dbState != commonv1.RecordState_RECORD_STATE_NUDGED.String() {
		t.Errorf("record_state.current_state = %q, want unchanged NUDGED", dbState)
	}
}

// A record that already reached a terminal state (or was never in NUDGED
// at all) must discard a delayed report rather than reopen it.
//
// pending_action is forced to real SQL NULL here, not seedScheduled's
// literal "ACTION_TYPE_UNSPECIFIED" string: a real terminal record has
// NULL there (nullIfUnspecified, store.go), and loadNudged's first version
// scanned it into a plain string and crashed on exactly this case, caught
// only by a live smoke test against the running binary, not this suite,
// because seedScheduled's fixture never produced a real NULL. This test
// exists specifically to make that gap impossible to reopen silently.
func TestResumeNudgeDiscardsWhenNotInNudgedState(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RECOVERED, commonv1.ActionType_ACTION_TYPE_UNSPECIFIED, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Time{})
	if _, err := pool.Exec(ctx, `UPDATE record_state SET attempt_count=1, due_at=NULL, pending_action=NULL WHERE record_id=$1`, recordID); err != nil {
		t.Fatalf("seed attempt_count: %v", err)
	}

	sched := NewScheduler(pool, &fakeClassifier{}, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	applied, state, err := sched.ResumeNudge(ctx, recordID, 1, commonv1.Outcome_OUTCOME_SUCCESS, "")
	if err != nil {
		t.Fatalf("ResumeNudge: %v", err)
	}
	if applied {
		t.Error("applied = true, want false: the record already reached RECOVERED")
	}
	if state != commonv1.RecordState_RECORD_STATE_RECOVERED {
		t.Errorf("resultingState = %v, want the record's actual current state RECOVERED", state)
	}
}

func TestResumeNudgeDiscardsUnknownRecord(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()

	sched := NewScheduler(pool, &fakeClassifier{}, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	applied, state, err := sched.ResumeNudge(ctx, uuid.NewString(), 1, commonv1.Outcome_OUTCOME_SUCCESS, "")
	if err != nil {
		t.Fatalf("ResumeNudge: %v", err)
	}
	if applied {
		t.Error("applied = true, want false: the record_id does not exist")
	}
	if state != commonv1.RecordState_RECORD_STATE_UNSPECIFIED {
		t.Errorf("resultingState = %v, want UNSPECIFIED", state)
	}
}

// The concurrency proof, mirroring TestSchedulerConcurrentSchedulersClaimExactlyOnce:
// this RPC is at-least-once (decisionengine.proto), so a redelivered or
// duplicated report can arrive genuinely concurrently with itself, and only
// applyResumedOutcome's row lock (store.go) stops both copies from applying.
// 25 concurrent callers, not 2, for the same reason claimDue's test uses 25:
// true database-level overlap needs enough contenders to be reliable across
// runs, not just possible in principle.
func TestResumeNudgeConcurrentReportsApplyExactlyOnce(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_NUDGED, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Time{})
	if _, err := pool.Exec(ctx, `UPDATE record_state SET attempt_count=1, due_at=NULL WHERE record_id=$1`, recordID); err != nil {
		t.Fatalf("seed attempt_count: %v", err)
	}

	sched := NewScheduler(pool, &fakeClassifier{}, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	const numReporters = 25
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	appliedCount := 0
	errs := make(chan error, numReporters)
	for i := 0; i < numReporters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			applied, _, err := sched.ResumeNudge(ctx, recordID, 1, commonv1.Outcome_OUTCOME_SUCCESS, "")
			if err != nil {
				errs <- err
				return
			}
			if applied {
				mu.Lock()
				appliedCount++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("ResumeNudge: %v", err)
	}

	if appliedCount != 1 {
		t.Errorf("applied = true for %d of %d concurrent reports, want exactly 1", appliedCount, numReporters)
	}

	var entryCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_entry WHERE record_id=$1`, recordID).Scan(&entryCount); err != nil {
		t.Fatalf("query audit_entry: %v", err)
	}
	if entryCount != 1 {
		t.Errorf("audit_entry rows = %d, want exactly 1 (NUDGED -> RECOVERED once, not once per racer)", entryCount)
	}
}
