package engine

import (
	"context"
	"time"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
)

// clients wraps the two gRPC dependencies this service calls out to,
// bounding every call with an explicit deadline (docs/ENGINEERING.md
// section 3). Kept separate from engine.go and scheduler.go so neither has
// to repeat the context-deadline boilerplate per call site.
type clients struct {
	classifier    classifierv1.ClassifierServiceClient
	executor      executorv1.ExecutorServiceClient
	callTimeout   time.Duration
	llmSampleRate float64
	// nudgeMaxChars bounds a composed nudge's raw length (before amount
	// substitution). Zero here would make every ComposeNudge call fail
	// validation on the Classifier's side (an SMS-realistic cap of 0 chars
	// accepts nothing), so NewScheduler validates it is positive rather than
	// leaving that failure to surface confusingly at request time.
	nudgeMaxChars int32
}

func (c *clients) classify(ctx context.Context, record *commonv1.Record, history, instrumentHistory []*commonv1.InterventionAttempt) (*classifierv1.ClassifyResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	return c.classifier.Classify(callCtx, &classifierv1.ClassifyRequest{
		Record:            record,
		History:           history,
		InstrumentHistory: instrumentHistory,
		ForceRulesOnly:    !sampledForLLM(record.GetId(), c.llmSampleRate),
	})
}

// evScoreAtDecision and pRecoveryAtDecision are the economics scorer's
// decision snapshot from when this action was scheduled (or re-scored),
// forwarded so the Executor can persist what was actually decided rather
// than nothing at all (docs/PHASE2_IMPLEMENTATION.md Unit G). The Decision
// Engine is the only service that scores; the Executor never recomputes
// these.
//
// message is the ComposeNudge equivalent for a nudge-type action, empty for
// a retry: the Executor's own ports.Router just forwards it to the
// notification port, it never writes wording itself
// (docs/PHASE5_IMPLEMENTATION.md Unit E). messageSource is which rung wrote
// it (SOURCE_UNSPECIFIED for a retry), persisted so the audit trail can
// tell a generated message from a templated one.
func (c *clients) execute(ctx context.Context, recordID, batchID string, action commonv1.ActionType, attemptNumber int32, amountPaise int64, evScoreAtDecision, pRecoveryAtDecision float64, message string, messageSource commonv1.Source) (*executorv1.ExecuteResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	return c.executor.Execute(callCtx, &executorv1.ExecuteRequest{
		RecordId:            recordID,
		BatchId:             batchID,
		ActionType:          action,
		AttemptNumber:       attemptNumber,
		AmountPaise:         amountPaise,
		EvScoreAtDecision:   evScoreAtDecision,
		PRecoveryAtDecision: pRecoveryAtDecision,
		Message:             message,
		MessageSource:       messageSource,
	})
}

// composedNudge is composeNudge's result: the text and which rung wrote it.
type composedNudge struct {
	message string
	source  commonv1.Source
}

// composeNudge asks the Classifier to write a nudge's wording
// (docs/ARCHITECTURE.md section 5b). contactNumber is 1 for the first
// contact on this record, 2+ for a follow-up (attemptNumber IS the contact
// number: each attempt on a nudge-type action is one contact,
// docs/ARCHITECTURE.md section 7).
func (c *clients) composeNudge(ctx context.Context, record *commonv1.Record, bucket commonv1.RootCauseBucket, action commonv1.ActionType, contactNumber int32) (composedNudge, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	resp, err := c.classifier.ComposeNudge(callCtx, &classifierv1.ComposeNudgeRequest{
		Record:        record,
		Bucket:        bucket,
		ActionType:    action,
		Locale:        nudgeLocale,
		ContactNumber: contactNumber,
		MaxChars:      c.nudgeMaxChars,
	})
	if err != nil {
		return composedNudge{}, err
	}
	return composedNudge{message: resp.GetMessage(), source: resp.GetSource()}, nil
}

// nudgeLocale is the only locale this project composes in
// (docs/ARCHITECTURE.md section 5b: "en-IN-hinglish is the default for this
// project"). Not configurable: there is no second locale to switch to yet,
// and a config knob with one legal value is not a knob.
const nudgeLocale = "en-IN-hinglish"
