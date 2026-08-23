//go:build integration

package server

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testRollingBatchSource returns a source name unique to this test, so
// concurrent test runs never fight over (or leave junk behind in) the same
// rolling batch row. Production always uses the fixed "webhook" source
// (docs/DECISIONS.md).
func testRollingBatchSource(t *testing.T) string {
	t.Helper()
	return "webhook-test-" + uuid.NewString()
}

func TestSubmitEventCreatesRecordAndPublishes(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	s := New(pool, producer, clock.New(), "raw.events", testRollingBatchSource(t))

	consumer, err := kafkax.NewConsumer(brokers(t), "ingestion-test-"+uuid.NewString(), []string{"raw.events"})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	ctx := context.Background()
	resp, err := s.SubmitEvent(ctx, &ingestionv1.SubmitEventRequest{Record: validRecord()})
	if err != nil {
		t.Fatalf("SubmitEvent: %v", err)
	}
	cleanupRollingBatch(t, pool, resp.BatchId, resp.RecordId)

	if resp.RecordId == "" {
		t.Fatal("RecordId is empty")
	}
	if resp.BatchId == "" {
		t.Fatal("BatchId is empty")
	}
	if resp.Deduplicated {
		t.Error("Deduplicated = true, want false for a first-time event")
	}

	var amountPaise int64
	if err := pool.QueryRow(ctx, `SELECT amount_paise FROM record WHERE id=$1`, resp.RecordId).Scan(&amountPaise); err != nil {
		t.Fatalf("query record: %v", err)
	}
	if amountPaise != 50000 {
		t.Errorf("amount_paise = %d, want 50000", amountPaise)
	}

	msgCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	found := make(chan kafkax.Message, 1)
	go func() {
		_ = consumer.Consume(msgCtx, func(ctx context.Context, m kafkax.Message) error {
			if m.Key == resp.RecordId {
				found <- m
			}
			return nil
		})
	}()

	select {
	case m := <-found:
		var evt RawEvent
		if err := json.Unmarshal(m.Value, &evt); err != nil {
			t.Fatalf("unmarshal raw event: %v", err)
		}
		if evt.BatchID != resp.BatchId {
			t.Errorf("event BatchID = %q, want %q", evt.BatchID, resp.BatchId)
		}
	case <-msgCtx.Done():
		t.Fatal("timed out waiting for the raw.events publish")
	}
}

func TestSubmitEventReusesTheSameRollingBatchAcrossCalls(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	source := testRollingBatchSource(t)
	s := New(pool, producer, clock.New(), "raw.events", source)

	ctx := context.Background()
	resp1, err := s.SubmitEvent(ctx, &ingestionv1.SubmitEventRequest{Record: validRecord()})
	if err != nil {
		t.Fatalf("SubmitEvent #1: %v", err)
	}
	resp2, err := s.SubmitEvent(ctx, &ingestionv1.SubmitEventRequest{Record: validRecord()})
	if err != nil {
		t.Fatalf("SubmitEvent #2: %v", err)
	}
	cleanupRollingBatch(t, pool, resp1.BatchId, resp1.RecordId, resp2.RecordId)

	if resp1.BatchId != resp2.BatchId {
		t.Errorf("BatchId #1 = %q, BatchId #2 = %q, want the same rolling batch", resp1.BatchId, resp2.BatchId)
	}
	if resp1.RecordId == resp2.RecordId {
		t.Error("two separate events got the same RecordId")
	}

	var totalRecords int
	if err := pool.QueryRow(ctx, `SELECT total_records FROM batch WHERE id=$1`, resp1.BatchId).Scan(&totalRecords); err != nil {
		t.Fatalf("query batch: %v", err)
	}
	if totalRecords != 2 {
		t.Errorf("total_records = %d, want 2", totalRecords)
	}
}

