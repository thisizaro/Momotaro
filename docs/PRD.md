# PRD: Momotaro (Payment Failure & Mandate Recovery Agent)

Track 03: AI Revenue Recovery

## 1. Product description

**One-liner**: An agent that watches every payment, mandate, checkout, and
invoice as it degrades, figures out why it's failing, and runs a bounded,
auditable recovery playbook instead of a generic retry, so merchants recover
revenue that would otherwise just be written off.

**The problem, in narrative terms**: A merchant on Razorpay loses revenue in a
dozen small ways that never show up as one clean incident. A card payment
times out at the bank. A UPI autopay mandate fails once and nobody retries it
before the next billing cycle rolls around. A customer opens checkout, sees
the OTP screen, and just leaves. A B2B buyer sits on an overdue invoice
because no one followed up. None of these are catastrophic on their own, but
added up across a merchant's monthly volume, they are a meaningful chunk of
revenue quietly leaking away, and today the only response is either a
generic retry (bad, it retries the wrong thing for the wrong failure) or a
human doing it manually (does not scale).

**The product**: a recovery agent that sits downstream of Razorpay's payment
and mandate events, classifies each failure by root cause, and executes the
one intervention that actually matches that root cause, within hard
compliance and contact limits, and produces a number a finance team can trust,
how much revenue did the agent actually get back, out of how much was at
risk.

**Who it's for**: a **merchant**, meaning a business that uses Razorpay to
collect payments (a D2C brand, a SaaS company billing subscriptions, a
lender collecting EMIs). Not Razorpay's own staff, and definitely not the
end customer. Momotaro would ship as something a merchant switches on.

- *Priya, finance ops lead at a subscription business that uses Razorpay*:
  every month some percentage of her recurring charges fail, and today she
  exports a failed-payment report, eyeballs it, and decides what to retry or
  chase by hand. She does not want another dashboard to babysit. She wants
  the chasing to already have happened, a number she can take to her CFO
  (recovered amount, recovery rate), and confidence that no customer was
  spammed and no rule was broken while it happened.
- *Rahul, Priya's customer*: his UPI autopay failed because his account was
  short that morning. Wants at most a couple of well-timed nudges, not a
  barrage, and ideally for it to be quietly fixed on retry without him doing
  anything.

**How it runs**: continuously, in the background, reacting to failure events
as Razorpay emits them. Nobody uploads a file or presses start. Priya opens
the dashboard to see what already happened, and to work the escalation queue
of records the agent deliberately refused to keep chasing. Full detail of
production shape vs. demo shape is in `docs/ARCHITECTURE.md` section 0a.

**Feature set**:
1. Root-cause classification of payment, mandate, checkout, and invoice
   failures.
2. A bounded intervention playbook per root cause (retry, method-switch nudge,
   reminder sequence, promise-to-pay tracking).
3. **Intervention economics**: every candidate action is scored on expected
   value before it runs, and the agent declines to chase a record when
   chasing it costs more than it can recover. Retry timing is
   cause-aware, not a fixed backoff. See section 2b.
4. Hard stopping rules: retry caps, contact caps, cooldown windows.
5. A batch-level recovery report: at-risk amount, **net** recovered amount
   (after intervention spend), recovery rate by root cause and by
   intervention type.
6. A full, replayable audit trail per record.

## 2. Why this fits me

This track sounds like an ML problem but it is mostly a workflow orchestration
problem. The reasoning surface area is deliberately narrow and bounded (see
"Diagnosis and intervention model" below), while the hard, interesting part is
everything around it: a state machine per payment or mandate, retry budgets,
idempotent action execution, an append-only audit trail, and a batch runner
that produces honest numbers. That is squarely systems design and distributed
systems work, not model training.

## 2a. Diagnosis and intervention model (decided)

A pure lookup table reads as an automation script, not an agent, to a judge
expecting genuine reasoning. A pure LLM making compliance decisions is a
liability. So the reasoning is split into two layers with different trust
levels:

