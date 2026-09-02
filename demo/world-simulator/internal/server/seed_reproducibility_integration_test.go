//go:build integration

// This is the claim docs/DEMO_READINESS.md Unit AD actually makes and the
// one rand_test.go's unit tests, on their own, cannot prove: that seeding
// two SEPARATE batches with the same seed rolls the same outcome sequence,
// matched by ordinal position within the batch. rand_test.go proves
// seededRand is a deterministic function of its inputs; it says nothing
// about whether SeedBatch and SimulateOutcome, wired together, ever hand
// seededRand the same inputs twice. They did not, before this fix:
// SeedBatch fed SimulateOutcome's roll a fresh uuid.NewString() record_id
// every run, so the "deterministic function" got different inputs each
// time and the outcome sequence still varied.
//
// This exercises the real Postgres write/read path (SeedBatch's insert,
// SimulateOutcome's loadRecordProfile), same as the other integration
// tests in this package, per docs/ENGINEERING.md section 1. It does not
// exercise Redis or Kafka's raw.events beyond what SeedBatch always does
// (a scratch topic per testRawEventsProducer); every action used here is
// ACTION_TYPE_RETRY, never a nudge, so queue.schedule is never reached and
// a nil redis client is safe to pass to New.
package server

import (
	"context"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
)

// seedBatchAndCollectRecordIDs seeds one batch through s and returns its
// records' ids in generation order (ordinal position 0..count-1), using
// the raw.events publish order on a single-partition scratch topic as the
// ordering signal, same as TestSeedBatchExplicitSeedReproducesTheExactSameSequence
// already relies on elsewhere in this package. It also returns the batch
// id, so the caller can clean up.
func seedBatchAndCollectRecordIDs(t *testing.T, s *Server, scenario string, count int32, seed int64, topic string) (ids []string, batchID string) {
	t.Helper()
	ctx := context.Background()
	resp, err := s.SeedBatch(ctx, &worldsimv1.SeedBatchRequest{Scenario: scenario, Count: count, Seed: seed})
	if err != nil {
		t.Fatalf("SeedBatch: %v", err)
	}
	events := consumeRawEvents(t, topic, int(count), 15*time.Second)
	ids = make([]string, len(events))
	for i, evt := range events {
		ids[i] = evt.RecordID
	}
	return ids, resp.GetBatchId()
}

// TestSameSeedTwoBatchesRollTheSameOutcomeSequenceByOrdinalPosition is the
// end-to-end reproducibility claim: two batches seeded with the same seed,
// through two independent Server instances (so neither can accidentally
// share in-memory state), must roll identical SUCCESS/FAILURE sequences
// when matched by ordinal position, even though every record_id and
// batch_id differs between the two runs. Covers the gap: seed.go's
// generation being seeded was never in question (seed_integration_test.go
// already proves that); this proves the ROLL is seeded too, all the way
// through Postgres and SimulateOutcome, not just the pure hash function in
// rand_test.go.
func TestSameSeedTwoBatchesRollTheSameOutcomeSequenceByOrdinalPosition(t *testing.T) {
	pool := testPool(t)
	const count = int32(25)
	const seed = int64(20260902)

	producer1, topic1 := testRawEventsProducer(t)
	s1 := New(pool, nil, clock.New(), noScale, producer1, topic1)
	ids1, batchID1 := seedBatchAndCollectRecordIDs(t, s1, "normal", count, seed, topic1)
	t.Cleanup(func() { cleanupBatch(t, pool, batchID1) })

	producer2, topic2 := testRawEventsProducer(t)
	s2 := New(pool, nil, clock.New(), noScale, producer2, topic2)
	ids2, batchID2 := seedBatchAndCollectRecordIDs(t, s2, "normal", count, seed, topic2)
	t.Cleanup(func() { cleanupBatch(t, pool, batchID2) })

	if len(ids1) != int(count) || len(ids2) != int(count) {
		t.Fatalf("got %d and %d record ids, want %d each", len(ids1), len(ids2), count)
	}
	for i := range ids1 {
		if ids1[i] == ids2[i] {
			t.Fatalf("ordinal %d: record ids are equal (%s); the two batches must generate distinct ids, or this test proves nothing", i, ids1[i])
		}
	}

	outcomes1 := rollAll(context.Background(), t, s1, ids1)
	outcomes2 := rollAll(context.Background(), t, s2, ids2)

	mismatches := 0
	for i := range outcomes1 {
		if outcomes1[i] != outcomes2[i] {
			mismatches++
			t.Errorf("ordinal %d: batch1 outcome %v (record %s), batch2 outcome %v (record %s), want equal for the same seed",
				i, outcomes1[i], ids1[i], outcomes2[i], ids2[i])
		}
	}
	if mismatches > 0 {
		t.Fatalf("%d/%d ordinal positions rolled a different outcome under the same seed", mismatches, count)
	}
}

// TestDifferentSeedsCanRollADifferentOutcomeSequence guards against the
// companion test above passing vacuously (e.g. every profile always
// succeeding regardless of the roll, which would make "identical
// sequences" true for a trivial, non-seeded reason too).
func TestDifferentSeedsCanRollADifferentOutcomeSequence(t *testing.T) {
	pool := testPool(t)
	const count = int32(25)

	producer1, topic1 := testRawEventsProducer(t)
	s1 := New(pool, nil, clock.New(), noScale, producer1, topic1)
	ids1, batchID1 := seedBatchAndCollectRecordIDs(t, s1, "normal", count, 111, topic1)
	t.Cleanup(func() { cleanupBatch(t, pool, batchID1) })

	producer2, topic2 := testRawEventsProducer(t)
	s2 := New(pool, nil, clock.New(), noScale, producer2, topic2)
	ids2, batchID2 := seedBatchAndCollectRecordIDs(t, s2, "normal", count, 222, topic2)
	t.Cleanup(func() { cleanupBatch(t, pool, batchID2) })

	outcomes1 := rollAll(context.Background(), t, s1, ids1)
	outcomes2 := rollAll(context.Background(), t, s2, ids2)

	for i := range outcomes1 {
		if outcomes1[i] != outcomes2[i] {
			return // found at least one divergence: the roll is not a constant
		}
	}
	t.Fatal("25/25 ordinal positions rolled the identical outcome under two different seeds; " +
		"either an extraordinary coincidence or the outcome does not actually depend on the roll")
}

// rollAll calls SimulateOutcome ACTION_TYPE_RETRY, attempt_number 1, for
// every id in order, and returns each Outcome. RETRY is never a nudge
// (bucket.go's isNudge), so this never touches the delayed-outcome queue.
func rollAll(ctx context.Context, t *testing.T, s *Server, ids []string) []commonv1.Outcome {
	t.Helper()
	out := make([]commonv1.Outcome, len(ids))
	for i, id := range ids {
		resp, err := s.SimulateOutcome(ctx, &worldsimv1.SimulateOutcomeRequest{
			RecordId: id, ActionType: commonv1.ActionType_ACTION_TYPE_RETRY, AttemptNumber: 1,
		})
		if err != nil {
			t.Fatalf("SimulateOutcome(%s): %v", id, err)
		}
		out[i] = resp.Outcome
	}
	return out
}
