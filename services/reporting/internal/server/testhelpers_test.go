//go:build integration

// Shared fixtures for the integration-tagged tests in this package.

package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
)

func dsn(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("POSTGRES_DSN"); v != "" {
		return v
	}
	return "postgres://momotaro:momotaro@localhost:5432/momotaro?sslmode=disable"
}

func testPool(t *testing.T) *pgxpkg.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpkg.NewPool(ctx, dsn(t))
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedBatch inserts a BATCH row and returns its id.
func seedBatch(ctx context.Context, t *testing.T, pool *pgxpkg.Pool) string {
	t.Helper()
	batchID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO batch (id, source) VALUES ($1, 'test')`, batchID); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM ground_truth WHERE record_id IN (SELECT id FROM record WHERE batch_id = $1)`, batchID)
		_, _ = pool.Exec(bg, `DELETE FROM intervention_attempt WHERE record_id IN (SELECT id FROM record WHERE batch_id = $1)`, batchID)
		_, _ = pool.Exec(bg, `DELETE FROM audit_entry WHERE batch_id = $1`, batchID)
		_, _ = pool.Exec(bg, `DELETE FROM record_state WHERE record_id IN (SELECT id FROM record WHERE batch_id = $1)`, batchID)
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE batch_id = $1`, batchID)
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})
	return batchID
}

// seedRecord inserts one RECORD row in batchID and returns its id.
func seedRecord(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, batchID string, amountPaise int64, recordType string) string {
	t.Helper()
	recordID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO record (id, batch_id, type, amount_paise, failure_code, instrument_ref)
		VALUES ($1, $2, $3, $4, 'BANK_TIMEOUT', 'card_1')`, recordID, batchID, recordType, amountPaise); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	return recordID
}

// seedRecordState inserts a RECORD_STATE row for recordID.
func seedRecordState(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID, state, bucket string, attemptCount int) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO record_state (record_id, current_state, root_cause_bucket, attempt_count) VALUES ($1, $2, $3, $4)`,
		recordID, state, bucket, attemptCount); err != nil {
		t.Fatalf("seed record_state: %v", err)
	}
}

// seedRecordStateWithDueAt inserts a RECORD_STATE row for recordID with an
// explicit due_at, for Unit AA's due_at surfacing tests. Kept separate from
// seedRecordState (rather than adding a nullable parameter there and
// touching every existing call site) since only these tests care about
// due_at.
func seedRecordStateWithDueAt(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID, state, bucket string, attemptCount int, dueAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO record_state (record_id, current_state, root_cause_bucket, attempt_count, due_at) VALUES ($1, $2, $3, $4, $5)`,
		recordID, state, bucket, attemptCount, dueAt); err != nil {
		t.Fatalf("seed record_state with due_at: %v", err)
	}
}

// seedAttempt inserts one INTERVENTION_ATTEMPT row for recordID.
func seedAttempt(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID string, attemptNumber int, actionType, outcome string, costPaise int64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO intervention_attempt (id, record_id, attempt_number, action_type, outcome, cost_paise)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.NewString(), recordID, attemptNumber, actionType, outcome, costPaise); err != nil {
		t.Fatalf("seed intervention_attempt: %v", err)
	}
}

// seedGroundTruth inserts a GROUND_TRUTH row for recordID.
func seedGroundTruth(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID, trueBucket string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ground_truth (record_id, true_bucket, recovery_probability) VALUES ($1, $2, 0.5)`,
		recordID, trueBucket); err != nil {
		t.Fatalf("seed ground_truth: %v", err)
	}
}

// seedGroundTruthFull inserts a GROUND_TRUTH row for recordID with an
// explicit recovery/wrong-action probability pair, for Unit K's baseline
// comparison tests, which need to control both numbers rather than
// seedGroundTruth's fixed 0.5/0 (accuracy scoring only ever reads
// true_bucket, so it never needed to).
func seedGroundTruthFull(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID, trueBucket string, recoveryProbability, wrongActionProbability float64) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ground_truth (record_id, true_bucket, recovery_probability, wrong_action_probability)
		VALUES ($1, $2, $3, $4)`,
		recordID, trueBucket, recoveryProbability, wrongActionProbability); err != nil {
		t.Fatalf("seed ground_truth: %v", err)
	}
}
