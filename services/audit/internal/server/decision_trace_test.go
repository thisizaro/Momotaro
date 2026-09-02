package server

import (
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// decodeDecisionTrace is the mechanism loadAuditEntries relies on to turn
// the raw audit_entry.decision_trace JSONB column into the typed proto the
// rest of the system reads. These are ordinary unit tests, no Postgres
// needed, unlike most of this package's tests, because the parsing logic
// has no database dependency of its own.

func TestDecodeDecisionTraceReturnsNilForEmptyColumn(t *testing.T) {
	trace, err := decodeDecisionTrace("")
	if err != nil {
		t.Fatalf("decodeDecisionTrace(\"\"): %v", err)
	}
	if trace != nil {
		t.Errorf("decodeDecisionTrace(\"\") = %v, want nil (no priced decision on this entry)", trace)
	}
}

func TestDecodeDecisionTraceParsesCandidatesAndBlocked(t *testing.T) {
	raw := `{
		"candidates": [
			{"action": "ACTION_TYPE_RETRY", "ev_paise": -625, "cost_paise": 625, "p_recovery": 0},
			{"action": "ACTION_TYPE_NUDGE_METHOD_UPDATE", "ev_paise": -29, "cost_paise": 29, "p_recovery": 0},
			{"action": "ACTION_TYPE_NUDGE_REMINDER", "ev_paise": 870.76, "cost_paise": 35, "p_recovery": 0.12}
		],
		"blocked": {
			"ACTION_TYPE_NUDGE_REMINDER": "contact cooldown active: last contact 73.169551ms ago, cooldown is 288ms"
		}
	}`

	trace, err := decodeDecisionTrace(raw)
	if err != nil {
		t.Fatalf("decodeDecisionTrace: %v", err)
	}
	if trace == nil {
		t.Fatal("decodeDecisionTrace returned nil for a populated column")
	}

	if len(trace.Candidates) != 3 {
		t.Fatalf("len(Candidates) = %d, want 3", len(trace.Candidates))
	}
	first := trace.Candidates[0]
	if first.Action != commonv1.ActionType_ACTION_TYPE_RETRY {
		t.Errorf("Candidates[0].Action = %v, want ACTION_TYPE_RETRY", first.Action)
	}
	if first.EvPaise != -625 {
		t.Errorf("Candidates[0].EvPaise = %v, want -625", first.EvPaise)
	}
	if first.CostPaise != 625 {
		t.Errorf("Candidates[0].CostPaise = %d, want 625", first.CostPaise)
	}

	// The fractional EV is the whole reason ev_paise is a double rather than
	// paise-integer money: this must survive the round trip exactly, not
	// get truncated to 870.
	third := trace.Candidates[2]
	if third.Action != commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER {
		t.Errorf("Candidates[2].Action = %v, want ACTION_TYPE_NUDGE_REMINDER", third.Action)
	}
	if third.EvPaise != 870.76 {
		t.Errorf("Candidates[2].EvPaise = %v, want 870.76", third.EvPaise)
	}
	if third.PRecovery != 0.12 {
		t.Errorf("Candidates[2].PRecovery = %v, want 0.12", third.PRecovery)
	}

	if len(trace.Blocked) != 1 {
		t.Fatalf("len(Blocked) = %d, want 1", len(trace.Blocked))
	}
	reason, ok := trace.Blocked["ACTION_TYPE_NUDGE_REMINDER"]
	if !ok {
		t.Fatalf("Blocked missing ACTION_TYPE_NUDGE_REMINDER key: %v", trace.Blocked)
	}
	if reason != "contact cooldown active: last contact 73.169551ms ago, cooldown is 288ms" {
		t.Errorf("Blocked reason = %q, want the cooldown message verbatim", reason)
	}
}

// The two shapes actually seen on a live stack: an entry that scored
// candidates and blocked nothing, and an entry where the guardrails
// refused every action before anything was scored.

func TestDecodeDecisionTraceCandidatesOnly(t *testing.T) {
	trace, err := decodeDecisionTrace(`{"candidates":[{"action":"ACTION_TYPE_RETRY","ev_paise":340,"cost_paise":25,"p_recovery":0.5}]}`)
	if err != nil {
		t.Fatalf("decodeDecisionTrace: %v", err)
	}
	if len(trace.Candidates) != 1 {
		t.Fatalf("len(Candidates) = %d, want 1", len(trace.Candidates))
	}
	if len(trace.Blocked) != 0 {
		t.Errorf("Blocked = %v, want empty for a candidates-only trace", trace.Blocked)
	}
}

func TestDecodeDecisionTraceBlockedOnly(t *testing.T) {
	trace, err := decodeDecisionTrace(`{"blocked":{"ACTION_TYPE_RETRY":"retry budget exhausted: 3 of 3 attempts used"}}`)
	if err != nil {
		t.Fatalf("decodeDecisionTrace: %v", err)
	}
	if len(trace.Candidates) != 0 {
		t.Errorf("Candidates = %v, want empty when guardrails blocked everything before scoring", trace.Candidates)
	}
	if len(trace.Blocked) != 1 {
		t.Fatalf("len(Blocked) = %d, want 1", len(trace.Blocked))
	}
}

func TestDecodeDecisionTraceMalformedJSONErrors(t *testing.T) {
	_, err := decodeDecisionTrace(`{not valid json`)
	if err == nil {
		t.Fatal("decodeDecisionTrace with malformed JSON: got nil error, want one")
	}
}

// An action string the running proto enum does not recognise must not
// panic or silently vanish the candidate; it decodes to UNSPECIFIED like
// every other enum lookup in this codebase (commonv1.X_value[...] on a
// miss), which is a visible, debuggable value rather than a dropped row.
func TestDecodeDecisionTraceUnknownActionNameDecodesToUnspecified(t *testing.T) {
	trace, err := decodeDecisionTrace(`{"candidates":[{"action":"ACTION_TYPE_MADE_UP","ev_paise":1,"cost_paise":1,"p_recovery":1}]}`)
	if err != nil {
		t.Fatalf("decodeDecisionTrace: %v", err)
	}
	if len(trace.Candidates) != 1 {
		t.Fatalf("len(Candidates) = %d, want 1", len(trace.Candidates))
	}
	if trace.Candidates[0].Action != commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		t.Errorf("Action = %v, want ACTION_TYPE_UNSPECIFIED for an unrecognised name", trace.Candidates[0].Action)
	}
}
