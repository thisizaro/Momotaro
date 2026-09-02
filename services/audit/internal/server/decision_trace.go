package server

import (
	"encoding/json"
	"fmt"

	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// decisionTraceCandidateJSON and decisionTraceJSON mirror the exact shape
// Decision Engine's encodeDecisionTrace persists into the audit_entry.
// decision_trace column (services/decision-engine/internal/engine/store.go):
// plain JSON in a JSONB column, field names matching the column's actual
// persisted shape. Kept as a private wire type here rather than reused
// across the module boundary, since Audit reads a column it does not own
// (docs/ARCHITECTURE.md section 10a) and must not import Decision Engine's
// internal package to do it.
type decisionTraceCandidateJSON struct {
	Action    string  `json:"action"`
	EVPaise   float64 `json:"ev_paise"`
	PRecovery float64 `json:"p_recovery"`
	CostPaise int64   `json:"cost_paise"`
}

type decisionTraceJSON struct {
	Candidates []decisionTraceCandidateJSON `json:"candidates,omitempty"`
	Blocked    map[string]string            `json:"blocked,omitempty"`
}

// decodeDecisionTrace turns the raw decision_trace column value into the
// typed proto message the rest of the system reads, or nil when the column
// was SQL NULL, i.e. this entry made no priced decision (loadAuditEntries
// passes sql.NullString.String, which is "" for NULL).
//
// Malformed JSON is returned as an error rather than silently dropped: this
// is the artifact "every money action explainable" depends on
// (docs/PRD.md section 0), unlike diagnostic-only data such as provider
// hops, so a decode failure here should surface, not disappear into a
// record that then looks like it made no decision at all.
func decodeDecisionTrace(raw string) (*auditv1.DecisionTrace, error) {
	if raw == "" {
		return nil, nil
	}

	var wire decisionTraceJSON
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return nil, fmt.Errorf("unmarshal decision_trace: %w", err)
	}

	trace := &auditv1.DecisionTrace{}
	if len(wire.Candidates) > 0 {
		trace.Candidates = make([]*auditv1.DecisionTrace_Candidate, len(wire.Candidates))
		for i, c := range wire.Candidates {
			trace.Candidates[i] = &auditv1.DecisionTrace_Candidate{
				Action:    commonv1.ActionType(commonv1.ActionType_value[c.Action]),
				EvPaise:   c.EVPaise,
				PRecovery: c.PRecovery,
				CostPaise: c.CostPaise,
			}
		}
	}
	if len(wire.Blocked) > 0 {
		trace.Blocked = wire.Blocked
	}
	return trace, nil
}
