//go:build integration

// Shared setup for every test in this package that talks to real Postgres
// and Kafka (docs/ENGINEERING.md section 1: "do not mock what you own").
// Kept in one file so submit_batch_test.go and submit_event_test.go don't
// each redeclare the same connection/cleanup boilerplate.

package server

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
)

func dsn(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("POSTGRES_DSN"); v != "" {
		return v
	}
	return "postgres://momotaro:momotaro@localhost:5432/momotaro?sslmode=disable"
}

func brokers(t *testing.T) []string {
	t.Helper()
	if v := os.Getenv("KAFKA_BROKERS"); v != "" {
		return strings.Split(v, ",")
	}
	return []string{"localhost:9092"}
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

func testProducer(t *testing.T) *kafkax.Producer {
	t.Helper()
	p, err := kafkax.NewProducer(brokers(t))
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// cleanupBatch deletes one SubmitBatch-style batch (records plus the batch
// row itself) once the test finishes.
func cleanupBatch(t *testing.T, pool *pgxpkg.Pool, batchID string) {
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE batch_id = $1`, batchID)
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})
}

// cleanupRollingBatch tears down a rolling batch used by SubmitEvent tests.
// Unlike cleanupBatch this only removes the specific records a test created
// (by id, not by batch_id), since a rolling batch is meant to be reused
// across many calls, and the batch row itself is only dropped once nothing
// in it remains.
func cleanupRollingBatch(t *testing.T, pool *pgxpkg.Pool, batchID string, recordIDs ...string) {
	t.Cleanup(func() {
		bg := context.Background()
		for _, id := range recordIDs {
			_, _ = pool.Exec(bg, `DELETE FROM record WHERE id = $1`, id)
		}
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
	})
}

func validRecord() *ingestionv1.NewRecord {
	return &ingestionv1.NewRecord{
		Type:          commonv1.RecordType_RECORD_TYPE_PAYMENT,
		AmountPaise:   50000,
		Currency:      "INR",
		FailureCode:   "BANK_TIMEOUT",
		InstrumentRef: "card_ref_1",
	}
}
