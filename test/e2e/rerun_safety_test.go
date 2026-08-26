//go:build e2e

// Unit J: Re-run safety (docs/PHASE2_IMPLEMENTATION.md).
//
// Two entry points exist: SubmitEvent (production, one webhook at a time)
// and SubmitBatch (demo/backfill). They have different idempotency
// guarantees, and this file proves both:
//
//  1. SubmitEvent: the idempotency_key dedup is the real guarantee. A
//     repeated key returns the same record_id, no duplicate is created,
//     and the record proceeds through the pipeline exactly once.
//
//  2. SubmitBatch: has no idempotency key at all. Every call creates a
//     new batch_id and new record rows unconditionally. This is the
//     documented scope of the demo/backfill path (ARCHITECTURE.md §0a),
//     which was never designed to be idempotent. Two calls with identical
//     content produce two independent records that both process and settle
//     separately -- this is correct, not a bug.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// TestSubmitEventIdempotencyDeduplicatesRecord proves the real idempotency
// guarantee: SubmitEvent with a repeated idempotency_key returns the original
// record (deduplicated: true, same record_id) and no duplicate is created.
// The record proceeds through the pipeline once and settles once -- no double
// spend, no double intervention_attempt rows, no double audit entries.
//
// This is the production entry point (POST /v1/webhooks/payment-failed) and
// its dedup is the only caller-supplied idempotency guarantee in the
// Ingestion service (proto/ingestion/v1/ingestion.proto).
func TestSubmitEventIdempotencyDeduplicatesRecord(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	stack := startStack(ctx, t, "1s")
	pool := connectPool(ctx, t)
	auditClient := dialAudit(ctx, t, stack.auditAddr)

	// Unique key for this test run to avoid collision with other concurrent
	// tests sharing the same Postgres.
	idempotencyKey := "rerun-j-idem-" + uuid.NewString()

	eventBody := fmt.Sprintf(`{
		"type":"PAYMENT",
		"amount_paise":75000,
		"currency":"INR",
		"failure_code":"BANK_TIMEOUT",
		"instrument_ref":"card_rerun_j",
		"idempotency_key":%q
	}`, idempotencyKey)

	// --- First submission: the real event. ---
	resp1 := submitEvent(ctx, t, stack.gatewayHTTP, eventBody)
	if resp1.Deduplicated {
		t.Fatal("first submission reported deduplicated: nothing to dedup against yet")
	}
	if resp1.RecordID == "" {
		t.Fatal("first submission returned empty record_id")
	}
	t.Logf("first submission: record_id=%s batch_id=%s", resp1.RecordID, resp1.BatchID)

	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM audit_entry WHERE record_id = $1`, resp1.RecordID)
		_, _ = pool.Exec(bg, `DELETE FROM intervention_attempt WHERE record_id = $1`, resp1.RecordID)
		_, _ = pool.Exec(bg, `DELETE FROM record_state WHERE record_id = $1`, resp1.RecordID)
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, resp1.RecordID)
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, resp1.BatchID)
	})

	// --- Second submission: same idempotency_key, same record content. ---
	resp2 := submitEvent(ctx, t, stack.gatewayHTTP, eventBody)

	// Assert: deduplicated, same record_id.
	if !resp2.Deduplicated {
		t.Fatal("second submission was NOT deduplicated: the idempotency_key lookup failed or was not invoked")
	}
	if resp2.RecordID != resp1.RecordID {
		t.Fatalf("second submission returned different record_id: got %s, want %s", resp2.RecordID, resp1.RecordID)
	}
	t.Logf("second submission correctly deduplicated: record_id=%s", resp2.RecordID)

	// --- Assert: exactly one record row in Postgres (created synchronously). ---
	var recordCount int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM record WHERE idempotency_key = $1`, idempotencyKey,
	).Scan(&recordCount)
	if err != nil {
		t.Fatalf("count records by idempotency_key: %v", err)
	}
	if recordCount != 1 {
		t.Errorf("record rows for idempotency_key %s = %d, want 1: dedup did not prevent a duplicate",
			idempotencyKey, recordCount)
	}

	// --- Wait for the pipeline to process the single record. ---
	waitForRecordState(ctx, t, pool, resp1.RecordID,
		commonv1.RecordState_RECORD_STATE_RECOVERED,
		"deduplicated event should process once and reach RECOVERED")

	// --- Assert: exactly one record_state row (created asynchronously by Decision Engine). ---
	var stateCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM record_state WHERE record_id = $1`, resp1.RecordID,
	).Scan(&stateCount)
	if err != nil {
		t.Fatalf("count record_state: %v", err)
	}
	if stateCount != 1 {
		t.Errorf("record_state rows for %s = %d, want 1", resp1.RecordID, stateCount)
	}

	// --- Assert: exactly one intervention_attempt (one execution, not two). ---
	var attemptCount int
	var totalCost int64
	err = pool.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(cost_paise), 0) FROM intervention_attempt WHERE record_id = $1`,
		resp1.RecordID,
	).Scan(&attemptCount, &totalCost)
	if err != nil {
		t.Fatalf("query intervention_attempt: %v", err)
	}
	if attemptCount != 1 {
		t.Errorf("intervention_attempt rows = %d, want 1: dedup did not prevent double execution", attemptCount)
	}
	if totalCost <= 0 {
		t.Errorf("total spend = %d, want > 0: the single execution should have logged a cost", totalCost)
	}
	t.Logf("intervention: count=%d total_cost_paise=%d", attemptCount, totalCost)

	// --- Assert: audit trail is complete and carried out exactly once. ---
	auditCtx, auditCancel := context.WithTimeout(ctx, 10*time.Second)
	defer auditCancel()

	auditResp, err := auditClient.GetRecordAudit(auditCtx,
		&auditv1.GetRecordAuditRequest{RecordId: resp1.RecordID})
	if err != nil {
		t.Fatalf("GetRecordAudit: %v", err)
	}
	if auditResp.GetCurrentState() != commonv1.RecordState_RECORD_STATE_RECOVERED {
		t.Errorf("audit CurrentState = %v, want RECOVERED", auditResp.GetCurrentState())
	}
	if !auditResp.GetTrailComplete() {
		t.Error("audit TrailComplete = false: the trail has a gap or an impossible transition")
	}
	entries := auditResp.GetEntries()
	if len(entries) == 0 {
		t.Fatal("no audit entries: a processed record must have at least one")
	}
	// Trail must chain and end at RECOVERED.
	for i := 1; i < len(entries); i++ {
		if entries[i].GetFromState() != entries[i-1].GetToState() {
			t.Errorf("audit gap: entry %d starts at %v, previous ended at %v",
				i, entries[i].GetFromState(), entries[i-1].GetToState())
		}
	}
	if last := entries[len(entries)-1]; last.GetToState() != commonv1.RecordState_RECORD_STATE_RECOVERED {
		t.Errorf("last audit entry ToState = %v, want RECOVERED", last.GetToState())
	}
}

