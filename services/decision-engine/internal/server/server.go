// Package server implements DecisionEngineServiceServer: the one RPC this
// service exposes over gRPC, ReportDelayedOutcome. Decision Engine is
// primarily a Kafka consumer, not a gRPC server (decisionengine.proto's own
// header comment); this exists only because an outcome that resolves after
// the request that started it (a customer acting on a nudge, hours later)
// has no request left to return an answer on, so it has to arrive as a new
// call instead (docs/ARCHITECTURE.md sections 6, 7a).
//
// All the actual decision logic lives in internal/engine.Scheduler.
// ResumeNudge; this file validates the request and translates its result
// to and from the proto shapes, the same division server.go files use
// throughout this repo (docs/ENGINEERING.md section 14).
package server

import (
	"context"
	"fmt"
	"log/slog"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	decisionenginev1 "github.com/thisizaro/Momotaro/proto/gen/decisionengine/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// resumer is the subset of *engine.Scheduler this handler needs, so a test
// can supply a fake without a real Postgres-backed Scheduler.
type resumer interface {
	ResumeNudge(ctx context.Context, recordID string, attemptNumber int, outcome commonv1.Outcome, failureCode string) (applied bool, resultingState commonv1.RecordState, err error)
}

// Server implements decisionenginev1.DecisionEngineServiceServer.
type Server struct {
	decisionenginev1.UnimplementedDecisionEngineServiceServer

	resumer resumer
	log     *slog.Logger
}

// New returns a Server that resumes nudges via r. log must not be nil.
func New(r resumer, log *slog.Logger) *Server {
	return &Server{resumer: r, log: log}
}

// ReportDelayedOutcome validates the request and delegates to
// Scheduler.ResumeNudge. A discarded report (Applied=false) is a normal
// response, not an error: this RPC is at-least-once, so a redelivered or
// late-arriving report for a record that has already moved on is expected
// traffic, not a failure to log loudly.
func (s *Server) ReportDelayedOutcome(ctx context.Context, req *decisionenginev1.ReportDelayedOutcomeRequest) (*decisionenginev1.ReportDelayedOutcomeResponse, error) {
	if req.GetRecordId() == "" {
		return nil, status.Error(codes.InvalidArgument, "record_id is required")
	}
	if req.GetAttemptNumber() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "attempt_number must be positive")
	}
	if req.GetOutcome() == commonv1.Outcome_OUTCOME_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "outcome is required")
	}

	applied, state, err := s.resumer.ResumeNudge(ctx, req.GetRecordId(), int(req.GetAttemptNumber()), req.GetOutcome(), req.GetFailureCode())
	if err != nil {
		return nil, fmt.Errorf("resume nudge for %s: %w", req.GetRecordId(), err)
	}

	if req.GetFailureCode() != "" {
		// Not fed back into re-scoring today (see ResumeNudge's own
		// comment): logged here so a delayed failure code is visible
		// somewhere rather than silently dropped.
		s.log.Info("delayed outcome reported a failure code",
			"record_id", req.GetRecordId(), "failure_code", req.GetFailureCode(), "applied", applied)
	}

	return &decisionenginev1.ReportDelayedOutcomeResponse{Applied: applied, ResultingState: state}, nil
}
