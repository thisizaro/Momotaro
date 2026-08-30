//go:build integration

package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
)

func schedulerTestConfig(dlqTopic, auditTopic string) SchedulerConfig {
	return SchedulerConfig{
		CallTimeout:      2 * time.Second,
		PollInterval:     50 * time.Millisecond,
		DLQTopic:         dlqTopic,
		AuditEventsTopic: auditTopic,
		RetryDelay:       time.Minute,
		NudgeDelay:       time.Minute,
		TimeScale:        1,
		Guardrails:       testGuardrails,
	}
}

// seedScheduled parks a record in a waiting state with the given due_at and
// bucket, as if HandleMessage had already classified and scheduled it.
func seedScheduled(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID string, state commonv1.RecordState, pendingAction commonv1.ActionType, bucket commonv1.RootCauseBucket, dueAt time.Time) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO record_state (record_id, current_state, attempt_count, pending_action, root_cause_bucket, due_at)
		VALUES ($1, $2, 0, $3, $4, $5)`,
		recordID, state.String(), pendingAction.String(), bucket.String(), dueAt)
	if err != nil {
		t.Fatalf("seed record_state: %v", err)
	}
}

// seedEVSnapshot sets record_state's economics decision snapshot directly,
// standing in for what a real scheduleNew/recordRescore write would have
// left there, for tests that only care whether the scheduler reads and
// forwards it correctly rather than how it got there.
func seedEVSnapshot(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID string, evScorePaise, pRecovery float64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE record_state SET ev_score_at_decision=$1, p_recovery_at_decision=$2 WHERE record_id=$3`,
		evScorePaise, pRecovery, recordID); err != nil {
		t.Fatalf("seed ev snapshot: %v", err)
	}
}

func TestSchedulerClaimsDueRetryAndRecordsSuccess(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	batchID, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Now().Add(-time.Minute))

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS, CostPaise: 20}}
	sched := NewScheduler(pool, &fakeClassifier{}, executor, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

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

	// Phase 5 Unit F: recordOutcome's transition IS the one that recovered
	// the record, so recovered_delta_paise must carry its real amount
	// (seedRecord's fixed 10000), not 0, so a live dashboard can animate
	// the running total without a refetch.
	evt := waitForAuditEvent(t, auditTopic, recordID, 5*time.Second)
	if evt.BatchID != batchID {
		t.Errorf("audit event BatchID = %q, want %q", evt.BatchID, batchID)
	}
	if evt.ToState != commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
		t.Errorf("audit event ToState = %q, want RECOVERED", evt.ToState)
	}
	if evt.RecoveredDeltaPaise != 10000 {
		t.Errorf("audit event RecoveredDeltaPaise = %d, want 10000", evt.RecoveredDeltaPaise)
	}
}

// Phase 2 Unit G: the EV snapshot record_state carries from Scoring time
// must reach the Executor on the ExecuteRequest unchanged, since Execute has
// no other way to learn what the Decision Engine decided. Proven by seeding
// a known snapshot directly (standing in for a real scheduleNew/
// recordRescore write) and asserting the fake Executor received exactly
// those numbers, not zero and not recomputed.
func TestSchedulerForwardsEVSnapshotToExecute(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Now().Add(-time.Minute))
	seedEVSnapshot(ctx, t, pool, recordID, 12345.5, 0.6789)

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS, CostPaise: 20}}
	sched := NewScheduler(pool, &fakeClassifier{}, executor, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	req := executor.LastRequest()
	if req == nil {
		t.Fatal("Execute was never called")
	}
	if req.GetRecordId() != recordID {
		// claimDue is unscoped (see the comment above), so a concurrently
		// running test could have this fake's last call belong to a
		// different record; that would make the assertions below meaningless
		// rather than wrong, so fail clearly instead of silently passing.
		t.Fatalf("last Execute call was for record %s, want %s (claimDue picked up an unrelated due record first)", req.GetRecordId(), recordID)
	}
	if req.GetEvScoreAtDecision() != 12345.5 {
		t.Errorf("ExecuteRequest.EvScoreAtDecision = %v, want 12345.5", req.GetEvScoreAtDecision())
	}
	if req.GetPRecoveryAtDecision() != 0.6789 {
		t.Errorf("ExecuteRequest.PRecoveryAtDecision = %v, want 0.6789", req.GetPRecoveryAtDecision())
	}
}

