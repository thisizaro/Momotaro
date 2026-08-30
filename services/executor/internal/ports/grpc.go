// Real, gRPC-backed implementations of RecoveryActionPort and
// NotificationPort, replacing Phase 1's in-process stubs (stub.go) with
// no change to internal/server or to the interfaces themselves
// (docs/ARCHITECTURE.md section 3b).
package ports

import (
	"context"
	"fmt"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
)

// WorldSimRecovery implements RecoveryActionPort against a real
// demo/world-simulator, over gRPC.
type WorldSimRecovery struct {
	client worldsimv1.WorldSimulatorServiceClient
}

func NewWorldSimRecovery(client worldsimv1.WorldSimulatorServiceClient) WorldSimRecovery {
	return WorldSimRecovery{client: client}
}

// SimulateOutcome translates worldsimv1.SimulateOutcomeResponse into a
// RecoveryAction. CostPaise is filled in here, not by World Simulator:
// the proto response carries no cost field by design (cost is a
// checked-in constant per docs/ARCHITECTURE.md section 5a's direct_cost
// term, not something "reality" reports back), matching how
// StubRecovery.SimulateOutcome already sets retryCostPaise itself.
// Non-RETRY actions cost nothing on this port: a nudge's cost is the
// notification's, reported by NotificationPort instead.
func (w WorldSimRecovery) SimulateOutcome(ctx context.Context, recordID string, action commonv1.ActionType, attemptNumber int32) (RecoveryAction, error) {
	resp, err := w.client.SimulateOutcome(ctx, &worldsimv1.SimulateOutcomeRequest{
		RecordId:      recordID,
		ActionType:    action,
		AttemptNumber: attemptNumber,
	})
	if err != nil {
		return RecoveryAction{}, fmt.Errorf("world simulator: %w", err)
	}

	var costPaise int64
	if action == commonv1.ActionType_ACTION_TYPE_RETRY {
		costPaise = retryCostPaise
	}

	var resolvesAt time.Time
	if !resp.GetImmediate() && resp.GetResolvesAt() != nil {
		resolvesAt = resp.GetResolvesAt().AsTime()
	}
	return RecoveryAction{
		Outcome:     resp.GetOutcome(),
		Immediate:   resp.GetImmediate(),
		ResolvesAt:  resolvesAt,
		FailureCode: resp.GetFailureCode(),
		CostPaise:   costPaise,
	}, nil
}

// NotificationSimAdapter implements NotificationPort against a real
// demo/notification-simulator, over gRPC.
type NotificationSimAdapter struct {
	client notifierv1.NotificationSimulatorServiceClient
}

func NewNotificationSimAdapter(client notifierv1.NotificationSimulatorServiceClient) NotificationSimAdapter {
	return NotificationSimAdapter{client: client}
}

// SimulateSend translates notifierv1.SimulateSendResponse into a
// Notification. Unlike the recovery port, the notification proto already
// carries its own cost_paise, so this is a direct field mapping.
func (n NotificationSimAdapter) SimulateSend(ctx context.Context, recordID string, channel notifierv1.Channel, message string) (Notification, error) {
	resp, err := n.client.SimulateSend(ctx, &notifierv1.SimulateSendRequest{
		RecordId: recordID,
		Channel:  channel,
		Message:  message,
	})
	if err != nil {
		return Notification{}, fmt.Errorf("notification simulator: %w", err)
	}
	return Notification{Sent: resp.GetSent(), CostPaise: resp.GetCostPaise()}, nil
}
