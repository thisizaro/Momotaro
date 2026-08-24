package ports

import (
	"context"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
)

func TestStubRecoveryScript(t *testing.T) {
	tests := []struct {
		name            string
		attempt         int32
		wantOutcome     commonv1.Outcome
		wantFailureCode bool
	}{
		// The e2e test walks this row: one record, first retry, recovered.
		{"first attempt succeeds", 1, commonv1.Outcome_OUTCOME_SUCCESS, false},
		{"second attempt fails", 2, commonv1.Outcome_OUTCOME_FAILURE, true},
		{"third attempt fails", 3, commonv1.Outcome_OUTCOME_FAILURE, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := StubRecovery{}.SimulateOutcome(context.Background(), "rec-1",
				commonv1.ActionType_ACTION_TYPE_RETRY, tc.attempt)
			if err != nil {
				t.Fatalf("SimulateOutcome: %v", err)
			}
			if got.Outcome != tc.wantOutcome {
				t.Errorf("Outcome = %v, want %v", got.Outcome, tc.wantOutcome)
			}
			if !got.Immediate {
				t.Error("Immediate = false: a retry answers synchronously against the rail")
			}
			if (got.FailureCode != "") != tc.wantFailureCode {
				t.Errorf("FailureCode = %q, want present=%v: it becomes the next classification's input", got.FailureCode, tc.wantFailureCode)
			}
			if got.CostPaise != retryCostPaise {
				t.Errorf("CostPaise = %d, want %d: an attempt costs the same whether or not it works", got.CostPaise, retryCostPaise)
			}
		})
	}
}

// Both branches of the Decision Engine's post-execute state machine have to be
// reachable, or half of it is never exercised end to end.
func TestStubRecoveryMakesBothOutcomesReachable(t *testing.T) {
	seen := map[commonv1.Outcome]bool{}
	for attempt := int32(1); attempt <= 3; attempt++ {
		got, err := StubRecovery{}.SimulateOutcome(context.Background(), "rec-1",
			commonv1.ActionType_ACTION_TYPE_RETRY, attempt)
		if err != nil {
			t.Fatalf("SimulateOutcome: %v", err)
		}
		seen[got.Outcome] = true
	}
	for _, want := range []commonv1.Outcome{commonv1.Outcome_OUTCOME_SUCCESS, commonv1.Outcome_OUTCOME_FAILURE} {
		if !seen[want] {
			t.Errorf("%v never produced across attempts 1..3", want)
		}
	}
}

// Phase 2's re-run safety test replays a batch and asserts an identical
// outcome, which is only possible if the stub is a pure function of its
// input. It also keeps the e2e test from flaking.
func TestStubsAreDeterministic(t *testing.T) {
	ctx := context.Background()
	for attempt := int32(1); attempt <= 3; attempt++ {
		first, err := StubRecovery{}.SimulateOutcome(ctx, "rec-1", commonv1.ActionType_ACTION_TYPE_RETRY, attempt)
		if err != nil {
			t.Fatalf("SimulateOutcome: %v", err)
		}
		for i := 0; i < 20; i++ {
			again, err := StubRecovery{}.SimulateOutcome(ctx, "rec-1", commonv1.ActionType_ACTION_TYPE_RETRY, attempt)
			if err != nil {
				t.Fatalf("SimulateOutcome: %v", err)
			}
			if again != first {
				t.Fatalf("attempt %d varied between calls: %+v then %+v", attempt, first, again)
			}
		}
	}

	firstSend, err := StubNotification{}.SimulateSend(ctx, "rec-1", notifierv1.Channel_CHANNEL_SMS, "msg")
	if err != nil {
		t.Fatalf("SimulateSend: %v", err)
	}
	for i := 0; i < 20; i++ {
		again, err := StubNotification{}.SimulateSend(ctx, "rec-1", notifierv1.Channel_CHANNEL_SMS, "msg")
		if err != nil {
			t.Fatalf("SimulateSend: %v", err)
		}
		if again != firstSend {
			t.Fatalf("notification varied between calls: %+v then %+v", firstSend, again)
		}
	}
}

func TestChannelCosts(t *testing.T) {
	sms := channelCostPaise(notifierv1.Channel_CHANNEL_SMS)
	whatsapp := channelCostPaise(notifierv1.Channel_CHANNEL_WHATSAPP)
	unspecified := channelCostPaise(notifierv1.Channel_CHANNEL_UNSPECIFIED)

	// This used to assert whatsapp > sms, on the assumption that WhatsApp is
	// the premium, better-read-rate channel. That assumption is false on the
	// sourced Indian rates in `configs/intervention_costs.yaml`: WhatsApp
	// Utility pricing (14 paise) is cheaper than SMS (25 paise), not dearer.
	// See that file's `executor_reconciliation` block, which flags
	// `route.go`'s channel policy rationale as inverted for India. Fixing
	// that policy is a separate, undecided change; this test only stops
	// asserting a direction the cost model no longer supports.
	for name, cost := range map[string]int64{"sms": sms, "whatsapp": whatsapp, "unspecified": unspecified} {
		if cost <= 0 {
			t.Errorf("%s costs %d: a free intervention makes Phase 2's expected-value maths meaningless", name, cost)
		}
	}
}
