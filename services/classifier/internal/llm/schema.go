// Package llm implements the classifier's real model rungs (Phase 3 Unit B,
// docs/PHASE3_IMPLEMENTATION.md). One package, two vendors, because they
// differ only in wire shape: the prompt, the output schema, the parsing and
// the sanitising are all shared, and only groq.go and gemini.go know what a
// request looks like.
//
// Nothing here reads a database, a clock, or GROUND_TRUTH. The classifier
// stays stateless (SPEC.md section 8) and the integrity rule (ARCHITECTURE.md
// section 5a) is free to honour because there is no pool to query with.
package llm

import (
	"sort"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// dialect is the JSON-Schema flavour a vendor accepts. The two differ in one
// keyword and it matters: Groq's strict mode *requires* additionalProperties
// on every object and rejects a schema without it, while Gemini supports only
// a subset of JSON Schema and rejects the keyword outright. One schema with
// both would fail on one vendor or the other.
type dialect int

const (
	dialectStrictJSONSchema dialect = iota // Groq: constrained decoding
	dialectGemini                          // Gemini: JSON Schema subset
)

// bucketNames and actionNames are derived from the proto enums rather than
// written out, so a bucket added to common.proto cannot silently fail to reach
// the model. Deterministic order: enum number, which is also declaration order.
func bucketNames() []string {
	return enumNames(commonv1.RootCauseBucket_name, nil)
}

// actionNames deliberately withholds two of the six ActionType values.
//
// ACTION_TYPE_NONE means "deliberately do nothing", which is an *economics*
// conclusion, not a diagnosis. It belongs to the Decision Engine's scorer
// closing a record as ClosedUneconomic (ARCHITECTURE.md section 5a), and a
// model that could pick it would be pricing an action, which is exactly the
// trust inversion PRD.md section 2a forbids.
//
// ACTION_TYPE_UNSPECIFIED is withheld because "I am not sure" already has an
// honest answer in this menu: ESCALATE, hand it to a human. The rules engine
// does the same thing on an unrecognised failure code (rules/actions.go), so
// the two rungs cannot disagree about what uncertainty looks like.
//
// The bucket menu, by contrast, DOES include UNSPECIFIED: "we cannot tell what
// went wrong" is a real diagnosis, and it is what the rules engine emits for an
// unknown code.
func actionNames() []string {
	return enumNames(commonv1.ActionType_name, map[string]bool{
		commonv1.ActionType_ACTION_TYPE_NONE.String():        true,
		commonv1.ActionType_ACTION_TYPE_UNSPECIFIED.String(): true,
	})
}

func enumNames(byNumber map[int32]string, exclude map[string]bool) []string {
	numbers := make([]int, 0, len(byNumber))
	for n := range byNumber {
		numbers = append(numbers, int(n))
	}
	sort.Ints(numbers)

	out := make([]string, 0, len(numbers))
	for _, n := range numbers {
		name := byNumber[int32(n)]
		if exclude[name] {
			continue
		}
		out = append(out, name)
	}
	return out
}

// outputSchema is the contract handed to the model: it may name a bucket and
// an action, and nothing else.
//
// This is the FIRST of two gates. Groq's strict mode makes it a hard guarantee
// (the model is constrained at the token level and cannot emit an
// out-of-vocabulary value); Gemini's is best effort and Google says so. Either
// way provider/validate.go checks the result again on the way out, because
// that gate is the one this repo owns. Do not remove the second gate on the
// strength of the first.
//
// Deliberately no minimum/maximum on confidence. Both are JSON Schema
// keywords, both are in the "may or may not be supported" tail of each
// vendor's subset, and a schema rejected at request time is worse than a
// confidence checked at parse time, which validate.go already does.
func outputSchema(d dialect) map[string]any {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bucket": map[string]any{
				"type":        "string",
				"enum":        bucketNames(),
				"description": "The single root cause that best explains this failure.",
			},
			"recommended_action": map[string]any{
				"type":        "string",
				"enum":        actionNames(),
				"description": "One action from the closed menu. A recommendation, not a decision.",
			},
			"rationale": map[string]any{
				"type":        "string",
				"description": "One or two plain sentences naming the observed signal. No invented amounts, dates or figures.",
			},
			"confidence": map[string]any{
				"type":        "number",
				"description": "0.0 to 1.0. Honest uncertainty, not a sales pitch.",
			},
		},
		"required": []string{"bucket", "recommended_action", "rationale", "confidence"},
	}
	if d == dialectStrictJSONSchema {
		// Mandatory under Groq strict mode: without it the API rejects the
		// schema rather than silently downgrading to best effort.
		schema["additionalProperties"] = false
	}
	return schema
}
