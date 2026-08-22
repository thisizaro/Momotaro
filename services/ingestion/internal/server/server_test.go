package server

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	ingestionv1 "github.com/thisizaro/Momotaro/proto/gen/ingestion/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func cleanupBatch(t *testing.T, pool *pgxpkg.Pool, batchID string) {
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM record WHERE batch_id = $1`, batchID)
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

func TestSubmitBatchCreatesBatchAndRecordAndPublishes(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	fake := clock.NewFake(time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC))
	s := New(pool, producer, fake, "raw.events")

	consumer, err := kafkax.NewConsumer(brokers(t), "ingestion-test-"+uuid.NewString(), []string{"raw.events"})
	if err != nil {
		t.Fatalf("NewConsumer: %v", err)
	}
	defer consumer.Close()

	ctx := context.Background()
	resp, err := s.SubmitBatch(ctx, &ingestionv1.SubmitBatchRequest{
		Source:  "test-suite",
		Records: []*ingestionv1.NewRecord{validRecord()},
	})
	if err != nil {
		t.Fatalf("SubmitBatch: %v", err)
	}
	cleanupBatch(t, pool, resp.BatchId)

	if resp.BatchId == "" {
		t.Fatal("BatchId is empty")
	}
	if resp.AcceptedCount != 1 {
		t.Errorf("AcceptedCount = %d, want 1", resp.AcceptedCount)
	}
	if len(resp.Rejected) != 0 {
		t.Errorf("Rejected = %v, want empty", resp.Rejected)
	}

	// BATCH row.
	var totalRecords int
	var source string
	if err := pool.QueryRow(ctx, `SELECT total_records, source FROM batch WHERE id=$1`, resp.BatchId).Scan(&totalRecords, &source); err != nil {
		t.Fatalf("query batch: %v", err)
	}
	if totalRecords != 1 || source != "test-suite" {
		t.Errorf("batch row = (total=%d, source=%q), want (1, test-suite)", totalRecords, source)
	}

	// RECORD row.
	var count int
	var amountPaise int64
	if err := pool.QueryRow(ctx, `SELECT count(*), max(amount_paise) FROM record WHERE batch_id=$1`, resp.BatchId).Scan(&count, &amountPaise); err != nil {
		t.Fatalf("query record: %v", err)
	}
	if count != 1 {
		t.Fatalf("record rows = %d, want 1", count)
	}
	if amountPaise != 50000 {
		t.Errorf("amount_paise = %d, want 50000", amountPaise)
	}

	// raw.events publish, key = record_id.
	msgCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	found := make(chan kafkax.Message, 1)
	go func() {
		_ = consumer.Consume(msgCtx, func(ctx context.Context, m kafkax.Message) error {
			var evt RawEvent
			if err := json.Unmarshal(m.Value, &evt); err == nil && evt.BatchID == resp.BatchId {
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
		if m.Key != evt.RecordID {
			t.Errorf("message key = %q, want record_id %q", m.Key, evt.RecordID)
		}
		if evt.AmountPaise != 50000 {
			t.Errorf("event AmountPaise = %d, want 50000", evt.AmountPaise)
		}
		if evt.FailureCode != "BANK_TIMEOUT" {
			t.Errorf("event FailureCode = %q, want BANK_TIMEOUT", evt.FailureCode)
		}
	case <-msgCtx.Done():
		t.Fatal("timed out waiting for the raw.events publish")
	}
}

func TestSubmitBatchRejectsInvalidRecordsButAcceptsTheRest(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	s := New(pool, producer, clock.New(), "raw.events")

	ctx := context.Background()
	resp, err := s.SubmitBatch(ctx, &ingestionv1.SubmitBatchRequest{
		Source: "test-suite",
		Records: []*ingestionv1.NewRecord{
			validRecord(),
			{Type: commonv1.RecordType_RECORD_TYPE_PAYMENT, AmountPaise: 0, FailureCode: "X"},       // invalid: zero amount
			{Type: commonv1.RecordType_RECORD_TYPE_UNSPECIFIED, AmountPaise: 100, FailureCode: "X"}, // invalid: no type
		},
	})
	if err != nil {
		t.Fatalf("SubmitBatch: %v", err)
	}
	cleanupBatch(t, pool, resp.BatchId)

	if resp.AcceptedCount != 1 {
		t.Errorf("AcceptedCount = %d, want 1", resp.AcceptedCount)
	}
	if len(resp.Rejected) != 2 {
		t.Fatalf("Rejected = %v, want 2 entries", resp.Rejected)
	}
	if _, ok := resp.Rejected[1]; !ok {
		t.Error("index 1 (zero amount) not reported as rejected")
	}
	if _, ok := resp.Rejected[2]; !ok {
		t.Error("index 2 (unspecified type) not reported as rejected")
	}
}

func TestSubmitBatchRejectsEmptyRequest(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	s := New(pool, producer, clock.New(), "raw.events")

	_, err := s.SubmitBatch(context.Background(), &ingestionv1.SubmitBatchRequest{Source: "test"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SubmitBatch with no records: err = %v, want InvalidArgument", err)
	}
}

func TestSubmitBatchDefaultsCurrencyToINR(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	s := New(pool, producer, clock.New(), "raw.events")

	rec := validRecord()
	rec.Currency = ""

	ctx := context.Background()
	resp, err := s.SubmitBatch(ctx, &ingestionv1.SubmitBatchRequest{Source: "t", Records: []*ingestionv1.NewRecord{rec}})
	if err != nil {
		t.Fatalf("SubmitBatch: %v", err)
	}
	cleanupBatch(t, pool, resp.BatchId)

	var currency string
	if err := pool.QueryRow(ctx, `SELECT currency FROM record WHERE batch_id=$1`, resp.BatchId).Scan(&currency); err != nil {
		t.Fatalf("query: %v", err)
	}
	if currency != "INR" {
		t.Errorf("currency = %q, want INR", currency)
	}
}

func TestSubmitEventIsUnimplemented(t *testing.T) {
	pool := testPool(t)
	producer := testProducer(t)
	s := New(pool, producer, clock.New(), "raw.events")

	_, err := s.SubmitEvent(context.Background(), &ingestionv1.SubmitEventRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("SubmitEvent: err = %v, want Unimplemented", err)
	}
}
