// Package server implements the ClassifierService gRPC handlers.
package server

import (
	"context"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Server implements classifierv1.ClassifierServiceServer.
//
// Walking skeleton (docs/PLAN.md): Classify returns a hardcoded diagnosis,
// always via the rules fallback. No LLM, no provider chain yet, that is
// Phase 3 (docs/ARCHITECTURE.md section 5).
type Server struct {
	classifierv1.UnimplementedClassifierServiceServer
}

// New returns a Server ready to handle requests.
func New() *Server {
	return &Server{}
}

// Classify always returns the same diagnosis, regardless of the record: a
// transient bank failure worth retrying, decided by the deterministic rules
// fallback. Real diagnosis (hybrid rules+LLM) lands in Phase 3.
func (s *Server) Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	if req.GetRecord() == nil {
		return nil, status.Error(codes.InvalidArgument, "record is required")
	}

	return &classifierv1.ClassifyResponse{
		Bucket:            commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecommendedAction: commonv1.ActionType_ACTION_TYPE_RETRY,
		Rationale:         "walking skeleton: hardcoded classification, no LLM call made",
		Confidence:        1.0,
		Source:            commonv1.Source_SOURCE_RULES_FALLBACK,
		Hops: []*commonv1.ProviderHop{
			{Provider: "rules", Result: "ok"},
		},
	}, nil
}

// ComposeNudge is out of scope for the walking skeleton (docs/PLAN.md
// Phase 5); it answers rather than leaving a caller to hang or crash.
func (s *Server) ComposeNudge(ctx context.Context, req *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ComposeNudge: out of scope for the walking skeleton")
}
