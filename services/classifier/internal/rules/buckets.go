package rules

import (
	"strings"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// normalizeFailureCode trims whitespace, uppercases, and collapses hyphens
// and spaces to underscores, so "bank_timeout", "BANK-TIMEOUT" and
// " Bank Timeout " all reach the same lookup key (SPEC.md section 4.1).
func normalizeFailureCode(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	return strings.NewReplacer("-", "_", " ", "_").Replace(s)
}

// failureCodeToBucket is the Phase 1 mapping (SPEC.md section 4.2). Kept as
// data, not a switch with fallthrough logic, so it can be audited at a
// glance and iterated by a test.
var failureCodeToBucket = map[string]commonv1.RootCauseBucket{
	"BANK_TIMEOUT":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"RAIL_CONGESTION":    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"ISSUER_UNAVAILABLE": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"GATEWAY_TIMEOUT":    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"TIMEOUT":            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,

	"INSUFFICIENT_FUNDS": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
	"LOW_BALANCE":        commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,

	"HARD_DECLINE":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"EXPIRED_INSTRUMENT": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"EXPIRED_CARD":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"BLOCKED_INSTRUMENT": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"INVALID_INSTRUMENT": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"CARD_INVALID":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"DO_NOT_HONOUR":      commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,

	"RISK_HOLD":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
	"FRAUD_REVIEW":    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
	"SUSPECTED_FRAUD": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,

	"AUTH_REQUIRED":      commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"REAUTH_REQUIRED":    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"MANDATE_REVOKED":    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"MANDATE_PAUSED":     commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"USER_ACTION_NEEDED": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,

	"CHECKOUT_ABANDONED": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,
	"ABANDONED":          commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,
	"ABANDONMENT":        commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,

	"INVOICE_OVERDUE": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE,
	"OVERDUE":         commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE,
	"PAST_DUE":        commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE,
}

// bucketForCode resolves a normalised failure code to a bucket. The second
// return value is false when the code is not in the table, so the caller
// can apply the record-type fallback (SPEC.md section 4.3) instead of
// guessing.
func bucketForCode(normalized string) (commonv1.RootCauseBucket, bool) {
	b, ok := failureCodeToBucket[normalized]
	return b, ok
}

// fallbackBucket applies the unknown-code fallback ordering (SPEC.md
// section 4.3): the record type carries genuine signal about the failure
// mode when the rail code itself is unrecognised or absent.
func fallbackBucket(t commonv1.RecordType) commonv1.RootCauseBucket {
	switch t {
	case commonv1.RecordType_RECORD_TYPE_CHECKOUT:
		return commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT
	case commonv1.RecordType_RECORD_TYPE_INVOICE:
		return commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE
	default:
		return commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED
	}
}
