//go:build integration

package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	"github.com/thisizaro/Momotaro/services/executor/internal/ports"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func retryReq(recordID string, attempt int32) *executorv1.ExecuteRequest {
	return &executorv1.ExecuteRequest{
		RecordId:      recordID,
		ActionType:    commonv1.ActionType_ACTION_TYPE_RETRY,
		AttemptNumber: attempt,
		AmountPaise:   10000,
	}
}

func TestExecuteClaimsTheAttemptBeforeRunningTheAction(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	rec := succeedingRetry()
	s := newServer(t, pool, rec, &countingNotification{})

	resp, err := s.Execute(ctx, retryReq(recordID, 1))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp.Outcome != commonv1.Outcome_OUTCOME_SUCCESS {
		t.Errorf("Outcome = %v, want SUCCESS", resp.Outcome)
	}
	if resp.AlreadyExecuted {
		t.Error("AlreadyExecuted = true on the first call")
	}
	if resp.CostPaise != 200 {
		t.Errorf("CostPaise = %d, want 200", resp.CostPaise)
	}
	if got := rec.calls.Load(); got != 1 {
		t.Errorf("recovery port called %d times, want 1", got)
	}

	stored := loadAttempt(ctx, t, pool, recordID, 1)
	if stored.Count != 1 {
		t.Fatalf("intervention_attempt rows = %d, want 1", stored.Count)
	}
	if stored.Outcome != commonv1.Outcome_OUTCOME_SUCCESS.String() {
		t.Errorf("stored outcome = %q, want SUCCESS", stored.Outcome)
	}
	// The response is not evidence on its own; cost has to be on the row,
	// because that row is what "net recovered" is computed from.
	if stored.CostPaise != 200 {
		t.Errorf("stored cost_paise = %d, want 200", stored.CostPaise)
	}
}

// Phase 2 Unit G: the Decision Engine's economics decision snapshot arrives
// on the request and must be recorded as-is, never recomputed here (the
// Executor is not the service that scores, docs/PHASE2_IMPLEMENTATION.md
// Unit G's LLD is explicit about this split of responsibility).
func TestExecutePersistsTheEVSnapshotFromTheRequest(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	s := newServer(t, pool, succeedingRetry(), &countingNotification{})

	req := retryReq(recordID, 1)
	req.EvScoreAtDecision = 4567.25
	req.PRecoveryAtDecision = 0.42

	if _, err := s.Execute(ctx, req); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored := loadAttempt(ctx, t, pool, recordID, 1)
	if stored.EVScoreAtDecision == nil || *stored.EVScoreAtDecision != 4567.25 {
		t.Errorf("stored ev_score_at_decision = %v, want 4567.25", stored.EVScoreAtDecision)
	}
	if stored.PRecoveryAtDecision == nil || *stored.PRecoveryAtDecision != 0.42 {
		t.Errorf("stored p_recovery_at_decision = %v, want 0.42", stored.PRecoveryAtDecision)
	}
}

// The durable guarantee (docs/ARCHITECTURE.md section 11) and the money-safety
// requirement of docs/ENGINEERING.md section 11 item 8.
func TestExecuteIsIdempotentOnRedelivery(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	rec := succeedingRetry()
	s := newServer(t, pool, rec, &countingNotification{})
	req := retryReq(recordID, 1)

	first, err := s.Execute(ctx, req)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	second, err := s.Execute(ctx, req)
	if err != nil {
		t.Fatalf("second Execute (redelivery): %v", err)
	}

	if !second.AlreadyExecuted {
		t.Error("AlreadyExecuted = false on redelivery, want true")
	}
	if second.Outcome != first.Outcome {
		t.Errorf("redelivery Outcome = %v, want the original %v", second.Outcome, first.Outcome)
	}
	if second.CostPaise != first.CostPaise {
		t.Errorf("redelivery CostPaise = %d, want the original %d", second.CostPaise, first.CostPaise)
	}
	if got := rec.calls.Load(); got != 1 {
		t.Errorf("the action ran %d times across both calls, want exactly 1", got)
	}
	if stored := loadAttempt(ctx, t, pool, recordID, 1); stored.Count != 1 {
		t.Errorf("intervention_attempt rows = %d, want exactly 1 despite two calls", stored.Count)
	}
}

