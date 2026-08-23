// Pure state-transition logic: given a classification or an execution
// outcome, what should this record's next state be. No I/O, no gRPC, no SQL,
// so every branch of docs/ARCHITECTURE.md section 7's diagram is testable in
// isolation (docs/ENGINEERING.md section 14).
//
// Deliberately missing Scoring and ClosedUneconomic: the expected-value
// economics gate is docs/PLAN.md Phase 2 work, built on top of this once it
// lands. Phase 1's job is to prove the pipeline shape (classify, schedule,
// wait, execute, terminate) actually works end to end.
package engine

import (
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// decideAfterClassify turns a classification into the record's next state
// and, for a scheduled action, which action was scheduled (persisted as
// record_state.pending_action so the scheduler knows what to resume without
// re-classifying, docs/DECISIONS.md).
func decideAfterClassify(resp *classifierv1.ClassifyResponse) (state commonv1.RecordState, pendingAction commonv1.ActionType, reason string) {
	switch action := resp.GetRecommendedAction(); action {
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
// next state. pending is the action that was actually executed (from
// record_state.pending_action), needed because a PENDING outcome means
// something different for a nudge (wait for the delayed callback) than for
// a retry (retries are synchronous per docs/ARCHITECTURE.md section 6, so
// PENDING there is unexpected).
//
// Phase 1 has no retry budget or cause-aware rescheduling yet (docs/PLAN.md
// Phase 2), so a failure escalates rather than looping back through
// classification for another attempt.
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