- **Guardrails (deterministic, never delegated to the model)**: retry caps,
  contact caps, cooldown windows, and escalation triggers. These are plain
  code, always enforced the same way, and are exactly what a judge audits for
  compliance. See section 10, "Stopping rules and compliance."
- **Diagnosis and intervention recommendation (LLM reasoning, bounded)**: for
  each failure, an LLM call receives the failure signal (error code, retry
  history, amount, prior outcomes on this instrument) and returns a root
  cause bucket, one recommended action chosen from a fixed enumerated menu
  (never a freeform action), and a short rationale. The rationale is stored
  verbatim in the audit trail, this is what makes the reasoning visible and
  checkable rather than a black box.
- **Fallback**: if the LLM call errors or times out, the record falls back to
  a deterministic rules-based classification for that record, and the
  fallback event is itself logged. This doubles as the "one failure handled
  gracefully" moment worth showing in the demo.

Full technical detail (call shape, prompt structure, fallback trigger
conditions) lives in `docs/ARCHITECTURE.md`, section "Diagnosis: hybrid rules
and LLM reasoning."

## 2b. Intervention economics (decided)

Diagnosing *why* a payment failed only gets you halfway. The next question
is a business one, and it is the one a finance team actually asks: **is
chasing this worth it?** An agent that retries and nudges everything
indiscriminately is not smart, it is just expensive.

Three things follow from taking that seriously:

**1. Interventions cost money, so score them.** An SMS costs roughly ₹0.20,
WhatsApp more, and a payment retry consumes a gateway attempt. Spending ₹15
of nudges to recover ₹50 is bad business, and doing it at scale is
noticeably bad business. So before acting, the agent computes expected value
for each allowed action:

```
EV(action) = P(recovery | action, root_cause, attempt_no) * amount_at_risk
             - direct_cost(action)
             - indirect_cost(action)
```

and picks the highest. If no action has positive expected value, the agent
**closes the record as not worth chasing** rather than spending money on it.
This is a distinct outcome from escalation: escalated means "a human should
look at this," uneconomic means "we deliberately decided this is not worth
the chase," and the report shows them separately. Knowing when not to act is
a feature.

**2. Retry timing should follow the reason for failure, not a fixed
backoff.** An insufficient-funds decline on the 28th of the month should not
be retried on the 29th. It should wait for the 1st to 7th, when salary
credits land in India. A bank timeout, by contrast, should be retried in
minutes, the money was there, the rail was busy. Same "retry" action, very
different timing, and a fixed exponential backoff gets both wrong.

**3. Retry caps protect an asset, they are not just red tape.** Hammering a
card with retries raises your decline rate with the issuer, which degrades
authorization rates on *future* legitimate payments. So the caps in section
11 are doing double duty: regulatory compliance (NPCI/RBI mandate limits)
and protection of the merchant's standing with issuers. That is why they are
deterministic and non-negotiable rather than something the model can talk
its way past.

**Where the probabilities come from, and why this stays honest**: the
`P(recovery | ...)` priors start as a static, checked-in config table of
plausible industry values, and are then refined from the agent's *own
observed outcomes* in the audit log. They are never read from the sealed
`ground_truth` table, doing that would be the agent grading its own exam.
See `docs/ARCHITECTURE.md` section 5a.

## 3. Goals

- Given a batch of synthetic revenue-at-risk events, detect which ones are
  recoverable, diagnose why they are failing, pick a bounded intervention, execute
  it, and record the outcome.
- Report measured money recovered across the batch, not a single cherry-picked
  case.
- Enforce stopping rules and compliance limits so the agent cannot retry or
  contact a customer indefinitely.
- Produce a full audit trail: what was tried, when, why, and what happened.

## 4. Non-goals

- No real payment gateway integration beyond Razorpay test-mode APIs (or a mocked
  equivalent for the hackathon).
- No training of a custom ML classifier. Diagnosis uses an existing LLM API
  call plus deterministic rules (section 2a), not a trained model.
