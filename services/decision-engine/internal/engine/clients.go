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
	classifier  classifierv1.ClassifierServiceClient
	executor    executorv1.ExecutorServiceClient
	callTimeout time.Duration
	// routeConfidenceThreshold is LLM_ROUTE_CONFIDENCE_THRESHOLD
	// (docs/DEMO_READINESS.md Unit AI): classify routes a record to a live
	// model only when the deterministic rules engine's own confidence for
	// it is below this. Not the same knob as ClassifyConfidenceThreshold
	// (engine.go), which decides whether to escalate AFTER classification;
	// this one decides whether to spend a call BEFORE it, on the rules
	// engine's answer alone.
	routeConfidenceThreshold float64
	// llmBudget is LLM_SAMPLE_RATE reinterpreted as a ceiling rather than a
	// selector (docs/ARCHITECTURE.md section 17): it bounds the running
	// fraction of every classified record that ever reaches a live
	// provider, whether or not routeConfidenceThreshold judged it
	// ambiguous. See llm_budget.go for why a running ratio rather than a
	// per-batch rank.
	llmBudget *llmBudget
	// nudgeMaxChars bounds a composed nudge's raw length (before amount
	// substitution). Zero here would make every ComposeNudge call fail
	// validation on the Classifier's side (an SMS-realistic cap of 0 chars
	// accepts nothing), so NewScheduler validates it is positive rather than
	// leaving that failure to surface confusingly at request time.
	nudgeMaxChars int32
}

// exhaustedHop is appended to a rules-only classification when routing
// judged the record ambiguous but the sampling ceiling was already spent:
// the deterministic answer is still real and still used, this only marks
// that a live call was wanted and not made, so Reporting can count it
// (services/reporting/internal/server/exhaustion.go,
// docs/API_GATEWAY.md's llm_quota_exhausted_count). Provider is not one of
// the classifier's own registered rungs on purpose: this hop is recorded
// by the Decision Engine, about a call it chose not to place, not by
// anything inside the classifier's provider chain.
var exhaustedHop = &commonv1.ProviderHop{Provider: "sample_budget", Result: "exhausted"}

// classify routes a record by ambiguity rather than by a random sample
// (docs/DEMO_READINESS.md Unit AI, replacing Phase 3 Unit H's
// hash-of-record-id sampledForLLM). It always asks the deterministic rules
// engine first (force_rules_only=true): that rung does no I/O and cannot
// fail (SPEC.md section 4.7), so this "peek" is cheap, and its Confidence
// is the actual signal routing needs. Only when that confidence is below
// routeConfidenceThreshold, AND the running budget still allows it, does a
// second call go out with the full provider chain enabled.
//
// This never changes what the model is allowed to decide: the guardrails
// and the economics scorer downstream (engine.go's decide) still only ever
// read the winning response's Bucket, exactly as before. Routing only
// changes which records the model gets to see at all.
func (c *clients) classify(ctx context.Context, record *commonv1.Record, history, instrumentHistory []*commonv1.InterventionAttempt) (*classifierv1.ClassifyResponse, error) {
	ruleResp, err := c.classifyOnce(ctx, record, history, instrumentHistory, true)
	if err != nil {
		return nil, err
	}

	eligible := ruleResp.GetConfidence() < c.routeConfidenceThreshold
	if !c.llmBudget.consider(eligible) {
		if eligible {
			ruleResp.Hops = append(ruleResp.Hops, exhaustedHop)
		}
		return ruleResp, nil
	}

	return c.classifyOnce(ctx, record, history, instrumentHistory, false)
}

// classifyOnce places one Classify RPC. forceRulesOnly=true is the
// confidence "peek" (and also the load generator's original cost-safety
// switch, SPEC.md section 4.8); false allows the full provider chain,
// including a live model, on this specific record.
func (c *clients) classifyOnce(ctx context.Context, record *commonv1.Record, history, instrumentHistory []*commonv1.InterventionAttempt, forceRulesOnly bool) (*classifierv1.ClassifyResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.callTimeout)
	defer cancel()
	return c.classifier.Classify(callCtx, &classifierv1.ClassifyRequest{
		Record:            record,
		History:           history,
		InstrumentHistory: instrumentHistory,
		ForceRulesOnly:    forceRulesOnly,
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

	// The sampling ceiling covers composition as well as classification.
	// It did not before, and a live run that nudged 146 records made 479
	// Groq attempts and held the circuit breaker open for 361 of them,
	// after which every classification degraded to rules too: an
	// unbudgeted path spent the budget the budgeted path was protecting
	// (docs/INCIDENTS.md 2026-09-03). Every nudge is eligible for a live
	// call, so this is consider(true) unconditionally; when the ceiling is
	// spent the request is marked template-only and the Classifier's
	// terminal rung answers, which is a real Hinglish message either way.
	if !c.llmBudget.consider(true) {
		return c.composeNudgeTemplateOnly(callCtx, record, bucket, action, contactNumber)
	}

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

// composeNudgeTemplateOnly asks for the same wording with the live rungs
// skipped, used when the sampling ceiling is spent. The Classifier's
// terminal rung is the static Hinglish template, which does no I/O and
// cannot fail, so this always produces a message.
func (c *clients) composeNudgeTemplateOnly(ctx context.Context, record *commonv1.Record, bucket commonv1.RootCauseBucket, action commonv1.ActionType, contactNumber int32) (composedNudge, error) {
	resp, err := c.classifier.ComposeNudge(ctx, &classifierv1.ComposeNudgeRequest{
		Record:            record,
		Bucket:            bucket,
		ActionType:        action,
		Locale:            nudgeLocale,
		ContactNumber:     contactNumber,
		MaxChars:          c.nudgeMaxChars,
		ForceTemplateOnly: true,
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
