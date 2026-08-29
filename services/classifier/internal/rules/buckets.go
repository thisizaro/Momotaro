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

// failureCodeToBucket is the classification vocabulary (SPEC.md section
// 4.2, docs/PHASE5_IMPLEMENTATION.md Unit I). Kept as data, not a switch
// with fallthrough logic, so it can be audited at a glance and iterated by
// a test.
//
// Extended 2026-08-29 with Razorpay's own published payment error codes
// (https://razorpay.com/docs/errors/payments/list/), cited as [SOURCED],
// alongside the original Phase 1 invented codes, kept as aliases so nothing
// that already worked stops working. normalizeFailureCode already
// uppercases and collapses separators, so a lowercase Razorpay code like
// "insufficient_funds" resolves without needing its own entry when an
// equivalent uppercase key already exists.
//
// GATEWAY_TIMEOUT and TIMEOUT moved OUT of TRANSIENT_BANK in the same pass:
// see indeterminateCodes below, this is the one behavioural change in this
// table, not a rename.
var failureCodeToBucket = map[string]commonv1.RootCauseBucket{
	"BANK_TIMEOUT":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"RAIL_CONGESTION":    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"ISSUER_UNAVAILABLE": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	// [SOURCED] Razorpay, source: gateway/bank. Systemic, not the
	// customer's fault, so retrying is right and contacting them is not.
	"BANK_NOT_AVAILABLE":                   commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"BANK_TECHNICAL_ERROR":                 commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"ISSUER_TECHNICAL_ERROR":               commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"GATEWAY_TECHNICAL_ERROR":              commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"PAYMENT_DECLINED_DUE_TO_HIGH_TRAFFIC": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"PSP_NOT_AVAILABLE":                    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
	"UPI_APP_TECHNICAL_ERROR":              commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,

	"INSUFFICIENT_FUNDS": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
	"LOW_BALANCE":        commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
	// [SOURCED] Razorpay, source: customer.
	"TRANSACTION_LIMIT_EXCEEDED":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
	"TRANSACTION_DAILY_LIMIT_EXCEEDED": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
	"CREDIT_LIMIT_EXCEEDED":            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,

	"HARD_DECLINE":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"EXPIRED_INSTRUMENT": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"EXPIRED_CARD":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"BLOCKED_INSTRUMENT": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"INVALID_INSTRUMENT": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"CARD_INVALID":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"DO_NOT_HONOUR":      commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	// [SOURCED] Razorpay, source: customer/gateway. A retry cannot succeed
	// on any of these; only a method update can.
	"CARD_EXPIRED":             commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"CARD_DECLINED":            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"DEBIT_INSTRUMENT_BLOCKED": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"CARD_NUMBER_INVALID":      commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"BANK_ACCOUNT_INVALID":     commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
	"INVALID_VPA":              commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,

	"RISK_HOLD":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
	"FRAUD_REVIEW":    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
	"SUSPECTED_FRAUD": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
	// [SOURCED] Razorpay, source: razorpay.
	"PAYMENT_RISK_CHECK_FAILED": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,

	"AUTH_REQUIRED":      commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"REAUTH_REQUIRED":    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"MANDATE_REVOKED":    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"MANDATE_PAUSED":     commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"USER_ACTION_NEEDED": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	// [SOURCED] Razorpay, source: customer.
	"AUTHENTICATION_FAILED":            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"INCORRECT_OTP":                    commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"OTP_EXPIRED":                      commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"OTP_ATTEMPTS_EXCEEDED":            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"MANDATE_CREATION_FAILED":          commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"MANDATE_CREATION_DECLINED":        commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
	"REQAUTH_MANDATE_NOT_ACKNOWLEDGED": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,

	"CHECKOUT_ABANDONED": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,
	"ABANDONED":          commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,
	"ABANDONMENT":        commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,
	// [SOURCED] Razorpay, source: customer.
	"PAYMENT_CANCELLED":       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,
	"PAYMENT_SESSION_EXPIRED": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,

	"INVOICE_OVERDUE": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE,
	"OVERDUE":         commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE,
	"PAST_DUE":        commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE,

	// Indeterminate outcome, not a bucket of their own: see
	// indeterminateCodes and composeRationale's override below. Mapped to
	// RISK_HOLD because that bucket is the only one whose policy is a
	// guaranteed escalation (never auto-retried, never auto-messaged,
	// bucketToAction confidence 1.00), which is the safe behaviour for
	// "we do not know if this succeeded" until a real reconciliation path
	// exists (docs/BACKLOG.md).
	"GATEWAY_TIMEOUT":               commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
	"TIMEOUT":                       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
	"PAYMENT_TIMED_OUT":             commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
	"PAYMENT_PENDING":               commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
	"VERIFICATION_FAILED":           commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
	"INVALID_RESPONSE_FROM_GATEWAY": commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
}

// indeterminateCodes are failure codes that mean "we do not know whether
// the bank actually completed this payment", which is not the same fact as
// "it failed". Retrying one of these risks a duplicate charge on a payment
// that may have already succeeded, so they resolve to RISK_HOLD (never
// auto-retried, never auto-messaged) and get a rationale that says so
// explicitly, rather than borrowing RISK_HOLD's generic fraud-review
// wording, which would be a false explanation for a purely technical
// ambiguity (docs/PHASE5_IMPLEMENTATION.md Unit I).
//
// The full fix, a real ROOT_CAUSE_BUCKET_INDETERMINATE with a reconciliation
// step that asks the rail what actually happened, is parked in
// docs/BACKLOG.md. The Executor already implements the underlying
// principle: it refuses to re-run an unresolved claim rather than guess
// (docs/DECISIONS.md 2026-08-23).
var indeterminateCodes = map[string]bool{
	"GATEWAY_TIMEOUT":               true,
	"TIMEOUT":                       true,
	"PAYMENT_TIMED_OUT":             true,
	"PAYMENT_PENDING":               true,
	"VERIFICATION_FAILED":           true,
	"INVALID_RESPONSE_FROM_GATEWAY": true,
}

// isIndeterminate reports whether normalized is a code whose true outcome
// is unknown rather than failed.
func isIndeterminate(normalized string) bool {
	return indeterminateCodes[normalized]
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
