// Package server implements the ClassifierService gRPC handlers.
//
// This file validates the request, delegates to the provider chain, and
// logs the outcome (ENGINEERING.md section 14); the rules table lives in
// internal/rules, the chain-walking logic in internal/provider.
package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/provider"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// chain is the subset of *provider.Chain's behaviour this handler needs, so
// a test can supply a fake without building a real chain.
type chain interface {
	Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error)
}

// Server implements classifierv1.ClassifierServiceServer.
type Server struct {
	classifierv1.UnimplementedClassifierServiceServer

	chain    chain
	log      *slog.Logger
	fallback prometheus.Counter
}

// New returns a Server that answers Classify via chain. log must not be
// nil. fallback is incremented once per call answered by the rules engine
// rather than a live LLM rung (docs/ARCHITECTURE.md section 13's
// llm_fallback_total, the numerator Alertmanager's LLM-fallback-rate alert
// reads, docs/PHASE4_IMPLEMENTATION.md Unit D).
func New(c chain, log *slog.Logger, fallback prometheus.Counter) *Server {
	return &Server{chain: c, log: log, fallback: fallback}
}

// Classify validates the request, delegates to the provider chain, and logs
// the outcome. failure_code being empty or unrecognised is not a request
// error (SPEC.md section 5): it is classified via the unknown-code path and
// returned as a normal response.
func (s *Server) Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	rec := req.GetRecord()
	if rec == nil {
		return nil, status.Error(codes.InvalidArgument, "record is required")
	}
	if rec.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "record.id is required")
	}

	resp, err := s.chain.Classify(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("classify record %s: %w", rec.GetId(), err)
	}

	if resp.GetSource() == commonv1.Source_SOURCE_RULES_FALLBACK && llmWasAttempted(resp.GetHops()) {
		s.fallback.Inc()
	}

	logger.ForRecord(s.log, rec.GetId(), rec.GetBatchId()).Info("classified record",
		logger.KeyBucket, resp.GetBucket().String(),
		logger.KeyAction, resp.GetRecommendedAction().String(),
		logger.KeySource, resp.GetSource().String(),
		"confidence", resp.GetConfidence(),
	)

	return resp, nil
}

// ComposeNudge is out of scope for Phase 1 (SPEC.md section 2): no caller
// exists yet (Phase 5, ARCHITECTURE.md section 5b).
func (s *Server) ComposeNudge(ctx context.Context, req *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ComposeNudge: not implemented until Phase 5")
}

// llmWasAttempted reports whether any hop names a rung other than the
// deterministic rules engine. force_rules_only strips every non-rules rung
// before the chain ever runs (SPEC.md section 4.8), so a response with only
// a "rules" hop was never offered to a live model at all: it did not fail,
// it was never asked. That distinction is exactly what llm_fallback_total
// (docs/ARCHITECTURE.md section 13) needs to be a useful alert signal
// rather than one that fires constantly under normal LLM_SAMPLE_RATE < 1.0
// operation (docs/PHASE3_IMPLEMENTATION.md Unit H).
func llmWasAttempted(hops []*commonv1.ProviderHop) bool {
	for _, h := range hops {
		if h.GetProvider() != provider.RulesName {
			return true
		}
	}
	return false
}