// TestSubmitBatchResubmitCreatesIndependentRecords documents that SubmitBatch
// has no idempotency key and no dedup. Every call to POST /v1/batches
// creates a brand-new batch_id and new record rows, unconditionally --
// there is no way to "resubmit the same batch_id" through the API as it
// exists today (SubmitBatchRequest in proto/ingestion/v1/ingestion.proto has
// no batch_id field and no idempotency key).
//
// This is the documented, deliberate scope of the demo/backfill path
// (ARCHITECTURE.md section 0a): SubmitBatch was never designed to be
// idempotent. SubmitEvent's idempotency_key is the only per-event dedup
// guarantee in Ingestion.
//
// This test asserts the actual behavior honestly: two different batch IDs,
// two distinct record IDs, and both records process and settle independently.
// A future reader should not mistake the assertion for a known gap left
// untested; it is the correct behavior for this API.
func TestSubmitBatchResubmitCreatesIndependentRecords(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	stack := startStack(ctx, t, "1s")
	pool := connectPool(ctx, t)
	auditClient := dialAudit(ctx, t, stack.auditAddr)

	// Unique source to scope Postgres queries to this test.
	source := "e2e-rerun-j-batch-" + uuid.NewString()

	// The exact same record content, submitted twice.
	body := fmt.Sprintf(`{
		"source":%q,
		"records":[
			{"type":"PAYMENT","amount_paise":50000,"currency":"INR",
			 "failure_code":"INSUFFICIENT_FUNDS","instrument_ref":"card_rerun_j_batch"}
		]
	}`, source)

	// --- First submission. ---
	resp1 := submitBatch(ctx, t, stack.gatewayHTTP, body)
	t.Logf("first batch:  batch_id=%s accepted=%d", resp1.BatchID, resp1.AcceptedCount)

	// --- Second submission: identical body. ---
	resp2 := submitBatch(ctx, t, stack.gatewayHTTP, body)
	t.Logf("second batch: batch_id=%s accepted=%d", resp2.BatchID, resp2.AcceptedCount)

	// --- Assert: two different batch IDs. ---
	if resp1.BatchID == resp2.BatchID {
		t.Errorf("both submissions returned the same batch_id %s: SubmitBatch should create a new batch each time",
			resp1.BatchID)
	}
	if resp1.BatchID == "" || resp2.BatchID == "" {
		t.Fatalf("one or both batch IDs are empty: batch_id1=%q batch_id2=%q", resp1.BatchID, resp2.BatchID)
	}

	// Both accepted.
	if resp1.AcceptedCount != 1 {
		t.Errorf("first batch accepted_count = %d, want 1", resp1.AcceptedCount)
	}
	if resp2.AcceptedCount != 1 {
		t.Errorf("second batch accepted_count = %d, want 1", resp2.AcceptedCount)
	}

	t.Cleanup(func() {
		bg := context.Background()
		for _, bid := range []string{resp1.BatchID, resp2.BatchID} {
			// Delete in FK-safe order: audit_entry references record, record_state references record.
			rows, _ := pool.Query(bg, `SELECT id FROM record WHERE batch_id = $1`, bid)
			if rows != nil {
				for rows.Next() {
					var rid string
					_ = rows.Scan(&rid)
					_, _ = pool.Exec(bg, `DELETE FROM audit_entry WHERE record_id = $1`, rid)
					_, _ = pool.Exec(bg, `DELETE FROM intervention_attempt WHERE record_id = $1`, rid)
					_, _ = pool.Exec(bg, `DELETE FROM record_state WHERE record_id = $1`, rid)
					_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, rid)
				}
				rows.Close()
			}
			_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, bid)
		}
	})

	// --- Assert: two distinct record rows in Postgres. ---
	var id1, id2 string
	err := pool.QueryRow(ctx,
		`SELECT id FROM record WHERE batch_id = $1`, resp1.BatchID,
	).Scan(&id1)
	if err != nil {
		t.Fatalf("find record in batch 1: %v", err)
	}
	err = pool.QueryRow(ctx,
		`SELECT id FROM record WHERE batch_id = $1`, resp2.BatchID,
	).Scan(&id2)
	if err != nil {
		t.Fatalf("find record in batch 2: %v", err)
	}
	if id1 == id2 {
		t.Errorf("both batches created the same record ID %s: each batch must create its own", id1)
	}
	t.Logf("record IDs: batch1=%s record=%s  batch2=%s record=%s", resp1.BatchID, id1, resp2.BatchID, id2)

	// --- Wait for both records to process and settle. ---
	waitForRecordState(ctx, t, pool, id1,
		commonv1.RecordState_RECORD_STATE_RECOVERED,
		"first batch record should reach RECOVERED")
	waitForRecordState(ctx, t, pool, id2,
		commonv1.RecordState_RECORD_STATE_RECOVERED,
		"second batch record should reach RECOVERED")

	// --- Assert: both settled at RECOVERED, both with one intervention. ---
	for _, tc := range []struct {
		batchID, recordID string
		label             string
	}{
		{resp1.BatchID, id1, "batch 1"},
		{resp2.BatchID, id2, "batch 2"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			assertRecoveredOnce(ctx, t, pool, tc.recordID)
			assertAuditTrailComplete(ctx, t, auditClient, tc.recordID,
				commonv1.RecordState_RECORD_STATE_RECOVERED)
		})
	}
}

