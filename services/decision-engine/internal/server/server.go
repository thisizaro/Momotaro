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

// ConfigSnapshot is the behavioral subset of this service's own config that
// GetAgentConfig returns (docs/DEMO_READINESS.md Unit AM): the guardrail and
// LLM-routing values cmd/main.go loaded and validated at startup, captured
// once rather than re-read per request, since every one of them is fixed
// for the process's lifetime. This is what makes the RPC a pure read with
// nothing to fail: no Postgres, no I/O, just the values this process was
// started with.
//
// LLM_PROVIDER_CHAIN is deliberately absent, see decisionengine.proto's
// GetAgentConfigResponse doc comment and docs/DECISIONS.md: it belongs to
// the Classifier, not this service.
type ConfigSnapshot struct {
	// DemoTimeScale is DEMO_TIME_SCALE (internal/platform/config), loaded
	// identically by every service in this repo.
	DemoTimeScale float64
	// Guardrails is exactly what cmd/main.go's guardrailsFrom(cfg) built and
	// handed to the engine and the scheduler, so this can never show a
	// value other than the one actually enforced (ContactCooldown already
	// scaled by DemoTimeScale, RecoveryWindow deliberately not, see
	// guardrailsFrom's own comment).
	Guardrails engine.GuardrailConfig
	// LLMSampleRate, RouteConfidenceThreshold, ClassifyConfidenceThreshold,
	// NudgeMaxChars: this service's own LLM-routing config
	// (services/decision-engine/cmd/main.go).
	LLMSampleRate               float64
	RouteConfidenceThreshold    float64
	ClassifyConfidenceThreshold float64
	NudgeMaxChars               int32
}

// Server implements decisionenginev1.DecisionEngineServiceServer.
type Server struct {
	decisionenginev1.UnimplementedDecisionEngineServiceServer

	resumer resumer
	cfg     ConfigSnapshot
	log     *slog.Logger
}

// New returns a Server that resumes nudges via r and answers GetAgentConfig
// from cfg. log must not be nil.
func New(r resumer, cfg ConfigSnapshot, log *slog.Logger) *Server {
	return &Server{resumer: r, cfg: cfg, log: log}
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

// GetAgentConfig answers docs/DEMO_READINESS.md Unit AM: it never touches
// the resumer, Postgres, or anything else that could fail, because s.cfg is
// already the exact values this process validated at startup. There is
// nothing to validate on the request (it carries no fields) and nothing
// that can fail here beyond context cancellation, which the gRPC framework
// already handles.
func (s *Server) GetAgentConfig(ctx context.Context, req *decisionenginev1.GetAgentConfigRequest) (*decisionenginev1.GetAgentConfigResponse, error) {
	return &decisionenginev1.GetAgentConfigResponse{
		DemoTimeScale:                    s.cfg.DemoTimeScale,
		MaxRetries:                       int32(s.cfg.Guardrails.MaxRetries),
		MaxContacts:                      int32(s.cfg.Guardrails.MaxContacts),
		ContactCooldownSeconds:           int64(s.cfg.Guardrails.ContactCooldown.Seconds()),
		RecoveryWindowSeconds:            int64(s.cfg.Guardrails.RecoveryWindow.Seconds()),
		LlmSampleRate:                    s.cfg.LLMSampleRate,
		RouteConfidenceThreshold:         s.cfg.RouteConfidenceThreshold,
		ClassifyConfidenceThreshold:      s.cfg.ClassifyConfidenceThreshold,
		NudgeMaxChars:                    s.cfg.NudgeMaxChars,
		DowntimeMaxUnresolvedHoldSeconds: int64(engine.DowntimeMaxUnresolvedHold.Seconds()),
	}, nil
}
