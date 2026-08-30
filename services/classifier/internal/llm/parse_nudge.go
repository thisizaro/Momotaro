package llm

import (
	"fmt"
	"strings"
	"unicode"
)

// parseNudgeAnswer cleans a model's raw nudge text: control characters out
// (a literal newline or tab in an SMS body is a rendering bug), whitespace
// collapsed, and one layer of surrounding quotes stripped, since the system
// prompt asks for none but models do not reliably comply.
//
// Deliberately does NOT truncate to a length cap the way sanitizeRationale
// does for Classify's rationale: a nudge that ran over max_chars is not a
// cosmetic overflow to trim, it is provider/validate_nudge.go's job to
// reject so the chain falls through to the next rung (ultimately the
// static template) rather than silently sending a message cut off
// mid-sentence.
func parseNudgeAnswer(raw string) (string, error) {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	cleaned = strings.Trim(cleaned, `"'`)

	if cleaned == "" {
		return "", fmt.Errorf("model returned an empty message after cleanup")
	}
	return cleaned, nil
}
