package ports

import (
	"context"
	"errors"
	"testing"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
)

type fakeRecovery struct {
	calls int
	out   RecoveryAction
	err   error
}

func (f *fakeRecovery) SimulateOutcome(ctx context.Context, recordID string, action commonv1.ActionType, attemptNumber int32) (RecoveryAction, error) {
	f.calls++
	return f.out, f.err
}

type fakeNotification struct {
	calls    int
	channel  notifierv1.Channel
	message  string
	out      Notification
	err      error
	gotCalls []string
}

func (f *fakeNotification) SimulateSend(ctx context.Context, recordID string, channel notifierv1.Channel, message string) (Notification, error) {
	f.calls++
	f.channel = channel
	f.message = message
	f.gotCalls = append(f.gotCalls, recordID)
	return f.out, f.err
}

func newTestRouter(t *testing.T, rec RecoveryActionPort, note NotificationPort) *Router {
	t.Helper()
	return NewRouter(rec, note)
}

// Every ActionType must have a decided destination. Table-driven over the
// whole enum so adding an action to common.proto without deciding what
// executes it fails here rather than falling silently into a default.
func TestExecuteRoutesEveryActionType(t *testing.T) {
	tests := []struct {
		action       commonv1.ActionType
		wantRecovery int
		wantNotify   int
	}{
		{commonv1.ActionType_ACTION_TYPE_RETRY, 1, 0},
		// A nudge calls both ports now: NotificationPort to actually send it,
		// then RecoveryActionPort to ask whether/when the customer reacts
		// (docs/ARCHITECTURE.md section 6).
		{commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, 1, 1},
		{commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, 1, 1},
		{commonv1.ActionType_ACTION_TYPE_ESCALATE, 0, 0},
		{commonv1.ActionType_ACTION_TYPE_NONE, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.action.String(), func(t *testing.T) {
			rec := &fakeRecovery{out: RecoveryAction{Outcome: commonv1.Outcome_OUTCOME_SUCCESS, Immediate: true}}
			note := &fakeNotification{out: Notification{Sent: true}}
			r := newTestRouter(t, rec, note)

			if _, err := r.Execute(context.Background(), "rec-1", tc.action, 1, "hi"); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if rec.calls != tc.wantRecovery {
				t.Errorf("recovery port called %d times, want %d", rec.calls, tc.wantRecovery)
			}
			if note.calls != tc.wantNotify {
				t.Errorf("notification port called %d times, want %d", note.calls, tc.wantNotify)
			}
		})
	}
}

func TestExecuteRejectsUnspecifiedAction(t *testing.T) {
	r := newTestRouter(t, &fakeRecovery{}, &fakeNotification{})
	if _, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_UNSPECIFIED, 1, ""); err == nil {
		t.Fatal("Execute with an unspecified action returned no error")
	}
}

// ESCALATE must not report success. Pretending the Executor handed a record
// to a human would put a false success in the audit trail.
func TestExecuteEscalateReportsFailureWithoutCallingAPort(t *testing.T) {
	rec := &fakeRecovery{}
	note := &fakeNotification{}
	r := newTestRouter(t, rec, note)

	got, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_ESCALATE, 1, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Outcome != commonv1.Outcome_OUTCOME_FAILURE {
		t.Errorf("Outcome = %v, want FAILURE", got.Outcome)
	}
	if got.FailureCode == "" {
		t.Error("FailureCode is empty; the next classification has nothing to read")
	}
	if got.CostPaise != 0 {
		t.Errorf("CostPaise = %d, want 0: nothing was actually sent or attempted", got.CostPaise)
	}
	if rec.calls != 0 || note.calls != 0 {
		t.Errorf("a port was called for ESCALATE: recovery=%d notification=%d", rec.calls, note.calls)
	}
}

func TestExecuteNoneSucceedsAtZeroCost(t *testing.T) {
	r := newTestRouter(t, &fakeRecovery{}, &fakeNotification{})

	got, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_NONE, 1, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Outcome != commonv1.Outcome_OUTCOME_SUCCESS {
		t.Errorf("Outcome = %v, want SUCCESS: doing nothing deliberately is not a failure", got.Outcome)
	}
	if got.CostPaise != 0 {
		t.Errorf("CostPaise = %d, want 0", got.CostPaise)
	}
}

