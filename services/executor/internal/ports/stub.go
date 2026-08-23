package ports

import (
	"context"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
)

// StubRecovery and StubNotification are Phase 1's stand-ins for
// demo/world-simulator and demo/notification-simulator, which are empty
// scaffolds until Phase 5 (docs/PLAN.md). They exist to prove the state
// machine reaches every terminal state, not to be realistic.
//
// Scripted, never random. Two reasons, both concrete: Phase 2 has a re-run
// safety test that replays a batch and asserts an identical outcome, and a
// stub that rolled dice would make the end-to-end test flaky. So the same
// request always produces the same answer, derived from the action and the
// attempt number and nothing else. In particular nothing here reads the
// clock: a time-dependent stub is a time-dependent test.
//
// The script:
//
//	RETRY, attempt 1    -> SUCCESS            (the happy path the e2e test walks)
//	RETRY, attempt 2+   -> FAILURE            (so escalation is reachable)
//	any nudge           -> sent, PENDING      (the customer answers later, Phase 5)
//
// Retrying once and succeeding, then failing on later attempts, is what makes
// both branches of the Decision Engine's post-execute state machine reachable
// without any randomness.
type StubRecovery struct{}

func (StubRecovery) SimulateOutcome(ctx context.Context, recordID string, action commonv1.ActionType, attemptNumber int32) (RecoveryAction, error) {
	if attemptNumber <= 1 {
		return RecoveryAction{
			Outcome:   commonv1.Outcome_OUTCOME_SUCCESS,
			Immediate: true,
			CostPaise: retryCostPaise,
		}, nil
	}
	// A second attempt on the same record means the first one did not stick.
	// The rail is charged for the attempt either way, which is the whole
	// point of tracking cost against outcome.
	return RecoveryAction{
		Outcome:     commonv1.Outcome_OUTCOME_FAILURE,
		Immediate:   true,
		CostPaise:   retryCostPaise,
		FailureCode: "BANK_TIMEOUT",
	}, nil
}

// StubNotification always accepts the message and reports the channel's cost.
// A send failure is a Phase 5 concern: a real provider can reject a number,
// and the port already has a Sent=false path for it (route.go), which is
// tested against a fake rather than fabricated here.
type StubNotification struct{}

func (StubNotification) SimulateSend(ctx context.Context, recordID string, channel notifierv1.Channel, message string) (Notification, error) {
	return Notification{Sent: true, CostPaise: channelCostPaise(channel)}, nil
}