- No real SMS/WhatsApp delivery. A logged, simulated send is enough for the demo.
- No real user/session authentication. The API Gateway checks a static
  shared API key, deliberately, so a judge can try the system with zero
  setup, see `docs/ARCHITECTURE.md` section 17. There is no concept of a
  "user" to authenticate in this system.

## 5. Users

- Primary: the finance/ops team **at a business that uses Razorpay** (not
  Razorpay staff), who today chase failed payments and overdue invoices by
  hand. See section 1 for the full framing.
- Secondary: that business's own customer, who receives at most a bounded
  number of recovery touches and ideally notices nothing.

## 6. Scope for the hackathon

Pick 2, at most 3, of these failure classes to actually implement end to end,
rather than shallow coverage of all of them:

1. Payment degradation -> root cause -> retry or method-switch nudge
2. Failed UPI mandate -> retry sequencer with NPCI-style retry caps
3. Checkout abandonment -> time-boxed reminder sequence
4. B2B overdue invoice -> promise-to-pay tracker with escalation tiers

Recommendation: do (1) and (2) together since they share the same retry/backoff
machinery, then add (3) if time allows. (4) is the most different shape (longer
time horizon, human promises) and is a good stretch goal, not a core requirement.

## 7. Core workflow (per record)

1. **Detect**: ingest a revenue-at-risk event from the synthetic batch.
2. **Diagnose**: classify into a root cause bucket (transient bank issue, hard
   decline, user action needed, risk hold, abandonment, overdue).
3. **Decide**: given the bucket and the record's history so far, pick the next
   bounded action, or decide to stop and escalate to a human.
4. **Act**: execute the action through an idempotent executor (retry call, nudge
   message, escalation ticket).
5. **Record**: append an immutable audit entry regardless of outcome.
6. Repeat until a stopping rule fires (recovered, exhausted, or escalated).

## 8. Functional requirements

- Batch runner that processes N synthetic records (submitted as one batch,
  identified by a `batch_id`) and produces a summary report scoped to that
  batch, not a lifetime cumulative total.
- Root-cause classifier with a fixed, enumerable set of buckets.
- Decision engine that is a deterministic state machine per record, so every
  transition is explainable after the fact.
- Action executor with per-record retry budget and cooldown enforcement.
- Audit log that is append-only and queryable by record id.
- Reporting view: total at-risk amount, recovered amount, recovery rate by
  intervention type, escalation rate.

## 9. Success metrics ("the bar")

- Total revenue at risk vs. total recovered, in the batch, not per-record.
- **Net** recovered: gross recovered minus total intervention spend. This is
  the headline number, a CFO does not care about revenue recovered at a loss.
- Total intervention spend, and cost per rupee recovered.
- Count and value of records **deliberately closed as uneconomic**, shown
  separately from escalations (section 2b).
- Recovery rate broken down by root-cause bucket and by intervention type.
- Zero stopping-rule violations (no record retried or contacted past its cap).
- 100% of records have a complete audit trail from detection to terminal state.

## 10. Non-functional requirements

Targets only, mechanism (Prometheus, tracing, alerting, load-test setup) lives
in `docs/ARCHITECTURE.md`, section "NFRs and observability."

- **Latency**: p95 end-to-end (detect to terminal state or next action queued)
  under 3s per record on the LLM-reasoning path, under 50ms on the
  rules-fallback path. These are starting targets, to be replaced with real
  measured numbers once the load generator is running (see section 12).
- **Throughput**: pipeline sustains at least 50 records/sec end to end without
  unbounded Kafka consumer lag growth. Also a starting target, to be replaced
  with a measured number. Throughput/latency load tests run in the load
  generator's synthetic mode (no real LLM calls) by default, precisely so
  this NFR can be validated repeatedly without a per-call cost, see
  `docs/ARCHITECTURE.md` section 5.