// --- helpers ----------------------------------------------------------------

// submitEvent sends POST /v1/webhooks/payment-failed with the given JSON
// body (which must include idempotency_key and record fields per
// docs/API_GATEWAY.md) and returns the decoded response.
func submitEvent(ctx context.Context, t *testing.T, gwAddr, body string) submitEventResp {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+gwAddr+"/v1/webhooks/payment-failed", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/webhooks/payment-failed: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /v1/webhooks/payment-failed status = %d, body: %s", resp.StatusCode, raw)
	}

	var decoded submitEventResp
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, raw)
	}
	return decoded
}

type submitEventResp struct {
	RecordID     string `json:"record_id"`
	BatchID      string `json:"batch_id"`
	Deduplicated bool   `json:"deduplicated"`
}

// submitBatch sends POST /v1/batches with the given JSON body and returns
// the decoded response.
func submitBatch(ctx context.Context, t *testing.T, gwAddr, body string) submitBatchResp {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+gwAddr+"/v1/batches", strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/batches: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/batches status = %d, body: %s", resp.StatusCode, raw)
	}

	var decoded submitBatchResp
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode response: %v\nraw: %s", err, raw)
	}
	return decoded
}

type submitBatchResp struct {
	BatchID       string `json:"batch_id"`
	AcceptedCount int32  `json:"accepted_count"`
}

