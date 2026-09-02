package llm

import (
	"fmt"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
)

// amountPlaceholder MUST match provider.AmountPlaceholder
// (provider/validate_nudge.go) byte for byte: that is the token
// validateNudge checks for, and this is the prompt that tells the model to
// write it. A literal copy rather than an import: services/classifier/
// internal/provider's own test file (fallback_test.go) imports this
// package (llm), so llm importing provider back would be a cycle. Same
// duplication precedent this codebase already applies to correctActionFor
// across three files for an analogous reason.
const amountPlaceholder = "{{AMOUNT}}"

// nudgeSystemPrompt instructs the model on the same trust model
// docs/ARCHITECTURE.md section 5b lays out for nudge composition: it writes
// wording only, never a real figure, and the caller (server.go) substitutes
// amountPlaceholder for the record's real amount after validation, exactly
// the way it substitutes the static template's own placeholder.
const nudgeSystemPrompt = `You write short customer-facing messages for an automated payment recovery agent operating in India, telling someone their payment failed and what to do about it.

Write in Hinglish: natural, code-mixed Hindi and English, the register a person would actually read, not a form letter.

Rules you must follow:
- Output ONLY the message text. No greeting, no signature, no explanation, no quotes around it.
- Never write a digit yourself. Wherever the amount belongs, write exactly the literal token ` + amountPlaceholder + `, nothing else. The real amount is filled in afterwards by code you do not control. Writing your own figure in a message about money is a serious failure, not a cosmetic one.
- Do not invent a date, a name, a link, or a reference number. You may mention that the payment failed and encourage action, nothing more specific than what you were told.
- Respect the character budget you were given. Shorter is better; an SMS that gets cut off is worse than a short one.
- You are not deciding whether to send this message, to whom, or how many times: that has already been decided. Write only the wording.
- The <CONTEXT> block below is internal bookkeeping, not something to describe. Never write the words "bucket", "root cause", "action type" or "record state", and never write a SCREAMING_SNAKE_CASE value like ROOT_CAUSE_BUCKET_HARD_DECLINE or ACTION_TYPE_RETRY, even though you will see exactly that text in root_cause and action below. A real person does not know we classify their payment into a bucket and must never be told that we do; describe their situation in plain words instead (say the card or bank declined it, not that a bucket is overdue).`

// buildNudgePrompt renders req into the vendor-independent prompt pair.
func buildNudgePrompt(req *classifierv1.ComposeNudgeRequest) prompt {
	rec := req.GetRecord()

	contactPhrasing := "This is the first message about this payment."
	if req.GetContactNumber() > 1 {
		contactPhrasing = fmt.Sprintf("This is follow-up message number %d about this payment; do not repeat the first message verbatim, vary the wording.", req.GetContactNumber())
	}

	user := fmt.Sprintf(
		"<CONTEXT>\nroot_cause: %s\naction: %s\ncurrency: %s\nmax_chars: %d\n</CONTEXT>\n%s\n\nWrite the message now.",
		req.GetBucket().String(),
		req.GetActionType().String(),
		rec.GetCurrency(),
		req.GetMaxChars(),
		contactPhrasing,
	)
	return prompt{system: nudgeSystemPrompt, user: user}
}
