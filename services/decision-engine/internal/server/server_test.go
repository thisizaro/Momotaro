package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	decisionenginev1 "github.com/thisizaro/Momotaro/proto/gen/decisionengine/v1"
	"github.com/thisizaro/Momotaro/services/decision-engine/internal/engine"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fakeResumer stands in for Scheduler.ResumeNudge and RecordDowntimeEvent:
// this package has no logic of its own beyond request validation and
// translating a result to and from the proto shapes, so a fake is enough
// here.
type fakeResumer struct {
	applied bool
	state   commonv1.RecordState
	err     error

	gotRecordID      string
	gotAttemptNumber int
	gotOutcome       commonv1.Outcome
	gotFailureCode   string

	downtimeErr error
	gotDowntime engine.DowntimeEvent
}

func (f *fakeResumer) ResumeNudge(ctx context.Context, recordID string, attemptNumber int, outcome commonv1.Outcome, failureCode string) (bool, commonv1.RecordState, error) {
	f.gotRecordID, f.gotAttemptNumber, f.gotOutcome, f.gotFailureCode = recordID, attemptNumber, outcome, failureCode
	return f.applied, f.state, f.err
}

func (f *fakeResumer) RecordDowntimeEvent(ctx context.Context, evt engine.DowntimeEvent) error {
	f.gotDowntime = evt
	return f.downtimeErr
}

