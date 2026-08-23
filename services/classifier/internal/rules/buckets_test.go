package rules

import (
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
