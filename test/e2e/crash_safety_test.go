//go:build e2e

// Unit K: Crash safety (docs/PHASE2_IMPLEMENTATION.md).
//
// Kills the Decision Engine mid-batch with SIGKILL -- not the graceful
// SIGTERM every other test's teardown uses -- restarts it with identical
// configuration (same Kafka consumer group and topic, so it resumes from
// the last committed offset rather than replaying or skipping anything),
// and asserts the batch still finishes with no record lost, no audit gap,
// and no record processed twice. This is what the transactional write
// (every state change and its audit entry commit together, store.go) and
// the contiguous-prefix Kafka commits (kafkax.ConsumeKeyed) exist to
// guarantee; until something actually kills the process mid-flight, both
// are claims rather than facts.
//
// Needs a real, observable window to kill into: classify-and-schedule for a
// small batch completes in well under a second (confirmed by an earlier,
// unthrottled version of this test finishing the whole batch before the
// first kill call even ran), so RETRY_DELAY is set to a value that scales
// down to a real ~6s wait before the scheduler claims anything (see Unit
// H's docs/INCIDENTS.md 2026-08-27 entry for why the requested value looks
// this large: DEMO_TIME_SCALE=300000 divides it once). The kill lands while
// every record is genuinely still sitting in RETRY_SCHEDULED, unclaimed --
// real in-flight state, not a race against a window too fast to hit.
package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func TestCrashSafetyDecisionEngineRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// "1800000s" scales down to ~6s under DEMO_TIME_SCALE=300000 -- see the
	// file comment above.
	s := startStack(ctx, t, "1800000s")
	pool := connectPool(ctx, t)
	auditClient := dialAudit(ctx, t, s.auditAddr)

	const numRecords = 8
	source := "e2e-crash-safety-" + uuid.NewString()
	var recordsJSON []string
	for i := 0; i < numRecords; i++ {
		ref := fmt.Sprintf("card_k_%d_%s", i, uuid.NewString())
		recordsJSON = append(recordsJSON, fmt.Sprintf(
			`{"type":"PAYMENT","amount_paise":75000,"currency":"INR","failure_code":"BANK_TIMEOUT","instrument_ref":%q}`, ref))
	}
	body := fmt.Sprintf(`{"source":%q,"records":[%s]}`, source, strings.Join(recordsJSON, ","))

	resp := submitBatch(ctx, t, s.gatewayHTTP, body)
	if resp.AcceptedCount != numRecords {
		t.Fatalf("accepted_count = %d, want %d", resp.AcceptedCount, numRecords)
	}
	batchID := resp.BatchID

	recordIDs := recordIDsForBatch(ctx, t, pool, batchID, numRecords)

	// All eight classify as TRANSIENT_BANK (BANK_TIMEOUT), and Executor now
	// calls the real World Simulator for RETRY (Phase 5 Units C/D), which
	// requires a GROUND_TRUTH row per record. recovery_probability=1.0 for
	// the correct action reproduces the old stub's "attempt 1 succeeds"
	// deterministically, which is what every record must do here (crash
	// safety, not economics, is what this test proves).
	for _, rid := range recordIDs {
		if _, err := pool.Exec(ctx, `
			INSERT INTO ground_truth (record_id, true_bucket, recovery_probability, wrong_action_probability, response_delay_seconds)
			VALUES ($1, 'ROOT_CAUSE_BUCKET_TRANSIENT_BANK', 1.0, 0.0, 0)`, rid); err != nil {
			t.Fatalf("seed ground_truth for %s: %v", rid, err)
		}
	}

	t.Cleanup(func() {
		bg := context.Background()
		for _, rid := range recordIDs {
			_, _ = pool.Exec(bg, `DELETE FROM ground_truth WHERE record_id = $1`, rid)
			_, _ = pool.Exec(bg, `DELETE FROM audit_entry WHERE record_id = $1`, rid)
			_, _ = pool.Exec(bg, `DELETE FROM intervention_attempt WHERE record_id = $1`, rid)
			_, _ = pool.Exec(bg, `DELETE FROM record_state WHERE record_id = $1`, rid)
			_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, rid)
		}
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})

	// --- Wait for every record to reach its scheduled-but-unclaimed state,
	// then crash. All eight are genuinely mid-batch at this point: ingested,
	// classified, scored and scheduled, none executed yet. ---
	for _, rid := range recordIDs {
		waitForExactRecordState(ctx, t, pool, rid,
			commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED,
			"record should classify to TRANSIENT_BANK and schedule a retry before the crash")
	}
	s.restartDecisionEngine(t)

	// --- The restarted process must still finish the whole batch. ---
	for _, rid := range recordIDs {
		waitForExactRecordState(ctx, t, pool, rid,
			commonv1.RecordState_RECORD_STATE_RECOVERED,
			"every record should still reach RECOVERED after the Decision Engine is killed and restarted mid-batch")
	}

	// --- No record was lost, none was processed twice: exactly one
	// intervention_attempt per record, exactly one real side effect. ---
	for _, rid := range recordIDs {
		assertRecoveredOnce(ctx, t, pool, rid)
	}

	// --- No audit gap: every trail complete, no impossible transition,
	// across the whole batch. ---
	viCtx, viCancel := context.WithTimeout(ctx, 10*time.Second)
	defer viCancel()
	vi, err := auditClient.VerifyInvariants(viCtx, &auditv1.VerifyInvariantsRequest{BatchId: batchID})
	if err != nil {
		t.Fatalf("VerifyInvariants: %v", err)
	}
	if vi.GetRecordsChecked() != numRecords {
		t.Errorf("VerifyInvariants RecordsChecked = %d, want %d", vi.GetRecordsChecked(), numRecords)
	}
	if vi.GetIncompleteAuditTrails() != 0 || vi.GetImpossibleTransitions() != 0 {
		t.Errorf("VerifyInvariants reported violations after a Decision Engine crash and restart: %+v", vi)
	}
}

// recordIDsForBatch waits for exactly want record rows to exist for batchID
// (ingestion creates them synchronously, but this still tolerates a beat of
// replication/visibility lag) and returns their IDs.
func recordIDsForBatch(ctx context.Context, t *testing.T, p *pgxpkg.Pool, batchID string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(startupWindow)
	for {
		rows, err := p.Query(ctx, `SELECT id FROM record WHERE batch_id = $1`, batchID)
		if err != nil {
			t.Fatalf("query records for batch %s: %v", batchID, err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatalf("scan record id: %v", err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) == want {
			return ids
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch %s has %d record rows, want %d", batchID, len(ids), want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