func TestSchedulerIgnoresNotYetDueRecords(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Now().Add(time.Hour))

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS}}
	sched := NewScheduler(pool, &fakeClassifier{}, executor, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

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
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Now().Add(-time.Minute))

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_PENDING}}
	sched := NewScheduler(pool, &fakeClassifier{}, executor, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

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

// Phase 5 Unit E: a nudge-type due action must have its message text
// composed via the Classifier before Execute is called, since Execute has
// no way to write the wording itself (docs/ARCHITECTURE.md section 5b).
func TestSchedulerComposesNudgeMessageBeforeExecutingANudgeAction(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT, time.Now().Add(-time.Minute))

	classifier := &fakeClassifier{nudgeResp: &classifierv1.ComposeNudgeResponse{
		Message: "Aapka order abhi complete nahi hua, wapas aaiye!",
		Source:  commonv1.Source_SOURCE_LLM,
	}}
	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_PENDING}}
	sched := NewScheduler(pool, classifier, executor, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if classifier.nudgeCallCount() != 1 {
		t.Fatalf("ComposeNudge calls = %d, want 1", classifier.nudgeCallCount())
	}
	nudgeReq := classifier.lastNudgeRequest()
	if nudgeReq.GetBucket() != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT {
		t.Errorf("ComposeNudgeRequest.Bucket = %v, want ABANDONMENT", nudgeReq.GetBucket())
	}
	if nudgeReq.GetActionType() != commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER {
		t.Errorf("ComposeNudgeRequest.ActionType = %v, want NUDGE_REMINDER", nudgeReq.GetActionType())
	}

	execReq := executor.LastRequest()
	if execReq == nil {
		t.Fatal("Execute was never called")
	}
	if execReq.GetMessage() != "Aapka order abhi complete nahi hua, wapas aaiye!" {
		t.Errorf("ExecuteRequest.Message = %q, want the composed nudge text", execReq.GetMessage())
	}
	if execReq.GetMessageSource() != commonv1.Source_SOURCE_LLM {
		t.Errorf("ExecuteRequest.MessageSource = %v, want SOURCE_LLM, the source ComposeNudge answered with", execReq.GetMessageSource())
	}
}

// A retry never needs composed wording: only a nudge sends anything a
// customer reads.
func TestSchedulerDoesNotComposeNudgeForARetryAction(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Now().Add(-time.Minute))

	classifier := &fakeClassifier{}
	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS}}
	sched := NewScheduler(pool, classifier, executor, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if classifier.nudgeCallCount() != 0 {
		t.Errorf("ComposeNudge calls = %d, want 0 for a retry action", classifier.nudgeCallCount())
	}
	if req := executor.LastRequest(); req != nil && req.GetMessage() != "" {
		t.Errorf("ExecuteRequest.Message = %q, want empty for a retry action", req.GetMessage())
	}
}

