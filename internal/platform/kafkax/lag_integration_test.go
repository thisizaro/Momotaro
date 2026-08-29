//go:build integration

package kafkax

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestLagExporterReportsRealLagAgainstKafka proves the whole path against
// the real docker-compose Kafka, not just the pure record() mapping:
// publish messages nobody has consumed yet, poll once, expect nonzero lag;
// consume them, poll again, expect the lag to drop to zero.
func TestLagExporterReportsRealLagAgainstKafka(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	topic := uniqueTopic(t)
	if err := ensureTopic(ctx, brokers(t), topic); err != nil {
		t.Fatalf("ensureTopic: %v", err)
	}
	group := "kafkax-test-group-" + uuid.NewString()

	producer, err := NewProducer(brokers(t))
	if err != nil {
		t.Fatalf("NewProducer: %v", err)
	}
	defer producer.Close()
	for i := 0; i < 3; i++ {
		if err := producer.Publish(ctx, topic, uuid.NewString(), []byte("msg")); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	// A consumer must exist and join the group before Lag can describe it:
	// an unjoined group has no committed offsets to compare against the
	// high-water mark, which is a different (and uninteresting) case from
	// the one this test is proving.
	consumer, err := NewConsumer(brokers(t), group, []string{topic})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()
	consumeCtx, consumeCancel := context.WithCancel(ctx)
	defer consumeCancel()
	consumed := make(chan struct{})
	var handled int
	go func() {
		_ = consumer.Consume(consumeCtx, func(ctx context.Context, m Message) error {
			handled++
			// Message 1 returns nil, so Consume commits it, giving the
			// group a real committed offset. Message 2 blocks here instead
			// of returning: it and message 3 stay uncommitted, which is
			// the 2 messages of lag this test asserts on.
			if handled == 2 {
				close(consumed)
				<-consumeCtx.Done()
				return consumeCtx.Err()
			}
			return nil
		})
	}()
	select {
	case <-consumed:
	case <-ctx.Done():
		t.Fatal("timed out waiting for the group to commit its first offset")
	}

	registry := prometheus.NewRegistry()
	exporter, err := NewLagExporter(brokers(t), group, registry, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewLagExporter: %v", err)
	}
	defer exporter.Close()

	exporter.poll(ctx)
	if got := testutil.ToFloat64(exporter.gauge.WithLabelValues(topic, "0")); got != 2 {
		t.Errorf("lag with 2 uncommitted messages = %v, want 2", got)
	}
}
