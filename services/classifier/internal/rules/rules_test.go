package rules

import (
	"context"
	"strings"
	"testing"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/provider"
)

func classify(t *testing.T, rec *commonv1.Record) *classifierv1.ClassifyResponse {
	t.Helper()
	p := New(logger.Discard())
	resp, err := p.Classify(context.Background(), &classifierv1.ClassifyRequest{Record: rec})
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	return resp
}

func TestName(t *testing.T) {
	if got := New(logger.Discard()).Name(); got != provider.RulesName {
		t.Errorf("Name() = %q, want %q", got, provider.RulesName)
	}
}

func TestClassifyUnknownCodeByRecordType(t *testing.T) {
	cases := []struct {
		name       string
		recordType commonv1.RecordType
		wantBucket commonv1.RootCauseBucket
		wantAction commonv1.ActionType
		wantConf   float64
	}{
		{
			"checkout falls back to abandonment",
			commonv1.RecordType_RECORD_TYPE_CHECKOUT,
			commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,
			commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
			0.80,
		},
		{
			"invoice falls back to overdue",
			commonv1.RecordType_RECORD_TYPE_INVOICE,
			commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE,
			commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER,
			0.75,
		},
		{
			"payment has no fallback",
			commonv1.RecordType_RECORD_TYPE_PAYMENT,
			commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED,
			commonv1.ActionType_ACTION_TYPE_ESCALATE,
			0.0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := classify(t, &commonv1.Record{Id: "rec-1", Type: c.recordType, FailureCode: "SOME_UNKNOWN_CODE"})
			if resp.GetBucket() != c.wantBucket {
				t.Errorf("bucket = %v, want %v", resp.GetBucket(), c.wantBucket)
			}
			if resp.GetRecommendedAction() != c.wantAction {
				t.Errorf("action = %v, want %v", resp.GetRecommendedAction(), c.wantAction)
			}
			if resp.GetConfidence() != c.wantConf {
				t.Errorf("confidence = %v, want %v", resp.GetConfidence(), c.wantConf)
			}
		})
	}
}

func TestClassifyUnknownCodeRationaleNamesTheCodeVerbatim(t *testing.T) {
	resp := classify(t, &commonv1.Record{Id: "rec-1", Type: commonv1.RecordType_RECORD_TYPE_PAYMENT, FailureCode: "SOME_UNKNOWN_CODE"})
	if !strings.Contains(resp.GetRationale(), "SOME_UNKNOWN_CODE") {
		t.Errorf("rationale %q does not name the unrecognised code", resp.GetRationale())
	}
}

func TestClassifyEmptyFailureCodeDoesNotError(t *testing.T) {
	resp := classify(t, &commonv1.Record{Id: "rec-1", Type: commonv1.RecordType_RECORD_TYPE_PAYMENT})
	if resp.GetBucket() != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED {
		t.Errorf("bucket = %v, want UNSPECIFIED", resp.GetBucket())
	}
	if resp.GetRationale() == "" {
		t.Error("rationale is empty for an empty failure_code")
	}
}

func TestClassifyIsDeterministic(t *testing.T) {
	rec := &commonv1.Record{Id: "rec-1", FailureCode: "BANK_TIMEOUT"}
	first := classify(t, rec)
	second := classify(t, rec)
	if first.GetBucket() != second.GetBucket() ||
		first.GetRecommendedAction() != second.GetRecommendedAction() ||
		first.GetRationale() != second.GetRationale() ||
		first.GetConfidence() != second.GetConfidence() {
		t.Errorf("classification not deterministic: %+v vs %+v", first, second)
	}
}

func TestClassifyDifferentCodesYieldDifferentRationales(t *testing.T) {
	first := classify(t, &commonv1.Record{Id: "rec-1", FailureCode: "BANK_TIMEOUT"})
	second := classify(t, &commonv1.Record{Id: "rec-2", FailureCode: "RAIL_CONGESTION"})
	if first.GetBucket() != second.GetBucket() {
		t.Fatalf("test setup: expected both codes to map to the same bucket, got %v and %v", first.GetBucket(), second.GetBucket())
	}
	if first.GetRationale() == second.GetRationale() {
		t.Errorf("different failure codes produced identical rationale %q", first.GetRationale())
	}
}