func TestReportDelayedOutcomeRejectsEmptyRecordID(t *testing.T) {
	s := New(&fakeResumer{}, ConfigSnapshot{}, logger.Discard())
	_, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestReportDelayedOutcomeRejectsNonPositiveAttemptNumber(t *testing.T) {
	s := New(&fakeResumer{}, ConfigSnapshot{}, logger.Discard())
	_, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		RecordId: "rec-1", AttemptNumber: 0, Outcome: commonv1.Outcome_OUTCOME_SUCCESS,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestReportDelayedOutcomeRejectsUnspecifiedOutcome(t *testing.T) {
	s := New(&fakeResumer{}, ConfigSnapshot{}, logger.Discard())
	_, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		RecordId: "rec-1", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_UNSPECIFIED,
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestReportDelayedOutcomeForwardsRequestFieldsToResumer(t *testing.T) {
	fake := &fakeResumer{applied: true, state: commonv1.RecordState_RECORD_STATE_RECOVERED}
	s := New(fake, ConfigSnapshot{}, logger.Discard())

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
	s := New(fake, ConfigSnapshot{}, logger.Discard())

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
	s := New(fake, ConfigSnapshot{}, logger.Discard())

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
	s := New(fake, ConfigSnapshot{}, logger.Discard())

	_, err := s.ReportDelayedOutcome(context.Background(), &decisionenginev1.ReportDelayedOutcomeRequest{
		RecordId: "rec-1", AttemptNumber: 1, Outcome: commonv1.Outcome_OUTCOME_SUCCESS,
	})
	if err == nil {
		t.Fatal("ReportDelayedOutcome: want an error, got nil")
	}
}

// validDowntimeRequest is Razorpay's own documented payment.downtime.started
// example (docs/PHASE5_5_IMPLEMENTATION.md Unit Y), already translated into
// this RPC's flat shape the way the API Gateway would.
func validDowntimeRequest() *decisionenginev1.ReportDowntimeEventRequest {
	return &decisionenginev1.ReportDowntimeEventRequest{
		DowntimeId:    "down_F1Zppa6lcVheSE",
		Method:        "netbanking",
		Status:        "started",
		Scheduled:     false,
		Severity:      "high",
		InstrumentKey: "VIJB",
		BeginUnix:     1591935238,
		HasEnd:        false,
	}
}

func TestReportDowntimeEventRejectsEmptyDowntimeID(t *testing.T) {
	s := New(&fakeResumer{}, ConfigSnapshot{}, logger.Discard())
	req := validDowntimeRequest()
	req.DowntimeId = ""
	if _, err := s.ReportDowntimeEvent(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestReportDowntimeEventRejectsEmptyMethod(t *testing.T) {
	s := New(&fakeResumer{}, ConfigSnapshot{}, logger.Discard())
	req := validDowntimeRequest()
	req.Method = ""
	if _, err := s.ReportDowntimeEvent(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestReportDowntimeEventRejectsAnUnrecognisedStatus(t *testing.T) {
	s := New(&fakeResumer{}, ConfigSnapshot{}, logger.Discard())
	req := validDowntimeRequest()
	req.Status = "cancelled" // not one of started/updated/resolved
	if _, err := s.ReportDowntimeEvent(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

func TestReportDowntimeEventRejectsMissingBegin(t *testing.T) {
	s := New(&fakeResumer{}, ConfigSnapshot{}, logger.Discard())
	req := validDowntimeRequest()
	req.BeginUnix = 0
	if _, err := s.ReportDowntimeEvent(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument", err)
	}
}

// Accepts every documented severity value without treating the list as
// exhaustive: an unrecognised one must not be rejected, only passed through
// (docs/PHASE5_5_IMPLEMENTATION.md Unit Y: "do not assume that list is
// exhaustive; handle an unknown value without crashing").
func TestReportDowntimeEventAcceptsAnUnrecognisedSeverity(t *testing.T) {
	fake := &fakeResumer{}
	s := New(fake, ConfigSnapshot{}, logger.Discard())
	req := validDowntimeRequest()
	req.Severity = "critical"

	if _, err := s.ReportDowntimeEvent(context.Background(), req); err != nil {
		t.Fatalf("ReportDowntimeEvent: %v, want no error for an unrecognised severity", err)
	}
	if fake.gotDowntime.Severity != "critical" {
		t.Errorf("resumer got Severity = %q, want it passed through verbatim", fake.gotDowntime.Severity)
	}
}

// begin_unix/end_unix are UNIX SECONDS (docs/PHASE5_5_IMPLEMENTATION.md Unit
// Y: "getting this wrong is a 1000x error that will look plausible"). This
// pins the conversion so a future edit that swaps in milliseconds by
// accident fails loudly here rather than shipping silently.
func TestReportDowntimeEventConvertsUnixSecondsCorrectly(t *testing.T) {
	fake := &fakeResumer{}
	s := New(fake, ConfigSnapshot{}, logger.Discard())
	req := validDowntimeRequest()
	req.HasEnd = true
	req.EndUnix = 1591938838 // one hour after BeginUnix

	if _, err := s.ReportDowntimeEvent(context.Background(), req); err != nil {
		t.Fatalf("ReportDowntimeEvent: %v", err)
	}
	wantBegin := time.Unix(1591935238, 0).UTC()
	if !fake.gotDowntime.Begin.Equal(wantBegin) {
		t.Errorf("Begin = %v, want %v", fake.gotDowntime.Begin, wantBegin)
	}
	if fake.gotDowntime.End == nil {
		t.Fatal("End = nil, want a value: HasEnd was true")
	}
	wantEnd := time.Unix(1591938838, 0).UTC()
	if !fake.gotDowntime.End.Equal(wantEnd) {
		t.Errorf("End = %v, want %v", *fake.gotDowntime.End, wantEnd)
	}
	if got, want := fake.gotDowntime.End.Sub(fake.gotDowntime.Begin), time.Hour; got != want {
		t.Errorf("End - Begin = %v, want exactly %v", got, want)
	}
}

// end: null while a downtime is ongoing (docs/PHASE5_5_IMPLEMENTATION.md
// Unit Y) must become a nil *time.Time, never a zero-value time.Time that
// looks like a real (and wildly wrong) timestamp.
func TestReportDowntimeEventLeavesEndNilWhenHasEndIsFalse(t *testing.T) {
	fake := &fakeResumer{}
	s := New(fake, ConfigSnapshot{}, logger.Discard())

	if _, err := s.ReportDowntimeEvent(context.Background(), validDowntimeRequest()); err != nil {
		t.Fatalf("ReportDowntimeEvent: %v", err)
	}
	if fake.gotDowntime.End != nil {
		t.Errorf("End = %v, want nil: this downtime is still ongoing", *fake.gotDowntime.End)
	}
}

func TestReportDowntimeEventForwardsEveryFieldToTheResumer(t *testing.T) {
	fake := &fakeResumer{}
	s := New(fake, ConfigSnapshot{}, logger.Discard())
	req := validDowntimeRequest()
	req.Scheduled = true

	if _, err := s.ReportDowntimeEvent(context.Background(), req); err != nil {
		t.Fatalf("ReportDowntimeEvent: %v", err)
	}
	got := fake.gotDowntime
	if got.DowntimeID != "down_F1Zppa6lcVheSE" {
		t.Errorf("DowntimeID = %q", got.DowntimeID)
	}
	if got.Method != "netbanking" {
		t.Errorf("Method = %q", got.Method)
	}
	if got.Status != "started" {
		t.Errorf("Status = %q", got.Status)
	}
	if !got.Scheduled {
		t.Error("Scheduled = false, want true")
	}
	if got.Instrument != "VIJB" {
		t.Errorf("Instrument = %q", got.Instrument)
	}
}

func TestReportDowntimeEventReturnsAppliedTrueOnSuccess(t *testing.T) {
	s := New(&fakeResumer{}, ConfigSnapshot{}, logger.Discard())
	resp, err := s.ReportDowntimeEvent(context.Background(), validDowntimeRequest())
	if err != nil {
		t.Fatalf("ReportDowntimeEvent: %v", err)
	}
	if !resp.GetApplied() {
		t.Error("Applied = false, want true")
	}
}

func TestReportDowntimeEventPropagatesResumerError(t *testing.T) {
	fake := &fakeResumer{downtimeErr: errors.New("postgres unavailable")}
	s := New(fake, ConfigSnapshot{}, logger.Discard())

	if _, err := s.ReportDowntimeEvent(context.Background(), validDowntimeRequest()); err == nil {
		t.Fatal("ReportDowntimeEvent: want an error, got nil")
	}
}

// A resolved event carries no begin_unix of its own in a minimal payload in
// principle, but Razorpay's documented shape always sends the full entity,
// so this pins that ReportDowntimeEvent does not special-case "resolved" on
// validation: it is still required to look like a real event.
func TestReportDowntimeEventStillValidatesAResolvedEvent(t *testing.T) {
	s := New(&fakeResumer{}, ConfigSnapshot{}, logger.Discard())
	req := validDowntimeRequest()
	req.Status = "resolved"
	req.BeginUnix = 0

	if _, err := s.ReportDowntimeEvent(context.Background(), req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want InvalidArgument: resolved still needs begin_unix", err)
	}
}

// testConfigSnapshot is a fully populated, distinguishable-per-field
// ConfigSnapshot, so a test asserting on GetAgentConfig's response can tell
// a wired-through field from an accidentally zero one.
func testConfigSnapshot() ConfigSnapshot {
	return ConfigSnapshot{
		DemoTimeScale: 300000,
		Guardrails: engine.GuardrailConfig{
			MaxRetries:      3,
			MaxContacts:     3,
			ContactCooldown: 24 * time.Hour,
			RecoveryWindow:  7 * 24 * time.Hour,
		},
		LLMSampleRate:               0.15,
		RouteConfidenceThreshold:    0.6,
		ClassifyConfidenceThreshold: 0.4,
		NudgeMaxChars:               160,
	}
}

// GetAgentConfig backs GET /v1/demo/config (docs/DEMO_READINESS.md Unit
// AM): it must return exactly what this process was started with, since
// the whole point of proxying through the Decision Engine rather than
// having the Gateway re-read os.Getenv is that the two can never drift
// (docs/DECISIONS.md).
func TestGetAgentConfigReturnsTheConfiguredValuesVerbatim(t *testing.T) {
	s := New(&fakeResumer{}, testConfigSnapshot(), logger.Discard())

	resp, err := s.GetAgentConfig(context.Background(), &decisionenginev1.GetAgentConfigRequest{})
	if err != nil {
		t.Fatalf("GetAgentConfig: %v", err)
	}

	if resp.GetDemoTimeScale() != 300000 {
		t.Errorf("DemoTimeScale = %v, want 300000", resp.GetDemoTimeScale())
	}
	if resp.GetMaxRetries() != 3 {
		t.Errorf("MaxRetries = %d, want 3", resp.GetMaxRetries())
	}
	if resp.GetMaxContacts() != 3 {
		t.Errorf("MaxContacts = %d, want 3", resp.GetMaxContacts())
	}
	if resp.GetContactCooldownSeconds() != int64((24 * time.Hour).Seconds()) {
		t.Errorf("ContactCooldownSeconds = %d, want %d", resp.GetContactCooldownSeconds(), int64((24 * time.Hour).Seconds()))
	}
	if resp.GetRecoveryWindowSeconds() != int64((7 * 24 * time.Hour).Seconds()) {
		t.Errorf("RecoveryWindowSeconds = %d, want %d", resp.GetRecoveryWindowSeconds(), int64((7*24*time.Hour).Seconds()))
	}
	if resp.GetLlmSampleRate() != 0.15 {
		t.Errorf("LlmSampleRate = %v, want 0.15", resp.GetLlmSampleRate())
	}
	if resp.GetRouteConfidenceThreshold() != 0.6 {
		t.Errorf("RouteConfidenceThreshold = %v, want 0.6", resp.GetRouteConfidenceThreshold())
	}
	if resp.GetClassifyConfidenceThreshold() != 0.4 {
		t.Errorf("ClassifyConfidenceThreshold = %v, want 0.4", resp.GetClassifyConfidenceThreshold())
	}
	if resp.GetNudgeMaxChars() != 160 {
		t.Errorf("NudgeMaxChars = %d, want 160", resp.GetNudgeMaxChars())
	}
	if resp.GetDowntimeMaxUnresolvedHoldSeconds() != int64(engine.DowntimeMaxUnresolvedHold.Seconds()) {
		t.Errorf("DowntimeMaxUnresolvedHoldSeconds = %d, want %d", resp.GetDowntimeMaxUnresolvedHoldSeconds(), int64(engine.DowntimeMaxUnresolvedHold.Seconds()))
	}
}

// GetAgentConfig never calls the resumer at all: it is a pure read of the
// snapshot captured at startup, not a query against Postgres or anything
// else that could fail or block, which is exactly why it is safe to poll
// from a dashboard.
func TestGetAgentConfigNeverTouchesTheResumer(t *testing.T) {
	fake := &fakeResumer{}
	s := New(fake, testConfigSnapshot(), logger.Discard())

	if _, err := s.GetAgentConfig(context.Background(), &decisionenginev1.GetAgentConfigRequest{}); err != nil {
		t.Fatalf("GetAgentConfig: %v", err)
	}
	if fake.gotRecordID != "" || fake.gotDowntime != (engine.DowntimeEvent{}) {
		t.Error("GetAgentConfig called the resumer, want a pure read of the config snapshot")
	}
}
