//go:build integration

// Shared fixtures for the integration-tagged tests in this package.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
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

func uniqueTopic(t *testing.T) string {
	t.Helper()
	return "world-simulator-test-" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// testRawEventsProducer provisions one Kafka producer and one isolated
// scratch topic standing in for raw.events, so a test's own SeedBatch/
// InjectPoison publishes never collide with another test or another
// agent's run against the shared Kafka (docs/INCIDENTS.md 2026-08-23).
func testRawEventsProducer(t *testing.T) (producer *kafkax.Producer, topic string) {
	t.Helper()
	topic = uniqueTopic(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := kafkax.EnsureTopic(ctx, brokers(t), topic, 1); err != nil {
		t.Fatalf("EnsureTopic %s: %v", topic, err)
	}
	p, err := kafkax.NewProducer(brokers(t))
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	t.Cleanup(p.Close)
	return p, topic
}

// consumeRawEvents reads exactly n messages from topic, in publish order
// (a single-partition topic and Consumer.Consume's own one-at-a-time,
// in-fetch-order contract), decodes each as rawEvent, and fails the test if
// timeout elapses first.
func consumeRawEvents(t *testing.T, topic string, n int, timeout time.Duration) []rawEvent {
	t.Helper()
	group := "world-simulator-test-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	consumer, err := kafkax.NewConsumer(brokers(t), group, []string{topic})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var mu sync.Mutex
	var got []rawEvent
	done := make(chan error, 1)
	go func() {
		done <- consumer.Consume(ctx, func(_ context.Context, m kafkax.Message) error {
			var evt rawEvent
			if err := json.Unmarshal(m.Value, &evt); err != nil {
				return err
			}
			mu.Lock()
			got = append(got, evt)
			reached := len(got) >= n
			mu.Unlock()
			if reached {
				cancel()
			}
			return nil
		})
	}()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Consume: %v", err)
		}
	case <-ctx.Done():
		<-done
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) < n {
		t.Fatalf("consumed %d messages from %s within %v, want %d", len(got), topic, timeout, n)
	}
	return got[:n]
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
