package ports

import (
	"context"
	"errors"
	"testing"
	"time"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeWorldSimClient and fakeNotificationSimClient fake the generated gRPC
// client interfaces directly (no network), the same pattern
// services/decision-engine/internal/engine's own tests use to fake the
// Executor client.
type fakeWorldSimClient struct {
	resp *worldsimv1.SimulateOutcomeResponse
	err  error
	req  *worldsimv1.SimulateOutcomeRequest
}

func (f *fakeWorldSimClient) SimulateOutcome(ctx context.Context, in *worldsimv1.SimulateOutcomeRequest, opts ...grpc.CallOption) (*worldsimv1.SimulateOutcomeResponse, error) {
	f.req = in
	return f.resp, f.err
}

type fakeNotifierClient struct {
	resp *notifierv1.SimulateSendResponse
	err  error
	req  *notifierv1.SimulateSendRequest
}

func (f *fakeNotifierClient) SimulateSend(ctx context.Context, in *notifierv1.SimulateSendRequest, opts ...grpc.CallOption) (*notifierv1.SimulateSendResponse, error) {
	f.req = in
	return f.resp, f.err
}

func TestWorldSimRecoverySucceedsImmediately(t *testing.T) {
	client := &fakeWorldSimClient{resp: &worldsimv1.SimulateOutcomeResponse{
		Outcome: commonv1.Outcome_OUTCOME_SUCCESS, Immediate: true,
	}}
	w := NewWorldSimRecovery(client)

	got, err := w.SimulateOutcome(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_RETRY, 1)
	if err != nil {
		t.Fatalf("SimulateOutcome: %v", err)
	}
	if got.Outcome != commonv1.Outcome_OUTCOME_SUCCESS || !got.Immediate {
		t.Errorf("got = %+v, want SUCCESS/immediate", got)
	}
	if got.CostPaise != retryCostPaise {
		t.Errorf("CostPaise = %d, want retryCostPaise (%d): the proto carries no cost, this port must inject it", got.CostPaise, retryCostPaise)
	}
	if client.req.RecordId != "rec-1" || client.req.ActionType != commonv1.ActionType_ACTION_TYPE_RETRY || client.req.AttemptNumber != 1 {
		t.Errorf("request sent = %+v, want record_id/action_type/attempt_number passed through", client.req)
	}
}

func TestWorldSimRecoveryNudgeCostsNothingOnThisPort(t *testing.T) {
	client := &fakeWorldSimClient{resp: &worldsimv1.SimulateOutcomeResponse{
		Outcome: commonv1.Outcome_OUTCOME_PENDING, Immediate: false,
		ResolvesAt: timestamppb.New(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)),
	}}
	w := NewWorldSimRecovery(client)

	got, err := w.SimulateOutcome(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, 1)
	if err != nil {
		t.Fatalf("SimulateOutcome: %v", err)
	}
	if got.CostPaise != 0 {
		t.Errorf("CostPaise = %d, want 0: a nudge's cost is the notification port's, not this one's", got.CostPaise)
	}
	if got.Immediate {
		t.Error("Immediate = true, want false")
	}
	want := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	if !got.ResolvesAt.Equal(want) {
		t.Errorf("ResolvesAt = %v, want %v", got.ResolvesAt, want)
	}
}

func TestWorldSimRecoveryImmediateResponseHasZeroResolvesAt(t *testing.T) {
	// Immediate=true means SimulateOutcomeResponse.resolves_at was never
	// set; must not be misread as a real (zero-value) timestamp.
	client := &fakeWorldSimClient{resp: &worldsimv1.SimulateOutcomeResponse{
		Outcome: commonv1.Outcome_OUTCOME_SUCCESS, Immediate: true,
	}}
	w := NewWorldSimRecovery(client)

	got, err := w.SimulateOutcome(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_RETRY, 1)
	if err != nil {
		t.Fatalf("SimulateOutcome: %v", err)
	}
	if !got.ResolvesAt.IsZero() {
		t.Errorf("ResolvesAt = %v, want zero for an immediate answer", got.ResolvesAt)
	}
}

func TestWorldSimRecoverySurfacesTransportErrors(t *testing.T) {
	client := &fakeWorldSimClient{err: errors.New("unavailable")}
	w := NewWorldSimRecovery(client)

	if _, err := w.SimulateOutcome(context.Background(), "rec-1", commonv1.ActionType_ACTION_TYPE_RETRY, 1); err == nil {
		t.Fatal("a transport error did not surface")
	}
}

func TestNotificationSimAdapterMapsResponseFieldsThrough(t *testing.T) {
	client := &fakeNotifierClient{resp: &notifierv1.SimulateSendResponse{Sent: true, CostPaise: 14}}
	n := NewNotificationSimAdapter(client)

	got, err := n.SimulateSend(context.Background(), "rec-1", notifierv1.Channel_CHANNEL_WHATSAPP, "hi")
	if err != nil {
		t.Fatalf("SimulateSend: %v", err)
	}
	if !got.Sent || got.CostPaise != 14 {
		t.Errorf("got = %+v, want Sent=true CostPaise=14", got)
	}
	if client.req.RecordId != "rec-1" || client.req.Channel != notifierv1.Channel_CHANNEL_WHATSAPP || client.req.Message != "hi" {
		t.Errorf("request sent = %+v, want record_id/channel/message passed through", client.req)
	}
}

func TestNotificationSimAdapterSurfacesTransportErrors(t *testing.T) {
	client := &fakeNotifierClient{err: errors.New("unavailable")}
	n := NewNotificationSimAdapter(client)

	if _, err := n.SimulateSend(context.Background(), "rec-1", notifierv1.Channel_CHANNEL_SMS, "hi"); err == nil {
		t.Fatal("a transport error did not surface")
	}
}