func TestSubmitEventDeduplicatesRepeatedIdempotencyKey(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	s := New(pool, producer, clock.New(), "raw.events", testRollingBatchSource(t))

	consumer, err := kafkax.NewConsumer(brokers(t), "ingestion-test-"+uuid.NewString(), []string{"raw.events"})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	ctx := context.Background()
	req := &ingestionv1.SubmitEventRequest{Record: validRecord(), IdempotencyKey: "evt-" + uuid.NewString()}

	resp1, err := s.SubmitEvent(ctx, req)
	if err != nil {
		t.Fatalf("SubmitEvent #1: %v", err)
	}
	cleanupRollingBatch(t, pool, resp1.BatchId, resp1.RecordId)

	resp2, err := s.SubmitEvent(ctx, req)
	if err != nil {
		t.Fatalf("SubmitEvent #2 (retry): %v", err)
	}

	if resp2.RecordId != resp1.RecordId {
		t.Errorf("retry RecordId = %q, want the original %q", resp2.RecordId, resp1.RecordId)
	}
	if resp2.BatchId != resp1.BatchId {
		t.Errorf("retry BatchId = %q, want the original %q", resp2.BatchId, resp1.BatchId)
	}
	if !resp2.Deduplicated {
		t.Error("Deduplicated = false on the retry, want true")
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM record WHERE idempotency_key = $1`, req.IdempotencyKey).Scan(&count); err != nil {
		t.Fatalf("query record count: %v", err)
	}
	if count != 1 {
		t.Errorf("record rows for this idempotency_key = %d, want 1 (no duplicate created)", count)
	}

	// The retry must not republish: only one raw.events message for this key.
	// Consume runs for the whole poll window and is only safe to drain after
	// it returns, so wait on it via a WaitGroup rather than a fixed sleep
	// (docs/ENGINEERING.md section 1: no time.Sleep in tests).
	msgCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	matches := make(chan kafkax.Message, 4)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = consumer.Consume(msgCtx, func(ctx context.Context, m kafkax.Message) error {
			if m.Key == resp1.RecordId {
				matches <- m
			}
			return nil
		})
	}()
	wg.Wait()
	close(matches)

	seen := 0
	for range matches {
		seen++
	}
	if seen > 1 {
		t.Errorf("saw %d raw.events messages for the deduplicated record, want at most 1", seen)
	}
}

func TestSubmitEventWithNoIdempotencyKeyNeverDeduplicates(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	source := testRollingBatchSource(t)
	s := New(pool, producer, clock.New(), "raw.events", source)

	ctx := context.Background()
	resp1, err := s.SubmitEvent(ctx, &ingestionv1.SubmitEventRequest{Record: validRecord()})
	if err != nil {
		t.Fatalf("SubmitEvent #1: %v", err)
	}
	resp2, err := s.SubmitEvent(ctx, &ingestionv1.SubmitEventRequest{Record: validRecord()})
	if err != nil {
		t.Fatalf("SubmitEvent #2: %v", err)
	}
	cleanupRollingBatch(t, pool, resp1.BatchId, resp1.RecordId, resp2.RecordId)

	if resp1.RecordId == resp2.RecordId {
		t.Fatal("two keyless events collapsed into the same record")
	}
	if resp1.Deduplicated || resp2.Deduplicated {
		t.Error("a keyless event must never report Deduplicated = true")
	}
}

func TestSubmitEventRejectsInvalidRecord(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	s := New(pool, producer, clock.New(), "raw.events", testRollingBatchSource(t))

	_, err := s.SubmitEvent(context.Background(), &ingestionv1.SubmitEventRequest{
		Record: &ingestionv1.NewRecord{Type: commonv1.RecordType_RECORD_TYPE_UNSPECIFIED, AmountPaise: 100, FailureCode: "X"},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SubmitEvent with unspecified type: err = %v, want InvalidArgument", err)
	}
}

func TestSubmitEventRejectsMissingRecord(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	s := New(pool, producer, clock.New(), "raw.events", testRollingBatchSource(t))

	_, err := s.SubmitEvent(context.Background(), &ingestionv1.SubmitEventRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SubmitEvent with no record: err = %v, want InvalidArgument", err)
	}
}
