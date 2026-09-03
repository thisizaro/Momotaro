// Package server implements DecisionEngineServiceServer, the RPCs this
// service exposes over gRPC. Decision Engine is primarily a Kafka consumer,
// not a gRPC server (decisionengine.proto's own header comment); both RPCs
// here exist only because their event arrives after the request that would
// otherwise carry it has already returned: ReportDelayedOutcome for an
// outcome that resolves later (a customer acting on a nudge, hours after
// receiving it, docs/ARCHITECTURE.md sections 6, 7a), ReportDowntimeEvent
// for a Razorpay bank-outage webhook that has nothing to do with any one
// in-flight request at all (docs/PHASE5_5_IMPLEMENTATION.md Unit Y).
//
// All the actual decision logic lives in internal/engine.Scheduler; this
// file validates each request and translates its result to and from the
// proto shapes, the same division server.go files use throughout this repo
// (docs/ENGINEERING.md section 14).
package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/thisizaro/Momotaro/services/decision-engine/internal/engine"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	decisionenginev1 "github.com/thisizaro/Momotaro/proto/gen/decisionengine/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// resumer is the subset of *engine.Scheduler this handler needs, so a test
// can supply a fake without a real Postgres-backed Scheduler.
type resumer interface {
	ResumeNudge(ctx context.Context, recordID string, attemptNumber int, outcome commonv1.Outcome, failureCode string) (applied bool, resultingState commonv1.RecordState, err error)
	// RecordDowntimeEvent persists one payment.downtime.* webhook
	// (docs/PHASE5_5_IMPLEMENTATION.md Unit Y).
	RecordDowntimeEvent(ctx context.Context, evt engine.DowntimeEvent) error
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

// downtimeStatuses is the closed set ReportDowntimeEvent accepts for
// status: it decides how the row is written (an active upsert vs marking it
// resolved), so unlike severity (an open string, shown but never branched
// on) it must be one of these three or the request is rejected outright
// rather than silently mis-filed.
var downtimeStatuses = map[string]bool{"started": true, "updated": true, "resolved": true}

// ReportDowntimeEvent validates the request and delegates to
// Scheduler.RecordDowntimeEvent (docs/PHASE5_5_IMPLEMENTATION.md Unit Y).
// Unix-seconds conversion happens here, at the proto boundary, so
// engine.DowntimeEvent and everything downstream of it works in ordinary
// time.Time and never has to remember which unit an int64 timestamp is in.
func (s *Server) ReportDowntimeEvent(ctx context.Context, req *decisionenginev1.ReportDowntimeEventRequest) (*decisionenginev1.ReportDowntimeEventResponse, error) {
	if req.GetDowntimeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "downtime_id is required")
	}
	if req.GetMethod() == "" {
		return nil, status.Error(codes.InvalidArgument, "method is required")
	}
	if !downtimeStatuses[req.GetStatus()] {
		return nil, status.Errorf(codes.InvalidArgument, "status must be one of started, updated, resolved, got %q", req.GetStatus())
	}
	if req.GetBeginUnix() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "begin_unix is required")
	}

	evt := engine.DowntimeEvent{
		DowntimeID: req.GetDowntimeId(),
		Method:     req.GetMethod(),
		Status:     req.GetStatus(),
		Scheduled:  req.GetScheduled(),
		Severity:   req.GetSeverity(),
		Instrument: req.GetInstrumentKey(),
		Begin:      time.Unix(req.GetBeginUnix(), 0).UTC(),
	}
	if req.GetHasEnd() {
		end := time.Unix(req.GetEndUnix(), 0).UTC()
		evt.End = &end
	}

	if err := s.resumer.RecordDowntimeEvent(ctx, evt); err != nil {
		return nil, fmt.Errorf("record downtime event %s: %w", req.GetDowntimeId(), err)
	}
	return &decisionenginev1.ReportDowntimeEventResponse{Applied: true}, nil
}
