//go:build integration

// queue exercises real Redis rather than a mock, per
// docs/ENGINEERING.md section 1 ("do not mock what you own"). Needs the
// docker-compose stack up. Run with `make test-integration`.

package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func testRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() {
		client.Del(context.Background(), delayedOutcomesKey)
		client.Close()
	})
	// Each test starts from a clean set: a prior test's leftovers (or a
	// leftover from a real run against this same dev Redis) must not leak
	// into an unrelated test's due() count.
	client.Del(context.Background(), delayedOutcomesKey)
	return client
}

func TestQueueScheduleThenDueDeliversAndRemoves(t *testing.T) {
	client := testRedis(t)
	q := newQueue(client)
	ctx := context.Background()
	now := time.Now()

	d := delayedOutcome{RecordID: "rec-1", AttemptNumber: 2, Outcome: commonv1.Outcome_OUTCOME_SUCCESS}
	if err := q.schedule(ctx, d, now.Add(-time.Second)); err != nil {
		t.Fatalf("schedule: %v", err)
	}

	delivered, malformed, err := q.due(ctx, now)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(malformed) != 0 {
		t.Errorf("malformed = %v, want none", malformed)
	}
	if len(delivered) != 1 {
		t.Fatalf("len(delivered) = %d, want 1", len(delivered))
	}
	if delivered[0] != d {
		t.Errorf("delivered[0] = %+v, want %+v", delivered[0], d)
	}

	// Removed: a second poll at the same instant finds nothing left.
	delivered, _, err = q.due(ctx, now)
	if err != nil {
		t.Fatalf("second due: %v", err)
	}
	if len(delivered) != 0 {
		t.Errorf("second due delivered %d entries, want 0 (already removed)", len(delivered))
	}
}

func TestQueueDueOnlyReturnsEntriesAtOrBeforeNow(t *testing.T) {
	client := testRedis(t)
	q := newQueue(client)
	ctx := context.Background()
	now := time.Now()

	past := delayedOutcome{RecordID: "rec-past", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS}
	future := delayedOutcome{RecordID: "rec-future", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS}
	if err := q.schedule(ctx, past, now.Add(-time.Minute)); err != nil {
		t.Fatalf("schedule past: %v", err)
	}
	if err := q.schedule(ctx, future, now.Add(time.Hour)); err != nil {
		t.Fatalf("schedule future: %v", err)
	}

	delivered, _, err := q.due(ctx, now)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(delivered) != 1 || delivered[0].RecordID != "rec-past" {
		t.Fatalf("delivered = %+v, want only rec-past", delivered)
	}

	// The future entry must still be there, unaffected by the earlier poll.
	delivered, _, err = q.due(ctx, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("later due: %v", err)
	}
	if len(delivered) != 1 || delivered[0].RecordID != "rec-future" {
		t.Fatalf("later delivered = %+v, want only rec-future", delivered)
	}
}

func TestQueueDueRemovesAndReportsAMalformedMember(t *testing.T) {
	client := testRedis(t)
	q := newQueue(client)
	ctx := context.Background()
	now := time.Now()

	if err := client.ZAdd(ctx, delayedOutcomesKey, redis.Z{
		Score: float64(now.Add(-time.Second).Unix()), Member: "not-enough-fields",
	}).Err(); err != nil {
		t.Fatalf("seed malformed member: %v", err)
	}
	good := delayedOutcome{RecordID: "rec-good", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_FAILURE, FailureCode: "BANK_TIMEOUT"}
	if err := q.schedule(ctx, good, now.Add(-time.Second)); err != nil {
		t.Fatalf("schedule good: %v", err)
	}

	delivered, malformed, err := q.due(ctx, now)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(malformed) != 1 || malformed[0] != "not-enough-fields" {
		t.Fatalf("malformed = %v, want [not-enough-fields]", malformed)
	}
	if len(delivered) != 1 || delivered[0].RecordID != "rec-good" {
		t.Fatalf("delivered = %+v, want only rec-good", delivered)
	}

	// The malformed member must have been removed too, or every future
	// poll would report it again forever.
	card, err := client.ZCard(ctx, delayedOutcomesKey).Result()
	if err != nil {
		t.Fatalf("zcard: %v", err)
	}
	if card != 0 {
		t.Errorf("delayedOutcomesKey has %d members left, want 0", card)
	}
}

func TestMemberRoundTripsThroughParseMember(t *testing.T) {
	cases := []delayedOutcome{
		{RecordID: "rec-1", AttemptNumber: 3, Outcome: commonv1.Outcome_OUTCOME_SUCCESS},
		{RecordID: "rec-2", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_FAILURE, FailureCode: "CARD_EXPIRED"},
	}
	for _, d := range cases {
		got, err := parseMember(d.member())
		if err != nil {
			t.Fatalf("parseMember(%q): %v", d.member(), err)
		}
		if got != d {
			t.Errorf("round-trip %+v got %+v", d, got)
		}
	}
}
