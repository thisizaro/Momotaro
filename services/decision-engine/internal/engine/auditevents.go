package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/auditevent"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// auditEventPublisher owns turning a record's committed state transition
// into bytes and getting it onto audit.events (docs/ARCHITECTURE.md
// sections 8, 10a): a notification, never a system of record. Publishing
// happens strictly after the owning Postgres transaction has already
// committed, and a publish failure is logged, never propagated as a
// failure of the caller's own operation -- the state change is real and
// durable either way; losing this message costs a stale cache or a missed
// live-dashboard tick, never a wrong number.
type auditEventPublisher struct {
	producer *kafkax.Producer
	topic    string
}

func newAuditEventPublisher(producer *kafkax.Producer, topic string) *auditEventPublisher {
	return &auditEventPublisher{producer: producer, topic: topic}
}

// Publish marshals and sends one transition, keyed by recordID so per-record
// ordering on the topic matches per-record ordering of the writes that
// produced it. recoveredPaise is the record's amount when to is RECOVERED,
// 0 otherwise (auditevent.Event.RecoveredDeltaPaise's own contract).
func (p *auditEventPublisher) Publish(ctx context.Context, recordID, batchID string, from, to commonv1.RecordState, recoveredPaise int64, now time.Time) error {
	evt := auditevent.Event{
		RecordID:            recordID,
		BatchID:             batchID,
		FromState:           from.String(),
		ToState:             to.String(),
		RecoveredDeltaPaise: recoveredPaise,
		Timestamp:           now,
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal audit event for %s: %w", recordID, err)
	}
	if err := p.producer.Publish(ctx, p.topic, recordID, payload); err != nil {
		return fmt.Errorf("publish audit event for %s: %w", recordID, err)
	}
	return nil
}

// recoveredDelta is the shared recoveredPaise computation every publish
// call site needs: the record's amount when it just recovered, 0
// otherwise.
func recoveredDelta(to commonv1.RecordState, amountPaise int64) int64 {
	if to == commonv1.RecordState_RECORD_STATE_RECOVERED {
		return amountPaise
	}
	return 0
}
