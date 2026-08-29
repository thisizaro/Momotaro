package server

import (
	"context"
	"errors"
	"testing"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	decisionenginev1 "github.com/thisizaro/Momotaro/proto/gen/decisionengine/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeResumer stands in for Scheduler.ResumeNudge: this package has no
// logic of its own beyond request validation and translating that
// method's result to and from the proto shapes, so a fake is enough here.
type fakeResumer struct {
	applied bool
	state   commonv1.RecordState
	err     error

	gotRecordID      string
	gotAttemptNumber int
	gotOutcome       commonv1.Outcome
	gotFailureCode   string
}

func (f *fakeResumer) ResumeNudge(ctx context.Context, recordID string, attemptNumber int, outcome commonv1.Outcome, failureCode string) (bool, commonv1.RecordState, error) {
	f.gotRecordID, f.gotAttemptNumber, f.gotOutcome, f.gotFailureCode = recordID, attemptNumber, outcome, failureCode
	return f.applied, f.state, f.err
}

func TestReportDelayedOutcomeRejectsEmptyRecordID(t *testing.T) {
	s := New(&fakeResumer{}, logger.Discard())
	_, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestReportDelayedOutcomeRejectsNonPositiveAttemptNumber(t *testing.T) {
	s := New(&fakeResumer{}, logger.Discard())
	_, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		RecordId: "rec-1", AttemptNumber: 0, Outcome: commonv1.Outcome_OUTCOME_SUCCESS,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestReportDelayedOutcomeRejectsUnspecifiedOutcome(t *testing.T) {
	s := New(&fakeResumer{}, logger.Discard())
	_, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		RecordId: "rec-1", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_UNSPECIFIED,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestReportDelayedOutcomeForwardsRequestFieldsToResumer(t *testing.T) {
	fake := &fakeResumer{applied: true, state: commonv1.RecordState_RECORD_STATE_RECOVERED}
	s := New(fake, logger.Discard())

	_, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		RecordId: "rec-1", AttemptNumber: 3, Outcome: commonv1.Outcome_OUTCOME_FAILURE, FailureCode: "CUSTOMER_UNREACHABLE",
	})
	if err != nil {
		t.Fatalf("ReportDelayedOutcome: %v", err)
	}
	if fake.gotRecordID != "rec-1" {
		t.Errorf("resumer got recordID = %q, want %q", fake.gotRecordID, "rec-1")
	}
	if fake.gotAttemptNumber != 3 {
		t.Errorf("resumer got attemptNumber = %d, want 3", fake.gotAttemptNumber)
	}
	if fake.gotOutcome != commonv1.Outcome_OUTCOME_FAILURE {
		t.Errorf("resumer got outcome = %v, want OUTCOME_FAILURE", fake.gotOutcome)
	}
	if fake.gotFailureCode != "CUSTOMER_UNREACHABLE" {
		t.Errorf("resumer got failureCode = %q, want %q", fake.gotFailureCode, "CUSTOMER_UNREACHABLE")
	}
}

func TestReportDelayedOutcomeReturnsResumerResultVerbatim(t *testing.T) {
	fake := &fakeResumer{applied: true, state: commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED}
	s := New(fake, logger.Discard())

	resp, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		RecordId: "rec-1", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_FAILURE,
	})
	if err != nil {
		t.Fatalf("ReportDelayedOutcome: %v", err)
	}
	if !resp.GetApplied() {
		t.Error("Applied = false, want true")
	}
	if resp.GetResultingState() != commonv1.RecordState_RECORD_STATE_RETRY_SCHEDULED {
		t.Errorf("ResultingState = %v, want RETRY_SCHEDULED", resp.GetResultingState())
	}
}

// A discarded report (Applied=false) is a normal response, not an error:
// this RPC is at-least-once, so a redelivered or late-arriving report for a
// record that already moved on is expected traffic.
func TestReportDelayedOutcomeDiscardIsNotAnError(t *testing.T) {
	fake := &fakeResumer{applied: false, state: commonv1.RecordState_RECORD_STATE_RECOVERED}
	s := New(fake, logger.Discard())

	resp, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		RecordId: "rec-1", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS,
	})
	if err != nil {
		t.Fatalf("ReportDelayedOutcome: %v, want no error for a discarded report", err)
	}
	if resp.GetApplied() {
		t.Error("Applied = true, want false")
	}
}

func TestReportDelayedOutcomePropagatesResumerError(t *testing.T) {
	fake := &fakeResumer{err: errors.New("postgres unavailable")}
	s := New(fake, logger.Discard())

	_, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		RecordId: "rec-1", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS,
	})
	if err == nil {
		t.Fatal("ReportDelayedOutcome: want an error, got nil")
	}
}