// A failed attempt with budget remaining is re-scored, not escalated
// (docs/ARCHITECTURE.md section 7, docs/PHASE2_IMPLEMENTATION.md Unit E).
// The record is fresh (no prior attempts), so the first failed retry still
// has a positive-EV retry available at attempt 2, and must land back in
// RetryScheduled through Scoring rather than Escalated.
func TestSchedulerReschedulesAfterFailedAttemptRatherThanEscalating(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Now().Add(-time.Minute))

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_FAILURE, CostPaise: 25}}
	sched := NewScheduler(pool, &fakeClassifier{}, executor, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	var state string
	var attemptCount int
	var evScore, pRecovery *float64
	if err := pool.QueryRow(ctx, `SELECT current_state, attempt_count, ev_score_at_decision, p_recovery_at_decision FROM record_state WHERE record_id=$1`, recordID).
		Scan(&state, &attemptCount, &evScore, &pRecovery); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String() {
		t.Errorf("current_state = %q, want RETRY_SCHEDULED: a failed attempt with budget remaining is re-priced, not escalated", state)
	}
	if attemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1 (the failed attempt still counts)", attemptCount)
	}
	// recordRescore must persist the re-priced score (attempt 2's, not
	// attempt 1's stale value, and not left NULL from before this record was
	// ever scored): this record started with no snapshot at all
	// (seedScheduled sets neither column), so a real value here proves
	// scoreAndRoute's winning score actually reached the database.
	if evScore == nil {
		t.Error("ev_score_at_decision is NULL after re-scoring, want the re-priced EV")
	} else if *evScore <= 0 {
		t.Errorf("ev_score_at_decision = %v, want > 0 (only a positive-EV action is ever re-scheduled)", *evScore)
	}
	if pRecovery == nil {
		t.Error("p_recovery_at_decision is NULL after re-scoring, want the re-priced P(recovery)")
	}

	// One claim transition (Retrying) plus two rescore transitions
	// (Retrying -> Scoring, Scoring -> RetryScheduled): the trail replays
	// the re-entry edge from docs/ARCHITECTURE.md section 7, not a summary.
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
		{commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String(), commonv1.RecordState_RECORD_STATE_RETRYING.String()},
		{commonv1.RecordState_RECORD_STATE_RETRYING.String(), commonv1.RecordState_RECORD_STATE_SCORING.String()},
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

	// Phase 5 Unit F: handleFailedAttempt's own transaction (the re-entry
	// to Scoring) is a separate audit.events notification from the claim
	// transition above, spanning Retrying -> RetryScheduled (the
	// transaction's own start and end, not every internal microstep).
	evt := waitForAuditEvent(t, auditTopic, recordID, 5*time.Second)
	if evt.FromState != commonv1.RecordState_RECORD_STATE_RETRYING.String() {
		t.Errorf("audit event FromState = %q, want RETRYING", evt.FromState)
	}
	if evt.ToState != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String() {
		t.Errorf("audit event ToState = %q, want RETRY_SCHEDULED", evt.ToState)
	}
	if evt.RecoveredDeltaPaise != 0 {
		t.Errorf("audit event RecoveredDeltaPaise = %d, want 0 (a re-scheduled retry did not recover anything)", evt.RecoveredDeltaPaise)
	}
}

