package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func newHandlerWithAudit(a *fakeAudit) http.Handler {
	return New(&fakeIngestion{}, &fakeReporting{}, a, testAPIKey, 2*time.Second, 0, 0).Routes()
}

func TestGetRecordAuditRendersFullTrail(t *testing.T) {
	a := &fakeAudit{recordAuditResp: &auditv1.GetRecordAuditResponse{
		Record: &commonv1.Record{
			Id:            "rec-1",
			BatchId:       "batch-1",
			Type:          commonv1.RecordType_RECORD_TYPE_PAYMENT,
			AmountPaise:   50000,
			Currency:      "INR",
			FailureCode:   "bank_not_available",
			CreatedAt:     timestamppb.New(time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)),
			InstrumentRef: "card_ref_1",
		},
		CurrentState:  commonv1.RecordState_RECORD_STATE_NUDGED,
		TrailComplete: true,
		Entries: []*auditv1.AuditEntry{
			{
				Ts:            timestamppb.New(time.Date(2026, 8, 29, 14, 0, 5, 0, time.UTC)),
				FromState:     commonv1.RecordState_RECORD_STATE_NEW,
				ToState:       commonv1.RecordState_RECORD_STATE_SCORING,
				Reason:        "classified",
				Rationale:     "Transient bank-side timeout, no evidence of a hard decline; retry is likely to succeed on resubmission.",
				Source:        commonv1.Source_SOURCE_LLM,
				Actor:         "system",
				AttemptNumber: 0,
				CostPaise:     0,
				MessageText:   "",
				Hops:          []*commonv1.ProviderHop{{Provider: "groq", Result: "ok"}},
			},
		},
	}}

	req := httptest.NewRequest(http.MethodGet, "/v1/records/rec-1/audit", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithAudit(a).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if a.gotRecordAudit.GetRecordId() != "rec-1" {
		t.Errorf("proxied record_id = %q, want rec-1", a.gotRecordAudit.GetRecordId())
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	record := got["record"].(map[string]any)
	if record["id"] != "rec-1" || record["type"] != "RECORD_TYPE_PAYMENT" || record["amount_paise"] != float64(50000) {
		t.Errorf("record = %v", record)
	}
	if record["created_at"] != "2026-08-29T14:00:00Z" {
		t.Errorf("record.created_at = %v", record["created_at"])
	}
	if got["current_state"] != "RECORD_STATE_NUDGED" || got["trail_complete"] != true {
		t.Errorf("current_state/trail_complete = %v/%v", got["current_state"], got["trail_complete"])
	}
	entries := got["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want 1", entries)
	}
	entry := entries[0].(map[string]any)
	if entry["from_state"] != "RECORD_STATE_NEW" || entry["to_state"] != "RECORD_STATE_SCORING" {
		t.Errorf("entry from/to = %v/%v", entry["from_state"], entry["to_state"])
	}
	if entry["rationale"] == nil || entry["message_text"] != "" {
		t.Errorf("rationale/message_text must always be present, got rationale=%v message_text=%v", entry["rationale"], entry["message_text"])
	}
	hops := entry["hops"].([]any)
	if len(hops) != 1 || hops[0].(map[string]any)["provider"] != "groq" {
		t.Errorf("hops = %v", hops)
	}
}

func TestGetRecordAuditEmptyHopsRendersAsEmptyArrayNotNull(t *testing.T) {
	a := &fakeAudit{recordAuditResp: &auditv1.GetRecordAuditResponse{
		Record: &commonv1.Record{Id: "rec-1", CreatedAt: timestamppb.New(time.Now())},
		Entries: []*auditv1.AuditEntry{
			{Ts: timestamppb.New(time.Now()), FromState: commonv1.RecordState_RECORD_STATE_NEW, ToState: commonv1.RecordState_RECORD_STATE_SCORING},
		},
	}}
	req := httptest.NewRequest(http.MethodGet, "/v1/records/rec-1/audit", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithAudit(a).ServeHTTP(rr, req)

	if !contains(rr.Body.String(), `"hops":[]`) {
		t.Errorf("body = %s, want hops rendered as [] not null", rr.Body.String())
	}
}

func TestGetRecordAuditUnknownRecordIsNotFound(t *testing.T) {
	a := &fakeAudit{recordAuditErr: notFoundErr("record not found")}
	req := httptest.NewRequest(http.MethodGet, "/v1/records/unknown/audit", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithAudit(a).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestVerifyInvariantsForBatchProxiesBatchID(t *testing.T) {
	a := &fakeAudit{invariantsResp: &auditv1.VerifyInvariantsResponse{
		RecordsChecked: 100,
		Examples:       map[string]string{},
	}}
	req := httptest.NewRequest(http.MethodGet, "/v1/batches/batch-1/invariants", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithAudit(a).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if a.gotInvariants.GetBatchId() != "batch-1" {
		t.Errorf("proxied batch_id = %q, want batch-1", a.gotInvariants.GetBatchId())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["records_checked"] != float64(100) {
		t.Errorf("records_checked = %v, want 100", got["records_checked"])
	}
	if _, ok := got["examples"].(map[string]any); !ok {
		t.Errorf("examples = %v, want an object (even if empty), not null", got["examples"])
	}
}

func TestVerifyInvariantsSystemWideProxiesEmptyBatchID(t *testing.T) {
	a := &fakeAudit{invariantsResp: &auditv1.VerifyInvariantsResponse{RecordsChecked: 500}}
	req := httptest.NewRequest(http.MethodGet, "/v1/invariants", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithAudit(a).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if a.gotInvariants.GetBatchId() != "" {
		t.Errorf("proxied batch_id = %q, want empty (system-wide)", a.gotInvariants.GetBatchId())
	}
}

func TestVerifyInvariantsNonZeroCountsSurfaced(t *testing.T) {
	a := &fakeAudit{invariantsResp: &auditv1.VerifyInvariantsResponse{
		StoppingRuleViolations: 1,
		ImpossibleTransitions:  2,
		RecordsChecked:         10,
		Examples:               map[string]string{"rec-9": "impossible transition NEW->RECOVERED"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/v1/invariants", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithAudit(a).ServeHTTP(rr, req)

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["stopping_rule_violations"] != float64(1) || got["impossible_transitions"] != float64(2) {
		t.Errorf("counts = %v", got)
	}
	examples := got["examples"].(map[string]any)
	if examples["rec-9"] != "impossible transition NEW->RECOVERED" {
		t.Errorf("examples = %v", examples)
	}
}
