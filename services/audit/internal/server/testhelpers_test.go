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

// seedRecord inserts batch+record rows and returns (batchID, recordID).
func seedRecord(ctx context.Context, t *testing.T, pool *pgxpkg.Pool) (string, string) {
	t.Helper()
	batchID := uuid.NewString()
	recordID := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO batch (id, source) VALUES ($1, 'test')`, batchID); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO record (id, batch_id, type, amount_paise, failure_code, instrument_ref)
		VALUES ($1, $2, 'RECORD_TYPE_PAYMENT', 12345, 'BANK_TIMEOUT', 'card_1')`, recordID, batchID); err != nil {
		t.Fatalf("seed record: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM audit_entry WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM record_state WHERE record_id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, recordID)
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})
	return batchID, recordID
}

// seedRecordState inserts a RECORD_STATE row for recordID.
func seedRecordState(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID, state string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO record_state (record_id, current_state, attempt_count) VALUES ($1, $2, 1)`,
		recordID, state); err != nil {
		t.Fatalf("seed record_state: %v", err)
	}
}

// seedAuditEntry inserts one AUDIT_ENTRY row for recordID.
func seedAuditEntry(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID, batchID, fromState, toState string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO audit_entry (record_id, batch_id, ts, from_state, to_state, reason, source, actor, attempt_number, cost_paise)
		VALUES ($1, $2, now(), $3, $4, 'test', 'SOURCE_RULES_FALLBACK', 'system', 1, 0)`,
		recordID, batchID, fromState, toState); err != nil {
		t.Fatalf("seed audit_entry: %v", err)
	}
}
