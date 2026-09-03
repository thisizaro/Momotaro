package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeReporting implements reportingv1.ReportingServiceClient. Streaming is
// a separate fake (live_test.go), since it needs to hand back a stream
// object rather than a plain response/error pair.
type fakeReporting struct {
	reportResp *reportingv1.GetBatchReportResponse
	reportErr  error
	gotReport  *reportingv1.GetBatchReportRequest

	recordsResp *reportingv1.ListBatchRecordsResponse
	recordsErr  error
	gotRecords  *reportingv1.ListBatchRecordsRequest

	streamResp grpc.ServerStreamingClient[reportingv1.StreamBatchUpdatesResponse]
	streamErr  error
	gotStream  *reportingv1.StreamBatchUpdatesRequest
}

func (f *fakeReporting) GetBatchReport(ctx context.Context, in *reportingv1.GetBatchReportRequest, opts ...grpc.CallOption) (*reportingv1.GetBatchReportResponse, error) {
	f.gotReport = in
	return f.reportResp, f.reportErr
}

func (f *fakeReporting) ListBatchRecords(ctx context.Context, in *reportingv1.ListBatchRecordsRequest, opts ...grpc.CallOption) (*reportingv1.ListBatchRecordsResponse, error) {
	f.gotRecords = in
	return f.recordsResp, f.recordsErr
}

func (f *fakeReporting) StreamBatchUpdates(ctx context.Context, in *reportingv1.StreamBatchUpdatesRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[reportingv1.StreamBatchUpdatesResponse], error) {
	f.gotStream = in
	return f.streamResp, f.streamErr
}

func newHandlerWithReporting(rep *fakeReporting) http.Handler {
	return New(&fakeIngestion{}, rep, &fakeAudit{}, nil, testAPIKey, 2*time.Second, 0, 0).Routes()
}

