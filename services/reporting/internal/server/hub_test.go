package server

import (
	"testing"
	"time"

	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
)

func TestHubDeliversAPublishedUpdateToASubscriber(t *testing.T) {
	h := NewHub()
	ch, unsubscribe := h.subscribe("batch-1")
	defer unsubscribe()

	want := &reportingv1.BatchUpdate{RecordId: "rec-1"}
	h.publish("batch-1", want)

	select {
	case got := <-ch:
		if got != want {
			t.Errorf("got %+v, want the exact published pointer %+v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the published update")
	}
}

func TestHubDeliversToEverySubscriberOfTheSameBatch(t *testing.T) {
	h := NewHub()
	ch1, unsub1 := h.subscribe("batch-1")
	defer unsub1()
	ch2, unsub2 := h.subscribe("batch-1")
	defer unsub2()

	h.publish("batch-1", &reportingv1.BatchUpdate{RecordId: "rec-1"})

	for i, ch := range []chan *reportingv1.BatchUpdate{ch1, ch2} {
		select {
		case got := <-ch:
			if got.GetRecordId() != "rec-1" {
				t.Errorf("subscriber %d: RecordId = %q, want rec-1", i, got.GetRecordId())
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for the published update", i)
		}
	}
}

// TestHubDoesNotCrossBatches is the whole point of keying by batch_id: a
// dashboard watching batch A must never see batch B's traffic.
func TestHubDoesNotCrossBatches(t *testing.T) {
	h := NewHub()
	chA, unsubA := h.subscribe("batch-A")
	defer unsubA()
	chB, unsubB := h.subscribe("batch-B")
	defer unsubB()

	h.publish("batch-A", &reportingv1.BatchUpdate{RecordId: "rec-A"})

	select {
	case got := <-chA:
		if got.GetRecordId() != "rec-A" {
			t.Errorf("chA got RecordId = %q, want rec-A", got.GetRecordId())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting on chA")
	}

	select {
	case got := <-chB:
		t.Fatalf("chB received %+v, want nothing: it never subscribed to batch-A", got)
	case <-time.After(100 * time.Millisecond):
		// Correct: batch-B's subscriber saw nothing.
	}
}

// TestHubPublishToNoSubscribersDoesNotBlockOrPanic is the case a live batch
// with zero connected dashboards hits on every single transition: the
// Kafka consumer loop that calls publish must never stall because nobody
// is watching.
func TestHubPublishToNoSubscribersDoesNotBlockOrPanic(t *testing.T) {
	h := NewHub()
	done := make(chan struct{})
	go func() {
		h.publish("nobody-is-watching", &reportingv1.BatchUpdate{RecordId: "rec-1"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish with no subscribers did not return")
	}
}

// TestHubUnsubscribeStopsFurtherDeliveryAndClosesTheChannel proves
// unsubscribe is a real cleanup, not just a courtesy: a StreamBatchUpdates
// call that returns (client disconnected) must actually stop receiving,
// or the Hub leaks a channel and a goroutine forever.
func TestHubUnsubscribeStopsFurtherDeliveryAndClosesTheChannel(t *testing.T) {
	h := NewHub()
	ch, unsubscribe := h.subscribe("batch-1")
	unsubscribe()

	if _, open := <-ch; open {
		t.Error("channel is still open after unsubscribe, want it closed")
	}

	// A publish after unsubscribe must not panic (sending on the closed
	// channel would), it must simply have nobody left to deliver to.
	h.publish("batch-1", &reportingv1.BatchUpdate{RecordId: "rec-1"})
}

// TestHubDropsRatherThanBlocksWhenASubscriberIsFull is the backpressure
// contract: one subscriber that stops reading (a slow or stuck browser
// tab) must never make publish block, which would stall the Kafka
// consumer loop for every other batch too.
func TestHubDropsRatherThanBlocksWhenASubscriberIsFull(t *testing.T) {
	h := NewHub()
	// Deliberately never read from the channel: the point of this test is
	// that nothing draining it does not make publish block.
	_, unsubscribe := h.subscribe("batch-1")
	defer unsubscribe()

	done := make(chan struct{})
	go func() {
		// One more than the buffer holds: the last publish must not block
		// even though nothing is draining ch.
		for i := 0; i < subscribeBufferSize+1; i++ {
			h.publish("batch-1", &reportingv1.BatchUpdate{RecordId: "rec-1"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish blocked with a full subscriber channel, want it to drop instead")
	}
}

func TestHubMultipleUnrelatedBatchesEachTrackedIndependently(t *testing.T) {
	h := NewHub()
	ch1, unsub1 := h.subscribe("batch-1")
	defer unsub1()

	h.publish("batch-2", &reportingv1.BatchUpdate{RecordId: "rec-2"})

	select {
	case got := <-ch1:
		t.Fatalf("batch-1's subscriber received %+v from a batch-2 publish", got)
	case <-time.After(100 * time.Millisecond):
		// Correct.
	}
}
