package engine

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
)

// deadLetterPublisher owns turning a DeadLetter into bytes and getting it
// onto raw.events.dlq (docs/ARCHITECTURE.md section 8b): a record whose
// processing keeps failing for a non-transient reason is retried a bounded
// number of times, then dead-lettered so one poison record cannot wedge a
// partition. Records here are never counted as recovered, escalated, or
// uneconomic, they are processing failures, reported separately.
type deadLetterPublisher struct {
	producer *kafkax.Producer
	topic    string
}

func newDeadLetterPublisher(producer *kafkax.Producer, topic string) *deadLetterPublisher {
	return &deadLetterPublisher{producer: producer, topic: topic}
}

// Publish marshals dl and sends it keyed by RecordID when known, falling
// back to a random-ish key (the raw value itself) so even an unparseable
// payload still lands somewhere rather than failing to publish at all.
func (p *deadLetterPublisher) Publish(ctx context.Context, dl DeadLetter) error {
	key := dl.RecordID
	if key == "" {
		key = dl.RawValue
	}
	payload, err := json.Marshal(dl)
	if err != nil {
		return fmt.Errorf("marshal dead letter for %s: %w", key, err)
	}
	if err := p.producer.Publish(ctx, p.topic, key, payload); err != nil {
		return fmt.Errorf("publish dead letter for %s: %w", key, err)
	}
	return nil
}
