package server

import (
	"fmt"

	"github.com/google/uuid"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StreamBatchUpdates relays every hub-published update for req's batch_id
// to stream until the client disconnects (docs/ARCHITECTURE.md section
// 6a). The gRPC-facing half; consume.go's AuditConsumer is the
// Kafka-facing half that actually calls hub.publish. Neither knows about
// the other, they only share *Hub.
func (s *Server) StreamBatchUpdates(req *reportingv1.StreamBatchUpdatesRequest, stream reportingv1.ReportingService_StreamBatchUpdatesServer) error {
	batchID := req.GetBatchId()
	if batchID == "" {
		return status.Error(codes.InvalidArgument, "batch_id is required")
	}
	if _, err := uuid.Parse(batchID); err != nil {
		return status.Errorf(codes.InvalidArgument, "batch_id %q is not a valid UUID", batchID)
	}

	exists, err := s.store.batchExists(stream.Context(), batchID)
	if err != nil {
		return fmt.Errorf("check batch %s: %w", batchID, err)
	}
	if !exists {
		return status.Errorf(codes.NotFound, "batch %s not found", batchID)
	}

	ch, unsubscribe := s.hub.subscribe(batchID)
	defer unsubscribe()

	for {
		select {
		case update, ok := <-ch:
			if !ok {
				// The hub itself was torn down (shutdown); a clean end of
				// stream, not an error.
				return nil
			}
			if err := stream.Send(&reportingv1.StreamBatchUpdatesResponse{Update: update}); err != nil {
				return err
			}
		case <-stream.Context().Done():
			// The client disconnected (browser tab closed, WebSocket
			// dropped at the Gateway). Not an error from this service's
			// point of view, but returning stream.Context().Err() lets a
			// caller (and this unit's own tests) tell "client went away"
			// from "the hub closed" without inspecting state.
			return stream.Context().Err()
		}
	}
}
