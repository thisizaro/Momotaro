package rules

import (
	"fmt"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// bucketReasoning is the one-line "why" for each bucket (SPEC.md section
// 4.4's "reasoning to encode in the rationale" column).
var bucketReasoning = map[commonv1.RootCauseBucket]string{
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK:     "the rail was busy and funds were likely there, so a retry soon should succeed",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS: "the instrument is valid but the balance is not there yet",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE:       "a retry cannot succeed on a dead instrument, only a method update can",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED: "the customer needs to act before this can succeed",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD:          "the record is held for risk review and must never be auto-acted on",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT:        "nothing failed technically, the customer just left checkout",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE:            "there was no technical failure, a reminder is the whole intervention",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED:        "the root cause could not be determined from the available signal",
}

// actionLabel renders an action for use inside a rationale sentence.
func actionLabel(a commonv1.ActionType) string {
	switch a {
	case commonv1.ActionType_ACTION_TYPE_RETRY:
		return "a retry"
	case commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE:
		return "a method-update nudge"
	case commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER:
		return "a reminder nudge"
	case commonv1.ActionType_ACTION_TYPE_ESCALATE:
		return "escalation to a human"
	case commonv1.ActionType_ACTION_TYPE_NONE:
		return "no action"
	default:
		return a.String()
	}
}

// composeRationale builds the audit-trail rationale (SPEC.md section 4.5):
// plain English naming the actual failure code and the recommended action,
// never boilerplate alone. recognized is false on the unknown-code path
// (section 4.3), which changes the wording but must still name the code
// verbatim so a human reading the audit trail can add it to the table.
// indeterminate is true for a code whose true outcome is unknown rather
// than failed (docs/PHASE5_IMPLEMENTATION.md Unit I): it overrides the
// bucket's own canned reasoning, since RISK_HOLD's generic "held for risk
// review" wording would be a false explanation for a purely technical
// ambiguity.
func composeRationale(bucket commonv1.RootCauseBucket, action commonv1.ActionType, rawCode string, recognized bool, indeterminate bool) string {
	if indeterminate {
		return fmt.Sprintf("failure code %q means the payment's true outcome could not be confirmed, not that it failed; retrying risks a duplicate charge on a payment that may have already succeeded, so this is held for a human to reconcile rather than auto-retried. Recommending %s.", rawCode, actionLabel(action))
	}
	reason := bucketReasoning[bucket]
	if !recognized {
		if rawCode == "" {
			return fmt.Sprintf("no failure code was provided; %s. Recommending %s.", reason, actionLabel(action))
		}
		return fmt.Sprintf("failure code %q is not in the classification table; %s. Recommending %s.", rawCode, reason, actionLabel(action))
	}
	return fmt.Sprintf("failure code %q: %s. Recommending %s.", rawCode, reason, actionLabel(action))
}

// composeTaxonomyRationale builds the rationale for a record resolved via
// error_source/error_step rather than failure_code
// (docs/PHASE5_5_IMPLEMENTATION.md Unit Z, bucketForErrorTaxonomy in
// buckets.go): failure_code alone was not recognised, but the taxonomy
// fields let the rules engine make a real distinction the code could not.
// Named the fields explicitly, the same discipline composeRationale applies
// to rawCode, so a human reading the audit trail sees exactly which two
// values drove the answer.
func composeTaxonomyRationale(bucket commonv1.RootCauseBucket, action commonv1.ActionType, rawCode, source, step string) string {
	reason := bucketReasoning[bucket]
	codePart := fmt.Sprintf("failure code %q", rawCode)
	if rawCode == "" {
		codePart = "no failure code"
	}
	return fmt.Sprintf("%s was not in the classification table, but error_source=%q and error_step=%q were; %s. Recommending %s.",
		codePart, source, step, reason, actionLabel(action))
}
