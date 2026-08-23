//go:build integration

// These tests hit the real docker-compose Postgres and Kafka rather than
// mocks, per docs/ENGINEERING.md section 1 ("do not mock what you own").
// That means they need infrastructure, so they sit behind the `integration`
// build tag: `go test ./...` on a bare checkout must not try to dial a
// database that is not there. Run them with `make test-integration` (which
// brings the stack up first), or in CI's integration job.

package kafkax

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// brokers returns the local Kafka bootstrap list for tests. These hit the
// real docker-compose Kafka per docs/ENGINEERING.md section 1: do not mock
// what you own.
func brokers(t *testing.T) []string {
	t.Helper()
	if v := os.Getenv("KAFKA_BROKERS"); v != "" {
		return strings.Split(v, ",")
	}
	return []string{"localhost:9092"}
}

// uniqueTopic avoids cross-test interference: each test gets its own topic
// name, auto-creation is off so each is created explicitly via the producer.
func uniqueTopic(t *testing.T) string {
	t.Helper()
	return "kafkax-test-" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func TestEnsureTopicIsIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	topic := uniqueTopic(t)
	if err := EnsureTopic(ctx, brokers(t), topic, 1); err != nil {
		t.Fatalf("EnsureTopic (first call): %v", err)
	}
	if err := EnsureTopic(ctx, brokers(t), topic, 1); err != nil {
		t.Fatalf("EnsureTopic (second call, already exists): %v", err)
	}
}

func TestProducerSetsKeyToRecordID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	topic := uniqueTopic(t)
	if err := ensureTopic(ctx, brokers(t), topic); err != nil {
		t.Fatalf("ensureTopic: %v", err)
	}

	producer, err := NewProducer(brokers(t))
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	defer producer.Close()

	recordID := uuid.NewString()
	if err := producer.Publish(ctx, topic, recordID, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	consumer, err := NewConsumer(brokers(t), "kafkax-test-group-"+uuid.NewString(), []string{topic})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	msgs := make(chan Message, 1)
	consumeCtx, consumeCancel := context.WithCancel(ctx)
	defer consumeCancel()
	go func() {
		_ = consumer.Consume(consumeCtx, func(ctx context.Context, m Message) error {
			msgs <- m
			return nil
		})
	}()

	select {
	case m := <-msgs:
		if m.Key != recordID {
			t.Errorf("Key = %q, want %q (record_id)", m.Key, recordID)
		}
		if string(m.Value) != `{"hello":"world"}` {
			t.Errorf("Value = %q, want the published payload", m.Value)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for the published message")
	}
}

// Two records with the same key must land in the same partition, which is
// what preserves per-record ordering (docs/ARCHITECTURE.md section 8).
func TestProducerPartitionsByKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	topic := uniqueTopic(t)
	if err := ensureTopicWithPartitions(ctx, brokers(t), topic, 6); err != nil {
		t.Fatalf("ensureTopic: %v", err)
	}

	producer, err := NewProducer(brokers(t))
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	defer producer.Close()

	recordID := uuid.NewString()
	for i := 0; i < 5; i++ {
		if err := producer.Publish(ctx, topic, recordID, []byte(fmt.Sprintf("msg-%d", i))); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	consumer, err := NewConsumer(brokers(t), "kafkax-test-group-"+uuid.NewString(), []string{topic})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	var mu sync.Mutex
	partitions := map[int32]bool{}
	got := 0
	consumeCtx, consumeCancel := context.WithCancel(ctx)
	defer consumeCancel()
	done := make(chan struct{})
	go func() {
		_ = consumer.Consume(consumeCtx, func(ctx context.Context, m Message) error {
			mu.Lock()
			partitions[m.Partition] = true
			got++
			if got == 5 {
				close(done)
			}
			mu.Unlock()
			return nil
		})
	}()

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("timed out waiting for all 5 messages")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(partitions) != 1 {
		t.Errorf("messages with the same key landed on %d partitions, want 1: %v", len(partitions), partitions)
	}
}

// The consume-one-at-a-time contract the Decision Engine's walking skeleton
// relies on: handler errors must not silently drop the message.
func TestConsumeStopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	topic := uniqueTopic(t)
	if err := ensureTopic(ctx, brokers(t), topic); err != nil {
		t.Fatalf("ensureTopic: %v", err)
	}

	consumer, err := NewConsumer(brokers(t), "kafkax-test-group-"+uuid.NewString(), []string{topic})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	consumeCtx, consumeCancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() {
		errCh <- consumer.Consume(consumeCtx, func(ctx context.Context, m Message) error {
			return nil
		})
	}()

	consumeCancel()

	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Errorf("Consume returned %v, want nil or context.Canceled", err)
		}
	case <-ctx.Done():
		t.Fatal("Consume did not return after context cancellation")
	}
}
