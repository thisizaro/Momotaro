package ports

import (
	"context"
	"fmt"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
)

// Result is one executed action's outcome, in the shape ExecuteResponse needs.
type Result struct {
	Outcome     commonv1.Outcome
	CostPaise   int64
	FailureCode string
	// ResolvesAt is the zero time unless Outcome is PENDING.
	ResolvesAt time.Time
}

// Router decides which port an action belongs to and translates that port's
// answer into a Result. This is the only place the action-to-port mapping
// lives, so it reads in one glance and is testable without a database
// (docs/ENGINEERING.md section 14).
type Router struct {
	recovery     RecoveryActionPort
	notification NotificationPort
}

func NewRouter(recovery RecoveryActionPort, notification NotificationPort) *Router {
	return &Router{recovery: recovery, notification: notification}
}

// Execute performs action for a record and reports what happened.
//
// The returned error means "could not execute", an infrastructure failure. A
// declined retry or an undeliverable nudge comes back as a Result with a
// FAILURE outcome and a nil error, because the Decision Engine retries an
// error but escalates a FAILURE, and those are not the same decision
// (services/executor/SPEC.md section 5).
func (r *Router) Execute(ctx context.Context, recordID string, action commonv1.ActionType, attemptNumber int32, message string) (Result, error) {
	switch action {
	case commonv1.ActionType_ACTION_TYPE_RETRY:
		return r.retry(ctx, recordID, action, attemptNumber)

	case commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
		commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE:
		return r.nudge(ctx, recordID, action, attemptNumber, message)

	case commonv1.ActionType_ACTION_TYPE_NONE:
		// A deliberate decision to do nothing, which succeeds by definition
		// and costs nothing. Recorded as an attempt anyway so the trail shows
		// the decision was reached rather than skipped.
		return Result{Outcome: commonv1.Outcome_OUTCOME_SUCCESS}, nil

	case commonv1.ActionType_ACTION_TYPE_ESCALATE:
		// Handing a record to a human is not something the Executor can
		// accomplish, and pretending otherwise would put a false success in
		// the trail. Reported as a failure the Decision Engine can act on.
		// In practice this is unreachable today: decideAfterClassify sends an
		// escalated record straight to Escalated without scheduling anything,
		// so the scheduler never calls Execute with it. Handled because
		// Execute is a public RPC, not because a caller exists.
		return Result{
			Outcome:     commonv1.Outcome_OUTCOME_FAILURE,
			FailureCode: "ESCALATED_NO_AUTOMATED_ACTION",
		}, nil

	default:
		return Result{}, fmt.Errorf("no port handles action %s", action)
	}
}

func (r *Router) retry(ctx context.Context, recordID string, action commonv1.ActionType, attemptNumber int32) (Result, error) {
	out, err := r.recovery.SimulateOutcome(ctx, recordID, action, attemptNumber)
	if err != nil {
		return Result{}, fmt.Errorf("recovery action port: %w", err)
	}
	res := Result{
		Outcome:     out.Outcome,
		CostPaise:   out.CostPaise,
		FailureCode: out.FailureCode,
	}
	// A retry is synchronous against the rail, so a deferred answer is
	// unusual rather than impossible; carry the port's own resolves_at
	// instead of inventing one.
	if !out.Immediate {
		res.Outcome = commonv1.Outcome_OUTCOME_PENDING
		res.ResolvesAt = out.ResolvesAt
	}
	return res, nil
}

// nudge sends the message, then, only if it was actually delivered, asks
// the recovery port whether and when the customer is expected to react.
// Sending and reacting are different questions: SimulateSend answers
// "did the channel deliver this", the recovery port (demo/world-simulator
// in the demo, a real GROUND_TRUTH-less bank/CRM signal in production)
// answers "does this customer, specifically, pay after being asked", which
// only something holding a recoverability model can know
// (docs/ARCHITECTURE.md section 6). Not delivered means there is nothing
// to wait for, so the recovery port is never called in that case.
func (r *Router) nudge(ctx context.Context, recordID string, action commonv1.ActionType, attemptNumber int32, message string) (Result, error) {
	channel := channelFor(action)
	sent, err := r.notification.SimulateSend(ctx, recordID, channel, message)
	if err != nil {
		return Result{}, fmt.Errorf("notification port: %w", err)
	}
	if !sent.Sent {
		return Result{
			Outcome:     commonv1.Outcome_OUTCOME_FAILURE,
			CostPaise:   sent.CostPaise,
			FailureCode: "NOTIFICATION_NOT_SENT",
		}, nil
	}

	out, err := r.recovery.SimulateOutcome(ctx, recordID, action, attemptNumber)
	if err != nil {
		return Result{}, fmt.Errorf("recovery action port: %w", err)
	}
	res := Result{
		Outcome:     out.Outcome,
		CostPaise:   sent.CostPaise, // the notification's cost, not the recovery port's (always 0 for a nudge, see grpc.go)
		FailureCode: out.FailureCode,
	}
	// A nudge is "usually PENDING" (proto/worldsim/v1), since a customer
	// does not react inside an RPC deadline, but not always: a zero-delay
	// profile resolves immediately, and that real answer is passed
	// through rather than forced into PENDING.
	if !out.Immediate {
		res.Outcome = commonv1.Outcome_OUTCOME_PENDING
		res.ResolvesAt = out.ResolvesAt
	}
	return res, nil
}

// channelFor picks the delivery channel for a nudge type. A method-update ask
// needs the customer to actually act on it, so it goes out on the channel
// with better read rates and a higher cost; a plain reminder does not justify
// that.
func channelFor(action commonv1.ActionType) notifierv1.Channel {
	if action == commonv1.ActionType_ACTION_TYPE_NUDGE_METHOD_UPDATE {
		return notifierv1.Channel_CHANNEL_WHATSAPP
	}
	return notifierv1.Channel_CHANNEL_SMS
}
