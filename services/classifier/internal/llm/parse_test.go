package llm

import (
	"strings"
	"testing"
	"unicode/utf8"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func validAnswerJSON() string {
	return `{"bucket":"ROOT_CAUSE_BUCKET_TRANSIENT_BANK","recommended_action":"ACTION_TYPE_RETRY","rationale":"Issuer timed out; funds were likely present, so a retry should succeed.","confidence":0.9}`
}

func TestParseAnswerAcceptsAWellFormedAnswer(t *testing.T) {
	resp, err := parseAnswer(validAnswerJSON())
	if err != nil {
		t.Fatalf("parseAnswer: %v", err)
	}
	if resp.GetBucket() != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK {
		t.Errorf("bucket = %v", resp.GetBucket())
	}
	if resp.GetRecommendedAction() != commonv1.ActionType_ACTION_TYPE_RETRY {
		t.Errorf("action = %v", resp.GetRecommendedAction())
	}
	if resp.GetConfidence() != 0.9 {
		t.Errorf("confidence = %v, want 0.9", resp.GetConfidence())
	}
	if resp.GetRationale() == "" {
		t.Error("rationale is empty")
	}
}

// Every case here must be an error, because every error becomes a
// schema_invalid hop and the chain falls through to a rung that cannot fail.
// Being lenient anywhere means putting a value this service cannot verify into
// the audit trail.
func TestParseAnswerRejectsAnythingItCannotVerify(t *testing.T) {
	cases := map[string]string{
		"not json at all":           `I think this is a bank timeout.`,
		"truncated json":            `{"bucket":"ROOT_CAUSE_BUCKET_TRANSIENT_BANK","recommended_`,
		"bucket outside the enum":   `{"bucket":"ROOT_CAUSE_BUCKET_MERCHANT_FRAUD","recommended_action":"ACTION_TYPE_RETRY","rationale":"x","confidence":0.5}`,
		"action outside the enum":   `{"bucket":"ROOT_CAUSE_BUCKET_TRANSIENT_BANK","recommended_action":"ACTION_TYPE_REFUND","rationale":"x","confidence":0.5}`,
		"action withheld from menu": `{"bucket":"ROOT_CAUSE_BUCKET_TRANSIENT_BANK","recommended_action":"ACTION_TYPE_NONE","rationale":"x","confidence":0.5}`,
		"unspecified action":        `{"bucket":"ROOT_CAUSE_BUCKET_TRANSIENT_BANK","recommended_action":"ACTION_TYPE_UNSPECIFIED","rationale":"x","confidence":0.5}`,
		"empty rationale":           `{"bucket":"ROOT_CAUSE_BUCKET_TRANSIENT_BANK","recommended_action":"ACTION_TYPE_RETRY","rationale":"","confidence":0.5}`,
		"whitespace-only rationale": `{"bucket":"ROOT_CAUSE_BUCKET_TRANSIENT_BANK","recommended_action":"ACTION_TYPE_RETRY","rationale":"   ","confidence":0.5}`,
		"extra field smuggled in":   `{"bucket":"ROOT_CAUSE_BUCKET_TRANSIENT_BANK","recommended_action":"ACTION_TYPE_RETRY","rationale":"x","confidence":0.5,"execute":"true"}`,
		"lowercase bucket":          `{"bucket":"transient_bank","recommended_action":"ACTION_TYPE_RETRY","rationale":"x","confidence":0.5}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAnswer(raw); err == nil {
				t.Errorf("parseAnswer(%s): want error, got nil", name)
			}
		})
	}
}

// Out-of-range confidence is deliberately NOT rejected here: it is
// provider/validate.go's job, the vendor-independent gate the chain runs on
// every rung's answer. Duplicating it would mean two places to keep in step,
// and the chain would record the failure as schema_invalid either way.
func TestParseAnswerLeavesConfidenceRangeToTheChainsValidator(t *testing.T) {
	resp, err := parseAnswer(`{"bucket":"ROOT_CAUSE_BUCKET_TRANSIENT_BANK","recommended_action":"ACTION_TYPE_RETRY","rationale":"x","confidence":1.5}`)
	if err != nil {
		t.Fatalf("parseAnswer should pass an out-of-range confidence through to validate.go: %v", err)
	}
	if resp.GetConfidence() != 1.5 {
		t.Errorf("confidence = %v, want it passed through unchanged", resp.GetConfidence())
	}
}

func TestSanitizeRationaleStripsControlCharactersAndCollapsesWhitespace(t *testing.T) {
	got := sanitizeRationale("Issuer\x00 timed\tout.\n\nRetry\r\nsoon.\x07")
	if strings.ContainsAny(got, "\x00\x07\n\r\t") {
		t.Errorf("sanitizeRationale left control characters in %q", got)
	}
	if got != "Issuer timed out. Retry soon." {
		t.Errorf("sanitizeRationale = %q, want whitespace collapsed to single spaces", got)
	}
}

func TestSanitizeRationaleCapsLengthOnARuneBoundary(t *testing.T) {
	// Multi-byte runes: a naive byte slice would cut one in half and produce
	// invalid UTF-8 in an audit column.
	long := strings.Repeat("दे", maxRationaleChars*2)
	got := sanitizeRationale(long)
	if n := len([]rune(got)); n > maxRationaleChars {
		t.Errorf("rationale is %d runes, want at most %d", n, maxRationaleChars)
	}
	if !utf8.ValidString(got) {
		t.Error("sanitizeRationale produced invalid UTF-8: a rune was cut in half")
	}
}

func TestSanitizeRationaleLeavesANormalSentenceAlone(t *testing.T) {
	in := "Issuer timed out on a 4,999 rupee mandate debit; funds were likely present."
	if got := sanitizeRationale(in); got != in {
		t.Errorf("sanitizeRationale mangled ordinary prose:\n in:  %q\n got: %q", in, got)
	}
}
