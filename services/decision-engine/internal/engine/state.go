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
// Deliberately takes bucket, not a ClassifyResponse: the re-entry path
// (scheduler.go's handleFailedAttempt) never re-classifies, so it has no
// fresh confidence value to check. The confidence threshold (Phase 3 Unit
// G, engine.go's decide) therefore applies only on the New path, by
// construction rather than by a guard against a stale or zero value here.
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
//
// downtime is a live payment-downtime signal to weigh against this record's
// instrument (docs/PHASE5_5_IMPLEMENTATION.md Unit Y), loaded by the caller
// from PAYMENT_DOWNTIME the same way history is loaded from
// INTERVENTION_ATTEMPT: this function stays pure, everything I/O-shaped
// happens before it is called. downtimeStatus{} (its zero value) means "no
// downtime known for this instrument", the same as every existing call site
// before this parameter existed.
func scoreAndRoute(model *economics.Model, guardrails GuardrailConfig, bucket commonv1.RootCauseBucket, history attemptHistory, downtime downtimeStatus, amountPaise int64, now time.Time) (state commonv1.RecordState, pendingAction commonv1.ActionType, reason string, score economics.Score, trace DecisionTrace) {
	none := commonv1.ActionType_ACTION_TYPE_UNSPECIFIED

	verdict := applyGuardrails(history, guardrails, now)
	verdict = applyDowntimeGuardrail(verdict, downtime, now)
	permitted := permittedActions(verdict)

	// A downtime is the one guardrail here that is not a permanent stop: it
	// removes RETRY from the permitted set the same way every other rule
	// does, but unlike them it says nothing about whether a retry is worth
	// running once the outage clears. So when RETRY was excluded ONLY
	// because of a downtime (retryHeldByDowntime), it is still priced
	// alongside whatever else is permitted: if it is genuinely the best
	// candidate, the record is scheduled to retry as normal and simply held
	// from firing until the downtime lifts (docs/DECISIONS.md); if something
	// else wins (a nudge is unaffected by a bank outage) or nothing is worth
	// doing at all, the downtime made no difference to that outcome.
	scoreActions := permitted
	if verdict.retryHeldByDowntime {
		scoreActions = append(append([]commonv1.ActionType{}, permitted...), commonv1.ActionType_ACTION_TYPE_RETRY)
	}

	if len(scoreActions) == 0 {
		return commonv1.RecordState_RECORD_STATE_ESCALATED, none,
			fmt.Sprintf("guardrails permit no action: %s", guardrailRefusalReason(verdict)), economics.Score{},
			DecisionTrace{Blocked: verdict.blocked}
	}

	candidates := make([]economics.Candidate, 0, len(scoreActions))
	for _, action := range scoreActions {
		candidates = append(candidates, economics.Candidate{Action: action, AttemptNo: attemptNumberFor(action, history)})
	}

	allScores := model.ScoreAll(candidates, bucket, amountPaise)
	trace = DecisionTrace{Candidates: allScores, Blocked: verdict.blocked}

	best, worthDoing := economics.BestOf(allScores)
	if !worthDoing {
		return commonv1.RecordState_RECORD_STATE_CLOSED_UNECONOMIC, none, "no permitted action has positive expected value", economics.Score{}, trace
	}

	state, pendingAction, _ = decideForAction(best.Action)
	if best.Action == commonv1.ActionType_ACTION_TYPE_RETRY && verdict.retryHeldByDowntime {
		// Worth running, just not right now: schedule it exactly like a
		// normal retry (same state, same pending action, same due_at
		// computation by the caller), but the reason names the downtime
		// rather than the economics, since that is the real story for
		// anyone reading the audit trail.
		return state, pendingAction, verdict.reason(commonv1.ActionType_ACTION_TYPE_RETRY), best, trace
	}
	reason = fmt.Sprintf("best expected value: %s worth %.0f paise (p=%.4f, cost=%d paise, at risk=%d paise)",
		best.Action, best.EVPaise, best.PRecovery, best.CostPaise, amountPaise)
	return state, pendingAction, reason, best, trace
}

// DecisionTrace is the full record of one scoring decision: every permitted
// candidate's score (not just the winner) and every action the guardrails
// refused, with why. Persisted so the audit trail can answer "why not the
// alternatives" (docs/PRD.md section 0: "every money action explainable",
// docs/PHASE5_IMPLEMENTATION.md Unit M). Purely additive bookkeeping: it
// records the comparison scoreAndRoute already made, it does not change
// which action wins.
//
// Candidates is nil when the guardrails permitted nothing at all (there was
// nothing to score); Blocked is nil/empty when nothing was refused.
type DecisionTrace struct {
	Candidates []economics.Score
	Blocked    map[commonv1.ActionType]string
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

// dueAtFor computes when a record newly parked in a waiting state should
// first become actionable, or nil for a state that is not waiting on the
// scheduler (docs/ARCHITECTURE.md section 7a: due_at is what the scheduler
// polls). Shared by the New path (engine.go) and the re-entry path after a
// failed attempt (scheduler.go), so both compute timing the same way.
// RETRY_SCHEDULED is now handled by retryDueAt (schedule.go) for
// cause-aware timing; this function only covers NUDGE_SCHEDULED.
//
// The result also passes through contactHourWindow (schedule.go, docs/PRD.md
// section 11a): every non-nil result here is a customer-contacting nudge's
// due time by construction (the only state this function computes one for),
// so the TRAI contact-hour rule applies unconditionally, with no need to
// separately check the pending action.
func dueAtFor(state commonv1.RecordState, nudgeDelay time.Duration, now time.Time, timeScale float64) *time.Time {
	if state != commonv1.RecordState_RECORD_STATE_NUDGE_SCHEDULED {
		return nil
	}
	due := now.Add(nudgeDelay)
	due = contactHourWindow(due, timeScale)
	return &due
}