// The compliance stop, and the termination proof for it: a record whose
// retry budget AND contact cap are both exhausted must escalate because the
// guardrails refuse every action, not because of a fixed attempt-count
// check. Driven through the real loop (repeated ticks against a real due
// record) until it reaches a terminal state, with a hard iteration bound so
// a regression that made the loop spin forever fails this test instead of
// hanging it.
func TestSchedulerRetryLoopTerminatesViaGuardrailCap(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	// A bigger amount than seedRecord's default, so a retry's expected
	// value stays positive through every modelled attempt (1 to 3) and the
	// guardrail cap is what actually stops it, not the amount being too
	// small to be worth a third try.
	if _, err := pool.Exec(ctx, `UPDATE record SET amount_paise=500000 WHERE id=$1`, recordID); err != nil {
		t.Fatalf("seed amount_paise: %v", err)
	}

	// Contacts already at the cap, so once the retry budget is spent too,
	// nothing is left for the scorer to pick from. attempt_number is a
	// record-global sequence (UNIQUE (record_id, attempt_number)), so these
	// two nudges occupy slots 1 and 2, and record_state.attempt_count is
	// set to match: it is that same global count, not a per-action one.
	now := time.Now()
	seedAttempt(ctx, t, pool, recordID, 1, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, now.Add(-2*time.Hour))
	seedAttempt(ctx, t, pool, recordID, 2, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, now.Add(-time.Hour))

	fakeClock := clock.NewFake(now)
	const retryDelay = time.Minute
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, fakeClock.Now().Add(-time.Minute))
	if _, err := pool.Exec(ctx, `UPDATE record_state SET attempt_count=2 WHERE record_id=$1`, recordID); err != nil {
		t.Fatalf("seed attempt_count: %v", err)
	}

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_FAILURE, CostPaise: 25}}
	cfg := SchedulerConfig{CallTimeout: 2 * time.Second, PollInterval: time.Second, DLQTopic: dlqTopic, AuditEventsTopic: auditTopic, RetryDelay: retryDelay, NudgeDelay: retryDelay, TimeScale: 1, Guardrails: testGuardrails}
	sched := NewScheduler(pool, &fakeClassifier{}, executor, dlqProducer, fakeClock, testEconomics(t), cfg)

	const maxIterations = 10
	var state string
	for i := 0; i < maxIterations; i++ {
		// Simulate the Executor's insert-before-execute for the attempt
		// this tick is about to run (docs/ARCHITECTURE.md section 11): the
		// guardrails read from INTERVENTION_ATTEMPT, which only the real
		// Executor writes, so the fake stands in for that write here.
		var attemptCount int
		if err := pool.QueryRow(ctx, `SELECT attempt_count FROM record_state WHERE record_id=$1`, recordID).Scan(&attemptCount); err != nil {
			t.Fatalf("query attempt_count: %v", err)
		}
		seedAttempt(ctx, t, pool, recordID, attemptCount+1, commonv1.ActionType_ACTION_TYPE_RETRY, fakeClock.Now())

		if err := sched.tick(ctx); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}

		if err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id=$1`, recordID).Scan(&state); err != nil {
			t.Fatalf("query record_state: %v", err)
		}
		if state == commonv1.RecordState_RECORD_STATE_ESCALATED.String() ||
			state == commonv1.RecordState_RECORD_STATE_RECOVERED.String() ||
			state == commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC.String() {
			break
		}
		fakeClock.Advance(retryDelay + time.Second)
	}

	if state != commonv1.RecordState_RECORD_STATE_ESCALATED.String() {
		t.Fatalf("after %d iterations state = %q, want ESCALATED (the loop must terminate once the guardrails refuse everything)", maxIterations, state)
	}

	// attempt_count is the record-global sequence: 2 pre-seeded nudges plus
	// exactly MaxRetries retries, not one more, since the guardrail must
	// refuse the (MaxRetries+1)th before it is ever attempted.
	var attemptCount int
	if err := pool.QueryRow(ctx, `SELECT attempt_count FROM record_state WHERE record_id=$1`, recordID).Scan(&attemptCount); err != nil {
		t.Fatalf("query attempt_count: %v", err)
	}
	wantAttemptCount := 2 + testGuardrails.MaxRetries
	if attemptCount != wantAttemptCount {
		t.Errorf("attempt_count = %d, want %d (2 pre-seeded nudges + MaxRetries=%d retries): the record must not run one attempt over its budget", attemptCount, wantAttemptCount, testGuardrails.MaxRetries)
	}

	var retries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM intervention_attempt WHERE record_id=$1 AND action_type=$2`, recordID, commonv1.ActionType_ACTION_TYPE_RETRY.String()).Scan(&retries); err != nil {
		t.Fatalf("query retries: %v", err)
	}
	if retries != testGuardrails.MaxRetries {
		t.Errorf("retries executed = %d, want exactly MaxRetries=%d", retries, testGuardrails.MaxRetries)
	}
}

