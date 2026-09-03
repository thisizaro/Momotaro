package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Wire structs for GET /v1/batches/{batch_id}/report, hand-written per
// docs/API_GATEWAY.md's own wire convention 6: no protojson, no
// omitempty, every documented field always rendered -- except accuracy
// and baseline_comparison, which are the doc's one deliberate absent-key
// exception ("a missing key means no answer key exists, distinct from a
// real zero"), so those two alone use a pointer with omitempty.

type batchReportResponse struct {
	BatchID                string  `json:"batch_id"`
	TotalRecords           int32   `json:"total_records"`
	InFlightCount          int32   `json:"in_flight_count"`
	AtRiskPaise            int64   `json:"at_risk_paise"`
	RecoveredPaise         int64   `json:"recovered_paise"`
	InterventionSpendPaise int64   `json:"intervention_spend_paise"`
	NetRecoveredPaise      int64   `json:"net_recovered_paise"`
	CostPerRupeeRecovered  float64 `json:"cost_per_rupee_recovered"`
	RecoveryRate           float64 `json:"recovery_rate"`
	EscalatedCount         int32   `json:"escalated_count"`
	ClosedUneconomicCount  int32   `json:"closed_uneconomic_count"`
	ClosedUneconomicPaise  int64   `json:"closed_uneconomic_paise"`
	ProcessingFailureCount int32   `json:"processing_failure_count"`
	// LlmQuotaExhaustedCount is docs/API_GATEWAY.md's Unit AI addition:
	// records that wanted a live model call and did not get one (Groq's
	// free tier or the classifier's own breaker said no, or the Decision
	// Engine's LLM_SAMPLE_RATE ceiling was already spent). Always present,
	// defaulting to 0 like ProcessingFailureCount above, never the
	// missing-key convention Accuracy/BaselineComparison use.
	LlmQuotaExhaustedCount int32                        `json:"llm_quota_exhausted_count"`
	ByRootCause            map[string]bucketStatsJSON   `json:"by_root_cause"`
	ByIntervention         map[string]interventionStats `json:"by_intervention"`
	Accuracy               *classificationAccuracyJSON  `json:"accuracy,omitempty"`
	BaselineComparison     *baselineComparisonJSON      `json:"baseline_comparison,omitempty"`
	GeneratedAt            string                       `json:"generated_at"`
}

type bucketStatsJSON struct {
	RecordCount    int32   `json:"record_count"`
	AtRiskPaise    int64   `json:"at_risk_paise"`
	RecoveredPaise int64   `json:"recovered_paise"`
	RecoveryRate   float64 `json:"recovery_rate"`
}

type interventionStats struct {
	AttemptCount   int32   `json:"attempt_count"`
	SuccessCount   int32   `json:"success_count"`
	SpendPaise     int64   `json:"spend_paise"`
	RecoveredPaise int64   `json:"recovered_paise"`
	SuccessRate    float64 `json:"success_rate"`
}

type classificationAccuracyJSON struct {
	ScoredRecords   int32                      `json:"scored_records"`
	OverallAccuracy float64                    `json:"overall_accuracy"`
	ByBucket        map[string]float64         `json:"by_bucket"`
	Confusion       map[string]bucketConfusion `json:"confusion"`
}

type bucketConfusion struct {
	TrueBucketCounts map[string]int32 `json:"true_bucket_counts"`
}

type baselineComparisonJSON struct {
	PolicyName             string `json:"policy_name"`
	GrossRecoveredPaise    int64  `json:"gross_recovered_paise"`
	InterventionSpendPaise int64  `json:"intervention_spend_paise"`
	NetRecoveredPaise      int64  `json:"net_recovered_paise"`
	Note                   string `json:"note"`
}

// formatTimestamp renders a google.protobuf.Timestamp as RFC3339
// (docs/API_GATEWAY.md wire convention 4), e.g. "2026-08-29T14:03:11Z".
func formatTimestamp(ts interface{ AsTime() time.Time }) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
}

// formatOptionalTimestamp renders due_at: empty string when genuinely
// unset. Deliberately takes the concrete *timestamppb.Timestamp rather
// than formatTimestamp's interface parameter: a nil *timestamppb.Timestamp
// boxed into that interface is not == nil (the interface still carries the
// pointer's type), so formatTimestamp's nil check never fires and
// Timestamp.AsTime()'s own nil-safe getters silently return the Unix
// epoch instead of "not scheduled". due_at is the one field on this
// response that is genuinely, meaningfully absent rather than always
// populated, so it needs its own nil check ahead of the interface
// boundary.
func formatOptionalTimestamp(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
}

