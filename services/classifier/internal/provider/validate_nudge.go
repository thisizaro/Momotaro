package provider

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
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
	if leaked := forbiddenVocabularyPattern.FindString(msg); leaked != "" {
		return fmt.Errorf("message contains internal vocabulary %q: a customer-facing message must not name our classification scheme, even though the prompt already asks it not to (docs/DECISIONS.md 2026-08-28: a prompt instruction is not a guarantee, this validator is)", leaked)
	}
	return nil
}

// forbiddenVocabularyPattern is what actually stops an internal enum name
// reaching a customer, not the prompt (docs/DEMO_READINESS.md "Unit AE": 2
// of 2 LLM-composed nudges in a measured batch leaked the word "bucket",
// e.g. "kyunki aapka bucket overdue hai"). It is a single case-insensitive,
// word-bounded regex built from two tiers:
//
//  1. Every literal enum constant name for RootCauseBucket, ActionType and
//     RecordState (e.g. "ROOT_CAUSE_BUCKET_HARD_DECLINE"), read directly off
//     the generated proto maps (commonv1.RootCauseBucket_name and friends)
//     rather than copied by hand, so a new enum value added to the .proto is
//     covered the moment protoc-gen-go regenerates this map, with nothing
//     for a future agent to remember to update here. This is not a
//     theoretical risk: nudge_prompt.go interpolates req.GetBucket().String()
//     and friends into the model's own <CONTEXT> block, so the model reads
//     these exact tokens before writing its answer, and an occasional
//     verbatim echo is a real failure mode.
//
//  2. enumMetaWords below: a short, hand-written list of the words that name
//     the classification scheme itself, independent of which value was
//     picked. "bucket" is the word that actually leaked; "root cause",
//     "action type" and "record state" are the three enum families' own
//     humanized type names. This tier cannot be derived mechanically without
//     over-triggering: splitting every constant on "_" also yields "retry",
//     "action", "risk", "new", "user" and "hold" as standalone words, and
//     every one of those is ordinary Hinglish/English vocabulary that
//     belongs in a customer message (TestValidateNudgeAcceptsLegitimateCustomerFacingWords
//     below, and this file's own validNudgeResponse fixture, use "retry" for
//     exactly that reason). Keeping this list here, immediately below the
//     tier-1 loop, is the deliberate substitute: it is the one place a
//     reviewer adding a fourth enum family would see and have to decide
//     whether that family's name needs an entry too.
var forbiddenVocabularyPattern = buildForbiddenVocabularyPattern()

// enumMetaWords is tier 2 of forbiddenVocabularyPattern; see its comment.
var enumMetaWords = []string{
	"bucket",
	"root cause",
	"action type",
	"record state",
}

func buildForbiddenVocabularyPattern() *regexp.Regexp {
	seen := make(map[string]bool)
	var terms []string
	add := func(term string) {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" || seen[term] {
			return
		}
		seen[term] = true
		terms = append(terms, regexp.QuoteMeta(term))
	}

	for _, name := range commonv1.RootCauseBucket_name {
		add(name)
	}
	for _, name := range commonv1.ActionType_name {
		add(name)
	}
	for _, name := range commonv1.RecordState_name {
		add(name)
	}
	for _, word := range enumMetaWords {
		add(word)
	}

	// Longest first: under RE2's leftmost-alternative matching, a shorter
	// term earlier in the alternation would otherwise win over a longer one
	// that starts the same way. None of our terms currently overlap that
	// way, but the sort makes the pattern correct regardless.
	sort.Slice(terms, func(i, j int) bool { return len(terms[i]) > len(terms[j]) })

	return regexp.MustCompile(`(?i)\b(` + strings.Join(terms, "|") + `)\b`)
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