// waitForRecordState polls record_state until the record reaches wantState
// or the context deadline is exceeded. Uses pipelineWait as the polling
// deadline (no time.Sleep, bounded deadline per ENGINEERING.md section 1).
func waitForRecordState(ctx context.Context, t *testing.T, p *pgxpkg.Pool, recordID string, wantState commonv1.RecordState, reason string) {
	t.Helper()
	deadline := time.Now().Add(pipelineWait)
	for {
		var s string
		err := p.QueryRow(ctx,
			`SELECT current_state FROM record_state WHERE record_id = $1`, recordID,
		).Scan(&s)
		if err == nil {
			got := commonv1.RecordState(commonv1.RecordState_value[s])
			if got == wantState {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("record %s did not reach %v within %s (%s, last err=%v)",
				recordID, wantState, pipelineWait, reason, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// assertRecoveredOnce checks exactly one intervention_attempt row and
// positive spend for the given record.
func assertRecoveredOnce(ctx context.Context, t *testing.T, p *pgxpkg.Pool, recordID string) {
	t.Helper()
	var attemptCount int
	var totalCost int64
	err := p.QueryRow(ctx,
		`SELECT count(*), coalesce(sum(cost_paise), 0) FROM intervention_attempt WHERE record_id = $1`,
		recordID,
	).Scan(&attemptCount, &totalCost)
	if err != nil {
		t.Fatalf("query intervention_attempt for %s: %v", recordID, err)
	}
	if attemptCount != 1 {
		t.Errorf("record %s: intervention_attempt rows = %d, want 1", recordID, attemptCount)
	}
	if totalCost <= 0 {
		t.Errorf("record %s: logged spend = %d, want > 0", recordID, totalCost)
	}
}

// assertAuditTrailComplete verifies the audit trail chains correctly and
// ends at the expected state.
func assertAuditTrailComplete(ctx context.Context, t *testing.T, client auditv1.AuditServiceClient, recordID string, wantState commonv1.RecordState) {
	t.Helper()
	auditCtx, auditCancel := context.WithTimeout(ctx, 10*time.Second)
	defer auditCancel()

	resp, err := client.GetRecordAudit(auditCtx,
		&auditv1.GetRecordAuditRequest{RecordId: recordID})
	if err != nil {
		t.Fatalf("GetRecordAudit for %s: %v", recordID, err)
	}
	if resp.GetCurrentState() != wantState {
		t.Errorf("record %s: audit CurrentState = %v, want %v", recordID, resp.GetCurrentState(), wantState)
	}
	if !resp.GetTrailComplete() {
		t.Errorf("record %s: TrailComplete = false", recordID)
	}
	entries := resp.GetEntries()
	if len(entries) == 0 {
		t.Errorf("record %s: no audit entries", recordID)
		return
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].GetFromState() != entries[i-1].GetToState() {
			t.Errorf("record %s: audit gap at entry %d: starts at %v, previous ended at %v",
				recordID, i, entries[i].GetFromState(), entries[i-1].GetToState())
		}
	}
	if last := entries[len(entries)-1]; last.GetToState() != wantState {
		t.Errorf("record %s: last audit entry ToState = %v, want %v", recordID, last.GetToState(), wantState)
	}
}
