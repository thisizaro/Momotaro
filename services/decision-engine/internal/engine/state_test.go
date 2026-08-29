package engine

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/economics"
)

// loadEconomicsModel loads the real checked-in cost model and priors, same as
// testhelpers_test.go's testEconomics, but without the integration build tag:
// economics.Load only reads local YAML files, no Postgres or Kafka needed, so
// scoreAndRoute's unit tests can run under `make test`.
func loadEconomicsModel(t *testing.T) *economics.Model {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	m, err := economics.Load(filepath.Join(root, "configs", "intervention_costs.yaml"), filepath.Join(root, "configs", "recovery_priors.yaml"))
	if err != nil {
		t.Fatalf("load economics config: %v", err)
	}
	return m
}

func TestDecideAfterClassify(t *testing.T) {
	tests := []struct {
		name              string
		action            commonv1.ActionType
		wantState         commonv1.RecordState
		wantPendingAction commonv1.ActionType
	}{
		{
			name:              "retry is scheduled",
			action:            commonv1.ActionType_ACTION_TYPE_RETRY,
			wantState:         commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		},
		{
			name:              "nudge method-update is scheduled",
			action:            commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
			wantState:         commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
		},
		{
			name:              "nudge reminder is scheduled",
			action:            commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
			wantState:         commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
		},
		{
			name:              "explicit escalate goes straight to escalated",
			action:            commonv1.ActionType_ACTION_TYPE_ESCALATE,
			wantState:         commonv1.RecordState_RECORD_STATE_ESCALATED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_UNSPECIFIED,
		},
		{
			name:              "none has no Phase 1 economics gate to land in, so it escalates",
			action:            commonv1.ActionType_ACTION_TYPE_NONE,
			wantState:         commonv1.RecordState_RECORD_STATE_ESCALATED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_UNSPECIFIED,
		},
		{
			name:              "unspecified action escalates rather than being silently dropped",
			action:            commonv1.ActionType_ACTION_TYPE_UNSPECIFIED,
			wantState:         commonv1.RecordState_RECORD_STATE_ESCALATED,
			wantPendingAction: commonv1.ActionType_ACTION_TYPE_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &classifierv1.ClassifyResponse{RecommendedAction: tt.action}
			state, pendingAction, reason := decideAfterClassify(resp)
			if state != tt.wantState {
				t.Errorf("state = %v, want %v", state, tt.wantState)
			}
			if pendingAction != tt.wantPendingAction {
				t.Errorf("pendingAction = %v, want %v", pendingAction, tt.wantPendingAction)
			}
			if reason == "" {
				t.Error("reason must not be empty, it lands in the audit trail verbatim")
			}
		})
	}
}

func TestDecideAfterExecute(t *testing.T) {
	tests := []struct {
		name      string
		pending   commonv1.ActionType
		outcome   commonv1.Outcome
		wantState commonv1.RecordState
	}{
		{
			name:      "success recovers regardless of action type",
			pending:   commonv1.ActionType_ACTION_TYPE_RETRY,
			outcome:   commonv1.Outcome_OUTCOME_SUCCESS,
			wantState: commonv1.RecordState_RECORD_STATE_RECOVERED,
		},
		{
			name:      "failed retry escalates, Phase 1 has no retry budget to fall back into",
			pending:   commonv1.ActionType_ACTION_TYPE_RETRY,
			outcome:   commonv1.Outcome_OUTCOME_FAILURE,
			wantState: commonv1.RecordState_RECORD_STATE_ESCALATED,
		},
		{
			name:      "failed nudge escalates",
			pending:   commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
			outcome:   commonv1.Outcome_OUTCOME_FAILURE,
			wantState: commonv1.RecordState_RECORD_STATE_ESCALATED,
		},
		{
			name:      "pending nudge parks in Nudged awaiting the delayed callback",
			pending:   commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
			outcome:   commonv1.Outcome_OUTCOME_PENDING,
			wantState: commonv1.RecordState_RECORD_STATE_NUDGED,
		},
		{
			name:      "pending retry is unexpected (retries are synchronous) and escalates",
			pending:   commonv1.ActionType_ACTION_TYPE_RETRY,
			outcome:   commonv1.Outcome_OUTCOME_PENDING,
			wantState: commonv1.RecordState_RECORD_STATE_ESCALATED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, reason := decideAfterExecute(tt.pending, tt.outcome)
			if state != tt.wantState {
				t.Errorf("state = %v, want %v", state, tt.wantState)
			}
			if reason == "" {
				t.Error("reason must not be empty, it lands in the audit trail verbatim")
			}
		})
	}
}

// scoreAndRoute is the economics gate itself, shared by the New path and the
// re-entry path after a failed attempt (docs/PHASE2_IMPLEMENTATION.md Unit
// E). These tests exercise it directly, against the real checked-in
// cost/prior config, with no Postgres involved: guardrails and economics are
// both pure once loaded.
func TestScoreAndRoutePicksTheHighestEVPermittedAction(t *testing.T) {
	model := loadEconomicsModel(t)
	const amountPaise = 500000 // 5000 rupees, big enough that RETRY clearly wins

	state, pendingAction, reason, score := scoreAndRoute(model, testGuardrails, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, freshHistory(), amountPaise, testNow)

	if state != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED {
		t.Errorf("state = %v, want RETRY_SCHEDULED", state)
	}
	if pendingAction != commonv1.ActionType_ACTION_TYPE_RETRY {
		t.Errorf("pendingAction = %v, want RETRY", pendingAction)
	}
	if score.EVPaise <= 0 {
		t.Errorf("EVPaise = %v, want strictly positive for the chosen action", score.EVPaise)
	}
	if reason == "" {
		t.Error("reason must not be empty, it lands in the audit trail verbatim")
	}
}

