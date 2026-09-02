package httpapi

import (
	"context"
	"net/http"

	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// Wire structs for GET /v1/records/{record_id}/audit, mirroring
// audit.v1.GetRecordAuditResponse / AuditEntry field for field
// (docs/API_GATEWAY.md). rationale and message_text are always rendered,
// never omitted, even as "" -- so the frontend can read them
// unconditionally. hops is always an array, never null, on an entry that
// followed no classification.

type recordJSON struct {
	ID            string `json:"id"`
	BatchID       string `json:"batch_id"`
	Type          string `json:"type"`
	AmountPaise   int64  `json:"amount_paise"`
	Currency      string `json:"currency"`
	FailureCode   string `json:"failure_code"`
	CreatedAt     string `json:"created_at"`
	InstrumentRef string `json:"instrument_ref"`
}

type providerHopJSON struct {
	Provider string `json:"provider"`
	Result   string `json:"result"`
}

// decisionTraceCandidateJSON and decisionTraceJSON mirror
// audit.v1.DecisionTrace field for field (docs/API_GATEWAY.md, GET
// /v1/records/{record_id}/audit). candidates and blocked are each tagged
// omitempty: both are legitimately absent independently (an entry that
// scored everything and blocked nothing has candidates only, one where the
// guardrails refused everything before scoring has blocked only), the same
// "missing means no answer" rule the doc already uses for accuracy and
// baseline_comparison, not the zero-value-always-present rule that governs
// every other field on this response.
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

type auditEntryJSON struct {
	Ts            string            `json:"ts"`
	FromState     string            `json:"from_state"`
	ToState       string            `json:"to_state"`
	Reason        string            `json:"reason"`
	Rationale     string            `json:"rationale"`
	Source        string            `json:"source"`
	Actor         string            `json:"actor"`
	AttemptNumber int32             `json:"attempt_number"`
	CostPaise     int64             `json:"cost_paise"`
	MessageText   string            `json:"message_text"`
	Hops          []providerHopJSON `json:"hops"`
	// Pointer, tagged omitempty: present only on the entry that actually
	// compared candidate actions, absent (key omitted, not null) on every
	// other entry (docs/API_GATEWAY.md).
	DecisionTrace *decisionTraceJSON `json:"decision_trace,omitempty"`
}

func toDecisionTraceJSON(t *auditv1.DecisionTrace) *decisionTraceJSON {
	if t == nil {
		return nil
	}

	wire := &decisionTraceJSON{}
	if len(t.GetCandidates()) > 0 {
		wire.Candidates = make([]decisionTraceCandidateJSON, len(t.GetCandidates()))
		for i, c := range t.GetCandidates() {
			wire.Candidates[i] = decisionTraceCandidateJSON{
				Action:    c.GetAction().String(),
				EVPaise:   c.GetEvPaise(),
				PRecovery: c.GetPRecovery(),
				CostPaise: c.GetCostPaise(),
			}
		}
	}
	if len(t.GetBlocked()) > 0 {
		wire.Blocked = t.GetBlocked()
	}
	return wire
}

type getRecordAuditResponse struct {
	Record        recordJSON       `json:"record"`
	CurrentState  string           `json:"current_state"`
	TrailComplete bool             `json:"trail_complete"`
	Entries       []auditEntryJSON `json:"entries"`
}

func toRecordJSON(r *commonv1.Record) recordJSON {
	return recordJSON{
		ID:            r.GetId(),
		BatchID:       r.GetBatchId(),
		Type:          r.GetType().String(),
		AmountPaise:   r.GetAmountPaise(),
		Currency:      r.GetCurrency(),
		FailureCode:   r.GetFailureCode(),
		CreatedAt:     formatTimestamp(r.GetCreatedAt()),
		InstrumentRef: r.GetInstrumentRef(),
	}
}

func toAuditEntryJSON(e *auditv1.AuditEntry) auditEntryJSON {
	hops := make([]providerHopJSON, len(e.GetHops()))
	for i, h := range e.GetHops() {
		hops[i] = providerHopJSON{Provider: h.GetProvider(), Result: h.GetResult()}
	}
	return auditEntryJSON{
		Ts:            formatTimestamp(e.GetTs()),
		FromState:     e.GetFromState().String(),
		ToState:       e.GetToState().String(),
		Reason:        e.GetReason(),
		Rationale:     e.GetRationale(),
		Source:        e.GetSource().String(),
		Actor:         e.GetActor(),
		AttemptNumber: e.GetAttemptNumber(),
		CostPaise:     e.GetCostPaise(),
		MessageText:   e.GetMessageText(),
		Hops:          hops,
		DecisionTrace: toDecisionTraceJSON(e.GetDecisionTrace()),
	}
}

func (h *Handler) getRecordAudit(w http.ResponseWriter, r *http.Request) {
	recordID := r.PathValue("record_id")
	if recordID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "record_id is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.audit.GetRecordAudit(ctx, &auditv1.GetRecordAuditRequest{RecordId: recordID})
	if err != nil {
		writeGRPCError(w, err, "AUDIT_UNAVAILABLE")
		return
	}

	entries := make([]auditEntryJSON, len(resp.GetEntries()))
	for i, e := range resp.GetEntries() {
		entries[i] = toAuditEntryJSON(e)
	}
	writeJSON(w, http.StatusOK, getRecordAuditResponse{
		Record:        toRecordJSON(resp.GetRecord()),
		CurrentState:  resp.GetCurrentState().String(),
		TrailComplete: resp.GetTrailComplete(),
		Entries:       entries,
	})
}

// Wire struct for GET /v1/batches/{batch_id}/invariants and GET
// /v1/invariants, mirroring audit.v1.VerifyInvariantsResponse exactly
// (docs/API_GATEWAY.md). Every count must be zero; a non-zero count is a
// bug surfaced, not a business outcome.

type verifyInvariantsResponse struct {
	StoppingRuleViolations int64             `json:"stopping_rule_violations"`
	IncompleteAuditTrails  int64             `json:"incomplete_audit_trails"`
	ImpossibleTransitions  int64             `json:"impossible_transitions"`
	RecordsChecked         int64             `json:"records_checked"`
	Examples               map[string]string `json:"examples"`
}

func (h *Handler) verifyInvariants(w http.ResponseWriter, r *http.Request, batchID string) {
	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.audit.VerifyInvariants(ctx, &auditv1.VerifyInvariantsRequest{BatchId: batchID})
	if err != nil {
		writeGRPCError(w, err, "AUDIT_UNAVAILABLE")
		return
	}

	examples := resp.GetExamples()
	if examples == nil {
		examples = map[string]string{}
	}
	writeJSON(w, http.StatusOK, verifyInvariantsResponse{
		StoppingRuleViolations: resp.GetStoppingRuleViolations(),
		IncompleteAuditTrails:  resp.GetIncompleteAuditTrails(),
		ImpossibleTransitions:  resp.GetImpossibleTransitions(),
		RecordsChecked:         resp.GetRecordsChecked(),
		Examples:               examples,
	})
}

func (h *Handler) verifyInvariantsForBatch(w http.ResponseWriter, r *http.Request) {
	batchID := r.PathValue("batch_id")
	if batchID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "batch_id is required")
		return
	}
	h.verifyInvariants(w, r, batchID)
}

func (h *Handler) verifyInvariantsSystemWide(w http.ResponseWriter, r *http.Request) {
	h.verifyInvariants(w, r, "")
}
