// Package ports is the Executor's only door to the outside world.
//
// docs/ARCHITECTURE.md section 3b: "The Executor never calls 'the World
// Simulator' or 'the Notification Simulator' by name in its own code, it
// depends on two small interfaces." These are those interfaces, shaped to
// mirror the proto services that will implement them
// (proto/worldsim/v1, proto/notifier/v1) so the Phase 5 adapter is a thin
// translation rather than a redesign.
//
// Phase 1 backs them with a scripted in-process stub (stub.go). Nothing here
// reads GROUND_TRUTH, and nothing here ever will: a realistic outcome model
// needs the sealed answer key, which is precisely why it belongs behind this
// boundary in demo/world-simulator and not in services/executor
// (docs/ARCHITECTURE.md section 5a, the integrity rule).
package ports

import (
	"context"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
)

// RecoveryAction is what a retry against the payment rail did, mirroring
// worldsim.SimulateOutcomeResponse.
type RecoveryAction struct {
	Outcome commonv1.Outcome
	// Immediate is false when the real answer was deferred and will arrive
	// later via DecisionEngine.ReportDelayedOutcome (Phase 5).
	Immediate bool
	// ResolvesAt is set when Immediate is false: when the answer is expected.
	ResolvesAt time.Time
	// FailureCode is the rail's decline code, which becomes the next
	// classification's input, so it must be populated on failure.
	FailureCode string
	CostPaise   int64
}

// Notification is what a nudge send did, mirroring
// notifier.SimulateSendResponse.
type Notification struct {
	Sent      bool
	CostPaise int64
}

// RecoveryActionPort re-attempts the debit. demo/world-simulator implements
// this in Phase 5; a real bank or payment-gateway retry API implements it in
// production, with no change to services/executor.
//
// The error return is for infrastructure failure only. A declined retry is a
// successful call reporting OUTCOME_FAILURE, not an error: the Decision
// Engine treats an error as a retryable fault and a FAILURE outcome as a
// decision input, so conflating them dead-letters healthy records
// (services/executor/SPEC.md section 5).
type RecoveryActionPort interface {
	SimulateOutcome(ctx context.Context, recordID string, action commonv1.ActionType, attemptNumber int32) (RecoveryAction, error)
}

// NotificationPort sends the customer-facing message. demo/notification-
// simulator implements this in Phase 5; a real SMS/WhatsApp provider does in
// production. It never edits the text it is given.
type NotificationPort interface {
	SimulateSend(ctx context.Context, recordID string, channel notifierv1.Channel, message string) (Notification, error)
}