func toBatchReportResponse(r *reportingv1.BatchReport) batchReportResponse {
	byRootCause := make(map[string]bucketStatsJSON, len(r.GetByRootCause()))
	for k, v := range r.GetByRootCause() {
		byRootCause[k] = bucketStatsJSON{
			RecordCount:    v.GetRecordCount(),
			AtRiskPaise:    v.GetAtRiskPaise(),
			RecoveredPaise: v.GetRecoveredPaise(),
			RecoveryRate:   v.GetRecoveryRate(),
		}
	}
	byIntervention := make(map[string]interventionStats, len(r.GetByIntervention()))
	for k, v := range r.GetByIntervention() {
		byIntervention[k] = interventionStats{
			AttemptCount:   v.GetAttemptCount(),
			SuccessCount:   v.GetSuccessCount(),
			SpendPaise:     v.GetSpendPaise(),
			RecoveredPaise: v.GetRecoveredPaise(),
			SuccessRate:    v.GetSuccessRate(),
		}
	}

	resp := batchReportResponse{
		BatchID:                r.GetBatchId(),
		TotalRecords:           r.GetTotalRecords(),
		InFlightCount:          r.GetInFlightCount(),
		AtRiskPaise:            r.GetAtRiskPaise(),
		RecoveredPaise:         r.GetRecoveredPaise(),
		InterventionSpendPaise: r.GetInterventionSpendPaise(),
		NetRecoveredPaise:      r.GetNetRecoveredPaise(),
		CostPerRupeeRecovered:  r.GetCostPerRupeeRecovered(),
		RecoveryRate:           r.GetRecoveryRate(),
		EscalatedCount:         r.GetEscalatedCount(),
		ClosedUneconomicCount:  r.GetClosedUneconomicCount(),
		ClosedUneconomicPaise:  r.GetClosedUneconomicPaise(),
		ProcessingFailureCount: r.GetProcessingFailureCount(),
		LlmQuotaExhaustedCount: r.GetLlmQuotaExhaustedCount(),
		ByRootCause:            byRootCause,
		ByIntervention:         byIntervention,
		GeneratedAt:            formatTimestamp(r.GetGeneratedAt()),
	}

	if acc := r.GetAccuracy(); acc != nil {
		confusion := make(map[string]bucketConfusion, len(acc.GetConfusion()))
		for k, v := range acc.GetConfusion() {
			confusion[k] = bucketConfusion{TrueBucketCounts: v.GetTrueBucketCounts()}
		}
		resp.Accuracy = &classificationAccuracyJSON{
			ScoredRecords:   acc.GetScoredRecords(),
			OverallAccuracy: acc.GetOverallAccuracy(),
			ByBucket:        acc.GetByBucket(),
			Confusion:       confusion,
		}
	}
	if bc := r.GetBaselineComparison(); bc != nil {
		resp.BaselineComparison = &baselineComparisonJSON{
			PolicyName:             bc.GetPolicyName(),
			GrossRecoveredPaise:    bc.GetGrossRecoveredPaise(),
			InterventionSpendPaise: bc.GetInterventionSpendPaise(),
			NetRecoveredPaise:      bc.GetNetRecoveredPaise(),
			Note:                   bc.GetNote(),
		}
	}
	return resp
}

func (h *Handler) getBatchReport(w http.ResponseWriter, r *http.Request) {
	batchID := r.PathValue("batch_id")
	if batchID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "batch_id is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.reporting.GetBatchReport(ctx, &reportingv1.GetBatchReportRequest{BatchId: batchID})
	if err != nil {
		writeGRPCError(w, err, "REPORTING_UNAVAILABLE")
		return
	}
	writeJSON(w, http.StatusOK, toBatchReportResponse(resp.GetReport()))
}

// Wire structs for GET /v1/batches/{batch_id}/records.

type recordSummaryJSON struct {
	RecordID     string `json:"record_id"`
	Type         string `json:"type"`
	AmountPaise  int64  `json:"amount_paise"`
	CurrentState string `json:"current_state"`
	Bucket       string `json:"bucket"`
	AttemptCount int32  `json:"attempt_count"`
	SpendPaise   int64  `json:"spend_paise"`
	// RFC3339 (Wire conventions 4) when the Decision Engine's scheduler is
	// waiting on this record (RETRY_SCHEDULED or NUDGE_SCHEDULED), empty
	// string otherwise, always present (Wire conventions 6: no
	// omitempty). Never omitted: a terminal record and a NUDGED record
	// (waiting on the customer, not the scheduler) both have none, and
	// docs/API_GATEWAY.md documents empty string as that absence, the same
	// convention already used for rationale/message_text.
	DueAt string `json:"due_at"`
	// RFC3339 timestamp of the record's earliest audit entry (when it was
	// first classified), empty string only in the brief real window before
	// that first entry exists. Always present, same empty-string-for-absent
	// convention as DueAt (docs/API_GATEWAY.md).
	FirstActionAt string `json:"first_action_at"`
	// RFC3339 timestamp of the most recent Decision Engine transition for
	// this record, empty string until the Decision Engine has acted on it
	// at least once. Always present, same convention as DueAt.
	LastActionAt string `json:"last_action_at"`
}

