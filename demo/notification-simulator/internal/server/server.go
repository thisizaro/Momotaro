// Package server implements NotificationSimulatorService: logs what would
// have been sent and returns its price, per the proto's own comment
// ("nothing is really delivered", docs/PRD.md non-goals). No Postgres, no
// Redis: this service holds no state and answers from its own pricing
// table alone.
package server

import (
	"context"
	"log/slog"

	notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Per-channel prices in paise. These MUST MATCH
// services/executor/internal/ports/cost.go's smsCostPaise/whatsappCostPaise
// [SOURCED there]: cannot import that package (private to services/executor,
// AGENTS.md), so this is a deliberate small duplication, same precedent as
// scripts/batchgen/profile.go's ObviousBucket table and
// demo/world-simulator/internal/server/bucket.go's correctActionFor.
const (
	smsCostPaise      = 25
	whatsappCostPaise = 14
	// No sourced rate exists for email, and no caller in this system sends
	// it today (services/executor/internal/ports/route.go's channelFor
	// only ever picks SMS or WHATSAPP). Priced the same as the fallback
	// case below rather than left at zero, so a future caller cannot get a
	// free intervention by accident.
	emailCostPaise = smsCostPaise
)

func channelCostPaise(channel notifierv1.Channel) int64 {
	switch channel {
	case notifierv1.Channel_CHANNEL_WHATSAPP:
		return whatsappCostPaise
	case notifierv1.Channel_CHANNEL_EMAIL:
		return emailCostPaise
	default:
		// SMS is the fallback channel, matching services/executor/internal/
		// ports/cost.go's own reasoning: always available, cheapest, and
		// what an unspecified channel should cost rather than nothing.
		return smsCostPaise
	}
}

// Server implements notifierv1.NotificationSimulatorServiceServer.
type Server struct {
	notifierv1.UnimplementedNotificationSimulatorServiceServer

	log *slog.Logger
}

func New(log *slog.Logger) *Server {
	return &Server{log: log}
}

// SimulateSend logs what would have been sent and reports it delivered.
// Always Sent=true: nothing here models a delivery failure (no ground
// truth applies to notification delivery, unlike SimulateOutcome's
// recoverability model), matching Phase 1's StubNotification behaviour
// exactly, now over a real gRPC boundary instead of an in-process stub.
func (s *Server) SimulateSend(ctx context.Context, req *notifierv1.SimulateSendRequest) (*notifierv1.SimulateSendResponse, error) {
	if req.GetRecordId() == "" {
		return nil, status.Error(codes.InvalidArgument, "record_id is required")
	}
	cost := channelCostPaise(req.GetChannel())
	s.log.Info("simulated send",
		"record_id", req.GetRecordId(), "channel", req.GetChannel().String(),
		"message", req.GetMessage(), "cost_paise", cost)
	return &notifierv1.SimulateSendResponse{Sent: true, CostPaise: cost}, nil
}
