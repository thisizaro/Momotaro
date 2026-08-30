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
