package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
)

// RawEvent is the raw.events wire payload. There is no proto for this: the
// topic sits inside the cluster only, between Ingestion (producer) and
// Decision Engine (consumer), and docs/ARCHITECTURE.md section 9's proto
// discipline applies to gRPC contracts, not internal topic payloads.
//
// The mirror of this type lives in
// services/decision-engine/internal/engine; the two must be kept
// structurally in sync by hand until this is promoted to a real contract.
type RawEvent struct {
	RecordID      string    `json:"record_id"`
	BatchID       string    `json:"batch_id"`
	Type          string    `json:"type"`
	AmountPaise   int64     `json:"amount_paise"`
	Currency      string    `json:"currency"`
	FailureCode   string    `json:"failure_code"`
	InstrumentRef string    `json:"instrument_ref"`
	CreatedAt     time.Time `json:"created_at"`

	// Razorpay's four-field error taxonomy (docs/PHASE5_5_IMPLEMENTATION.md
	// Unit Z), all optional, all open strings: see common.v1.Record's proto
	// comment for why none of these is a closed enum.
	ErrorCode        string `json:"error_code,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
	ErrorSource      string `json:"error_source,omitempty"`
	ErrorStep        string `json:"error_step,omitempty"`
	ErrorReason      string `json:"error_reason,omitempty"`
}

// rawEventPublisher owns turning a RawEvent into bytes and getting it onto
// Kafka. Isolated in its own type so server.go reads as "build the event,
// publish it" rather than a JSON-plus-Kafka block inlined into the handler
// (docs/ENGINEERING.md section 14).
type rawEventPublisher struct {
	producer *kafkax.Producer
	topic    string
}

func newRawEventPublisher(producer *kafkax.Producer, topic string) *rawEventPublisher {
	return &rawEventPublisher{producer: producer, topic: topic}
}

// Publish marshals evt and sends it keyed by RecordID, so per-record
// ordering holds downstream (docs/ARCHITECTURE.md section 8).
func (p *rawEventPublisher) Publish(ctx context.Context, evt RawEvent) error {
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal raw event for record %s: %w", evt.RecordID, err)
	}
	if err := p.producer.Publish(ctx, p.topic, evt.RecordID, payload); err != nil {
		return fmt.Errorf("publish raw event for record %s: %w", evt.RecordID, err)
	}
	return nil
}
