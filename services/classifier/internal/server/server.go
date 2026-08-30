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
	"strings"

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

// nudgeChain is the ComposeNudge equivalent of chain, the subset of
// *provider.NudgeChain's behaviour this handler needs.
type nudgeChain interface {
	ComposeNudge(ctx context.Context, req *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error)
}

// Server implements classifierv1.ClassifierServiceServer.
type Server struct {
	classifierv1.UnimplementedClassifierServiceServer

	chain      chain
	nudgeChain nudgeChain
	log        *slog.Logger
	fallback   prometheus.Counter
	// nudgeFallback is fallback's ComposeNudge equivalent: incremented once
	// per call answered by the static Hinglish template rather than a live
	// LLM rung, the nudge_fallback_total docs/PHASE5_IMPLEMENTATION.md Unit
	// E adds alongside the existing llm_fallback_total.
	nudgeFallback prometheus.Counter
}

// New returns a Server that answers Classify via c and ComposeNudge via nc.
// log must not be nil. fallback/nudgeFallback are incremented once per call
// answered by the deterministic fallback (the rules engine, the static
// template) rather than a live LLM rung (docs/ARCHITECTURE.md section 13's
// llm_fallback_total, the numerator Alertmanager's LLM-fallback-rate alert
// reads, docs/PHASE4_IMPLEMENTATION.md Unit D).
func New(c chain, nc nudgeChain, log *slog.Logger, fallback, nudgeFallback prometheus.Counter) *Server {
	return &Server{chain: c, nudgeChain: nc, log: log, fallback: fallback, nudgeFallback: nudgeFallback}
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

// nudgeAmountPlaceholder MUST match provider.AmountPlaceholder
// (provider/validate_nudge.go) byte for byte: that is the token every rung
// (LLM or template) is required to write in place of the record's real
// amount, and substituteAmount below is what turns it into the real figure
// after a rung's response has already passed validation. A literal copy
// rather than an import for the same reason llm/nudge_prompt.go carries its
// own copy: provider's own test file (fallback_test.go) imports llm, so
// anything importing provider here risks the same cycle nudge_prompt.go
// hit, and server already imports provider for other reasons, so keeping
// this one local avoids relying on which direction happens to compile today.
const nudgeAmountPlaceholder = "{{AMOUNT}}"

// ComposeNudge validates the request, delegates to the nudge-composition
// chain, substitutes the record's real amount for the placeholder token
// every rung is required to use, and logs the outcome
// (docs/ARCHITECTURE.md section 5b, docs/PHASE5_IMPLEMENTATION.md Unit E).
func (s *Server) ComposeNudge(ctx context.Context, req *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error) {
	rec := req.GetRecord()
	if rec == nil {
		return nil, status.Error(codes.InvalidArgument, "record is required")
	}
	if rec.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "record.id is required")
	}

	resp, err := s.nudgeChain.ComposeNudge(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("compose nudge for record %s: %w", rec.GetId(), err)
	}

	if resp.GetSource() == commonv1.Source_SOURCE_RULES_FALLBACK && llmWasAttempted(resp.GetHops()) {
		s.nudgeFallback.Inc()
	}

	resp.Message = substituteAmount(resp.GetMessage(), rec.GetAmountPaise())

	logger.ForRecord(s.log, rec.GetId(), rec.GetBatchId()).Info("composed nudge",
		logger.KeyBucket, req.GetBucket().String(),
		logger.KeyAction, req.GetActionType().String(),
		logger.KeySource, resp.GetSource().String(),
	)

	return resp, nil
}

// substituteAmount replaces nudgeAmountPlaceholder with amountPaise
// formatted as rupees. Called only after the chain's own validation has
// confirmed the placeholder appears at most once and no rung wrote its own
// digit (provider/validate_nudge.go), so this is a plain string replace,
// not a second validation pass.
func substituteAmount(message string, amountPaise int64) string {
	return strings.ReplaceAll(message, nudgeAmountPlaceholder, formatRupees(amountPaise))
}

// formatRupees renders paise as a rupee amount for a customer-facing
// message: whole rupees when there is no fractional paise (the common
// case), two decimal places otherwise. Integer arithmetic throughout
// (docs/ENGINEERING.md section 8: "Money is integer paise. Never a
// float.").
func formatRupees(amountPaise int64) string {
	rupees := amountPaise / 100
	paise := amountPaise % 100
	if paise == 0 {
		return fmt.Sprintf("Rs %d", rupees)
	}
	return fmt.Sprintf("Rs %d.%02d", rupees, paise)
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
