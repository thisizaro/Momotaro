package rules

import (
	"strings"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func TestNormalizeFailureCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"bank_timeout", "BANK_TIMEOUT"},
		{"BANK_TIMEOUT", "BANK_TIMEOUT"},
		{" Bank-Timeout ", "BANK_TIMEOUT"},
		{"bank timeout", "BANK_TIMEOUT"},
	}
	for _, c := range cases {
		if got := normalizeFailureCode(c.in); got != c.want {
			t.Errorf("normalizeFailureCode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Every entry in the Phase 1 failure-code table (SPEC.md section 4.2) must
// resolve to its documented bucket. Iterating the map itself, rather than a
// separately hand-written list of cases, means a new code cannot be added
// to the table without this test covering it.
func TestFailureCodeToBucketTable(t *testing.T) {
	for code, want := range failureCodeToBucket {
		got, ok := bucketForCode(code)
		if !ok {
			t.Errorf("bucketForCode(%q): not found", code)
			continue
		}
		if got != want {
			t.Errorf("bucketForCode(%q) = %v, want %v", code, got, want)
		}
	}
}

// Every code documented as [SOURCED] Razorpay in buckets.go (Unit I) must
// actually resolve, and an indeterminate code must never resolve to a
// bucket whose policy is an automatic retry: that would retry a payment
// that may have already succeeded. Named explicitly rather than iterating
// the map, since "not in indeterminateCodes" wouldn't catch a new
// indeterminate-shaped code someone adds straight to failureCodeToBucket
// without also adding it to indeterminateCodes.
func TestIndeterminateCodesNeverAutoRetry(t *testing.T) {
	codes := []string{
		"GATEWAY_TIMEOUT",
		"TIMEOUT",
		"PAYMENT_TIMED_OUT",
		"PAYMENT_PENDING",
		"VERIFICATION_FAILED",
		"INVALID_RESPONSE_FROM_GATEWAY",
	}
	for _, code := range codes {
		if !isIndeterminate(code) {
			t.Errorf("isIndeterminate(%q) = false, want true", code)
		}
		bucket, ok := bucketForCode(code)
		if !ok {
			t.Fatalf("bucketForCode(%q): not found", code)
		}
		rule := actionFor(bucket)
		if rule.Action == commonv1.ActionType_ACTION_TYPE_RETRY {
			t.Errorf("indeterminate code %q resolves to bucket %v, action RETRY: an unresolved outcome must never be auto-retried", code, bucket)
		}
	}
}

// The indeterminate rationale must name the code and must NOT borrow
// RISK_HOLD's generic "held for risk review" wording, which would be a
// false explanation for a purely technical, non-fraud ambiguity.
func TestIndeterminateRationaleDoesNotClaimRiskReview(t *testing.T) {
	rationale := composeRationale(commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD, commonv1.ActionType_ACTION_TYPE_ESCALATE, "GATEWAY_TIMEOUT", true, true)
	if !strings.Contains(rationale, "GATEWAY_TIMEOUT") {
		t.Errorf("rationale %q does not name the failure code", rationale)
	}
	if strings.Contains(rationale, "risk review") {
		t.Errorf("rationale %q borrows the risk-review wording, which misdescribes a technical ambiguity as a fraud hold", rationale)
	}
	if !strings.Contains(rationale, "duplicate charge") {
		t.Errorf("rationale %q does not explain the actual reason (duplicate-charge risk)", rationale)
	}
}

// A representative sample of the new Razorpay-sourced codes (Unit I) must
// resolve to the bucket documented alongside them in buckets.go. Not every
// new entry, a hand-picked one per bucket family is enough to catch a typo
// in the table without duplicating the whole table here.
func TestNewRazorpayCodesResolve(t *testing.T) {
	cases := []struct {
		code string
		want commonv1.RootCauseBucket
	}{
		{"BANK_NOT_AVAILABLE", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK},
		{"TRANSACTION_LIMIT_EXCEEDED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS},
		{"CARD_EXPIRED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE},
		{"PAYMENT_RISK_CHECK_FAILED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD},
		{"AUTHENTICATION_FAILED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED},
		{"PAYMENT_CANCELLED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT},
	}
	for _, c := range cases {
		got, ok := bucketForCode(c.code)
		if !ok {
			t.Fatalf("bucketForCode(%q): not found", c.code)
		}
		if got != c.want {
			t.Errorf("bucketForCode(%q) = %v, want %v", c.code, got, c.want)
		}
	}
}

// normalizeFailureCode already uppercases and collapses separators, so a
// real Razorpay code arriving lowercase (as their docs write it) must
// resolve without needing its own separate table entry when an equivalent
// uppercase key already exists.
func TestLowercaseRazorpayCodeResolvesViaNormalization(t *testing.T) {
	bucket, ok := bucketForCode(normalizeFailureCode("insufficient_funds"))
	if !ok {
		t.Fatal("bucketForCode(normalized \"insufficient_funds\"): not found")
	}
	if bucket != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS {
		t.Errorf("bucket = %v, want INSUFFICIENT_FUNDS", bucket)
	}
}

func TestFallbackBucket(t *testing.T) {
	cases := []struct {
		recordType commonv1.RecordType
		want       commonv1.RootCauseBucket
	}{
		{commonv1.RecordType_RECORD_TYPE_CHECKOUT, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT},
		{commonv1.RecordType_RECORD_TYPE_INVOICE, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE},
		{commonv1.RecordType_RECORD_TYPE_PAYMENT, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED},
		{commonv1.RecordType_RECORD_TYPE_MANDATE, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED},
	}
	for _, c := range cases {
		if got := fallbackBucket(c.recordType); got != c.want {
			t.Errorf("fallbackBucket(%v) = %v, want %v", c.recordType, got, c.want)
		}
	}
}
