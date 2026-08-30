package server

import (
	"context"
	"io"
	"log/slog"
	"testing"

	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSimulateSendPricesByChannel(t *testing.T) {
	tests := []struct {
		channel  notifierv1.Channel
		wantCost int64
	}{
		{notifierv1.Channel_CHANNEL_SMS, smsCostPaise},
		{notifierv1.Channel_CHANNEL_WHATSAPP, whatsappCostPaise},
		{notifierv1.Channel_CHANNEL_EMAIL, emailCostPaise},
		{notifierv1.Channel_CHANNEL_UNSPECIFIED, smsCostPaise},
	}
	s := New(discardLog())
	for _, tc := range tests {
		t.Run(tc.channel.String(), func(t *testing.T) {
			resp, err := s.SimulateSend(context.Background(), &notifierv1.SimulateSendRequest{
				RecordId: "rec-1", Channel: tc.channel, Message: "hi",
			})
			if err != nil {
				t.Fatalf("SimulateSend: %v", err)
			}
			if !resp.Sent {
				t.Error("Sent = false, want true")
			}
			if resp.CostPaise != tc.wantCost {
				t.Errorf("CostPaise = %d, want %d", resp.CostPaise, tc.wantCost)
			}
		})
	}
}

func TestSimulateSendMissingRecordID(t *testing.T) {
	s := New(discardLog())
	_, err := s.SimulateSend(context.Background(), &notifierv1.SimulateSendRequest{Channel: notifierv1.Channel_CHANNEL_SMS})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SimulateSend with no record_id: err = %v, want InvalidArgument", err)
	}
}