// The economics stop, and its own termination proof: a retry budget that is
// nowhere near exhausted (MaxRetries=10) does not save a record whose
// priors have decayed past the deepest modelled attempt. The record closes
// as ClosedUneconomic, driven by the same repeated-tick loop and the same
// hard iteration bound as the guardrail-cap test above.
func TestSchedulerRetryLoopTerminatesViaEconomicsWhenPriorsRunOut(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	// Big enough that a retry's expected value is positive right up until
	// the priors themselves run out at attempt 4, isolating the economics
	// stop from a merely-too-small amount.
	if _, err := pool.Exec(ctx, `UPDATE record SET amount_paise=500000 WHERE id=$1`, recordID); err != nil {
		t.Fatalf("seed amount_paise: %v", err)
	}

	now := time.Now()
	// Contacts already at the (small) cap, so a nudge cannot rescue a
	// record whose retry priors have run dry: this isolates the economics
	// stop from the guardrail-cap stop tested above. attempt_number 1 is
	// this nudge's slot in the record-global sequence, so attempt_count
	// starts at 1 to match.
	seedAttempt(ctx, t, pool, recordID, 1, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, now.Add(-time.Hour))

	fakeClock := clock.NewFake(now)
	const retryDelay = time.Minute
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, fakeClock.Now().Add(-time.Minute))
	if _, err := pool.Exec(ctx, `UPDATE record_state SET attempt_count=1 WHERE record_id=$1`, recordID); err != nil {
		t.Fatalf("seed attempt_count: %v", err)
	}

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_FAILURE, CostPaise: 25}}
	guardrails := GuardrailConfig{MaxRetries: 10, MaxContacts: 1, ContactCooldown: 24 * time.Hour, RecoveryWindow: 7 * 24 * time.Hour}
	cfg := SchedulerConfig{CallTimeout: 2 * time.Second, PollInterval: time.Second, DLQTopic: dlqTopic, AuditEventsTopic: auditTopic, RetryDelay: retryDelay, NudgeDelay: retryDelay, TimeScale: 1, Guardrails: guardrails}
	sched := NewScheduler(pool, &fakeClassifier{}, executor, dlqProducer, fakeClock, testEconomics(t), cfg)

	const maxIterations = 10
	var state string
	for i := 0; i < maxIterations; i++ {
		var attemptCount int
		if err := pool.QueryRow(ctx, `SELECT attempt_count FROM record_state WHERE record_id=$1`, recordID).Scan(&attemptCount); err != nil {
			t.Fatalf("query attempt_count: %v", err)
		}
		seedAttempt(ctx, t, pool, recordID, attemptCount+1, commonv1.ActionType_ACTION_TYPE_RETRY, fakeClock.Now())

		if err := sched.tick(ctx); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}

		if err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id=$1`, recordID).Scan(&state); err != nil {
			t.Fatalf("query record_state: %v", err)
		}
		if state == commonv1.RecordState_RECORD_STATE_ESCALATED.String() ||
			state == commonv1.RecordState_RECORD_STATE_RECOVERED.String() ||
			state == commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC.String() {
			break
		}
		fakeClock.Advance(retryDelay + time.Second)
	}

	if state != commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC.String() {
		t.Fatalf("after %d iterations state = %q, want CLOSED_UNECONOMIC: a retry budget of 10 was never reached, only the modelled priors were", maxIterations, state)
	}

	// attempt_count is the record-global sequence: 1 pre-seeded nudge plus
	// exactly 3 executed retries. The scorer refuses a 4th retry before it
	// is ever attempted, since attemptNumberFor(RETRY) would be 4 and
	// TRANSIENT_BANK's prior only goes to attempt 3.
	var attemptCount int
	if err := pool.QueryRow(ctx, `SELECT attempt_count FROM record_state WHERE record_id=$1`, recordID).Scan(&attemptCount); err != nil {
		t.Fatalf("query attempt_count: %v", err)
	}
	if attemptCount != 4 {
		t.Errorf("attempt_count = %d, want 4 (1 pre-seeded nudge + 3 executed retries)", attemptCount)
	}

	var retries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM intervention_attempt WHERE record_id=$1 AND action_type=$2`, recordID, commonv1.ActionType_ACTION_TYPE_RETRY.String()).Scan(&retries); err != nil {
		t.Fatalf("query retries: %v", err)
	}
	if retries != 3 {
		t.Errorf("retries executed = %d, want 3: TRANSIENT_BANK's retry prior only goes to attempt 3, attempt 4 must be the one that scores zero and closes the record instead of running", retries)
	}
}

