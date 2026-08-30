//go:build integration

// StreamBatchUpdates against real Postgres for batchExists, matching this
// package's other integration tests. The fan-out itself (hub_test.go) and
// the Kafka-to-Hub translation (consume_test.go) are proven separately
// without infra, since neither depends on Postgres; this file proves the
// third piece, the gRPC-facing half, and that all three actually connect.

package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeStreamBatchUpdatesServer is a minimal stand-in for the generated
// grpc.ServerStreamingServer[StreamBatchUpdatesResponse]: only Send and
// Context are overridden, since StreamBatchUpdates's handler never calls
// anything else on the embedded grpc.ServerStream (SetHeader, SendHeader,
// SetTrailer, SendMsg, RecvMsg).
type fakeStreamBatchUpdatesServer struct {
	grpc.ServerStream
	ctx context.Context

	mu      sync.Mutex
	sent    []*reportingv1.BatchUpdate
	sendErr error
}

func (f *fakeStreamBatchUpdatesServer) Context() context.Context { return f.ctx }

func (f *fakeStreamBatchUpdatesServer) Send(resp *reportingv1.StreamBatchUpdatesResponse) error {
	if f.sendErr != nil {
		return f.sendErr
	}
	f.mu.Lock()
	f.sent = append(f.sent, resp.GetUpdate())
	f.mu.Unlock()
	return nil
}

func (f *fakeStreamBatchUpdatesServer) received() []*reportingv1.BatchUpdate {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*reportingv1.BatchUpdate, len(f.sent))
	copy(out, f.sent)
	return out
}

func TestStreamBatchUpdatesRejectsMissingBatchID(t *testing.T) {
	pool := testPool(t)
	s := New(pool, NewHub())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.StreamBatchUpdates(&reportingv1.StreamBatchUpdatesRequest{}, &fakeStreamBatchUpdatesServer{ctx: ctx})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("StreamBatchUpdates with no batch_id: err = %v, want InvalidArgument", err)
	}
}

func TestStreamBatchUpdatesRejectsUnknownBatch(t *testing.T) {
	pool := testPool(t)
	s := New(pool, NewHub())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := &reportingv1.StreamBatchUpdatesRequest{BatchId: uuid.NewString()}
	err := s.StreamBatchUpdates(req, &fakeStreamBatchUpdatesServer{ctx: ctx})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("StreamBatchUpdates for an unknown batch: err = %v, want NotFound", err)
	}
}

// TestStreamBatchUpdatesDeliversPublishedUpdatesUntilTheClientDisconnects
// is the real assembly proof: a subscription made through the actual gRPC
// handler receives what the Hub actually publishes, in order, and the
// handler returns cleanly when the client's context is cancelled (a
// dashboard tab closing), rather than blocking forever or erroring.
func TestStreamBatchUpdatesDeliversPublishedUpdatesUntilTheClientDisconnects(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	batchID := seedBatch(ctx, t, pool)

	h := NewHub()
	s := New(pool, h)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	fake := &fakeStreamBatchUpdatesServer{ctx: streamCtx}

	streamDone := make(chan error, 1)
	go func() {
		streamDone <- s.StreamBatchUpdates(&reportingv1.StreamBatchUpdatesRequest{BatchId: batchID}, fake)
	}()

	// Give StreamBatchUpdates a moment to reach Hub.subscribe before
	// publishing, or the publish could arrive before anyone is listening
	// and be correctly, but unhelpfully, dropped.
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.mu.Lock()
		subscribed := len(h.subs[batchID]) > 0
		h.mu.Unlock()
		if subscribed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("StreamBatchUpdates never subscribed to the Hub")
		}
		time.Sleep(10 * time.Millisecond)
	}

	h.publish(batchID, &reportingv1.BatchUpdate{RecordId: "rec-1", ToState: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED})
	h.publish(batchID, &reportingv1.BatchUpdate{RecordId: "rec-1", ToState: commonv1.RecordState_RECORD_STATE_RECOVERED, RecoveredDeltaPaise: 5000})

	deadline = time.Now().Add(2 * time.Second)
	for len(fake.received()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	got := fake.received()
	if len(got) != 2 {
		t.Fatalf("received %d updates, want 2: %+v", len(got), got)
	}
	if got[0].GetToState() != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED {
		t.Errorf("update[0].ToState = %v, want RETRY_SCHEDULED", got[0].GetToState())
	}
	if got[1].GetToState() != commonv1.RecordState_RECORD_STATE_RECOVERED || got[1].GetRecoveredDeltaPaise() != 5000 {
		t.Errorf("update[1] = %+v, want ToState RECOVERED and RecoveredDeltaPaise 5000", got[1])
	}

	cancelStream()
	select {
	case err := <-streamDone:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("StreamBatchUpdates returned %v after client disconnect, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("StreamBatchUpdates did not return after the client's context was cancelled")
	}
}
