package engine

import (
	"context"
	"testing"
	"time"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"google.golang.org/grpc"
)

// recordingClassifier captures what ComposeNudge was actually asked for.
type recordingClassifier struct {
	classifierv1.ClassifierServiceClient
	forceTemplateOnly []bool
}

func (r *recordingClassifier) ComposeNudge(ctx context.Context, in *classifierv1.ComposeNudgeRequest, _ ...grpc.CallOption) (*classifierv1.ComposeNudgeResponse, error) {
	r.forceTemplateOnly = append(r.forceTemplateOnly, in.GetForceTemplateOnly())
	return &classifierv1.ComposeNudgeResponse{Message: "ok"}, nil
}

// LLM_SAMPLE_RATE was a ceiling on classification only. Nudge composition
// went to the provider chain unbudgeted, so a live run that nudged 146
// records made 479 Groq attempts and tripped the circuit breaker 361 times,
// after which everything degraded to rules anyway (docs/INCIDENTS.md
// 2026-09-03). The ceiling has to cover both paths or it is not a ceiling.
func TestComposeNudgeRespectsTheSamplingCeiling(t *testing.T) {
	rc := &recordingClassifier{}
	c := &clients{
		classifier:    rc,
		callTimeout:   time.Second,
		nudgeMaxChars: 160,
		llmBudget:     newLLMBudget(0),
	}

	rec := &commonv1.Record{Id: "rec-1", AmountPaise: 1000}
	for i := 0; i < 3; i++ {
		if _, err := c.composeNudge(context.Background(), rec, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, 1); err != nil {
			t.Fatalf("composeNudge: %v", err)
		}
	}

	for i, forced := range rc.forceTemplateOnly {
		if !forced {
			t.Errorf("call %d: ForceTemplateOnly = false, want true: rate 0 must place no live nudge call", i)
		}
	}
}

// With budget available a nudge is allowed to reach the live chain, or the
// ceiling would be a permanent off switch rather than a limit.
func TestComposeNudgeAllowsALiveCallWithinBudget(t *testing.T) {
	rc := &recordingClassifier{}
	c := &clients{
		classifier:    rc,
		callTimeout:   time.Second,
		nudgeMaxChars: 160,
		llmBudget:     newLLMBudget(1.0),
	}

	rec := &commonv1.Record{Id: "rec-1", AmountPaise: 1000}
	if _, err := c.composeNudge(context.Background(), rec, commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT, commonv1.ActionType_ACTION_TYPE_NUDGE_REMINDER, 1); err != nil {
		t.Fatalf("composeNudge: %v", err)
	}
	if len(rc.forceTemplateOnly) != 1 || rc.forceTemplateOnly[0] {
		t.Errorf("ForceTemplateOnly = %v, want false at rate 1.0", rc.forceTemplateOnly)
	}
}

// NewScheduler must produce clients with a usable budget. It did not when
// composeNudge first started consulting one, and because the Scheduler is
// the only thing that composes nudges, that was a nil dereference on the
// first nudge of any real run, not merely in a test
// (docs/INCIDENTS.md 2026-09-03).
func TestNewSchedulerGivesItsClientsAnLLMBudget(t *testing.T) {
	s := NewScheduler(nil, nil, nil, nil, nil, nil, SchedulerConfig{
		CallTimeout:   time.Second,
		NudgeMaxChars: 160,
		LLMSampleRate: 0.15,
	})
	if s.clients.llmBudget == nil {
		t.Fatal("clients.llmBudget is nil: composeNudge would panic on the first nudge")
	}
	// Not asserting the first call is granted: llmBudget is a strict
	// running ratio, so at rate 0.15 the very first record would be 1 of 1,
	// which correctly exceeds the ceiling. Calls become affordable as the
	// denominator grows. What matters here is that the budget exists and
	// answers instead of panicking.
	s.clients.llmBudget.consider(true)
}