func TestExecuteIsIdempotentUnderConcurrentDuplicateDelivery(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	rec := succeedingRetry()
	s := newServer(t, pool, rec, &countingNotification{})
	req := retryReq(recordID, 1)

	const n = 8
	var wg sync.WaitGroup
	results := make([]*executorv1.ExecuteResponse, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.Execute(ctx, req)
		}(i)
	}
	wg.Wait()

	executed := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Execute[%d]: %v", i, err)
		}
		if !results[i].AlreadyExecuted {
			executed++
		}
	}
	if executed != 1 {
		t.Errorf("%d of %d concurrent calls performed the side effect, want exactly 1", executed, n)
	}
	if got := rec.calls.Load(); got != 1 {
		t.Errorf("the action ran %d times, want exactly 1", got)
	}
	if stored := loadAttempt(ctx, t, pool, recordID, 1); stored.Count != 1 {
		t.Errorf("intervention_attempt rows = %d, want 1 after %d concurrent calls", stored.Count, n)
	}
}

func TestExecuteTreatsDifferentAttemptNumbersIndependently(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	rec := succeedingRetry()
	s := newServer(t, pool, rec, &countingNotification{})

	for _, attempt := range []int32{1, 2} {
		resp, err := s.Execute(ctx, retryReq(recordID, attempt))
		if err != nil {
			t.Fatalf("Execute attempt %d: %v", attempt, err)
		}
		if resp.AlreadyExecuted {
			t.Errorf("attempt %d reported AlreadyExecuted, want a fresh execution", attempt)
		}
	}
	if got := rec.calls.Load(); got != 2 {
		t.Errorf("the action ran %d times, want 2 (one per attempt)", got)
	}
}

// This is the case the claim marker exists for. A nudge legitimately resolves
// to PENDING, so if PENDING were also the "still working" marker, a
// redelivered nudge would poll for an answer that is not coming until Phase 5,
// blow its deadline, and be dead-lettered despite having executed perfectly.
// See services/executor/SPEC.md section 4.3.
func TestExecuteRedeliveredPendingNudgeReplaysPromptly(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	note := &countingNotification{out: ports.Notification{Sent: true, CostPaise: 25}}
	// A real resolves_at, not the zero value: nudge() now sources it from
	// the recovery port's own answer (services/executor/internal/ports/
	// route.go), so a zero-value countingRecovery would give this test a
	// nil ResolvesAt, exactly what it exists to disprove.
	rec := &countingRecovery{out: ports.RecoveryAction{
		Outcome:    commonv1.Outcome_OUTCOME_SUCCESS,
		Immediate:  false,
		ResolvesAt: time.Now().Add(2 * time.Minute),
	}}
	s := newServer(t, pool, rec, note)
	req := &executorv1.ExecuteRequest{
		RecordId:      recordID,
		ActionType:    commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
		AttemptNumber: 1,
		Message:       "your autopay failed",
		AmountPaise:   10000,
	}

	first, err := s.Execute(ctx, req)
	if err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if first.Outcome != commonv1.Outcome_OUTCOME_PENDING {
		t.Fatalf("Outcome = %v, want PENDING: this test is meaningless otherwise", first.Outcome)
	}
	if first.ResolvesAt == nil {
		t.Error("ResolvesAt is nil on a PENDING outcome")
	}

	// Bounded tightly on purpose: asserting only the returned value would
	// pass even if the replay had spun for seconds first, which is exactly
	// the failure mode being guarded against.
	promptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	second, err := s.Execute(promptCtx, req)
	if err != nil {
		t.Fatalf("redelivered PENDING nudge did not replay promptly: %v", err)
	}
	if !second.AlreadyExecuted {
		t.Error("AlreadyExecuted = false on redelivery, want true")
	}
	if second.Outcome != commonv1.Outcome_OUTCOME_PENDING {
		t.Errorf("replayed Outcome = %v, want the original PENDING", second.Outcome)
	}
	if got := note.calls.Load(); got != 1 {
		t.Errorf("the nudge was sent %d times, want exactly 1", got)
	}
}

