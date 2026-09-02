//go:build integration

// GetBatchReport and ListBatchRecords exercise real Postgres rather than a
// mock, per docs/ENGINEERING.md section 1 ("do not mock what you own").
// They therefore need the docker-compose stack up. Run with
// `make test-integration`.

package server

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetBatchReportComputesHeadlineNumbers(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)

	recovered := seedRecord(ctx, t, pool, batchID, 100000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, recovered, "RECORD_STATE_RECOVERED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)
	seedAttempt(ctx, t, pool, recovered, 1, "ACTION_TYPE_RETRY", "OUTCOME_SUCCESS", 500)

	escalated := seedRecord(ctx, t, pool, batchID, 50000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, escalated, "RECORD_STATE_ESCALATED", "ROOT_CAUSE_BUCKET_RISK_HOLD", 0)

	uneconomic := seedRecord(ctx, t, pool, batchID, 20000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, uneconomic, "RECORD_STATE_CLOSED_UNECONOMIC", "ROOT_CAUSE_BUCKET_HARD_DECLINE", 0)

	inFlight := seedRecord(ctx, t, pool, batchID, 30000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, inFlight, "RECORD_STATE_RETRY_SCHEDULED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 0)

	s := New(pool, NewHub())
	resp, err := s.GetBatchReport(ctx, &reportingv1.GetBatchReportRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("GetBatchReport: %v", err)
	}
	r := resp.GetReport()

	if r.TotalRecords != 4 {
		t.Errorf("TotalRecords = %d, want 4", r.TotalRecords)
	}
	if r.InFlightCount != 1 {
		t.Errorf("InFlightCount = %d, want 1", r.InFlightCount)
	}
	if r.AtRiskPaise != 200000 {
		t.Errorf("AtRiskPaise = %d, want 200000", r.AtRiskPaise)
	}
	if r.RecoveredPaise != 100000 {
		t.Errorf("RecoveredPaise = %d, want 100000", r.RecoveredPaise)
	}
	if r.InterventionSpendPaise != 500 {
		t.Errorf("InterventionSpendPaise = %d, want 500", r.InterventionSpendPaise)
	}
	if r.NetRecoveredPaise != 99500 {
		t.Errorf("NetRecoveredPaise = %d, want 99500", r.NetRecoveredPaise)
	}
	// 500 paise spent / (100000 paise = 1000 rupees recovered) = 0.5 paise/rupee.
	if r.CostPerRupeeRecovered != 0.5 {
		t.Errorf("CostPerRupeeRecovered = %v, want 0.5", r.CostPerRupeeRecovered)
	}
	if r.RecoveryRate != 0.25 {
		t.Errorf("RecoveryRate = %v, want 0.25", r.RecoveryRate)
	}
	if r.EscalatedCount != 1 {
		t.Errorf("EscalatedCount = %d, want 1", r.EscalatedCount)
	}
	if r.ClosedUneconomicCount != 1 {
		t.Errorf("ClosedUneconomicCount = %d, want 1", r.ClosedUneconomicCount)
	}
	if r.ClosedUneconomicPaise != 20000 {
		t.Errorf("ClosedUneconomicPaise = %d, want 20000", r.ClosedUneconomicPaise)
	}
	if r.GeneratedAt == nil {
		t.Error("GeneratedAt is nil")
	}
}

func TestGetBatchReportGroupsByRootCauseAndIntervention(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)

	a := seedRecord(ctx, t, pool, batchID, 100000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, a, "RECORD_STATE_RECOVERED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)
	seedAttempt(ctx, t, pool, a, 1, "ACTION_TYPE_RETRY", "OUTCOME_SUCCESS", 500)

	b := seedRecord(ctx, t, pool, batchID, 40000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, b, "RECORD_STATE_ESCALATED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)
	seedAttempt(ctx, t, pool, b, 1, "ACTION_TYPE_RETRY", "OUTCOME_FAILURE", 500)

	s := New(pool, NewHub())
	resp, err := s.GetBatchReport(ctx, &reportingv1.GetBatchReportRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("GetBatchReport: %v", err)
	}
	r := resp.GetReport()

	bucket := r.GetByRootCause()["ROOT_CAUSE_BUCKET_TRANSIENT_BANK"]
	if bucket == nil {
		t.Fatal("by_root_cause missing ROOT_CAUSE_BUCKET_TRANSIENT_BANK")
	}
	if bucket.RecordCount != 2 {
		t.Errorf("bucket.RecordCount = %d, want 2", bucket.RecordCount)
	}
	if bucket.AtRiskPaise != 140000 {
		t.Errorf("bucket.AtRiskPaise = %d, want 140000", bucket.AtRiskPaise)
	}
	if bucket.RecoveredPaise != 100000 {
		t.Errorf("bucket.RecoveredPaise = %d, want 100000", bucket.RecoveredPaise)
	}
	if bucket.RecoveryRate != 0.5 {
		t.Errorf("bucket.RecoveryRate = %v, want 0.5", bucket.RecoveryRate)
	}

	iv := r.GetByIntervention()["ACTION_TYPE_RETRY"]
	if iv == nil {
		t.Fatal("by_intervention missing ACTION_TYPE_RETRY")
	}
	if iv.AttemptCount != 2 {
		t.Errorf("iv.AttemptCount = %d, want 2", iv.AttemptCount)
	}
	if iv.SuccessCount != 1 {
		t.Errorf("iv.SuccessCount = %d, want 1", iv.SuccessCount)
	}
	if iv.SpendPaise != 1000 {
		t.Errorf("iv.SpendPaise = %d, want 1000", iv.SpendPaise)
	}
	if iv.RecoveredPaise != 100000 {
		t.Errorf("iv.RecoveredPaise = %d, want 100000", iv.RecoveredPaise)
	}
	if iv.SuccessRate != 0.5 {
		t.Errorf("iv.SuccessRate = %v, want 0.5", iv.SuccessRate)
	}
}

func TestGetBatchReportOmitsAccuracyWithoutGroundTruth(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)
	rec := seedRecord(ctx, t, pool, batchID, 100000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, rec, "RECORD_STATE_RECOVERED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)

	s := New(pool, NewHub())
	resp, err := s.GetBatchReport(ctx, &reportingv1.GetBatchReportRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("GetBatchReport: %v", err)
	}
	if resp.GetReport().Accuracy != nil {
		t.Error("Accuracy present for a batch with no ground_truth, want nil (docs/API_GATEWAY.md: a missing key means no answer key exists)")
	}
}

func TestGetBatchReportComputesAccuracyAgainstGroundTruth(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)

	// Predicted correctly.
	a := seedRecord(ctx, t, pool, batchID, 100000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, a, "RECORD_STATE_RECOVERED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)
	seedGroundTruth(ctx, t, pool, a, "ROOT_CAUSE_BUCKET_TRANSIENT_BANK")

	// Predicted wrong: classified TRANSIENT_BANK, true HARD_DECLINE.
	b := seedRecord(ctx, t, pool, batchID, 50000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, b, "RECORD_STATE_ESCALATED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)
	seedGroundTruth(ctx, t, pool, b, "ROOT_CAUSE_BUCKET_HARD_DECLINE")

	s := New(pool, NewHub())
	resp, err := s.GetBatchReport(ctx, &reportingv1.GetBatchReportRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("GetBatchReport: %v", err)
	}
	acc := resp.GetReport().GetAccuracy()
	if acc == nil {
		t.Fatal("Accuracy is nil, want populated")
	}
	if acc.ScoredRecords != 2 {
		t.Errorf("ScoredRecords = %d, want 2", acc.ScoredRecords)
	}
	if acc.OverallAccuracy != 0.5 {
		t.Errorf("OverallAccuracy = %v, want 0.5", acc.OverallAccuracy)
	}
	if acc.ByBucket["ROOT_CAUSE_BUCKET_TRANSIENT_BANK"] != 0.5 {
		t.Errorf("ByBucket[TRANSIENT_BANK] = %v, want 0.5", acc.ByBucket["ROOT_CAUSE_BUCKET_TRANSIENT_BANK"])
	}
	confusion := acc.GetConfusion()["ROOT_CAUSE_BUCKET_TRANSIENT_BANK"]
	if confusion == nil {
		t.Fatal("Confusion missing ROOT_CAUSE_BUCKET_TRANSIENT_BANK")
	}
	if confusion.TrueBucketCounts["ROOT_CAUSE_BUCKET_TRANSIENT_BANK"] != 1 {
		t.Errorf("true TRANSIENT_BANK count = %d, want 1", confusion.TrueBucketCounts["ROOT_CAUSE_BUCKET_TRANSIENT_BANK"])
	}
	if confusion.TrueBucketCounts["ROOT_CAUSE_BUCKET_HARD_DECLINE"] != 1 {
		t.Errorf("true HARD_DECLINE count = %d, want 1", confusion.TrueBucketCounts["ROOT_CAUSE_BUCKET_HARD_DECLINE"])
	}
}

func TestGetBatchReportOmitsBaselineComparisonWithoutGroundTruth(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)
	rec := seedRecord(ctx, t, pool, batchID, 100000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, rec, "RECORD_STATE_RECOVERED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)

	s := New(pool, NewHub())
	resp, err := s.GetBatchReport(ctx, &reportingv1.GetBatchReportRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("GetBatchReport: %v", err)
	}
	if resp.GetReport().BaselineComparison != nil {
		t.Error("BaselineComparison present for a batch with no ground_truth, want nil (docs/API_GATEWAY.md: a missing key means no answer key exists)")
	}
}

func TestGetBatchReportComputesBaselineComparisonAgainstGroundTruth(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)

	// RISK_HOLD's correct action is ESCALATE, so neither the naive
	// policy's retries nor its one nudge is ever correct here: every one
	// of the four attempts sees wrong_action_probability, all four still
	// cost money regardless of outcome. Hand-computed in
	// baseline_test.go's TestEvaluateNaivePolicyRiskHoldSpendsForNearZeroRecovery:
	// gross 0, spend 100 (3 retries + 1 nudge at 25 paise each).
	rh := seedRecord(ctx, t, pool, batchID, 100000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, rh, "RECORD_STATE_ESCALATED", "ROOT_CAUSE_BUCKET_RISK_HOLD", 0)
	seedGroundTruthFull(ctx, t, pool, rh, "ROOT_CAUSE_BUCKET_RISK_HOLD", 0.05, 0.0)

	s := New(pool, NewHub())
	resp, err := s.GetBatchReport(ctx, &reportingv1.GetBatchReportRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("GetBatchReport: %v", err)
	}
	bc := resp.GetReport().GetBaselineComparison()
	if bc == nil {
		t.Fatal("BaselineComparison is nil, want populated")
	}
	if bc.PolicyName != "naive_retry3_nudge1" {
		t.Errorf("PolicyName = %q, want naive_retry3_nudge1", bc.PolicyName)
	}
	if bc.GrossRecoveredPaise != 0 {
		t.Errorf("GrossRecoveredPaise = %d, want 0 (RISK_HOLD's wrong_action_probability is 0)", bc.GrossRecoveredPaise)
	}
	if bc.InterventionSpendPaise != 100 {
		t.Errorf("InterventionSpendPaise = %d, want 100 (3 retries + 1 nudge at 25 paise each, charged regardless of outcome)", bc.InterventionSpendPaise)
	}
	if bc.NetRecoveredPaise != -100 {
		t.Errorf("NetRecoveredPaise = %d, want -100", bc.NetRecoveredPaise)
	}
	if bc.Note == "" {
		t.Error("Note is empty, want the honesty caveat")
	}
}

func TestGetBatchReportUnknownBatchNotFound(t *testing.T) {
	pool := testPool(t)
	s := New(pool, NewHub())

	_, err := s.GetBatchReport(context.Background(), &reportingv1.GetBatchReportRequest{BatchId: uuid.NewString()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("GetBatchReport for unknown batch: err = %v, want NotFound", err)
	}
}

func TestGetBatchReportMissingBatchID(t *testing.T) {
	pool := testPool(t)
	s := New(pool, NewHub())

	_, err := s.GetBatchReport(context.Background(), &reportingv1.GetBatchReportRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetBatchReport with no batch_id: err = %v, want InvalidArgument", err)
	}
}

func TestListBatchRecordsReturnsPageWithTotalCount(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)
	for i := 0; i < 3; i++ {
		rec := seedRecord(ctx, t, pool, batchID, 10000, "RECORD_TYPE_PAYMENT")
		seedRecordState(ctx, t, pool, rec, "RECORD_STATE_RECOVERED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)
	}

	s := New(pool, NewHub())
	resp, err := s.ListBatchRecords(ctx, &reportingv1.ListBatchRecordsRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("ListBatchRecords: %v", err)
	}
	if resp.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", resp.TotalCount)
	}
	if len(resp.Records) != 3 {
		t.Fatalf("len(Records) = %d, want 3", len(resp.Records))
	}
	if resp.NextPageToken != "" {
		t.Errorf("NextPageToken = %q, want empty (last page)", resp.NextPageToken)
	}
	for _, rec := range resp.Records {
		if rec.CurrentState != commonv1.RecordState_RECORD_STATE_RECOVERED {
			t.Errorf("record.CurrentState = %v, want RECOVERED", rec.CurrentState)
		}
	}
}

func TestListBatchRecordsFiltersByStateAndBucket(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)

	recovered := seedRecord(ctx, t, pool, batchID, 10000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, recovered, "RECORD_STATE_RECOVERED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)
	escalated := seedRecord(ctx, t, pool, batchID, 10000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, escalated, "RECORD_STATE_ESCALATED", "ROOT_CAUSE_BUCKET_RISK_HOLD", 0)

	s := New(pool, NewHub())
	resp, err := s.ListBatchRecords(ctx, &reportingv1.ListBatchRecordsRequest{
		BatchId:     batchID,
		StateFilter: commonv1.RecordState_RECORD_STATE_ESCALATED,
	})
	if err != nil {
		t.Fatalf("ListBatchRecords: %v", err)
	}
	if resp.TotalCount != 1 {
		t.Fatalf("TotalCount = %d, want 1", resp.TotalCount)
	}
	if resp.Records[0].RecordId != escalated {
		t.Errorf("Records[0].RecordId = %q, want the escalated record", resp.Records[0].RecordId)
	}
}

func TestListBatchRecordsPaginatesWithNextPageToken(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)
	for i := 0; i < 5; i++ {
		rec := seedRecord(ctx, t, pool, batchID, 10000, "RECORD_TYPE_PAYMENT")
		seedRecordState(ctx, t, pool, rec, "RECORD_STATE_RECOVERED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)
	}

	s := New(pool, NewHub())
	first, err := s.ListBatchRecords(ctx, &reportingv1.ListBatchRecordsRequest{BatchId: batchID, PageSize: 2})
	if err != nil {
		t.Fatalf("ListBatchRecords page 1: %v", err)
	}
	if len(first.Records) != 2 {
		t.Fatalf("page 1 len(Records) = %d, want 2", len(first.Records))
	}
	if first.NextPageToken == "" {
		t.Fatal("page 1 NextPageToken is empty, want a token (3 more records remain)")
	}

	second, err := s.ListBatchRecords(ctx, &reportingv1.ListBatchRecordsRequest{
		BatchId: batchID, PageSize: 2, PageToken: first.NextPageToken,
	})
	if err != nil {
		t.Fatalf("ListBatchRecords page 2: %v", err)
	}
	if len(second.Records) != 2 {
		t.Fatalf("page 2 len(Records) = %d, want 2", len(second.Records))
	}
	if second.Records[0].RecordId == first.Records[0].RecordId {
		t.Error("page 2 returned the same first record as page 1, pagination did not advance")
	}
}

// TestListBatchRecordsIncludesDueAt covers Unit AA: a record waiting on the
// Decision Engine's scheduler (RETRY_SCHEDULED) must carry its due_at
// through ListBatchRecords, and a terminal record must carry none, so the
// dashboard can tell "retry in 6s" apart from "not scheduled" instead of
// both rendering as an empty cell.
func TestListBatchRecordsIncludesDueAt(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)

	dueAt := time.Now().Add(90 * time.Second).Truncate(time.Second).UTC()
	scheduled := seedRecord(ctx, t, pool, batchID, 10000, "RECORD_TYPE_PAYMENT")
	seedRecordStateWithDueAt(ctx, t, pool, scheduled, "RECORD_STATE_RETRY_SCHEDULED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1, dueAt)

	recovered := seedRecord(ctx, t, pool, batchID, 10000, "RECORD_TYPE_PAYMENT")
	seedRecordState(ctx, t, pool, recovered, "RECORD_STATE_RECOVERED", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1)

	s := New(pool, NewHub())
	resp, err := s.ListBatchRecords(ctx, &reportingv1.ListBatchRecordsRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("ListBatchRecords: %v", err)
	}

	var gotScheduled, gotRecovered *reportingv1.RecordSummary
	for _, rec := range resp.Records {
		switch rec.RecordId {
		case scheduled:
			gotScheduled = rec
		case recovered:
			gotRecovered = rec
		}
	}
	if gotScheduled == nil || gotRecovered == nil {
		t.Fatalf("did not find both seeded records in response: %+v", resp.Records)
	}

	if gotScheduled.GetDueAt() == nil {
		t.Fatal("scheduled record: DueAt is nil, want the seeded timestamp")
	}
	if !gotScheduled.GetDueAt().AsTime().Equal(dueAt) {
		t.Errorf("scheduled record: DueAt = %v, want %v", gotScheduled.GetDueAt().AsTime(), dueAt)
	}
	if gotRecovered.GetDueAt() != nil {
		t.Errorf("recovered (terminal) record: DueAt = %v, want nil", gotRecovered.GetDueAt().AsTime())
	}
}

// TestListBatchRecordsIncludesFirstAndLastActionAt covers Unit AH: the
// historical timeline needs real timing, not just the scheduler's future
// due_at, so RecordSummary must carry when a record was first classified
// (MIN(audit_entry.ts)) and when the Decision Engine last touched it
// (record_state.last_action_at). A record with neither audit entries nor a
// record_state row (just landed, RECORD_STATE_NEW, nothing has acted on it
// yet) must carry neither field, the same "absent means no answer"
// convention due_at already established, not a zeroed-out epoch.
func TestListBatchRecordsIncludesFirstAndLastActionAt(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)

	acted := seedRecord(ctx, t, pool, batchID, 10000, "RECORD_TYPE_PAYMENT")
	firstTs := time.Now().Add(-90 * time.Second).Truncate(time.Second).UTC()
	lastTs := time.Now().Add(-10 * time.Second).Truncate(time.Second).UTC()
	// Two audit entries: MIN(ts) must pick the earlier one, not the last
	// row inserted or the last one in id order.
	seedAuditEntry(ctx, t, pool, acted, batchID, lastTs, "RECORD_STATE_RETRY_SCHEDULED", "RECORD_STATE_RETRYING")
	seedAuditEntry(ctx, t, pool, acted, batchID, firstTs, "RECORD_STATE_NEW", "RECORD_STATE_SCORING")
	seedRecordStateWithLastActionAt(ctx, t, pool, acted, "RECORD_STATE_RETRYING", "ROOT_CAUSE_BUCKET_TRANSIENT_BANK", 1, lastTs)

	untouched := seedRecord(ctx, t, pool, batchID, 10000, "RECORD_TYPE_PAYMENT")
	// No audit_entry, no record_state row at all: brand new, nothing has
	// acted on it yet.

	s := New(pool, NewHub())
	resp, err := s.ListBatchRecords(ctx, &reportingv1.ListBatchRecordsRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("ListBatchRecords: %v", err)
	}

	var gotActed, gotUntouched *reportingv1.RecordSummary
	for _, rec := range resp.Records {
		switch rec.RecordId {
		case acted:
			gotActed = rec
		case untouched:
			gotUntouched = rec
		}
	}
	if gotActed == nil || gotUntouched == nil {
		t.Fatalf("did not find both seeded records in response: %+v", resp.Records)
	}

	if gotActed.GetFirstActionAt() == nil {
		t.Fatal("acted record: FirstActionAt is nil, want the earlier of the two seeded audit entries")
	}
	if !gotActed.GetFirstActionAt().AsTime().Equal(firstTs) {
		t.Errorf("acted record: FirstActionAt = %v, want the earlier entry %v, not the later one", gotActed.GetFirstActionAt().AsTime(), firstTs)
	}
	if gotActed.GetLastActionAt() == nil {
		t.Fatal("acted record: LastActionAt is nil, want the seeded record_state.last_action_at")
	}
	if !gotActed.GetLastActionAt().AsTime().Equal(lastTs) {
		t.Errorf("acted record: LastActionAt = %v, want %v", gotActed.GetLastActionAt().AsTime(), lastTs)
	}

	if gotUntouched.GetFirstActionAt() != nil {
		t.Errorf("untouched record: FirstActionAt = %v, want nil (no audit_entry rows yet)", gotUntouched.GetFirstActionAt().AsTime())
	}
	if gotUntouched.GetLastActionAt() != nil {
		t.Errorf("untouched record: LastActionAt = %v, want nil (no record_state row yet)", gotUntouched.GetLastActionAt().AsTime())
	}
}

func TestListBatchRecordsUnknownBatchNotFound(t *testing.T) {
	pool := testPool(t)
	s := New(pool, NewHub())

	_, err := s.ListBatchRecords(context.Background(), &reportingv1.ListBatchRecordsRequest{BatchId: uuid.NewString()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("ListBatchRecords for unknown batch: err = %v, want NotFound", err)
	}
}
