package rules

// docs/PHASE5_5_IMPLEMENTATION.md Unit Z: error_source and error_step are a
// second, independent signal the rules engine can use when failure_code
// itself is unrecognised -- "source: bank plus step: payment_authorization
// says systemic-and-not-the-customer's-fault, which is a retry; source:
// customer plus step: payment_authentication is a failed OTP, which is
// not." These tests prove that distinction is real, that it only ever
// kicks in when failure_code alone was not enough (never overriding a
// recognised code), and that a value from a payment method this engine has
// never seen is handled without crashing rather than rejected.

import (
	"strings"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// TestClassifyUsesErrorStepToDistinguishAuthenticationFromAuthorization is
// the exact worked example from docs/PHASE5_5_IMPLEMENTATION.md Unit Z,
// using Razorpay's own documented field values
// (https://razorpay.com/docs/webhooks/payloads/payments/).
func TestClassifyUsesErrorStepToDistinguishAuthenticationFromAuthorization(t *testing.T) {
	cases := []struct {
		name        string
		errorSource string
		errorStep   string
		wantBucket  commonv1.RootCauseBucket
		wantAction  commonv1.ActionType
	}{
		{
			name:        "bank failed at authorization: systemic, retry",
			errorSource: "bank",
			errorStep:   "payment_authorization",
			wantBucket:  commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
			wantAction:  commonv1.ActionType_ACTION_TYPE_RETRY,
		},
		{
			name:        "customer failed at authentication: their OTP, not a retry",
			errorSource: "customer",
			errorStep:   "payment_authentication",
			wantBucket:  commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
			wantAction:  commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := classify(t, &commonv1.Record{
				Id:          "rec-1",
				Type:        commonv1.RecordType_RECORD_TYPE_PAYMENT,
				FailureCode: "payment_failed", // Razorpay's own vague generic code, not in failureCodeToBucket
				ErrorSource: c.errorSource,
				ErrorStep:   c.errorStep,
			})
			if resp.GetBucket() != c.wantBucket {
				t.Errorf("bucket = %v, want %v", resp.GetBucket(), c.wantBucket)
			}
			if resp.GetRecommendedAction() != c.wantAction {
				t.Errorf("action = %v, want %v", resp.GetRecommendedAction(), c.wantAction)
			}
			if !strings.Contains(resp.GetRationale(), c.errorSource) || !strings.Contains(resp.GetRationale(), c.errorStep) {
				t.Errorf("rationale %q does not name error_source/error_step used to resolve it", resp.GetRationale())
			}
		})
	}
}

// TestClassifyRecognisedFailureCodeIgnoresContradictingTaxonomy proves the
// taxonomy fields are a FALLBACK signal, never a veto over a failure_code
// that already matched: a contradicting error_source must not flip the
// answer once failure_code alone resolved it.
func TestClassifyRecognisedFailureCodeIgnoresContradictingTaxonomy(t *testing.T) {
	resp := classify(t, &commonv1.Record{
		Id:          "rec-1",
		FailureCode: "BANK_TIMEOUT", // recognised directly, maps to TRANSIENT_BANK
		ErrorSource: "customer",     // would say USER_ACTION_NEEDED if consulted
		ErrorStep:   "payment_authentication",
	})
	if resp.GetBucket() != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK {
		t.Errorf("bucket = %v, want TRANSIENT_BANK (BANK_TIMEOUT's own table entry); a recognised failure_code must not be overridden by error_source/error_step", resp.GetBucket())
	}
}

// TestClassifyHandlesAnUnrecognisedErrorSourceWithoutCrashing is the "trap"
// this unit calls out explicitly: error_source and error_step are OPEN
// vocabularies that vary by payment method, never a closed enum. A value
// from a method this engine has never seen (a hypothetical Cardless EMI or
// e-mandate step) must not panic and must not be silently discarded; it
// falls through to the existing record-type fallback, exactly the path an
// unrecognised failure_code alone already takes.
func TestClassifyHandlesAnUnrecognisedErrorSourceWithoutCrashing(t *testing.T) {
	resp := classify(t, &commonv1.Record{
		Id:          "rec-1",
		Type:        commonv1.RecordType_RECORD_TYPE_PAYMENT,
		FailureCode: "payment_failed",
		ErrorSource: "cardless_emi_partner_never_documented",
		ErrorStep:   "emi_conversion_a_step_this_engine_has_never_seen",
	})
	if resp.GetBucket() != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED {
		t.Errorf("bucket = %v, want UNSPECIFIED (PAYMENT type has no fallback): an unrecognised error_source/error_step must fall through cleanly, not guess", resp.GetBucket())
	}
	if resp.GetRationale() == "" {
		t.Error("rationale is empty for an unrecognised error_source/error_step")
	}
}

// TestClassifyIgnoresTaxonomyWhenBothFieldsAreEmpty is the regression guard
// for the pre-Unit-Z behaviour: a record with no error_source/error_step at
// all (every record before this unit, and every record from a caller that
// never sends them) must classify exactly as it always did.
func TestClassifyIgnoresTaxonomyWhenBothFieldsAreEmpty(t *testing.T) {
	resp := classify(t, &commonv1.Record{Id: "rec-1", Type: commonv1.RecordType_RECORD_TYPE_PAYMENT, FailureCode: "SOME_UNKNOWN_CODE"})
	if resp.GetBucket() != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED {
		t.Errorf("bucket = %v, want UNSPECIFIED", resp.GetBucket())
	}
}