- **Availability/resilience**: an LLM provider failure or timeout never
  blocks or drops a record, it falls through the provider chain and
  ultimately to rules-based classification within the same request (no
  manual retry needed). A *sustained* provider outage must not degrade
  pipeline throughput either, which is what the circuit breakers are for,
  not just the timeouts.
- **Crash safety**: killing any service mid-batch and restarting it loses no
  record and leaves no gap in any audit trail. This is a testable
  requirement, not an aspiration, see `docs/PLAN.md` Phase 2.
- **No single poison record can stall the pipeline.** A record that cannot
  be processed goes to a dead letter queue and is reported as a processing
  failure, never silently dropped and never counted as a business outcome.
- **Correctness invariants** (must hold across every batch run, not just
  typical ones): zero stopping-rule violations, 100% of records have a
  complete, replayable audit trail.
- **Observability**: every service exposes metrics (throughput, latency
  histograms, error/fallback rates) and structured logs correlated by
  `record_id`; a trace exists per record spanning every service it touched;
  alerting exists for consumer lag, elevated LLM fallback rate, and any
  stopping-rule violation (the last one should be treated as a page, since it
  should be impossible).
- **Scalability**: stateless services (classifier, executor) scale
  horizontally with no code change, demonstrated via k8s HPA in the final
  minikube deployment under the load-test profile.

## 11. Stopping rules and compliance (must-have, this is what judges check)

- Hard cap on retry attempts per mandate/payment (mirror real NPCI-style limits).
- Hard cap on customer contact attempts, with a minimum cooldown between them.
- No action taken outside a defined time window (no retries queued once a record
  is past its recovery window).
- Any ambiguous or repeated-failure record downgrades automatically to "needs
  human", it never loops forever.

## 12. Demo script

1. Load a batch of ~50-100 synthetic at-risk records, each seeded with a
   hidden ground-truth recoverability profile in the world simulator (see
   `docs/ARCHITECTURE.md`, "World simulator"), so outcomes are measured
   against a known answer, not just observed.
2. Run the batch runner live, watch the dashboard fill in **live** (a real
   WebSocket push from the API Gateway, not polling, see
   `docs/ARCHITECTURE.md` section 6a) as the world simulator resolves each
   intervention's outcome, including nudges that resolve after a simulated
   delay.
3. Show the summary dashboard: at-risk amount, gross and **net** recovered,
   cost per rupee recovered, recovery rate, and accuracy of root-cause
   classification against the hidden ground truth.
3a. Show the records the agent **deliberately declined to chase** because no
   intervention had positive expected value, and the money that decision
   saved. This is the beat that separates a smart agent from an expensive
   one.
4. Drill into one record's audit trail end to end, including the LLM's stored
   rationale for that record's classification and chosen action.
5. Show one record that hit a stopping rule and was escalated instead of
   retried forever, and one record where the LLM call failed and fell back to
   rules, both are the "graceful failure" the bar is looking for.
6. Run the load generator (`scripts/`) against the deployed system, show real
   p50/p95/p99 latency and sustained throughput, then show the same run
   against the minikube deployment with HPA scaling pods under load.

## 13. Open questions

- Which LLM provider(s) back the diagnosis call, deliberately deferred, cost
  and rate limits need real evaluation first. The design already supports
  this being deferred: the Classifier calls a priority-ordered provider
  chain (candidate primary/secondary providers, falling back to rules) behind
  a swappable interface, and the load generator defaults to a synthetic
  (no real API calls) mode so cost isn't burned during throughput testing.
  See `docs/ARCHITECTURE.md` section 5. Decide actual provider(s) once
  cost/rate-limit numbers are in hand.
- How much of the retry-cap logic should mirror real RBI/NPCI mandate rules
  vs. a simplified stand-in we state explicitly as an assumption?
- Exact latency/throughput NFR numbers in section 10 are starting targets,
  need to be replaced with real measured numbers once the first load test
  runs, and re-confirmed after the minikube/HPA deployment.
