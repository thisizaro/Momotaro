// Pure state-transition logic: given a classification or an execution
// outcome, what should this record's next state be. No I/O, no gRPC, no SQL,
// so every branch of docs/ARCHITECTURE.md section 7's diagram is testable in
// isolation (docs/ENGINEERING.md section 14).
//
// scoreAndRoute is the economics gate itself (docs/ARCHITECTURE.md section
// 5a): guardrails filter, then economics decides. It is the one function
// both the New path (engine.go's decide) and the re-entry path after a
// failed attempt (scheduler.go's handleFailedAttempt) call, so the two paths
// cannot drift apart (docs/PHASE2_IMPLEMENTATION.md Unit E).
package engine

import (
	"fmt"
	"time"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/economics"
)

// decideAfterClassify turns a classification into the record's next state
// and, for a scheduled action, which action was scheduled (persisted as
// record_state.pending_action so the scheduler knows what to resume without
// re-classifying, docs/DECISIONS.md).
func decideAfterClassify(resp *classifierv1.ClassifyResponse) (state commonv1.RecordState, pendingAction commonv1.ActionType, reason string) {
	return decideForAction(resp.GetRecommendedAction())
}

// decideForAction is decideAfterClassify's body, split out because the action
// that gets scheduled is not always the one the Classifier recommended: the
// guardrails (guardrails.go) may have removed it, in which case the caller
// passes the downgraded action here instead (docs/ARCHITECTURE.md section 5a,
// guardrails filter between classification and the decision).
func decideForAction(action commonv1.ActionType) (state commonv1.RecordState, pendingAction commonv1.ActionType, reason string) {
	switch action {
	case commonv1.ActionType_ACTION_TYPE_RETRY:
		return commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED, action, "classified, retry scheduled"
	case commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER:
		return commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED, action, "classified, nudge scheduled"
	case commonv1.ActionType_ACTION_TYPE_ESCALATE:
		return commonv1.RecordState_RECORD_STATE_ESCALATED, commonv1.ActionType_ACTION_TYPE_UNSPECIFIED, "classifier recommended escalation"
	default:
		// ACTION_TYPE_NONE (Phase 2's ClosedUneconomic is the honest home
		// for this, once the economics gate exists) or an unrecognised
		// value: escalate rather than silently doing nothing with a
		// record nobody is now tracking.
		return commonv1.RecordState_RECORD_STATE_ESCALATED, commonv1.ActionType_ACTION_TYPE_UNSPECIFIED, "no actionable recommendation, escalating for review"
	}
}

// decideAfterExecute turns an executed action's outcome into the record's
// next state, for the two outcomes that need no re-pricing: a success
// terminates as Recovered regardless of action type, and a pending outcome
// parks a nudge awaiting its delayed callback (or escalates if a
// synchronous action somehow came back pending). pending is the action that
// was actually executed (from record_state.pending_action).
//
// A FAILURE outcome is deliberately NOT handled by returning Escalated here.
// docs/ARCHITECTURE.md section 7 sends a failed attempt back to Scoring so
// it is re-priced with one more attempt spent, and that routing needs the
// guardrails and the economics model, which this pure function does not
// have. scheduler.go's handleFailedAttempt calls scoreAndRoute for that case
// instead and never reaches this function's FAILURE branch in production;
// it is kept here only as a defensive default should an unexpected outcome
// value ever reach it directly.
func decideAfterExecute(pending commonv1.ActionType, outcome commonv1.Outcome) (state commonv1.RecordState, reason string) {
	switch outcome {
	case commonv1.Outcome_OUTCOME_SUCCESS:
		return commonv1.RecordState_RECORD_STATE_RECOVERED, "action succeeded"
	case commonv1.Outcome_OUTCOME_PENDING:
		if isNudge(pending) {
			return commonv1.RecordState_RECORD_STATE_NUDGED, "awaiting delayed outcome"
		}
		return commonv1.RecordState_RECORD_STATE_ESCALATED, "unexpected pending outcome for a synchronous action"
	default: // OUTCOME_FAILURE or OUTCOME_UNSPECIFIED
		return commonv1.RecordState_RECORD_STATE_ESCALATED, "action failed"
	}
}

func isNudge(action commonv1.ActionType) bool {
	return action == commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE ||
		action == commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER
}

// claimedState is the state a scheduled record moves into the moment the
// scheduler claims it (docs/ARCHITECTURE.md section 7a), before Execute is
// even called. RetryScheduled always resumes as Retrying; either nudge
// subtype resumes as the single Nudged state.
func claimedState(scheduled commonv1.RecordState) commonv1.RecordState {
	if scheduled == commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED {
		return commonv1.RecordState_RECORD_STATE_RETRYING
	}
	return commonv1.RecordState_RECORD_STATE_NUDGED
}

// stateStep is one transition to record. A record can move through more than
// one state in a single handling pass: docs/ARCHITECTURE.md section 7 routes
// every classified record through Scoring before it is scheduled, and Scoring
// is a gate rather than somewhere a record waits. Both transitions are written
// in one database transaction, so the trail is a complete replay of the
// diagram rather than a summary of where a record ended up.
type stateStep struct {
	From, To commonv1.RecordState
	Reason   string
}

