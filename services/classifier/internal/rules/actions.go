package rules

import commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"

// Confidence values for the Phase 1 bucket -> action mapping (SPEC.md
// section 4.4). Named so the reasoning sits next to the number instead of
// being buried in a literal.
const (
	confidenceTransientBank     = 0.90 // rail was busy, funds were likely there, retry soon
	confidenceInsufficientFunds = 0.80 // instrument is valid, balance is not there yet; timing is the Decision Engine's call
	confidenceHardDecline       = 0.85 // a retry cannot succeed on a dead instrument, only a method update can (ARCHITECTURE.md section 5a)
	confidenceUserActionNeeded  = 0.70 // needs the customer to act; lower because this bucket is the broadest
	confidenceRiskHold          = 1.00 // never auto-act around a risk decision (ARCHITECTURE.md section 5a)
	confidenceAbandonment       = 0.80 // nothing failed technically, they just left
	confidenceOverdue           = 0.75 // no technical failure, a reminder is the whole intervention
	confidenceUnspecified       = 0.00 // we do not know, so a human should
)

// actionRule is the recommended action and honest confidence for a bucket.
type actionRule struct {
	Action     commonv1.ActionType
	Confidence float64
}

// bucketToAction is the Phase 1 mapping (SPEC.md section 4.4), kept as data
// so a test can iterate every RootCauseBucket value and catch a bucket added
// to the proto without a rule here.
var bucketToAction = map[commonv1.RootCauseBucket]actionRule{
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK:     {commonv1.ActionType_ACTION_TYPE_RETRY, confidenceTransientBank},
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS: {commonv1.ActionType_ACTION_TYPE_RETRY, confidenceInsufficientFunds},
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE:       {commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, confidenceHardDecline},
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED: {commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE, confidenceUserActionNeeded},
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD:          {commonv1.ActionType_ACTION_TYPE_ESCALATE, confidenceRiskHold},
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT:        {commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, confidenceAbandonment},
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE:            {commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, confidenceOverdue},
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED:        {commonv1.ActionType_ACTION_TYPE_ESCALATE, confidenceUnspecified},
}

// actionFor returns the recommended action and confidence for bucket.
func actionFor(bucket commonv1.RootCauseBucket) actionRule {
	return bucketToAction[bucket]
}
