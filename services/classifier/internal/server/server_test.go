package server

import (
	"context"
	"testing"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/provider"
	"github.com/thisizaro/Momotaro/services/classifier/internal/rules"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newTestServer wires the real rules engine behind a rules-only chain: the
// rules engine is pure and cannot fail, so there is no need for a fake here
// except at the chain interface boundary, which exists for that purpose.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	log := logger.Discard()
	c, err := provider.NewChain(
		[]string{provider.RulesName},
		map[string]provider.Provider{provider.RulesName: rules.New(log)},
		log,
	)
	if err != nil {
		t.Fatalf("build chain: %v", err)
	}
	return New(c, log)
}

func TestClassifyRejectsNilRecord(t *testing.T) {
	s := newTestServer(t)

	_, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Classify with nil record: err = %v, want InvalidArgument", err)
	}
}

func TestClassifyRejectsEmptyRecordID(t *testing.T) {
	s := newTestServer(t)

	_, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{
		Record: &commonv1.Record{FailureCode: "BANK_TIMEOUT"},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Classify with empty record.id: err = %v, want InvalidArgument", err)
	}
}

func TestClassifyReturnsFullyPopulatedResponse(t *testing.T) {
	s := newTestServer(t)

	resp, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{
		Record: &commonv1.Record{Id: "rec-1", FailureCode: "BANK_TIMEOUT"},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if resp.GetBucket() == commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED {
		t.Error("Bucket left UNSPECIFIED for a recognised code")
	}
	if resp.GetRecommendedAction() == commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		t.Error("RecommendedAction left UNSPECIFIED")
	}
	if resp.GetRationale() == "" {
		t.Error("Rationale is empty")
	}
	if resp.GetSource() == commonv1.Source_SOURCE_UNSPECIFIED {
		t.Error("Source left UNSPECIFIED")
	}
	if len(resp.GetHops()) == 0 {
		t.Error("no provider hops recorded")
	}
}

// Empty history/instrument_history is the production path today (SPEC.md
// section 3: the Decision Engine never populates either field), so it is
// tested explicitly rather than assumed to work.
func TestClassifyHandlesEmptyHistoryWithoutError(t *testing.T) {
	s := newTestServer(t)

	_, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{
		Record:            &commonv1.Record{Id: "rec-1", FailureCode: "BANK_TIMEOUT"},
		History:           nil,
		InstrumentHistory: nil,
	})
	if err != nil {
		t.Fatalf("Classify with empty history: %v", err)
	}
}

func TestComposeNudgeIsUnimplemented(t *testing.T) {
	s := newTestServer(t)

	_, err := s.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("ComposeNudge: err = %v, want Unimplemented", err)
	}
}