// scoringPath is the sequence of transitions for a record that reached the
// economics gate: New -> Scoring, then Scoring -> wherever the scorer sent it.
func scoringPath(to commonv1.RecordState, reason string) []stateStep {
	return []stateStep{
		{From: commonv1.RecordState_RECORD_STATE_NEW, To: commonv1.RecordState_RECORD_STATE_SCORING, Reason: "classified, guardrails applied, scoring"},
		{From: commonv1.RecordState_RECORD_STATE_SCORING, To: to, Reason: reason},
	}
}

// rescoringPath is scoringPath's counterpart for the re-entry edges in
// docs/ARCHITECTURE.md section 7 (Retrying -> Scoring, Nudged -> Scoring):
// from is the claimed state the failed attempt was executed from, and to is
// wherever the re-priced economics sent the record next.
func rescoringPath(from, to commonv1.RecordState, reason string) []stateStep {
	return []stateStep{
		{From: from, To: commonv1.RecordState_RECORD_STATE_SCORING, Reason: "attempt failed, re-scoring with one more attempt spent"},
		{From: commonv1.RecordState_RECORD_STATE_SCORING, To: to, Reason: reason},
	}
}

// directPath is a single transition out of New, for a record that never
// reaches the economics gate. Only escalation takes this route: a risk hold or
// a low-confidence classification is a safety decision, and pricing it would
// imply it were negotiable.
func directPath(to commonv1.RecordState, reason string) []stateStep {
	return []stateStep{{From: commonv1.RecordState_RECORD_STATE_NEW, To: to, Reason: reason}}
}

// scoreAndRoute runs the fixed order from docs/ARCHITECTURE.md section 5a:
// guardrails filter, then economics decides. It is the entire body of the
// economics gate, shared by every caller that reaches Scoring, whether that
// is a fresh record's first classification or a failed attempt's re-entry
// (docs/PHASE2_IMPLEMENTATION.md Unit E). No I/O: guardrails and the
// economics model are both pure once loaded, so this is table-driven
// testable without Postgres.
//
// Two independent stops keep a record from cycling through Scoring forever,
// and both are visible in this one function:
//
//   - the guardrails refuse every spending action (retry budget or contact
//     cap exhausted, cooldown active, recovery window closed): Escalated.
//     This is the compliance stop, and it is driven by applyGuardrails
//     refusing, never by a hardcoded attempt-count check here.
//   - the guardrails permit something but nothing has positive expected
//     value (the priors have decayed to beyondListedAttemptsBps past the
//     deepest modelled attempt): ClosedUneconomic. This is the economics
//     stop.
func scoreAndRoute(model *economics.Model, guardrails GuardrailConfig, bucket commonv1.RootCauseBucket, history attemptHistory, amountPaise int64, now time.Time) (state commonv1.RecordState, pendingAction commonv1.ActionType, reason string, score economics.Score) {
	none := commonv1.ActionType_ACTION_TYPE_UNSPECIFIED

	verdict := applyGuardrails(history, guardrails, now)
	permitted := permittedActions(verdict)

	if len(permitted) == 0 {
		return commonv1.RecordState_RECORD_STATE_ESCALATED, none,
			fmt.Sprintf("guardrails permit no action: %s", guardrailRefusalReason(verdict)), economics.Score{}
	}

	candidates := make([]economics.Candidate, 0, len(permitted))
	for _, action := range permitted {
		candidates = append(candidates, economics.Candidate{Action: action, AttemptNo: attemptNumberFor(action, history)})
	}

	best, worthDoing := model.Best(candidates, bucket, amountPaise)
	if !worthDoing {
		return commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC, none, "no permitted action has positive expected value", economics.Score{}
	}

	state, pendingAction, _ = decideForAction(best.Action)
	reason = fmt.Sprintf("best expected value: %s worth %.0f paise (p=%.4f, cost=%d paise, at risk=%d paise)",
		best.Action, best.EVPaise, best.PRecovery, best.CostPaise, amountPaise)
	return state, pendingAction, reason, best
}

// guardrailRefusalReason names the rule that closed off every spending
// action, for the audit trail. spendingActions is checked in order so the
// message is deterministic rather than picking whichever guardrail happened
// to run last.
func guardrailRefusalReason(v guardrailVerdict) string {
	for _, action := range spendingActions {
		if reason := v.reason(action); reason != "" {
			return reason
		}
	}
	return "no reason recorded"
}

// dueAtFor computes when a record newly parked in state should first become
// actionable, or nil for a state that is not waiting on the scheduler
// (docs/ARCHITECTURE.md section 7a: due_at is what the scheduler polls).
// Shared by the New path (engine.go) and the re-entry path after a failed
// attempt (scheduler.go), so both compute timing the same way.
func dueAtFor(state commonv1.RecordState, retryDelay, nudgeDelay time.Duration, now time.Time) *time.Time {
	var delay time.Duration
	switch state {
	case commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED:
		delay = retryDelay
	case commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED:
		delay = nudgeDelay
	default:
		return nil
	}
	due := now.Add(delay)
	return &due
}