// A nudge is sent synchronously but resolves later, so it must come back
// PENDING with a resolves_at, never SUCCESS. resolves_at comes from the
// recovery port's own answer now, not a router-level constant: only
// something holding a recoverability model (demo/world-simulator in the
// demo) can say when a specific customer is expected to react
// (docs/ARCHITECTURE.md section 6).
func TestExecuteNudgeIsPendingWithResolvesAtFromTheRecoveryPort(t *testing.T) {
	resolves := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	rec := &fakeRecovery{out: RecoveryAction{Outcome: commonv1.Outcome_OUTCOME_SUCCESS, Immediate: false, ResolvesAt: resolves}}
	note := &fakeNotification{out: Notification{Sent: true, CostPaise: smsCostPaise}}
	r := newTestRouter(t, rec, note)

	got, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, 1, "pay up")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Outcome != commonv1.Outcome_OUTCOME_PENDING {
		t.Errorf("Outcome = %v, want PENDING: the customer has not answered yet", got.Outcome)
	}
	if !got.ResolvesAt.Equal(resolves) {
		t.Errorf("ResolvesAt = %v, want the recovery port's own %v", got.ResolvesAt, resolves)
	}
	if got.CostPaise != smsCostPaise {
		t.Errorf("CostPaise = %d, want the notification's cost %d, not anything from the recovery port", got.CostPaise, smsCostPaise)
	}
	if rec.calls != 1 {
		t.Errorf("recovery port called %d times, want 1", rec.calls)
	}
}

// A nudge's recoverability profile can resolve immediately (a zero-delay
// GROUND_TRUTH profile, docs/PHASE5_IMPLEMENTATION.md Unit C), and that
// real answer must pass through rather than being forced into PENDING.
func TestExecuteNudgeWithImmediateRecoveryPortAnswerPassesItThrough(t *testing.T) {
	rec := &fakeRecovery{out: RecoveryAction{Outcome: commonv1.Outcome_OUTCOME_FAILURE, Immediate: true, FailureCode: "CARD_EXPIRED"}}
	note := &fakeNotification{out: Notification{Sent: true, CostPaise: smsCostPaise}}
	r := newTestRouter(t, rec, note)

	got, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, 1, "update your card")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Outcome != commonv1.Outcome_OUTCOME_FAILURE {
		t.Errorf("Outcome = %v, want FAILURE, passed through rather than forced to PENDING", got.Outcome)
	}
	if got.FailureCode != "CARD_EXPIRED" {
		t.Errorf("FailureCode = %q, want the recovery port's own CARD_EXPIRED", got.FailureCode)
	}
	if !got.ResolvesAt.IsZero() {
		t.Errorf("ResolvesAt = %v, want zero: an immediate answer never waits", got.ResolvesAt)
	}
}

// Nothing was delivered, so there is no customer reaction to ask about:
// the recovery port must not be called at all.
func TestExecuteUndeliveredNudgeNeverCallsTheRecoveryPort(t *testing.T) {
	rec := &fakeRecovery{}
	note := &fakeNotification{out: Notification{Sent: false}}
	r := newTestRouter(t, rec, note)

	if _, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, 1, "msg"); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if rec.calls != 0 {
		t.Errorf("recovery port called %d times, want 0: nothing was delivered, there is no reaction to wait for", rec.calls)
	}
}

// A recovery port failure after a successful send is an infrastructure
// failure like any other port error, not folded into the send result.
func TestExecuteNudgeSurfacesRecoveryPortErrorAfterASuccessfulSend(t *testing.T) {
	rec := &fakeRecovery{err: errors.New("world simulator unreachable")}
	note := &fakeNotification{out: Notification{Sent: true}}
	r := newTestRouter(t, rec, note)

	if _, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, 1, "msg"); err == nil {
		t.Fatal("a recovery port error after a successful send did not surface")
	}
}

func TestExecuteNudgeChannelAndCostDifferByNudgeType(t *testing.T) {
	tests := []struct {
		action      commonv1.ActionType
		wantChannel notifierv1.Channel
		wantCost    int64
	}{
		{commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, notifierv1.Channel_CHANNEL_SMS, smsCostPaise},
		{commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, notifierv1.Channel_CHANNEL_WHATSAPP, whatsappCostPaise},
	}
	for _, tc := range tests {
		t.Run(tc.action.String(), func(t *testing.T) {
			note := &fakeNotification{}
			// Route through the real stub so the channel-to-cost pairing is
			// exercised end to end rather than asserted twice.
			r := newTestRouter(t, &fakeRecovery{}, StubNotification{})
			got, err := r.Execute(context.Background(), "rec-1", tc.action, 1, "msg")
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got.CostPaise != tc.wantCost {
				t.Errorf("CostPaise = %d, want %d", got.CostPaise, tc.wantCost)
			}

			// And separately that the channel handed to the port is right.
			r2 := newTestRouter(t, &fakeRecovery{}, note)
			note.out = Notification{Sent: true}
			if _, err := r2.Execute(context.Background(), "rec-1", tc.action, 1, "msg"); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if note.channel != tc.wantChannel {
				t.Errorf("channel = %v, want %v", note.channel, tc.wantChannel)
			}
		})
	}
}

