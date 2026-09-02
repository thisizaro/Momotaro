package provider

import (
	"strings"
	"testing"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
)

// validNudgeResponse is a message that plays by the rules: it names the
// amount only via the placeholder token, never a digit of its own.
func validNudgeResponse() *classifierv1.ComposeNudgeResponse {
	return &classifierv1.ComposeNudgeResponse{
		Message: "Aapka " + AmountPlaceholder + " ka payment fail ho gaya. Please retry karein.",
	}
}

func TestValidateNudgeAcceptsAValidResponse(t *testing.T) {
	if err := validateNudge(validNudgeResponse(), 160); err != nil {
		t.Errorf("validateNudge(valid): %v", err)
	}
}

func TestValidateNudgeRejectsNil(t *testing.T) {
	if err := validateNudge(nil, 160); err == nil {
		t.Error("validateNudge(nil): want error, got nil")
	}
}

func TestValidateNudgeRejectsEmptyMessage(t *testing.T) {
	if err := validateNudge(&classifierv1.ComposeNudgeResponse{Message: ""}, 160); err == nil {
		t.Error("validateNudge(empty message): want error, got nil")
	}
}

// TestValidateNudgeRejectsInventedDigits is the constraint ARCHITECTURE.md
// section 5b exists for: "a model inventing a figure in a message about
// money is a serious failure mode, not a cosmetic one." Any digit outside
// the placeholder token means the model wrote its own number.
func TestValidateNudgeRejectsInventedDigits(t *testing.T) {
	cases := []string{
		"Aapka payment of Rs 750 fail ho gaya.",            // invented amount, no placeholder at all
		"Aapka " + AmountPlaceholder + " due on the 15th.", // invented date-like digit alongside a valid placeholder
		"Call us at 1800123456 to complete your payment.",  // an invented phone number
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			resp := &classifierv1.ComposeNudgeResponse{Message: msg}
			if err := validateNudge(resp, 500); err == nil {
				t.Errorf("validateNudge(%q): want error (invented digit), got nil", msg)
			}
		})
	}
}

func TestValidateNudgeRejectsMultiplePlaceholderOccurrences(t *testing.T) {
	msg := AmountPlaceholder + " again, " + AmountPlaceholder + " confirmed."
	if err := validateNudge(&classifierv1.ComposeNudgeResponse{Message: msg}, 500); err == nil {
		t.Error("validateNudge(two placeholders): want error, got nil")
	}
}

func TestValidateNudgeRejectsOverLengthMessage(t *testing.T) {
	long := strings.Repeat("a", 200)
	if err := validateNudge(&classifierv1.ComposeNudgeResponse{Message: long}, 160); err == nil {
		t.Error("validateNudge(over max_chars): want error, got nil")
	}
}

func TestValidateNudgeAcceptsAMessageWithNoPlaceholderAtAll(t *testing.T) {
	// Not every nudge need mention an amount (e.g. ABANDONMENT: "you left
	// something in your cart"). Zero placeholder occurrences is valid; more
	// than one is not (TestValidateNudgeRejectsMultiplePlaceholderOccurrences).
	msg := "Aapka cart mein kuch reh gaya hai, complete karein!"
	if err := validateNudge(&classifierv1.ComposeNudgeResponse{Message: msg}, 160); err != nil {
		t.Errorf("validateNudge(no placeholder): %v", err)
	}
}

// TestValidateNudgeRejectsTheActualLeakedMessage is the regression this unit
// exists for (docs/DEMO_READINESS.md "Unit AE"): both LLM-composed nudges in
// a measured 100-record batch leaked the internal RootCauseBucket vocabulary
// word "bucket" into customer-facing Hinglish. This is that exact string.
// docs/DECISIONS.md 2026-08-28 already established that a prompt instruction
// is not a guarantee; this test proves the validator catches the leak even
// though nudge_prompt.go's system prompt now also forbids it explicitly, so
// the control does not depend on the prompt behaving.
func TestValidateNudgeRejectsTheActualLeakedMessage(t *testing.T) {
	msg := "Aapka " + AmountPlaceholder + " ka payment fail ho gaya kyunki aapka bucket overdue hai."
	if err := validateNudge(&classifierv1.ComposeNudgeResponse{Message: msg}, 500); err == nil {
		t.Error("validateNudge(leaked 'bucket'): want error, got nil")
	}
}

// TestValidateNudgeRejectsInternalEnumVocabulary covers the two other
// enum families the same rule protects (ActionType, RecordState) and the
// literal SCREAMING_SNAKE_CASE constant form, which is what the model
// literally sees in its own prompt (nudge_prompt.go interpolates
// req.GetBucket().String() etc into <CONTEXT>), so an occasional verbatim
// echo of it is a real failure mode, not a hypothetical one.
func TestValidateNudgeRejectsInternalEnumVocabulary(t *testing.T) {
	cases := map[string]string{
		"bare 'bucket', any case":       "Aapka BUCKET abhi bhi pending hai.",
		"root cause as a phrase":        "Ismein root cause dekha gaya hai.",
		"action type as a phrase":       "Yeh action type automatically choose hua.",
		"record state as a phrase":      "Aapka record state update ho gaya.",
		"literal RootCauseBucket token": "status: ROOT_CAUSE_BUCKET_HARD_DECLINE, please retry.",
		"literal ActionType token":      "next step: ACTION_TYPE_NUDGE_REMINDER scheduled.",
		"literal RecordState token":     "current: RECORD_STATE_RETRY_SCHEDULED.",
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateNudge(&classifierv1.ComposeNudgeResponse{Message: msg}, 500); err == nil {
				t.Errorf("validateNudge(%q): want error (leaked internal vocabulary), got nil", msg)
			}
		})
	}
}

// TestValidateNudgeAcceptsLegitimateCustomerFacingWords guards the false
// positive side of the same rule. Several of these words are substrings of
// enum constant names (e.g. "retry" is the tail of ACTION_TYPE_RETRY,
// "risk" is inside ROOT_CAUSE_BUCKET_RISK_HOLD) but are ordinary
// Hinglish/English words a real customer message legitimately uses; the
// matcher must not ban them just because they overlap with the taxonomy.
func TestValidateNudgeAcceptsLegitimateCustomerFacingWords(t *testing.T) {
	cases := []string{
		"Aapka payment fail ho gaya, please retry karein.",
		"Koi action lein, warna aapka card block ho sakta hai.",
		"Aapka account mein insufficient funds hain.",
		"Yeh ek naya (new) payment link hai.",
		"Risk mat lijiye, abhi update karein.",
		"Aapka " + AmountPlaceholder + " ka payment fail ho gaya kyunki aapka overdue hai.",
	}
	for _, msg := range cases {
		t.Run(msg, func(t *testing.T) {
			if err := validateNudge(&classifierv1.ComposeNudgeResponse{Message: msg}, 500); err != nil {
				t.Errorf("validateNudge(%q): want no error, got %v", msg, err)
			}
		})
	}
}