// The compliance stop: once the guardrails refuse every spending action
// (here, both the retry budget and the contact cap are spent), the record
// must escalate, and it must do so because applyGuardrails said so, not
// because of a hardcoded attempt-count check duplicated here.
func TestScoreAndRouteEscalatesWhenGuardrailsRefuseEveryAction(t *testing.T) {
	model := loadEconomicsModel(t)

	h := freshHistory()
	h.Retries = testGuardrails.MaxRetries
	h.Contacts = testGuardrails.MaxContacts

	state, pendingAction, reason, score := scoreAndRoute(model, testGuardrails, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, h, 500000, testNow)

	if state != commonv1.RecordState_RECORD_STATE_ESCALATED {
		t.Errorf("state = %v, want ESCALATED", state)
	}
	if pendingAction != commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		t.Errorf("pendingAction = %v, want UNSPECIFIED for an escalated record", pendingAction)
	}
	if score != (economics.Score{}) {
		t.Errorf("score = %+v, want the zero value: escalation is not a priced action", score)
	}
	if !strings.Contains(reason, "guardrails permit no action") {
		t.Errorf("reason = %q, want it to name the guardrail refusal rather than fall through the economics branch", reason)
	}
}

// The economics stop: the guardrails still permit a retry (the cap is far
// away), but the priors have decayed past the deepest modelled attempt
// (docs/ARCHITECTURE.md section 5a: an unmodelled attempt falls to
// beyondListedAttemptsBps, which the checked-in config sets to 0), so
// nothing has positive expected value and the record closes as
// ClosedUneconomic rather than cycling forever.
func TestScoreAndRouteClosesUneconomicWhenPriorsRunOutPastGuardrailReach(t *testing.T) {
	model := loadEconomicsModel(t)

	guardrails := GuardrailConfig{MaxRetries: 10, MaxContacts: 1, ContactCooldown: 24 * time.Hour, RecoveryWindow: 7 * 24 * time.Hour}
	h := freshHistory()
	h.Retries = 3  // three retries already spent: attempt 4 is unmodelled
	h.Contacts = 1 // contact cap already reached, so a nudge cannot rescue this

	state, pendingAction, reason, score := scoreAndRoute(model, guardrails, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK, h, 500000, testNow)

	if state != commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC {
		t.Errorf("state = %v, want CLOSED_UNECONOMIC: a retry budget of 10 has not been exhausted, only the modelled priors have", state)
	}
	if pendingAction != commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		t.Errorf("pendingAction = %v, want UNSPECIFIED", pendingAction)
	}
	if score != (economics.Score{}) {
		t.Errorf("score = %+v, want the zero value", score)
	}
	if reason != "no permitted action has positive expected value" {
		t.Errorf("reason = %q, want the economics-stop reason, not a fallback from some other branch", reason)
	}
}

func TestGuardrailRefusalReasonNamesTheBlockingRule(t *testing.T) {
	h := freshHistory()
	h.Retries = testGuardrails.MaxRetries
	h.Contacts = testGuardrails.MaxContacts
	v := applyGuardrails(h, testGuardrails, testNow)

	if got := guardrailRefusalReason(v); got == "" || got == "no reason recorded" {
		t.Errorf("guardrailRefusalReason = %q, want a specific rule named", got)
	}
}

func TestDueAtFor(t *testing.T) {
	now := testNow
	const nudgeDelay = time.Hour

	tests := []struct {
		name  string
		state commonv1.RecordState
		want  *time.Time
	}{
		{name: "nudge scheduled waits NudgeDelay", state: commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED, want: timePtr(now.Add(nudgeDelay))},
		{name: "retry scheduled is now handled by retryDueAt, dueAtFor returns nil", state: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, want: nil},
		{name: "escalated is terminal, no due_at", state: commonv1.RecordState_RECORD_STATE_ESCALATED, want: nil},
		{name: "closed uneconomic is terminal, no due_at", state: commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dueAtFor(tt.state, nudgeDelay, now, 1)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("dueAtFor(%v) = %v, want %v", tt.state, got, tt.want)
			}
			if got != nil && !got.Equal(*tt.want) {
				t.Errorf("dueAtFor(%v) = %v, want %v", tt.state, *got, *tt.want)
			}
		})
	}
}

// dueAtFor must actually apply the TRAI contact-hour rule to a nudge's
// due_at, not just compute the flat delay: a real end-to-end check that
// the two functions are actually wired together, not only each correct on
// its own.
func TestDueAtForDefersOutsideContactHours(t *testing.T) {
	// 20:45 UTC = 02:15 IST: now + nudgeDelay lands well outside the window.
	now := time.Date(2026, 8, 24, 20, 45, 0, 0, time.UTC)
	const nudgeDelay = time.Hour

	got := dueAtFor(commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED, nudgeDelay, now, 1)
	if got == nil {
		t.Fatal("dueAtFor returned nil for NUDGE_SCHEDULED")
	}
	naive := now.Add(nudgeDelay) // 21:45 UTC = 03:15 IST, 25th, still outside the window
	if got.Equal(naive) {
		t.Fatalf("dueAtFor(%v) = %v, equals the un-deferred naive delay: the contact-hour window was not applied", now, *got)
	}
	wantHourIST := 10
	if gotHour := got.In(istZone).Hour(); gotHour != wantHourIST {
		t.Errorf("dueAtFor(%v) = %v, IST hour %d, want %d (deferred to the window open)", now, *got, gotHour, wantHourIST)
	}
}