// The Decision Engine does not compose nudge text until Phase 5, so every
// nudge arrives with an empty message today. That must not error.
func TestExecuteNudgeWithEmptyMessageStillSends(t *testing.T) {
	note := &fakeNotification{out: Notification{Sent: true, CostPaise: smsCostPaise}}
	r := newTestRouter(t, &fakeRecovery{}, note)

	got, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, 1, "")
	if err != nil {
		t.Fatalf("Execute with an empty message: %v", err)
	}
	if got.Outcome != commonv1.Outcome_OUTCOME_PENDING {
		t.Errorf("Outcome = %v, want PENDING", got.Outcome)
	}
	if note.calls != 1 {
		t.Errorf("notification port called %d times, want 1", note.calls)
	}
}

func TestExecuteUndeliveredNudgeFailsRatherThanWaiting(t *testing.T) {
	note := &fakeNotification{out: Notification{Sent: false, CostPaise: 0}}
	r := newTestRouter(t, &fakeRecovery{}, note)

	got, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, 1, "msg")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Outcome != commonv1.Outcome_OUTCOME_FAILURE {
		t.Errorf("Outcome = %v, want FAILURE: nothing was delivered, so there is no reaction to wait for", got.Outcome)
	}
	if !got.ResolvesAt.IsZero() {
		t.Errorf("ResolvesAt = %v, want zero for a failed send", got.ResolvesAt)
	}
}

// A port that cannot be reached is an infrastructure failure and must surface
// as an error, which the Decision Engine retries. A declined action is not.
func TestExecuteSurfacesPortErrorsAsErrors(t *testing.T) {
	t.Run("recovery", func(t *testing.T) {
		r := newTestRouter(t, &fakeRecovery{err: errors.New("rail unreachable")}, &fakeNotification{})
		if _, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_RETRY, 1, ""); err == nil {
			t.Fatal("a recovery port error did not surface")
		}
	})
	t.Run("notification", func(t *testing.T) {
		r := newTestRouter(t, &fakeRecovery{}, &fakeNotification{err: errors.New("provider unreachable")})
		if _, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, 1, ""); err == nil {
			t.Fatal("a notification port error did not surface")
		}
	})
}

// A declined retry is a successful call reporting a business failure, NOT an
// error. Getting this backwards makes the Decision Engine dead-letter a
// perfectly healthy record (services/executor/SPEC.md section 5).
func TestExecuteDeclinedRetryIsNotAnError(t *testing.T) {
	rec := &fakeRecovery{out: RecoveryAction{
		Outcome:     commonv1.Outcome_OUTCOME_FAILURE,
		Immediate:   true,
		CostPaise:   retryCostPaise,
		FailureCode: "HARD_DECLINE",
	}}
	r := newTestRouter(t, rec, &fakeNotification{})

	got, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_RETRY, 1, "")
	if err != nil {
		t.Fatalf("a declined retry surfaced as an error: %v", err)
	}
	if got.Outcome != commonv1.Outcome_OUTCOME_FAILURE {
		t.Errorf("Outcome = %v, want FAILURE", got.Outcome)
	}
	if got.FailureCode != "HARD_DECLINE" {
		t.Errorf("FailureCode = %q, want the rail's code passed through", got.FailureCode)
	}
	if got.CostPaise != retryCostPaise {
		t.Errorf("CostPaise = %d, want %d: a failed attempt still costs money", got.CostPaise, retryCostPaise)
	}
}

func TestExecuteDeferredRetryBecomesPending(t *testing.T) {
	resolves := time.Date(2026, 8, 23, 15, 0, 0, 0, time.UTC)
	rec := &fakeRecovery{out: RecoveryAction{
		Outcome:    commonv1.Outcome_OUTCOME_SUCCESS,
		Immediate:  false,
		ResolvesAt: resolves,
	}}
	r := newTestRouter(t, rec, &fakeNotification{})

	got, err := r.Execute(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_RETRY, 1, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got.Outcome != commonv1.Outcome_OUTCOME_PENDING {
		t.Errorf("Outcome = %v, want PENDING when the port defers the answer", got.Outcome)
	}
	if !got.ResolvesAt.Equal(resolves) {
		t.Errorf("ResolvesAt = %v, want the port's own %v", got.ResolvesAt, resolves)
	}
}
