package provider

import (
	"fmt"
	"strings"
	"unicode"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
)

// AmountPlaceholder is the literal token both the LLM prompt and the static
// Hinglish template use in place of the record's real amount
// (docs/ARCHITECTURE.md section 5b: "the record's real amount and due date
// are interpolated by us, not written by the model"). The caller (server.go)
// substitutes it with the formatted rupee amount after a rung's response has
// passed validateNudge, so a model that writes its own figure instead of
// this token is caught before that substitution ever runs.
const AmountPlaceholder = "{{AMOUNT}}"

// validateNudge checks a rung's response before it is allowed to answer, the
// nudge-composition equivalent of validate() in validate.go. A response this
// service cannot trust is a rung failure, not an answer, so it falls through
// to the next rung (ultimately the static template, which cannot fail)
// exactly the way an invalid Classify response does.
//
// maxChars bounds the RAW message (before amount substitution), not the
// final one: substituting AmountPlaceholder for a formatted rupee amount
// (e.g. "{{AMOUNT}}" -> "Rs 750") only ever shortens the string in this
// project (INR amounts are short; the placeholder token is deliberately
// longer than any amount it stands in for), so a raw message within budget
// guarantees the substituted one is too, provided the placeholder appears
// at most once (enforced below).
func validateNudge(resp *classifierv1.ComposeNudgeResponse, maxChars int32) error {
	if resp == nil {
		return fmt.Errorf("nil response")
	}
	msg := resp.GetMessage()
	if msg == "" {
		return fmt.Errorf("empty message")
	}
	if int32(len(msg)) > maxChars {
		return fmt.Errorf("message is %d characters, exceeds max_chars %d", len(msg), maxChars)
	}
	if occurrences := strings.Count(msg, AmountPlaceholder); occurrences > 1 {
		return fmt.Errorf("message contains %s %d times, want at most once", AmountPlaceholder, occurrences)
	}
	if stray := stripAll(msg, AmountPlaceholder); containsDigit(stray) {
		return fmt.Errorf("message contains a digit outside %s: a model-invented figure in a message about money is a serious failure, not a cosmetic one (docs/ARCHITECTURE.md section 5b)", AmountPlaceholder)
	}
	return nil
}

func stripAll(s, substr string) string {
	return strings.ReplaceAll(s, substr, "")
}

func containsDigit(s string) bool {
	for _, r := range s {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
