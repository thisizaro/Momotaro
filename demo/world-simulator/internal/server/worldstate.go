package server

import (
	"context"

	worldsimv1 "github.com/thisizaro/Momotaro/proto/gen/worldsim/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetWorldState reports every entry still sitting in the delayed-outcome
// queue and when it is due, without draining any of it. Backs
// GET /v1/demo/world (docs/PHASE5_5_IMPLEMENTATION.md Unit W): the first
// time this component's state is visible anywhere outside its own logs.
func (s *Server) GetWorldState(ctx context.Context, req *worldsimv1.GetWorldStateRequest) (*worldsimv1.GetWorldStateResponse, error) {
	entries, err := s.queue.peekAll(ctx)
	if err != nil {
		return nil, err
	}

	pending := make([]*worldsimv1.PendingOutcome, len(entries))
	for i, e := range entries {
		pending[i] = &worldsimv1.PendingOutcome{
			RecordId:      e.RecordID,
			AttemptNumber: e.AttemptNumber,
			Outcome:       e.Outcome,
			DueAt:         timestamppb.New(e.DueAt),
		}
	}
	return &worldsimv1.GetWorldStateResponse{Pending: pending}, nil
}
