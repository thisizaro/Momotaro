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

// seedRecordWithGroundTruth inserts a batch, a record with failureCode,
// and its GROUND_TRUTH profile, returning the record id.
func seedRecordWithGroundTruth(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, failureCode, trueBucket string, recoveryP, wrongP float64, delaySeconds int32) string {
	t.Helper()
	batchID := uuid.NewString()
	recordID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO batch (id, source) VALUES ($1, 'test')`, batchID); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO record (id, batch_id, type, amount_paise, failure_code, instrument_ref)
		VALUES ($1, $2, 'RECORD_TYPE_PAYMENT', 50000, $3, 'card_1')`, recordID, batchID, failureCode); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO ground_truth (record_id, true_bucket, recovery_probability, wrong_action_probability, response_delay_seconds)
		VALUES ($1, $2, $3, $4, $5)`, recordID, trueBucket, recoveryP, wrongP, delaySeconds); err != nil {
		t.Fatalf("seed ground_truth: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM ground_truth WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})
	return recordID
}
