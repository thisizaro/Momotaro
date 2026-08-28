package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// maxRationaleChars caps the one freeform field a model controls.
//
// The rationale is stored verbatim in audit_entry.rationale and will be
// rendered on the Phase 5 dashboard, so it is the entire residual surface of
// the prompt-injection concern in docs/PHASE3_IMPLEMENTATION.md Flaw 10: the
// decision fields cannot be influenced because they are enum-checked twice.
// 500 characters is generous for the "one or two sentences" the prompt asks
// for and small enough that a runaway generation cannot bloat an audit row.
const maxRationaleChars = 500

// answer is the JSON the model is constrained to produce (schema.go).
type answer struct {
	Bucket            string  `json:"bucket"`
	RecommendedAction string  `json:"recommended_action"`
	Rationale         string  `json:"rationale"`
	Confidence        float64 `json:"confidence"`
}

// parseAnswer turns the model's JSON into a ClassifyResponse, rejecting
// anything outside the closed vocabularies.
//
// Every error here becomes a schema_invalid hop in the chain and the next rung
// runs, which is the behaviour SPEC.md section 4.7 designed the chain around.
// So this function should be strict: an answer this service cannot trust is a
// rung failure, not an answer, and falling through to a rung that cannot fail
// is strictly better than putting a guess in the audit trail.
func parseAnswer(raw string) (*classifierv1.ClassifyResponse, error) {
	var a answer
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("decode model answer: %w", err)
	}

	bucket, ok := commonv1.RootCauseBucket_value[a.Bucket]
	if !ok {
		return nil, fmt.Errorf("bucket %q is not a RootCauseBucket", a.Bucket)
	}
	action, ok := commonv1.ActionType_value[a.RecommendedAction]
	if !ok {
		return nil, fmt.Errorf("recommended_action %q is not an ActionType", a.RecommendedAction)
	}
	// Withheld from the schema (see actionNames), so a model naming one is
	// either ignoring the schema or the vendor is not enforcing it. Either way
	// it is not an answer: NONE would have the model closing a record on
	// economics grounds it was never given.
	if action == int32(commonv1.ActionType_ACTION_TYPE_NONE) ||
		action == int32(commonv1.ActionType_ACTION_TYPE_UNSPECIFIED) {
		return nil, fmt.Errorf("recommended_action %q is outside the menu offered to the model", a.RecommendedAction)
	}

	rationale := sanitizeRationale(a.Rationale)
	if rationale == "" {
		// A classification with no stated reason defeats the point of having
		// a model in the path at all: the audit trail is the deliverable.
		return nil, fmt.Errorf("rationale is empty")
	}

	return &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket(bucket),
		RecommendedAction: commonv1.ActionType(action),
		Rationale:         rationale,
		Confidence:        a.Confidence,
	}, nil
}

// sanitizeRationale makes model prose safe to store verbatim and cheap to
// render: control characters out (they corrupt a log line and a psql dump),
// whitespace collapsed to single spaces, length capped on a rune boundary so a
// multi-byte character is never cut in half.
func sanitizeRationale(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	cleaned = strings.Join(strings.Fields(cleaned), " ")

	runes := []rune(cleaned)
	if len(runes) > maxRationaleChars {
		return strings.TrimSpace(string(runes[:maxRationaleChars-1])) + "…"
	}
	return cleaned
}
