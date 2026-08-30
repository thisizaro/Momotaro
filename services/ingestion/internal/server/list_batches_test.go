//go:build integration

package server

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
)

// seedBatchAt inserts a BATCH row directly, with an explicit created_at, so
// ordering assertions are deterministic rather than depending on how fast
// two SubmitBatch calls happen to run.
func seedBatchAt(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, createdAt time.Time, source string, totalRecords int32) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO batch (id, created_at, source, total_records) VALUES ($1, $2, $3, $4)`,
		id, createdAt, source, totalRecords); err != nil {
		t.Fatalf("seed batch: %v", err)
	}
	t.Cleanup(func() { cleanupBatch(t, pool, id) })
	return id
}

// indexOf finds want's position among batches by batch_id, or -1.
func indexOf(batches []*ingestionv1.BatchSummary, want string) int {
	for i, b := range batches {
		if b.GetBatchId() == want {
			return i
		}
	}
	return -1
}

// TestListBatchesReturnsNewestFirst is the only ordering guarantee this RPC
// makes (docs/API_GATEWAY.md: "newest first, so a 'pick the most recent
// one' default has something real to select"). ListBatches is
// system-wide, like claimDue, so other tests' or other runs' rows may be
// present too; the assertion is about the RELATIVE order of these two
// seeded rows among however many come back, not the total count.
func TestListBatchesReturnsNewestFirst(t *testing.T) {
	pool := testPool(t)
	s := New(pool, testProducer(t), clock.New(), "raw.events", "webhook")
	ctx := context.Background()

	older := seedBatchAt(ctx, t, pool, time.Now().Add(-2*time.Hour), "test-older", 3)
	newer := seedBatchAt(ctx, t, pool, time.Now().Add(-1*time.Hour), "test-newer", 5)

	resp, err := s.ListBatches(ctx, &ingestionv1.ListBatchesRequest{Limit: 1000})
	if err != nil {
		t.Fatalf("ListBatches: %v", err)
	}

	newerIdx, olderIdx := indexOf(resp.GetBatches(), newer), indexOf(resp.GetBatches(), older)
	if newerIdx == -1 || olderIdx == -1 {
		t.Fatalf("seeded batches not both present: newer at %d, older at %d (returned %d batches)", newerIdx, olderIdx, len(resp.GetBatches()))
	}
	if newerIdx >= olderIdx {
		t.Errorf("newer batch at index %d, older at %d: want newer strictly before older", newerIdx, olderIdx)
	}
}

// TestListBatchesReturnsFieldsCorrectly proves the row's own data survives
// the round trip, since ordering alone would not catch a swapped column.
func TestListBatchesReturnsFieldsCorrectly(t *testing.T) {
	pool := testPool(t)
	s := New(pool, testProducer(t), clock.New(), "raw.events", "webhook")
	ctx := context.Background()

	createdAt := time.Now().Add(-30 * time.Minute).Truncate(time.Second)
	id := seedBatchAt(ctx, t, pool, createdAt, "synthetic-demo", 42)

	resp, err := s.ListBatches(ctx, &ingestionv1.ListBatchesRequest{Limit: 1000})
	if err != nil {
		t.Fatalf("ListBatches: %v", err)
	}

	idx := indexOf(resp.GetBatches(), id)
	if idx == -1 {
		t.Fatalf("seeded batch %s not present in %d returned batches", id, len(resp.GetBatches()))
	}
	got := resp.GetBatches()[idx]
	if got.GetSource() != "synthetic-demo" {
		t.Errorf("Source = %q, want synthetic-demo", got.GetSource())
	}
	if got.GetTotalRecords() != 42 {
		t.Errorf("TotalRecords = %d, want 42", got.GetTotalRecords())
	}
	if !got.GetCreatedAt().AsTime().Equal(createdAt) {
		t.Errorf("CreatedAt = %v, want %v", got.GetCreatedAt().AsTime(), createdAt)
	}
}

// TestListBatchesDefaultLimitAppliesWhenUnset proves the "optional, default
// 20" contract (docs/API_GATEWAY.md) is enforced by the RPC itself, not
// left to every caller to remember.
func TestListBatchesDefaultLimitAppliesWhenUnset(t *testing.T) {
	pool := testPool(t)
	s := New(pool, testProducer(t), clock.New(), "raw.events", "webhook")
	ctx := context.Background()

	// 25 rows, all newer than anything else a concurrent test could
	// plausibly seed in this same instant, so the default-20 cap is what
	// is actually being measured, not the shared table's real contents.
	base := time.Now()
	var ids []string
	for i := 0; i < 25; i++ {
		ids = append(ids, seedBatchAt(ctx, t, pool, base.Add(time.Duration(i)*time.Millisecond), "test-default-limit", 1))
	}

	resp, err := s.ListBatches(ctx, &ingestionv1.ListBatchesRequest{})
	if err != nil {
		t.Fatalf("ListBatches: %v", err)
	}

	// Of the 25 seeded (newest-first order: ids[24] is newest), only the
	// top 20 by created_at can appear under a default-20 cap. ids[0..4]
	// are the 5 oldest of the 25 and must be excluded.
	for i := 0; i < 5; i++ {
		if idx := indexOf(resp.GetBatches(), ids[i]); idx != -1 {
			t.Errorf("batch %d (one of the 5 oldest seeded) appeared in the default-limited response, want excluded", i)
		}
	}
	// And the cap must actually be 20, not 0 or unbounded: the newest of
	// the 25 must be present.
	if idx := indexOf(resp.GetBatches(), ids[24]); idx == -1 {
		t.Error("newest seeded batch is missing from the default-limited response, want it present")
	}
}
