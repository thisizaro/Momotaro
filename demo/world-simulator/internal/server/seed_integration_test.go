//go:build integration

// SeedBatch exercises real Postgres and real Kafka rather than a mock, per
// docs/ENGINEERING.md section 1 ("do not mock what you own"). Needs the
// docker-compose stack up. Run with `make test-integration`, or
// `go test -tags=integration ./demo/world-simulator/...`.

package server

import (
	"context"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSeedBatchWritesGroundTruthAndPublishesRawEvents(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	producer, topic := testRawEventsProducer(t)
	ctx := context.Background()

	s := New(pool, redisClient, clock.New(), noScale, producer, topic)

	const count = int32(20)
	resp, err := s.SeedBatch(ctx, &worldsimv1.SeedBatchRequest{Scenario: "dead-cards", Count: count, Seed: 42})
	if err != nil {
		t.Fatalf("SeedBatch: %v", err)
	}
	if resp.GetBatchId() == "" {
		t.Fatal("BatchId is empty")
	}
	if resp.GetGeneratedCount() != count {
		t.Errorf("GeneratedCount = %d, want %d", resp.GetGeneratedCount(), count)
	}
	if resp.GetSeed() != 42 {
		t.Errorf("Seed = %d, want 42 (echoed back)", resp.GetSeed())
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM ground_truth WHERE record_id IN (SELECT id FROM record WHERE batch_id = $1)`, resp.GetBatchId())
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE batch_id = $1`, resp.GetBatchId())
		_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, resp.GetBatchId())
	})

	var recordCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM record WHERE batch_id = $1`, resp.GetBatchId()).Scan(&recordCount); err != nil {
		t.Fatalf("count records: %v", err)
	}
	if recordCount != int(count) {
		t.Errorf("record count = %d, want %d", recordCount, count)
	}

	var groundTruthCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM ground_truth gt JOIN record r ON r.id = gt.record_id WHERE r.batch_id = $1`,
		resp.GetBatchId(),
	).Scan(&groundTruthCount); err != nil {
		t.Fatalf("count ground_truth: %v", err)
	}
	if groundTruthCount != int(count) {
		t.Errorf("ground_truth count = %d, want %d (every seeded record must carry hidden ground truth)", groundTruthCount, count)
	}

	// The scenario's concentration must actually show up in what got
	// written, not just in the in-memory generator (statistical, not exact:
	// dead-cards forces HARD_DECLINE 85% of the time).
	var concentratedCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM record r JOIN ground_truth gt ON gt.record_id = r.id
		WHERE r.batch_id = $1 AND gt.true_bucket = 'ROOT_CAUSE_BUCKET_HARD_DECLINE'
		  AND r.failure_code IN ('CARD_EXPIRED', 'DEBIT_INSTRUMENT_BLOCKED')`,
		resp.GetBatchId(),
	).Scan(&concentratedCount); err != nil {
		t.Fatalf("count concentrated records: %v", err)
	}
	if concentratedCount < int(count)/2 {
		t.Errorf("only %d/%d records concentrated on HARD_DECLINE with a real dead-card code, want a clear majority", concentratedCount, count)
	}

	events := consumeRawEvents(t, topic, int(count), 15*time.Second)
	if len(events) != int(count) {
		t.Fatalf("consumed %d raw.events messages, want %d", len(events), count)
	}
	for _, evt := range events {
		if evt.BatchID != resp.GetBatchId() {
			t.Errorf("published event has BatchID %q, want %q", evt.BatchID, resp.GetBatchId())
		}
		if evt.RecordID == "" {
			t.Error("published event has empty RecordID")
		}
	}
}

func TestSeedBatchSeedZeroPicksARandomSeedAndEchoesIt(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	producer, topic := testRawEventsProducer(t)
	ctx := context.Background()

	s := New(pool, redisClient, clock.New(), noScale, producer, topic)

	first, err := s.SeedBatch(ctx, &worldsimv1.SeedBatchRequest{Scenario: "normal", Count: 1})
	if err != nil {
		t.Fatalf("first SeedBatch: %v", err)
	}
	if first.GetSeed() == 0 {
		t.Error("Seed = 0, want a real picked seed echoed back")
	}
	t.Cleanup(func() { cleanupBatch(t, pool, first.GetBatchId()) })

	second, err := s.SeedBatch(ctx, &worldsimv1.SeedBatchRequest{Scenario: "normal", Count: 1})
	if err != nil {
		t.Fatalf("second SeedBatch: %v", err)
	}
	t.Cleanup(func() { cleanupBatch(t, pool, second.GetBatchId()) })

	if first.GetSeed() == second.GetSeed() {
		t.Error("two Seed:0 calls picked the identical seed; want independently random picks")
	}

	// Drain the two published events so they do not linger unconsumed
	// (harmless either way, but keeps the topic tidy for anything else
	// reading it during this test run).
	consumeRawEvents(t, topic, 2, 10*time.Second)
}

func TestSeedBatchExplicitSeedReproducesTheExactSameSequence(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	ctx := context.Background()

	const count = int32(10)

	producer1, topic1 := testRawEventsProducer(t)
	s1 := New(pool, redisClient, clock.New(), noScale, producer1, topic1)
	resp1, err := s1.SeedBatch(ctx, &worldsimv1.SeedBatchRequest{Scenario: "salary-day", Count: count, Seed: 555})
	if err != nil {
		t.Fatalf("first SeedBatch: %v", err)
	}
	t.Cleanup(func() { cleanupBatch(t, pool, resp1.GetBatchId()) })

	producer2, topic2 := testRawEventsProducer(t)
	s2 := New(pool, redisClient, clock.New(), noScale, producer2, topic2)
	resp2, err := s2.SeedBatch(ctx, &worldsimv1.SeedBatchRequest{Scenario: "salary-day", Count: count, Seed: 555})
	if err != nil {
		t.Fatalf("second SeedBatch: %v", err)
	}
	t.Cleanup(func() { cleanupBatch(t, pool, resp2.GetBatchId()) })

	events1 := consumeRawEvents(t, topic1, int(count), 15*time.Second)
	events2 := consumeRawEvents(t, topic2, int(count), 15*time.Second)

	for i := range events1 {
		// record_id and batch_id are fresh uuids each call and must differ;
		// everything the rng actually decided must match exactly.
		if events1[i].Type != events2[i].Type {
			t.Errorf("event %d: Type = %q vs %q, want equal for the same seed", i, events1[i].Type, events2[i].Type)
		}
		if events1[i].AmountPaise != events2[i].AmountPaise {
			t.Errorf("event %d: AmountPaise = %d vs %d, want equal for the same seed", i, events1[i].AmountPaise, events2[i].AmountPaise)
		}
		if events1[i].FailureCode != events2[i].FailureCode {
			t.Errorf("event %d: FailureCode = %q vs %q, want equal for the same seed", i, events1[i].FailureCode, events2[i].FailureCode)
		}
		// Not the exact InstrumentRef: syntheticgen.InstrumentRefPool mints
		// its pool with uuid.NewString(), which is crypto-random and not
		// derived from the seeded rng (internal/platform/syntheticgen.go),
		// so only whether an instrument was assigned at all is reproducible
		// for a fixed seed, not which literal ref it got.
		if (events1[i].InstrumentRef == "") != (events2[i].InstrumentRef == "") {
			t.Errorf("event %d: InstrumentRef assigned = %v vs %v, want the same pattern for the same seed",
				i, events1[i].InstrumentRef != "", events2[i].InstrumentRef != "")
		}
	}
}

func TestSeedBatchUnknownScenarioIsInvalidArgument(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	s := New(pool, redisClient, clock.New(), noScale, nil, "")

	_, err := s.SeedBatch(context.Background(), &worldsimv1.SeedBatchRequest{Scenario: "not-a-real-scenario", Count: 5})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SeedBatch with unknown scenario: err = %v, want InvalidArgument", err)
	}
}

func TestSeedBatchInvalidCountIsRejected(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	s := New(pool, redisClient, clock.New(), noScale, nil, "")

	for _, count := range []int32{0, -1, maxSeedBatchCount + 1} {
		_, err := s.SeedBatch(context.Background(), &worldsimv1.SeedBatchRequest{Scenario: "normal", Count: count})
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("SeedBatch with count=%d: err = %v, want InvalidArgument", count, err)
		}
	}
}

func TestSeedBatchWithNoProducerIsFailedPrecondition(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	s := New(pool, redisClient, clock.New(), noScale, nil, "")

	_, err := s.SeedBatch(context.Background(), &worldsimv1.SeedBatchRequest{Scenario: "normal", Count: 1})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SeedBatch with no producer configured: err = %v, want FailedPrecondition", err)
	}
}

// cleanupBatch deletes a seeded batch's rows so repeated test runs against
// a shared dev Postgres do not accumulate.
func cleanupBatch(t *testing.T, pool *pgxpkg.Pool, batchID string) {
	t.Helper()
	bg := context.Background()
	_, _ = pool.Exec(bg, `DELETE FROM ground_truth WHERE record_id IN (SELECT id FROM record WHERE batch_id = $1)`, batchID)
	_, _ = pool.Exec(bg, `DELETE FROM record WHERE batch_id = $1`, batchID)
	_, _ = pool.Exec(bg, `DELETE FROM batch WHERE id = $1`, batchID)
}
