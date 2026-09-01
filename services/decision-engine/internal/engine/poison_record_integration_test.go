//go:build integration

// Unit U's regression test (docs/PHASE5_5_IMPLEMENTATION.md "Unit U",
// docs/INCIDENTS.md 2026-08-31 "one orphaned Kafka message permanently
// wedges the decision-engine"): a raw.events message referencing a record
// that no longer exists made loadAttemptHistory return pgx.ErrNoRows,
// HandleMessage returned that as an error, and kafkax.ConsumeKeyed's
// contract (any handler error is an infrastructure failure) stopped the
// whole consumer, permanently. Restarting just consumed the next poisoned
// offset and died again.
//
// This has to run the real kafkax.ConsumeKeyed loop, not just call
// HandleMessage directly: the bug is specifically about the LOOP surviving
// a poison message and going on to the next one, which calling
// HandleMessage once in isolation cannot prove either way.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// TestConsumeKeyedDeadLettersMissingRecordAndKeepsGoing publishes one
// poison message (a record_id with no RECORD row) followed by one entirely
// normal message, onto a real, isolated raw.events-shaped topic, and
// requires all three things the fix promises:
//
//  1. the poison message is dead-lettered with a reason, not dropped or
//     left to crash the consumer;
//  2. its offset is actually committed, proven by a fresh consumer in the
//     same group seeing nothing outstanding, not merely inferred from
//     processing having continued;
//  3. the loop goes on to successfully process the message right behind
//     it.
//
// That third point is the one that matters most. Before the fix, this test
// hangs (until the context deadline) waiting for the good record to reach
// RETRY_SCHEDULED, because ConsumeKeyed already exited on the poison
// message and nothing is left running to process anything behind it.
func TestConsumeKeyedDeadLettersMissingRecordAndKeepsGoing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := testPool(t)
	dlqProducer, dlqTopic, auditTopic := testProducer(t)

	// An isolated topic standing in for raw.events, same reason every other
	// test in this package uses one (testProducer's dlqTopic/auditTopic):
	// tests in this package can run concurrently and must not share state,
	// and this repo has a whole README note (added by this unit) about what
	// happens when a test suite's cleanup shares raw.events with a live
	// consumer.
	rawTopic := uniqueTopic(t)
	if err := kafkax.EnsureTopic(ctx, brokers(t), rawTopic, 1); err != nil {
		t.Fatalf("EnsureTopic: %v", err)
	}
	rawProducer, err := kafkax.NewProducer(brokers(t))
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	defer rawProducer.Close()

	group := "decision-engine-poison-test-" + uuid.NewString()
	classifier := retryClassifier()
	e := New(pool, classifier, &fakeExecutor{}, dlqProducer, clock.New(), testEconomics(t), testConfig(dlqTopic, auditTopic))

	// Scoped so the first consumer is fully closed (and has left the
	// group) before the verification pass below joins the same group.
	func() {
		consumer, err := kafkax.NewConsumer(brokers(t), group, []string{rawTopic})
		if err != nil {
			t.Fatalf("NewConsumer: %v", err)
		}
		defer consumer.Close()

		consumeCtx, consumeCancel := context.WithCancel(ctx)
		defer consumeCancel()
		consumeReturned := make(chan error, 1)
		go func() {
			consumeReturned <- consumer.ConsumeKeyed(consumeCtx, 4, e.HandleMessage)
		}()

		// The poison message: a syntactically fine raw.events payload for a
		// record_id that was never inserted into RECORD. This is exactly
		// what an integration test's own cleanup leaves behind against a
		// live stack (docs/INCIDENTS.md 2026-08-31).
		missingRecordID := uuid.NewString()
		poison := RawEvent{
			RecordID:    missingRecordID,
			BatchID:     uuid.NewString(),
			Type:        "RECORD_TYPE_PAYMENT",
			AmountPaise: 10000,
			FailureCode: "BANK_TIMEOUT",
			CreatedAt:   time.Now(),
		}
		poisonBytes, err := json.Marshal(poison)
		if err != nil {
			t.Fatalf("marshal poison event: %v", err)
		}
		if err := rawProducer.Publish(ctx, rawTopic, missingRecordID, poisonBytes); err != nil {
			t.Fatalf("publish poison message: %v", err)
		}

		// A perfectly normal message right behind it, for a record that
		// really exists.
		batchID, goodRecordID := seedRecord(context.Background(), t, pool)
		good := RawEvent{
			RecordID:    goodRecordID,
			BatchID:     batchID,
			Type:        "RECORD_TYPE_PAYMENT",
			AmountPaise: 10000,
			FailureCode: "BANK_TIMEOUT",
			CreatedAt:   time.Now(),
		}
		goodBytes, err := json.Marshal(good)
		if err != nil {
			t.Fatalf("marshal good event: %v", err)
		}
		if err := rawProducer.Publish(ctx, rawTopic, goodRecordID, goodBytes); err != nil {
			t.Fatalf("publish good message: %v", err)
		}

		// (1) Proves the poison message was dead-lettered rather than
		// crashing the loop.
		dl := waitForDeadLetter(t, dlqTopic, missingRecordID, 15*time.Second)
		if dl.FailureReason == "" {
			t.Error("dead letter has no FailureReason")
		}
		if dl.RecordID != missingRecordID {
			t.Errorf("dead letter RecordID = %q, want %q", dl.RecordID, missingRecordID)
		}

		// (3) Proves the loop survived it and went on to process the next
		// message. Before the fix this poll never succeeds: the consumer
		// already exited on the poison message, so it spins until ctx's
		// deadline and the test fails there instead.
		waitForRecordState(ctx, t, pool, goodRecordID, commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED)

		// Direct proof this never touched ConsumeKeyed's fatal path: with
		// the bug, ConsumeKeyed would already have returned a non-nil error
		// well before this point.
		select {
		case err := <-consumeReturned:
			t.Fatalf("ConsumeKeyed exited early with err=%v; the poison message killed the loop instead of being dead-lettered", err)
		default:
		}

		consumeCancel()
		select {
		case err := <-consumeReturned:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("ConsumeKeyed returned %v after cancel, want context.Canceled", err)
			}
		case <-ctx.Done():
			t.Fatal("ConsumeKeyed did not return after cancellation")
		}
	}()

	// (2) The offset-commit half of the claim: a fresh consumer in the SAME
	// group must see nothing outstanding on this topic, which is what
	// proves both messages' offsets were actually committed rather than
	// merely processed in memory (mirrors
	// internal/platform/kafkax.TestConsumeKeyedCommitsSoRedeliveryDoesNotHappen's
	// shape).
	verifyConsumer, err := kafkax.NewConsumer(brokers(t), group, []string{rawTopic})
	if err != nil {
		t.Fatalf("NewConsumer (verify pass): %v", err)
	}
	defer verifyConsumer.Close()

	redelivered := make(chan kafkax.Message, 1)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer waitCancel()
	go func() {
		_ = verifyConsumer.ConsumeKeyed(waitCtx, 4, func(ctx context.Context, m kafkax.Message) error {
			redelivered <- m
			return nil
		})
	}()

	select {
	case m := <-redelivered:
		t.Fatalf("message redelivered after a clean pass: %+v; an offset was not committed", m)
	case <-waitCtx.Done():
		// Expected: nothing arrived.
	}
}

// waitForRecordState polls record_state until recordID reaches want, or
// fails the test when ctx is done. A ticker synchronized on ctx.Done rather
// than a single blind wait (docs/ENGINEERING.md section 1: no
// time.Sleep-and-hope in tests): the fact under test is a Postgres row, not
// a channel send, so there is nothing else to synchronize on directly.
func waitForRecordState(ctx context.Context, t *testing.T, pool *pgxpkg.Pool, recordID string, want commonv1.RecordState) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			var state string
			err := pool.QueryRow(context.Background(), `SELECT current_state FROM record_state WHERE record_id=$1`, recordID).Scan(&state)
			if err == nil {
				if state != want.String() {
					t.Fatalf("record_state for %s = %q, want %q", recordID, state, want.String())
				}
				return
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for a record_state row for %s (want %s); the consumer likely died on the poison message before ever reaching it", recordID, want.String())
		}
	}
}
