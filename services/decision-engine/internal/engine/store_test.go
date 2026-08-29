package engine

import (
	"encoding/json"
	"log/slog"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/economics"
)

// encodeDecisionTrace is pure (docs/ENGINEERING.md section 14), so it is
// tested directly, without Postgres, same as economics.Model.Score.

// An empty trace (nothing scored, nothing blocked) must store SQL NULL, not
// "{}": a reader needs to be able to tell "no scoring happened here" apart
// from an empty JSON object.
func TestEncodeDecisionTraceReturnsNilForAnEmptyTrace(t *testing.T) {
	if got := encodeDecisionTrace(DecisionTrace{}, slog.Default()); got != nil {
		t.Errorf("encodeDecisionTrace(empty) = %q, want nil (SQL NULL)", *got)
	}
}

// The encoded JSON must round-trip every candidate's fields and every
// blocked action's reason, with the action serialized as its proto string
// name (so a psql session can read it without a Go lookup table).
func TestEncodeDecisionTraceRoundTrips(t *testing.T) {
	trace := DecisionTrace{
		Candidates: []economics.Score{
			{Action: commonv1.ActionType_ACTION_TYPE_RETRY, EVPaise: 340, PRecovery: 0.8, CostPaise: 25},
			{Action: commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, EVPaise: -15, PRecovery: 0.1, CostPaise: 35},
		},
		Blocked: map[commonv1.ActionType]string{
			commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE: "contact cap reached: 3 of 3 contacts used",
		},
	}

	got := encodeDecisionTrace(trace, slog.Default())
	if got == nil {
		t.Fatal("encodeDecisionTrace returned nil for a non-empty trace")
	}

	var decoded decisionTraceJSON
	if err := json.Unmarshal([]byte(*got), &decoded); err != nil {
		t.Fatalf("encoded trace is not valid JSON: %v\n%s", err, *got)
	}

	if len(decoded.Candidates) != 2 {
		t.Fatalf("decoded %d candidates, want 2", len(decoded.Candidates))
	}
	if decoded.Candidates[0].Action != "ACTION_TYPE_RETRY" {
		t.Errorf("candidates[0].Action = %q, want the full proto enum string \"ACTION_TYPE_RETRY\"", decoded.Candidates[0].Action)
	}
	if decoded.Candidates[0].EVPaise != 340 || decoded.Candidates[0].CostPaise != 25 {
		t.Errorf("candidates[0] = %+v, want EVPaise=340 CostPaise=25", decoded.Candidates[0])
	}
	if decoded.Candidates[1].Action != "ACTION_TYPE_NUDGE_REMINDER" {
		t.Errorf("candidates[1].Action = %q, want \"ACTION_TYPE_NUDGE_REMINDER\"", decoded.Candidates[1].Action)
	}

	reason, ok := decoded.Blocked["ACTION_TYPE_NUDGE_METHOD_UPDATE"]
	if !ok || reason != "contact cap reached: 3 of 3 contacts used" {
		t.Errorf("decoded.Blocked[ACTION_TYPE_NUDGE_METHOD_UPDATE] = %q, ok=%v, want the contact-cap reason", reason, ok)
	}
}

// A trace with candidates but nothing blocked must omit the blocked key
// rather than emit an empty object, and vice versa: this is what
// `,omitempty` is for, checked explicitly so a refactor cannot silently
// drop it.
func TestEncodeDecisionTraceOmitsEmptySides(t *testing.T) {
	onlyCandidates := DecisionTrace{Candidates: []economics.Score{{Action: commonv1.ActionType_ACTION_TYPE_RETRY, EVPaise: 100}}}
	got := encodeDecisionTrace(onlyCandidates, slog.Default())
	if got == nil {
		t.Fatal("encodeDecisionTrace returned nil for a trace with candidates")
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*got), &decoded); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, ok := decoded["blocked"]; ok {
		t.Errorf("encoded JSON %s has a \"blocked\" key with nothing blocked, want it omitted", *got)
	}

	onlyBlocked := DecisionTrace{Blocked: map[commonv1.ActionType]string{commonv1.ActionType_ACTION_TYPE_RETRY: "blocked"}}
	got = encodeDecisionTrace(onlyBlocked, slog.Default())
	if got == nil {
		t.Fatal("encodeDecisionTrace returned nil for a trace with a blocked action")
	}
	decoded = nil
	if err := json.Unmarshal([]byte(*got), &decoded); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if _, ok := decoded["candidates"]; ok {
		t.Errorf("encoded JSON %s has a \"candidates\" key with nothing scored, want it omitted", *got)
	}
}
