package server

import commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"

// correctActionFor mirrors services/classifier/internal/rules/actions.go's
// actionFor table. It cannot import that package (it is private to
// services/classifier, and cross-service code is gRPC only,
// demo/world-simulator/AGENTS.md), so this is a deliberate, small
// duplication rather than an in-process dependency. The two tables MUST
// stay in sync: this is "what the classifier would have recommended for
// the record's true bucket", which is what distinguishes a correctly
// diagnosed record (recovery_probability applies) from an incorrectly
// diagnosed one (wrong_action_probability applies), per
// docs/ARCHITECTURE.md section 6 and scripts/batchgen/profile.go's own
// "chance of recovery given the CORRECT action" comment. Same precedent as
// scripts/batchgen/profile.go's ObviousBucket table, which carries an
// identical "must agree" note.
var correctActionFor = map[commonv1.RootCauseBucket]commonv1.ActionType{
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK:     commonv1.ActionType_ACTION_TYPE_RETRY,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS: commonv1.ActionType_ACTION_TYPE_RETRY,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE:       commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED: commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD:          commonv1.ActionType_ACTION_TYPE_ESCALATE,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT:        commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE:            commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED:        commonv1.ActionType_ACTION_TYPE_ESCALATE,
}

// isCorrectAction reports whether action is what the classifier would have
// recommended for a record whose true bucket is trueBucket. An unrecognised
// bucket (should not happen: GROUND_TRUTH.true_bucket is always one of the
// seven) is never correct, matching the fail-safe default elsewhere in the
// system (e.g. classifier's own unrecognised-code fallback escalates
// rather than guesses).
func isCorrectAction(action commonv1.ActionType, trueBucket commonv1.RootCauseBucket) bool {
	correct, ok := correctActionFor[trueBucket]
	return ok && action == correct
}

// isNudge mirrors services/decision-engine/internal/engine/state.go's
// helper of the same name: the two customer-contacting action types that
// resolve later rather than inside this RPC.
func isNudge(action commonv1.ActionType) bool {
	return action == commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE ||
		action == commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER
}