func TestExecuteNudgePersistsMessageAndChannelCost(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	// Real stub, so the channel-to-cost mapping is exercised rather than
	// restated: a method-update nudge goes out on the dearer channel.
	s := newServer(t, pool, &countingRecovery{}, ports.StubNotification{})
	resp, err := s.Execute(ctx, &executorv1.ExecuteRequest{
		RecordId:      recordID,
		ActionType:    commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
		AttemptNumber: 1,
		Message:       "update your card",
		AmountPaise:   10000,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	stored := loadAttempt(ctx, t, pool, recordID, 1)
	if stored.MessageText != "update your card" {
		t.Errorf("stored message_text = %q, want the message we sent", stored.MessageText)
	}
	if stored.CostPaise != resp.CostPaise {
		t.Errorf("stored cost_paise = %d but response said %d", stored.CostPaise, resp.CostPaise)
	}
	if stored.CostPaise <= 0 {
		t.Errorf("stored cost_paise = %d: a sent message is never free", stored.CostPaise)
	}
	if stored.Outcome != commonv1.Outcome_OUTCOME_PENDING.String() {
		t.Errorf("stored outcome = %q, want PENDING", stored.Outcome)
	}
}

// A declined action is a successful RPC reporting a business failure. Getting
// this backwards makes the Decision Engine's scheduler retry three times and
// then dead-letter a record that executed perfectly.
func TestExecuteDeclinedActionIsNotAnRPCError(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	rec := &countingRecovery{out: ports.RecoveryAction{
		Outcome:     commonv1.Outcome_OUTCOME_FAILURE,
		Immediate:   true,
		CostPaise:   200,
		FailureCode: "HARD_DECLINE",
	}}
	s := newServer(t, pool, rec, &countingNotification{})

	resp, err := s.Execute(ctx, retryReq(recordID, 1))
	if err != nil {
		t.Fatalf("a declined retry surfaced as an RPC error: %v", err)
	}
	if resp.Outcome != commonv1.Outcome_OUTCOME_FAILURE {
		t.Errorf("Outcome = %v, want FAILURE", resp.Outcome)
	}
	if resp.FailureCode != "HARD_DECLINE" {
		t.Errorf("FailureCode = %q, want the rail's code: it is the next classification's input", resp.FailureCode)
	}

	stored := loadAttempt(ctx, t, pool, recordID, 1)
	if stored.FailureCode != "HARD_DECLINE" {
		t.Errorf("stored failure_code = %q, want HARD_DECLINE", stored.FailureCode)
	}
	if stored.CostPaise != 200 {
		t.Errorf("stored cost_paise = %d, want 200: a failed attempt still costs money", stored.CostPaise)
	}
}

// The idempotency guard must be outcome-agnostic. Previously only the success
// path was proven, which left the two paths a real pipeline actually produces
// (a failure and a pending nudge) untested.
func TestExecuteIdempotencyHoldsForEveryOutcomeKind(t *testing.T) {
	tests := []struct {
		name string
		rec  ports.RecoveryAction
	}{
		{"failure", ports.RecoveryAction{Outcome: commonv1.Outcome_OUTCOME_FAILURE, Immediate: true, CostPaise: 200, FailureCode: "BANK_TIMEOUT"}},
		{"deferred/pending", ports.RecoveryAction{Outcome: commonv1.Outcome_OUTCOME_SUCCESS, Immediate: false, ResolvesAt: time.Now().Add(time.Hour)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPool(t)
			ctx := context.Background()
			recordID := seedRecord(ctx, t, pool)

			rec := &countingRecovery{out: tc.rec}
			s := newServer(t, pool, rec, &countingNotification{})
			req := retryReq(recordID, 1)

			first, err := s.Execute(ctx, req)
			if err != nil {
				t.Fatalf("first Execute: %v", err)
			}
			second, err := s.Execute(ctx, req)
			if err != nil {
				t.Fatalf("redelivery: %v", err)
			}
			if !second.AlreadyExecuted {
				t.Error("AlreadyExecuted = false on redelivery")
			}
			if second.Outcome != first.Outcome {
				t.Errorf("replayed Outcome = %v, want the original %v", second.Outcome, first.Outcome)
			}
			if got := rec.calls.Load(); got != 1 {
				t.Errorf("the action ran %d times, want exactly 1", got)
			}
		})
	}
}

// A port that cannot be reached is an infrastructure failure: it must surface
// as an error so the caller retries, and the claim must stay put so the retry
// cannot re-run an action that may already have reached the outside world.
func TestExecutePortFailureErrorsAndKeepsTheClaim(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	rec := &countingRecovery{err: errors.New("rail unreachable")}
	s := newServer(t, pool, rec, &countingNotification{})

	if _, err := s.Execute(ctx, retryReq(recordID, 1)); err == nil {
		t.Fatal("an unreachable port did not surface as an error")
	}

	stored := loadAttempt(ctx, t, pool, recordID, 1)
	if stored.Count != 1 {
		t.Errorf("intervention_attempt rows = %d, want 1: the claim must survive so a retry cannot re-run the action", stored.Count)
	}
	if stored.Outcome != commonv1.Outcome_OUTCOME_UNSPECIFIED.String() {
		t.Errorf("stored outcome = %q, want the unresolved claim marker", stored.Outcome)
	}
}

func TestExecuteUnknownRecordIsNotFound(t *testing.T) {
	pool := testPool(t)
	s := newServer(t, pool, succeedingRetry(), &countingNotification{})

	_, err := s.Execute(context.Background(), retryReq(uuid.NewString(), 1))
	if status.Code(err) != codes.NotFound {
		t.Fatalf("Execute for an unknown record: err = %v, want NotFound", err)
	}
}

func TestExecuteValidation(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	rec := succeedingRetry()
	note := &countingNotification{}
	s := newServer(t, pool, rec, note)

	tests := []struct {
		name string
		req  *executorv1.ExecuteRequest
	}{
		{"missing record_id", &executorv1.ExecuteRequest{ActionType: commonv1.ActionType_ACTION_TYPE_RETRY, AttemptNumber: 1}},
		{"zero attempt_number", retryReq(recordID, 0)},
		{"negative attempt_number", retryReq(recordID, -1)},
		{"unspecified action_type", &executorv1.ExecuteRequest{RecordId: recordID, AttemptNumber: 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Execute(ctx, tc.req); status.Code(err) != codes.InvalidArgument {
				t.Errorf("Execute(%s): err = %v, want InvalidArgument", tc.name, err)
			}
		})
	}
	if got := rec.calls.Load() + note.calls.Load(); got != 0 {
		t.Errorf("a port was called %d times on invalid input; validation must run before any side effect", got)
	}
}

// The Decision Engine composes no nudge text until Phase 5, so every nudge
// arrives with an empty message today. That must execute, not error.
func TestExecuteNudgeWithNoComposedMessageStillExecutes(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	note := &countingNotification{out: ports.Notification{Sent: true, CostPaise: 25}}
	s := newServer(t, pool, &countingRecovery{}, note)

	resp, err := s.Execute(ctx, &executorv1.ExecuteRequest{
		RecordId:      recordID,
		ActionType:    commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
		AttemptNumber: 1,
		AmountPaise:   10000,
	})
	if err != nil {
		t.Fatalf("nudge with no message: %v", err)
	}
	if resp.Outcome != commonv1.Outcome_OUTCOME_PENDING {
		t.Errorf("Outcome = %v, want PENDING", resp.Outcome)
	}
	if got := note.calls.Load(); got != 1 {
		t.Errorf("notification port called %d times, want 1", got)
	}
}

// A slot claimed but never resolved means the process holding it died between
// claiming and recording the answer. The await is bounded rather than waiting
// out the caller's whole deadline, and it reports the situation instead of
// guessing: re-running could double-charge, and inventing an outcome would put
// a fiction in the audit trail.
func TestExecuteAbandonedClaimIsReportedNotGuessed(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	recordID := seedRecord(ctx, t, pool)

	// A claim that no one will ever resolve, exactly as a crash mid-attempt
	// would leave it.
	if _, err := pool.Exec(ctx, `
		INSERT INTO intervention_attempt
			(id, record_id, attempt_number, action_type, outcome, cost_paise, failure_code)
		VALUES ($1, $2, 1, $3, $4, 0, '')`,
		uuid.NewString(), recordID,
		commonv1.ActionType_ACTION_TYPE_RETRY.String(),
		commonv1.Outcome_OUTCOME_UNSPECIFIED.String()); err != nil {
		t.Fatalf("seed abandoned claim: %v", err)
	}

	rec := succeedingRetry()
	s := newServer(t, pool, rec, &countingNotification{})

	// Generously longer than the await budget, so a failure here means the
	// budget was not enforced rather than that the machine was slow.
	boundedCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.Execute(boundedCtx, retryReq(recordID, 1))

	if status.Code(err) != codes.Aborted {
		t.Fatalf("Execute against an abandoned claim: err = %v, want Aborted", err)
	}
	if got := rec.calls.Load(); got != 0 {
		t.Errorf("the action ran %d times, want 0: re-running an abandoned claim risks a double charge", got)
	}
}