type listBatchRecordsResponse struct {
	Records       []recordSummaryJSON `json:"records"`
	NextPageToken string              `json:"next_page_token"`
	TotalCount    int32               `json:"total_count"`
}

func (h *Handler) listBatchRecords(w http.ResponseWriter, r *http.Request) {
	batchID := r.PathValue("batch_id")
	if batchID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "batch_id is required")
		return
	}

	q := r.URL.Query()
	req := &reportingv1.ListBatchRecordsRequest{
		BatchId:      batchID,
		PageSize:     parseInt32(q.Get("page_size")),
		PageToken:    q.Get("page_token"),
		StateFilter:  commonv1.RecordState(commonv1.RecordState_value[q.Get("state")]),
		BucketFilter: commonv1.RootCauseBucket(commonv1.RootCauseBucket_value[q.Get("bucket")]),
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.reporting.ListBatchRecords(ctx, req)
	if err != nil {
		writeGRPCError(w, err, "REPORTING_UNAVAILABLE")
		return
	}

	records := make([]recordSummaryJSON, len(resp.GetRecords()))
	for i, rec := range resp.GetRecords() {
		records[i] = recordSummaryJSON{
			RecordID:      rec.GetRecordId(),
			Type:          rec.GetType().String(),
			AmountPaise:   rec.GetAmountPaise(),
			CurrentState:  rec.GetCurrentState().String(),
			Bucket:        rec.GetBucket().String(),
			AttemptCount:  rec.GetAttemptCount(),
			SpendPaise:    rec.GetSpendPaise(),
			DueAt:         formatOptionalTimestamp(rec.GetDueAt()),
			FirstActionAt: formatOptionalTimestamp(rec.GetFirstActionAt()),
			LastActionAt:  formatOptionalTimestamp(rec.GetLastActionAt()),
		}
	}
	writeJSON(w, http.StatusOK, listBatchRecordsResponse{
		Records:       records,
		NextPageToken: resp.GetNextPageToken(),
		TotalCount:    resp.GetTotalCount(),
	})
}

// Wire structs for GET /v1/batches.

type batchSummaryJSON struct {
	BatchID      string `json:"batch_id"`
	CreatedAt    string `json:"created_at"`
	TotalRecords int32  `json:"total_records"`
	Source       string `json:"source"`
}

type listBatchesResponse struct {
	Batches []batchSummaryJSON `json:"batches"`
}

func (h *Handler) listBatches(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.callTimeout)
	defer cancel()

	resp, err := h.ingestion.ListBatches(ctx, &ingestionv1.ListBatchesRequest{Limit: parseInt32(r.URL.Query().Get("limit"))})
	if err != nil {
		writeGRPCError(w, err, "INGESTION_UNAVAILABLE")
		return
	}

	batches := make([]batchSummaryJSON, len(resp.GetBatches()))
	for i, b := range resp.GetBatches() {
		batches[i] = batchSummaryJSON{
			BatchID:      b.GetBatchId(),
			CreatedAt:    formatTimestamp(b.GetCreatedAt()),
			TotalRecords: b.GetTotalRecords(),
			Source:       b.GetSource(),
		}
	}
	writeJSON(w, http.StatusOK, listBatchesResponse{Batches: batches})
}

func parseInt32(s string) int32 {
	n, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return 0
	}
	return int32(n)
}

// writeGRPCError translates a downstream gRPC error into the right HTTP
// status: NotFound/InvalidArgument pass through as the same meaning to the
// caller, since the Gateway did nothing wrong, the request was; anything
// else is reported as fallbackCode, a Bad Gateway-shaped failure of the
// backend the Gateway depends on.
func writeGRPCError(w http.ResponseWriter, err error, fallbackCode string) {
	switch status.Code(err) {
	case codes.NotFound:
		writeError(w, http.StatusNotFound, "NOT_FOUND", status.Convert(err).Message())
	case codes.InvalidArgument:
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", status.Convert(err).Message())
	default:
		writeError(w, http.StatusBadGateway, fallbackCode, err.Error())
	}
}
