package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/auditevent"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %+v: %v", v, err)
	}
	return b
}

func testAuditMessage(t *testing.T, evt auditevent.Event) kafkax.Message {
	t.Helper()
	return kafkax.Message{Topic: auditevent.Topic, Key: evt.RecordID, Value: mustJSON(t, evt)}
}

func TestAuditConsumerPublishesADecodedEventToTheHub(t *testing.T) {
	h := NewHub()
	c := NewAuditConsumer(h, logger.Discard())

	ch, unsubscribe := h.subscribe("batch-1")
	defer unsubscribe()

	ts := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	msg := testAuditMessage(t, auditevent.Event{
		RecordID:            "rec-1",
		BatchID:             "batch-1",
		FromState:           commonv1.RecordState_RECORD_STATE_NEW.String(),
		ToState:             commonv1.RecordState_RECORD_STATE_RECOVERED.String(),
		RecoveredDeltaPaise: 5000,
		Timestamp:           ts,
	})

	if err := c.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	select {
	case update := <-ch:
		if update.GetRecordId() != "rec-1" {
			t.Errorf("RecordId = %q, want rec-1", update.GetRecordId())
		}
		if update.GetFromState() != commonv1.RecordState_RECORD_STATE_NEW {
			t.Errorf("FromState = %v, want NEW", update.GetFromState())
		}
		if update.GetToState() != commonv1.RecordState_RECORD_STATE_RECOVERED {
			t.Errorf("ToState = %v, want RECOVERED", update.GetToState())
		}
		if update.GetRecoveredDeltaPaise() != 5000 {
			t.Errorf("RecoveredDeltaPaise = %d, want 5000", update.GetRecoveredDeltaPaise())
		}
		if !update.GetTs().AsTime().Equal(ts) {
			t.Errorf("Ts = %v, want %v", update.GetTs().AsTime(), ts)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the Hub to receive the published update")
	}
}

// TestAuditConsumerSkipsMalformedMessagesWithoutError is the "never stop
// the consumer loop for one bad message" contract: kafkax.Consumer.Consume
// stops entirely on a handler error (its own doc comment: "there is no DLQ
// path yet either"), so a single malformed audit.events message must never
// propagate as an error here.
func TestAuditConsumerSkipsMalformedMessagesWithoutError(t *testing.T) {
	h := NewHub()
	c := NewAuditConsumer(h, logger.Discard())

	msg := kafkax.Message{Topic: auditevent.Topic, Key: "bad", Value: []byte("not json")}
	if err := c.HandleMessage(context.Background(), msg); err != nil {
		t.Errorf("HandleMessage on malformed payload: err = %v, want nil (skip, do not stop the consumer)", err)
	}
}

func TestAuditConsumerRoutesByBatchIDNotRecordID(t *testing.T) {
	h := NewHub()
	c := NewAuditConsumer(h, logger.Discard())

	chA, unsubA := h.subscribe("batch-A")
	defer unsubA()
	chB, unsubB := h.subscribe("batch-B")
	defer unsubB()

	msg := testAuditMessage(t, auditevent.Event{
		RecordID:  "rec-1",
		BatchID:   "batch-A",
		FromState: commonv1.RecordState_RECORD_STATE_NEW.String(),
		ToState:   commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED.String(),
		Timestamp: time.Now(),
	})
	if err := c.HandleMessage(context.Background(), msg); err != nil {
		t.Fatalf("HandleMessage: %v", err)
	}

	select {
	case <-chA:
	case <-time.After(time.Second):
		t.Fatal("batch-A's subscriber never received the update")
	}
	select {
	case got := <-chB:
		t.Fatalf("batch-B's subscriber received %+v from a batch-A event", got)
	case <-time.After(100 * time.Millisecond):
	}
}
