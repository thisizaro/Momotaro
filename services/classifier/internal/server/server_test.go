package server

import (
	"context"
	"testing"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClassifyReturnsHardcodedRulesFallback(t *testing.T) {
	s := New()

	resp, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{
		Record: &commonv1.Record{Id: "rec-1", FailureCode: "BANK_TIMEOUT"},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	if resp.Source != commonv1.Source_SOURCE_RULES_FALLBACK {
		t.Errorf("Source = %v, want SOURCE_RULES_FALLBACK", resp.Source)
	}
	if resp.Bucket == commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED {
		t.Error("Bucket left UNSPECIFIED")
	}
	if resp.RecommendedAction == commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		t.Error("RecommendedAction left UNSPECIFIED")
	}
	if resp.Rationale == "" {
		t.Error("Rationale is empty, audit trail needs a human-readable reason")
	}
	if len(resp.Hops) == 0 {
		t.Error("no provider hops recorded")
	}
}

// The hardcoded response must not depend on which record was sent: this is
// the walking skeleton, not real diagnosis.
func TestClassifyIsHardcodedRegardlessOfInput(t *testing.T) {
	s := New()

	first, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{
		Record: &commonv1.Record{Id: "rec-1", FailureCode: "BANK_TIMEOUT"},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	second, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{
		Record: &commonv1.Record{Id: "rec-2", FailureCode: "INSUFFICIENT_FUNDS"},
	})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}

	if first.Bucket != second.Bucket || first.RecommendedAction != second.RecommendedAction {
		t.Errorf("hardcoded classification varied by input: %+v vs %+v", first, second)
	}
}

func TestClassifyRejectsMissingRecord(t *testing.T) {
	s := New()

	_, err := s.Classify(context.Background(), &classifierv1.ClassifyRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Classify with no record: err = %v, want InvalidArgument", err)
	}
}

// Out of scope for the walking skeleton (docs/PLAN.md), but the RPC must
// still answer rather than crash a caller that dials it.
func TestComposeNudgeIsUnimplemented(t *testing.T) {
	s := New()

	_, err := s.ComposeNudge(context.Background(), &classifierv1.ComposeNudgeRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("ComposeNudge: err = %v, want Unimplemented", err)
	}
}
