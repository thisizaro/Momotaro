//go:build integration

// InjectPoison exercises real Postgres and real Kafka rather than a mock,
// per docs/ENGINEERING.md section 1 ("do not mock what you own"). Needs
// the docker-compose stack up.

package server

import (
	"context"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInjectPoisonPublishesARecordIDWithNoRecordRow(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	producer, topic := testRawEventsProducer(t)
	ctx := context.Background()

	s := New(pool, redisClient, clock.New(), noScale, producer, topic)

	resp, err := s.InjectPoison(ctx, &worldsimv1.InjectPoisonRequest{})
	if err != nil {
		t.Fatalf("InjectPoison: %v", err)
	}
	if resp.GetRecordId() == "" {
		t.Fatal("RecordId is empty")
	}

	// The whole point: this id was never inserted into RECORD.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM record WHERE id = $1`, resp.GetRecordId()).Scan(&count); err != nil {
		t.Fatalf("count record: %v", err)
	}
	if count != 0 {
		t.Errorf("record row count for the poisoned id = %d, want 0 (it must not exist)", count)
	}

	events := consumeRawEvents(t, topic, 1, 15*time.Second)
	evt := events[0]
	if evt.RecordID != resp.GetRecordId() {
		t.Errorf("published RecordID = %q, want %q", evt.RecordID, resp.GetRecordId())
	}
	if evt.BatchID != resp.GetBatchId() {
		t.Errorf("published BatchID = %q, want %q", evt.BatchID, resp.GetBatchId())
	}
	if evt.FailureCode == "" {
		t.Error("published FailureCode is empty, want a real Razorpay code")
	}
	if evt.Type != commonv1.RecordType_RECORD_TYPE_PAYMENT.String() {
		t.Errorf("published Type = %q, want %q", evt.Type, commonv1.RecordType_RECORD_TYPE_PAYMENT.String())
	}
}

func TestInjectPoisonEachCallUsesAFreshRecordID(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	producer, topic := testRawEventsProducer(t)
	ctx := context.Background()
	s := New(pool, redisClient, clock.New(), noScale, producer, topic)

	first, err := s.InjectPoison(ctx, &worldsimv1.InjectPoisonRequest{})
	if err != nil {
		t.Fatalf("first InjectPoison: %v", err)
	}
	second, err := s.InjectPoison(ctx, &worldsimv1.InjectPoisonRequest{})
	if err != nil {
		t.Fatalf("second InjectPoison: %v", err)
	}
	if first.GetRecordId() == second.GetRecordId() {
		t.Error("two InjectPoison calls returned the same RecordId, want distinct ids")
	}

	consumeRawEvents(t, topic, 2, 15*time.Second)
}

func TestInjectPoisonWithNoProducerIsFailedPrecondition(t *testing.T) {
	pool := testPool(t)
	redisClient := testRedis(t)
	s := New(pool, redisClient, clock.New(), noScale, nil, "")

	_, err := s.InjectPoison(context.Background(), &worldsimv1.InjectPoisonRequest{})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("InjectPoison with no producer configured: err = %v, want FailedPrecondition", err)
	}
}