func TestGetBatchReportProxiesAndTranslatesFieldForField(t *testing.T) {
	genAt := time.Date(2026, 8, 30, 14, 5, 0, 0, time.UTC)
	rep := &fakeReporting{reportResp: &reportingv1.GetBatchReportResponse{
		Report: &reportingv1.BatchReport{
			BatchId:                "batch-1",
			TotalRecords:           100,
			InFlightCount:          12,
			AtRiskPaise:            5000000,
			RecoveredPaise:         3410000,
			InterventionSpendPaise: 84500,
			NetRecoveredPaise:      3325500,
			CostPerRupeeRecovered:  0.0248,
			RecoveryRate:           0.68,
			EscalatedCount:         9,
			ClosedUneconomicCount:  4,
			ClosedUneconomicPaise:  210000,
			ProcessingFailureCount: 0,
			ByRootCause: map[string]*reportingv1.BucketStats{
				"ROOT_CAUSE_BUCKET_TRANSIENT_BANK": {RecordCount: 40, AtRiskPaise: 2000000, RecoveredPaise: 1800000, RecoveryRate: 0.9},
			},
			ByIntervention: map[string]*reportingv1.InterventionStats{
				"ACTION_TYPE_RETRY": {AttemptCount: 120, SuccessCount: 95, SpendPaise: 3000, RecoveredPaise: 3000000, SuccessRate: 0.79},
			},
			GeneratedAt: timestamppb.New(genAt),
		},
	}}

	req := httptest.NewRequest(http.MethodGet, "/v1/batches/batch-1/report", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithReporting(rep).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if got["batch_id"] != "batch-1" {
		t.Errorf("batch_id = %v, want batch-1", got["batch_id"])
	}
	if got["at_risk_paise"] != float64(5000000) {
		t.Errorf("at_risk_paise = %v, want 5000000", got["at_risk_paise"])
	}
	if got["net_recovered_paise"] != float64(3325500) {
		t.Errorf("net_recovered_paise = %v, want 3325500", got["net_recovered_paise"])
	}
	if got["generated_at"] != "2026-08-30T14:05:00Z" {
		t.Errorf("generated_at = %v, want 2026-08-30T14:05:00Z", got["generated_at"])
	}
	byRootCause, ok := got["by_root_cause"].(map[string]any)
	if !ok {
		t.Fatalf("by_root_cause is not an object: %v", got["by_root_cause"])
	}
	bucket, ok := byRootCause["ROOT_CAUSE_BUCKET_TRANSIENT_BANK"].(map[string]any)
	if !ok {
		t.Fatalf("by_root_cause missing ROOT_CAUSE_BUCKET_TRANSIENT_BANK key: %v", byRootCause)
	}
	if bucket["record_count"] != float64(40) {
		t.Errorf("by_root_cause[...].record_count = %v, want 40", bucket["record_count"])
	}
	// accuracy/baseline_comparison were never set on the fake response
	// (no ground truth): both keys must be absent entirely, not null.
	if _, present := got["accuracy"]; present {
		t.Error("accuracy key present with no ground truth, want the key absent entirely")
	}
	if _, present := got["baseline_comparison"]; present {
		t.Error("baseline_comparison key present with no ground truth, want the key absent entirely")
	}
}

// TestGetBatchReportIncludesLLMQuotaExhaustedCount proves
// llm_quota_exhausted_count (docs/API_GATEWAY.md, Unit AI) is proxied
// through field for field like every other always-present count on this
// response, not dropped and not folded into the accuracy/baseline_comparison
// "missing key" convention: it has no GROUND_TRUTH dependency.
func TestGetBatchReportIncludesLLMQuotaExhaustedCount(t *testing.T) {
	rep := &fakeReporting{reportResp: &reportingv1.GetBatchReportResponse{
		Report: &reportingv1.BatchReport{
			BatchId:                "batch-1",
			LlmQuotaExhaustedCount: 12,
		},
	}}

	req := httptest.NewRequest(http.MethodGet, "/v1/batches/batch-1/report", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithReporting(rep).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["llm_quota_exhausted_count"] != float64(12) {
		t.Errorf("llm_quota_exhausted_count = %v, want 12", got["llm_quota_exhausted_count"])
	}
}

// TestGetBatchReportIncludesAccuracyAndBaselineComparisonWhenPresent is the
// other half of the absent-vs-present contract: when Reporting DOES return
// them, the Gateway must not drop them, and must not require a zero
// sub-field to trigger absence.
func TestGetBatchReportIncludesAccuracyAndBaselineComparisonWhenPresent(t *testing.T) {
	rep := &fakeReporting{reportResp: &reportingv1.GetBatchReportResponse{
		Report: &reportingv1.BatchReport{
			BatchId: "batch-1",
			Accuracy: &reportingv1.ClassificationAccuracy{
				ScoredRecords:   10,
				OverallAccuracy: 0.9,
				ByBucket:        map[string]float64{"ROOT_CAUSE_BUCKET_TRANSIENT_BANK": 0.92},
				Confusion: map[string]*reportingv1.BucketConfusion{
					"ROOT_CAUSE_BUCKET_TRANSIENT_BANK": {TrueBucketCounts: map[string]int32{"ROOT_CAUSE_BUCKET_TRANSIENT_BANK": 9}},
				},
			},
			BaselineComparison: &reportingv1.BaselineComparison{
				PolicyName:             "naive_retry3_nudge1",
				GrossRecoveredPaise:    100,
				InterventionSpendPaise: 10,
				NetRecoveredPaise:      90,
				Note:                   "modelled world caveat",
			},
			GeneratedAt: timestamppb.Now(),
		},
	}}

	req := httptest.NewRequest(http.MethodGet, "/v1/batches/batch-1/report", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithReporting(rep).ServeHTTP(rr, req)

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	accuracy, ok := got["accuracy"].(map[string]any)
	if !ok {
		t.Fatalf("accuracy missing or wrong shape: %v", got["accuracy"])
	}
	if accuracy["scored_records"] != float64(10) {
		t.Errorf("accuracy.scored_records = %v, want 10", accuracy["scored_records"])
	}
	baseline, ok := got["baseline_comparison"].(map[string]any)
	if !ok {
		t.Fatalf("baseline_comparison missing or wrong shape: %v", got["baseline_comparison"])
	}
	if baseline["policy_name"] != "naive_retry3_nudge1" {
		t.Errorf("baseline_comparison.policy_name = %v, want naive_retry3_nudge1", baseline["policy_name"])
	}
	if baseline["net_recovered_paise"] != float64(90) {
		t.Errorf("baseline_comparison.net_recovered_paise = %v, want 90", baseline["net_recovered_paise"])
	}
}

func TestGetBatchReportRendersEmptyMapsAsObjectsNotNull(t *testing.T) {
	rep := &fakeReporting{reportResp: &reportingv1.GetBatchReportResponse{
		Report: &reportingv1.BatchReport{BatchId: "batch-1", GeneratedAt: timestamppb.Now()},
	}}

	req := httptest.NewRequest(http.MethodGet, "/v1/batches/batch-1/report", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithReporting(rep).ServeHTTP(rr, req)

	// A nil Go map marshals to JSON null; API_GATEWAY.md's wire convention
	// (every documented field always emitted, zero value included) means
	// an empty by_root_cause/by_intervention must render as {}, not null,
	// since a frontend iterating Object.entries(by_root_cause) would throw
	// on null but handles {} fine.
	if !contains(rr.Body.String(), `"by_root_cause":{}`) {
		t.Errorf("body does not contain by_root_cause:{}: %s", rr.Body.String())
	}
	if !contains(rr.Body.String(), `"by_intervention":{}`) {
		t.Errorf("body does not contain by_intervention:{}: %s", rr.Body.String())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

func TestGetBatchReportMissingBatchIDIsBadRequest(t *testing.T) {
	rep := &fakeReporting{}
	// No {batch_id} path segment at all: the mux itself has nothing to
	// match, so this never even reaches getBatchReport's own empty-string
	// guard. A double-slash variant would instead hit net/http.ServeMux's
	// own path-cleaning redirect (307) before either check runs, so it's
	// deliberately not used here.
	req := httptest.NewRequest(http.MethodGet, "/v1/batches/report", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithReporting(rep).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound && rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 or 404 for a missing batch_id path segment", rr.Code)
	}
}

func TestListBatchRecordsProxiesQueryParamsAndResponse(t *testing.T) {
	rep := &fakeReporting{recordsResp: &reportingv1.ListBatchRecordsResponse{
		Records: []*reportingv1.RecordSummary{
			{
				RecordId:     "rec-1",
				Type:         commonv1.RecordType_RECORD_TYPE_PAYMENT,
				AmountPaise:  50000,
				CurrentState: commonv1.RecordState_RECORD_STATE_NUDGED,
				Bucket:       commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
				AttemptCount: 2,
				SpendPaise:   50,
			},
		},
		NextPageToken: "20",
		TotalCount:    100,
	}}

	req := httptest.NewRequest(http.MethodGet, "/v1/batches/batch-1/records?page_size=10&page_token=abc&state=RECORD_STATE_NUDGED&bucket=ROOT_CAUSE_BUCKET_TRANSIENT_BANK", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithReporting(rep).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if rep.gotRecords.GetPageSize() != 10 {
		t.Errorf("proxied PageSize = %d, want 10", rep.gotRecords.GetPageSize())
	}
	if rep.gotRecords.GetPageToken() != "abc" {
		t.Errorf("proxied PageToken = %q, want abc", rep.gotRecords.GetPageToken())
	}
	if rep.gotRecords.GetStateFilter() != commonv1.RecordState_RECORD_STATE_NUDGED {
		t.Errorf("proxied StateFilter = %v, want RECORD_STATE_NUDGED", rep.gotRecords.GetStateFilter())
	}
	if rep.gotRecords.GetBucketFilter() != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK {
		t.Errorf("proxied BucketFilter = %v, want ROOT_CAUSE_BUCKET_TRANSIENT_BANK", rep.gotRecords.GetBucketFilter())
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["next_page_token"] != "20" {
		t.Errorf("next_page_token = %v, want 20", got["next_page_token"])
	}
	if got["total_count"] != float64(100) {
		t.Errorf("total_count = %v, want 100", got["total_count"])
	}
	records, ok := got["records"].([]any)
	if !ok || len(records) != 1 {
		t.Fatalf("records = %v, want a one-element array", got["records"])
	}
	rec := records[0].(map[string]any)
	if rec["current_state"] != "RECORD_STATE_NUDGED" {
		t.Errorf("records[0].current_state = %v, want RECORD_STATE_NUDGED", rec["current_state"])
	}
	if rec["type"] != "RECORD_TYPE_PAYMENT" {
		t.Errorf("records[0].type = %v, want RECORD_TYPE_PAYMENT", rec["type"])
	}
}

// TestListBatchRecordsRendersDueAtOrEmptyStringWhenAbsent covers Unit AA.
// A record the Decision Engine's scheduler is waiting on carries its
// due_at as an RFC3339 string (Wire conventions 4); a record with none
// (terminal, or NUDGED waiting on the customer) still gets the field, as
// an empty string rather than being omitted, per Wire conventions 6 ("no
// omitempty, every documented field always rendered") and matching the
// existing rationale/message_text precedent for "empty string, never
// absent, when not applicable".
func TestListBatchRecordsRendersDueAtOrEmptyStringWhenAbsent(t *testing.T) {
	dueAt := time.Date(2026, 8, 29, 14, 30, 0, 0, time.UTC)
	rep := &fakeReporting{recordsResp: &reportingv1.ListBatchRecordsResponse{
		Records: []*reportingv1.RecordSummary{
			{
				RecordId:     "rec-scheduled",
				CurrentState: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED,
				DueAt:        timestamppb.New(dueAt),
			},
			{
				RecordId:     "rec-nudged",
				CurrentState: commonv1.RecordState_RECORD_STATE_NUDGED,
				DueAt:        nil,
			},
		},
	}}

	req := httptest.NewRequest(http.MethodGet, "/v1/batches/batch-1/records", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithReporting(rep).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	records := got["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}

	scheduled := records[0].(map[string]any)
	dueAtRaw, present := scheduled["due_at"]
	if !present {
		t.Fatal("scheduled record: due_at key missing, want the field always present")
	}
	if dueAtRaw != "2026-08-29T14:30:00Z" {
		t.Errorf("scheduled record: due_at = %v, want 2026-08-29T14:30:00Z", dueAtRaw)
	}

	nudged := records[1].(map[string]any)
	nudgedDueAt, present := nudged["due_at"]
	if !present {
		t.Fatal("nudged record: due_at key missing, want the field always present, empty string")
	}
	if nudgedDueAt != "" {
		t.Errorf("nudged record: due_at = %v, want empty string for an absent due_at", nudgedDueAt)
	}
}

// TestListBatchRecordsRendersFirstAndLastActionAtOrEmptyStringWhenAbsent
// covers Unit AH: the historical timeline needs real timing on
// RecordSummary, distinct from due_at's future scheduling. A record the
// Decision Engine has acted on carries both as RFC3339 strings; a brand
// new record with neither yet still gets both keys, as empty strings
// rather than omitted, the same convention TestListBatchRecordsRendersDueAt
// OrEmptyStringWhenAbsent already established for due_at.
func TestListBatchRecordsRendersFirstAndLastActionAtOrEmptyStringWhenAbsent(t *testing.T) {
	firstAt := time.Date(2026, 8, 29, 14, 0, 5, 0, time.UTC)
	lastAt := time.Date(2026, 8, 29, 14, 3, 11, 0, time.UTC)
	rep := &fakeReporting{recordsResp: &reportingv1.ListBatchRecordsResponse{
		Records: []*reportingv1.RecordSummary{
			{
				RecordId:      "rec-acted",
				CurrentState:  commonv1.RecordState_RECORD_STATE_RETRYING,
				FirstActionAt: timestamppb.New(firstAt),
				LastActionAt:  timestamppb.New(lastAt),
			},
			{
				RecordId:      "rec-new",
				CurrentState:  commonv1.RecordState_RECORD_STATE_NEW,
				FirstActionAt: nil,
				LastActionAt:  nil,
			},
		},
	}}

	req := httptest.NewRequest(http.MethodGet, "/v1/batches/batch-1/records", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithReporting(rep).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	records := got["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}

	acted := records[0].(map[string]any)
	firstRaw, present := acted["first_action_at"]
	if !present {
		t.Fatal("acted record: first_action_at key missing, want the field always present")
	}
	if firstRaw != "2026-08-29T14:00:05Z" {
		t.Errorf("acted record: first_action_at = %v, want 2026-08-29T14:00:05Z", firstRaw)
	}
	lastRaw, present := acted["last_action_at"]
	if !present {
		t.Fatal("acted record: last_action_at key missing, want the field always present")
	}
	if lastRaw != "2026-08-29T14:03:11Z" {
		t.Errorf("acted record: last_action_at = %v, want 2026-08-29T14:03:11Z", lastRaw)
	}

	fresh := records[1].(map[string]any)
	freshFirst, present := fresh["first_action_at"]
	if !present {
		t.Fatal("new record: first_action_at key missing, want the field always present, empty string")
	}
	if freshFirst != "" {
		t.Errorf("new record: first_action_at = %v, want empty string for an absent value", freshFirst)
	}
	freshLast, present := fresh["last_action_at"]
	if !present {
		t.Fatal("new record: last_action_at key missing, want the field always present, empty string")
	}
	if freshLast != "" {
		t.Errorf("new record: last_action_at = %v, want empty string for an absent value", freshLast)
	}
}

func TestListBatchRecordsUnknownBatchIsNotFound(t *testing.T) {
	rep := &fakeReporting{recordsErr: notFoundErr("batch not found")}
	req := httptest.NewRequest(http.MethodGet, "/v1/batches/unknown/records", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	newHandlerWithReporting(rep).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestListBatchesProxiesToIngestion(t *testing.T) {
	fi := &fakeIngestion{listBatchesResp: &ingestionv1.ListBatchesResponse{
		Batches: []*ingestionv1.BatchSummary{
			{BatchId: "batch-1", CreatedAt: timestamppb.New(time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)), TotalRecords: 80, Source: "synthetic-demo"},
		},
	}}
	handler := New(fi, &fakeReporting{}, &fakeAudit{}, nil, testAPIKey, 2*time.Second, 0, 0).Routes()

	req := httptest.NewRequest(http.MethodGet, "/v1/batches?limit=5", nil)
	req.Header.Set("X-API-Key", testAPIKey)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if fi.gotListBatches.GetLimit() != 5 {
		t.Errorf("proxied Limit = %d, want 5", fi.gotListBatches.GetLimit())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	batches, ok := got["batches"].([]any)
	if !ok || len(batches) != 1 {
		t.Fatalf("batches = %v, want a one-element array", got["batches"])
	}
	b := batches[0].(map[string]any)
	if b["batch_id"] != "batch-1" || b["source"] != "synthetic-demo" || b["total_records"] != float64(80) {
		t.Errorf("batches[0] = %v", b)
	}
	if b["created_at"] != "2026-08-29T14:00:00Z" {
		t.Errorf("batches[0].created_at = %v, want 2026-08-29T14:00:00Z", b["created_at"])
	}
}
