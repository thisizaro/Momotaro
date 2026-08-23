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

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
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

	chain chain
	log   *slog.Logger
}

// New returns a Server that answers Classify via chain. log must not be
// nil.
func New(c chain, log *slog.Logger) *Server {
	return &Server{chain: c, log: log}
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