func TestSchedulerDeadLettersAfterExecuteRetriesExhausted(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Now().Add(-time.Minute))

	executor := &fakeExecutor{err: errors.New("executor unavailable")}
	sched := NewScheduler(pool, &fakeClassifier{}, executor, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

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
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, time.Now().Add(-time.Minute))

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS}}
	sched := NewScheduler(pool, &fakeClassifier{}, executor, dlqProducer, clock.New(), testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

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

func TestSchedulerFiresOnceWhenFakeClockPassesDueAt(t *testing.T) {
	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)

	start := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(start)
	dueAt := fakeClock.Now().Add(time.Minute)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, dueAt)

	executor := &fakeExecutor{resp: &executorv1.ExecuteResponse{Outcome: commonv1.Outcome_OUTCOME_SUCCESS}}
	sched := NewScheduler(pool, &fakeClassifier{}, executor, dlqProducer, fakeClock, testEconomics(t), schedulerTestConfig(dlqTopic, auditTopic))

	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick before due_at: %v", err)
	}

	var state string
	var storedDueAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT current_state, due_at FROM record_state WHERE record_id=$1`, recordID).Scan(&state, &storedDueAt); err != nil {
		t.Fatalf("query record_state before due_at: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String() {
		t.Fatalf("current_state before due_at = %q, want RETRY_SCHEDULED", state)
	}
	if storedDueAt == nil || !storedDueAt.Equal(dueAt) {
		t.Fatalf("due_at before due_at = %v, want unchanged %v", storedDueAt, dueAt)
	}

	fakeClock.Advance(time.Minute + time.Nanosecond)
	if err := sched.tick(ctx); err != nil {
		t.Fatalf("tick after due_at: %v", err)
	}
	if err := sched.tick(ctx); err != nil {
		t.Fatalf("second tick after due_at: %v", err)
	}

	var attemptCount, auditEntries int
	if err := pool.QueryRow(ctx, `SELECT current_state, attempt_count FROM record_state WHERE record_id=$1`, recordID).Scan(&state, &attemptCount); err != nil {
		t.Fatalf("query record_state after due_at: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_RECOVERED.String() {
		t.Errorf("current_state after due_at = %q, want RECOVERED", state)
	}
	if attemptCount != 1 {
		t.Errorf("attempt_count after two due ticks = %d, want 1", attemptCount)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_entry WHERE record_id=$1`, recordID).Scan(&auditEntries); err != nil {
		t.Fatalf("count audit entries: %v", err)
	}
	if auditEntries != 2 {
		t.Errorf("audit_entry rows = %d, want 2 (one claim + one outcome)", auditEntries)
	}
}

// TestSchedulerConcurrentSchedulersClaimExactlyOnce exercises the row lock in
// claimDue directly, not through the full Scheduler.tick -> process ->
// Execute path: many concurrent callers race claimDue for the same due
// record, each scoped to limit=1 so no racer can grab more than the one row
// this test cares about. claimDue's query is a system-wide poll by design
// (see its own doc comment); going through tick()'s claimBatchSize=20 per
// racer here risked grabbing OTHER tests' due records under a full ./...
// run, where every package's Postgres-backed tests share one database --
// this happened during review (services/decision-engine and test/e2e
// interfering with each other under `go test ./...`). Testing claimDue in
// isolation also sidesteps process()/executeWithRetry entirely, so a
// genuine double-claim fails with a clear assertion instead of the two
// claimants racing a real Executor call.
func TestSchedulerConcurrentSchedulersClaimExactlyOnce(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	_, recordID := seedRecord(ctx, t, pool)

	start := time.Date(2026, time.August, 26, 12, 0, 0, 0, time.UTC)
	fakeClock := clock.NewFake(start)
	seedScheduled(ctx, t, pool, recordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, commonv1.ActionType_ACTION_TYPE_RETRY, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, fakeClock.Now().Add(-time.Minute))

	s := newStore(pool)

	// 25 concurrent claimers, not 2: with only two racers, true overlap at
	// the database level was rare enough in practice that this test passed
	// even with the row lock removed entirely (verified during review). More
	// contenders racing the same connection pool makes genuine overlap far
	// more likely per run; the GOMAXPROCS=2 loop in the verify step is what
	// makes it reliable across runs.
	const numClaimers = 25
	startClaims := make(chan struct{})
	errs := make(chan error, numClaimers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	claimedMine := 0
	for i := 0; i < numClaimers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startClaims
			claimed, err := s.claimDue(ctx, fakeClock.Now(), 1)
			if err != nil {
				errs <- err
				return
			}
			for _, c := range claimed {
				if c.RecordID == recordID {
					mu.Lock()
					claimedMine++
					mu.Unlock()
				}
			}
		}()
	}
	close(startClaims)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("claimDue: %v", err)
	}

	if claimedMine != 1 {
		t.Errorf("record %s was claimed %d times across %d concurrent claimDue calls, want exactly 1", recordID, claimedMine, numClaimers)
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT current_state FROM record_state WHERE record_id=$1`, recordID).Scan(&state); err != nil {
		t.Fatalf("query record_state: %v", err)
	}
	if state != commonv1.RecordState_RECORD_STATE_RETRYING.String() {
		t.Errorf("current_state = %q, want RETRYING (claimed exactly once)", state)
	}
}
