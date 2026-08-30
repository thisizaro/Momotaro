package server

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/thisizaro/Momotaro/internal/platform/auditevent"
	"github.com/thisizaro/Momotaro/internal/platform/kafkax"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AuditConsumer turns audit.events messages into Hub publishes
// (docs/ARCHITECTURE.md section 6a). The Kafka-facing half of
// StreamBatchUpdates; server.go's StreamBatchUpdates handler is the
// gRPC-facing half, and the two meet only at the shared *Hub. Exported
// (unlike Hub's other internals) because cmd/main.go, a different package,
// constructs one to wire into a kafkax.Consumer.
type AuditConsumer struct {
	hub *Hub
	log *slog.Logger
}

// NewAuditConsumer returns an AuditConsumer publishing to h.
func NewAuditConsumer(h *Hub, log *slog.Logger) *AuditConsumer {
	return &AuditConsumer{hub: h, log: log}
}

// HandleMessage is the handler kafkax.Consumer.Consume calls per record.
// It never returns an error for a bad payload: Consume's own contract is
// that a handler error stops the whole consumer loop (there is no DLQ path
// for this topic, matching audit.events' own best-effort, notification-only
// status, docs/ARCHITECTURE.md section 10a), so one malformed message must
// be skipped and logged, not treated as a reason to stop consuming every
// subsequent one.
func (c *AuditConsumer) HandleMessage(ctx context.Context, msg kafkax.Message) error {
	var evt auditevent.Event
	if err := json.Unmarshal(msg.Value, &evt); err != nil {
		c.log.Warn("skipping malformed audit event", "err", err, "key", msg.Key)
		return nil
	}

	c.hub.publish(evt.BatchID, &reportingv1.BatchUpdate{
		RecordId:            evt.RecordID,
		FromState:           commonv1.RecordState(commonv1.RecordState_value[evt.FromState]),
		ToState:             commonv1.RecordState(commonv1.RecordState_value[evt.ToState]),
		Ts:                  timestamppb.New(evt.Timestamp),
		RecoveredDeltaPaise: evt.RecoveredDeltaPaise,
	})
	return nil
}
