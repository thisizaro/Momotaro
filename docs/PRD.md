# PRD: Momotaro (Payment Failure & Mandate Recovery Agent)

Track 03: AI Revenue Recovery

## 0. The bar we are scored against (verbatim)

Recorded here on 2026-08-29 because it was nowhere in the repo before, and
every triage decision from here should be argued against this text rather
than against our own summary of it. Source: Razorpay AI Buildathon track
page.

**Track 03, AI Revenue Recovery:**

> "Build an agent that detects revenue at risk, determines the right
> intervention, and executes a bounded recovery workflow."

> Must demonstrate "measured money recovered across a batch, with compliant
> escalation, stopping rules, and an audit trail."

**General evaluation criteria, applied across tracks:**

> **AI Judgment**: whether AI tools, LLMs, or agents were applied
> appropriately "instead of forcing unnecessary tech stacks".

> **Failure Recovery**: "how you identified system failures at runtime and
> engineered graceful fallbacks".

Track 01's phrasing is worth borrowing as the house standard for money
handling, since it states plainly what this track only implies: "Every money
action explainable, bounded and gated."

**Selection process**: shortlisted builders go straight to a technical
panel. No aptitude test, no group discussion. **The artefact being judged is
therefore something defended in conversation, not only something
demonstrated on a screen.** That materially changes what is worth building:
depth that can be explained beats surface area that cannot, and
`docs/DECISIONS.md` plus `docs/INCIDENTS.md` are first-class deliverables
rather than internal hygiene.

**How each clause maps onto this system**, so a gap is visible rather than
assumed:

| Rubric clause | Where it lives |
|---|---|
| detects revenue at risk | Ingestion, Classifier (§2a) |
| determines the right intervention | Decision Engine economics scorer (§2b) |
| bounded recovery workflow | Guardrails: retry budgets, contact caps, cooldowns, recovery window (§11) |
| stopping rules | §11, plus Audit's continuous invariant verifier proving zero violations |
| an audit trail | `AUDIT_ENTRY`, `GetRecordAudit`, provider hops |
| compliant escalation | §11a, the cited TRAI/RBI rules |
| **measured money recovered across a batch** | **Reporting Service, §9. This is the headline deliverable.** |

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
5. **Hinglish nudge composition**: when the chosen action is a nudge, the
   message itself is generated in natural code-mixed Hinglish rather than
   pulled from a stilted template, with a template fallback if the model is
   unavailable. Text only, not voice. See `docs/ARCHITECTURE.md` §5b.
6. A batch-level recovery report: at-risk amount, **net** recovered amount
   (after intervention spend), recovery rate by root cause and by
   intervention type.
7. A full, replayable audit trail per record, including the actual message
   text sent.

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
- No real SMS/WhatsApp delivery. The message text is genuinely composed
  (section 1, feature 5) but the send itself is simulated and logged.
- No voice. "Hinglish voice recovery" is a listed track direction, but
  text-to-speech is real work for little added credit, so nudges are
  composed as text only.
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

## 11a. What "compliant" actually means (cited, not asserted)

The track asks for "compliant escalation". Until 2026-08-29 this document
used the word without ever saying which rules it meant, and §13 carried the
gap as an open question ("how much of the retry-cap logic should mirror real
RBI/NPCI mandate rules vs. a simplified stand-in"). Two real Indian
regulations bear directly on what this agent does, and both are cheap to
honour because the guardrail layer that enforces them already exists.

**TRAI TCCCPR 2018 (customer contact timing).** Commercial communications are
classified Transactional, Service, Promotional or Government. Promotional
messages may only be delivered between 10:00 and 21:00 IST and may not be
sent to numbers registered on DND. Transactional and Service messages carry
neither restriction.

The interesting question, and the one worth being able to answer at a panel,
is which category a payment recovery nudge falls into. A message telling an
existing customer that a payment they initiated has failed is defensibly a
Service message. A message trying to win back an abandoned checkout is closer
to Promotional. This system takes the conservative position: **all
customer-contacting interventions respect the 10:00 to 21:00 IST window**,
because the cost of being wrong (a regulatory breach and an annoyed customer
at 3am) exceeds the cost of a delayed nudge, and because a recovery agent
that can quietly message people overnight is the exact failure mode a
"bounded" workflow is supposed to prevent. A nudge that becomes due outside
the window is deferred to the next window open, not dropped.

**RBI Digital Payments E-mandate Framework (recurring debits).** An issuer
must send a pre-transaction notification to the customer **at least 24 hours
before** an auto-debit, showing merchant name, amount and debit date, and a
post-transaction notification afterwards. The mandate itself is registered
only after additional factor authentication.

Consequence for this system: a retry on a `RECORD_TYPE_MANDATE` record is not
a free action that can be scheduled minutes out the way a card retry can. It
carries a mandatory 24 hour notification lead time. `RETRY_MANDATE_LEAD_TIME`
encodes this, and the cause-aware scheduler treats it as a floor that the
salary-window calculation may push later but never earlier.

**Both are guardrails, not model decisions.** They sit in the same layer as
retry budgets and contact caps, are evaluated inside the same transaction as
the state change they gate, and appear in the audit trail with a reason
naming the rule. Per §2b's fixed ordering, a guardrail can only ever remove
an option, never add one, so neither rule can cause an action that would not
otherwise have happened.

**Stated limits, so this is not oversold.** These are two rules, not a
compliance programme. DND list checking is not implemented (there is no real
telecom integration to check against). The 24 hour mandate lead time is
enforced as a scheduling floor, but this system does not itself send the
pre-debit notification, since in the real flow that is the issuer's
obligation, not the merchant's agent's. NPCI's per-mandate presentation
limits are still the simplified stand-in `docs/ARCHITECTURE.md` §17
describes.

## 12. Demo script

1. Load a batch of ~50-100 synthetic at-risk records, each seeded with a
   hidden ground-truth recoverability profile in the world simulator (see
   `docs/ARCHITECTURE.md`, "World simulator"), so outcomes are measured
   against a known answer, not just observed. **This must be a batch seeded
   with `scripts/batchgen` and selected via `GET /v1/batches`, not one made
   with the dashboard's own "generate" button.** The button's `count` form
   (`docs/API_GATEWAY.md`) submits through Ingestion, which never writes
   `GROUND_TRUTH`, so a batch made that way has no accuracy score and no
   baseline comparison, i.e. neither of beat 3's headline numbers. Confirm
   which batch is selected before going on stage.
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
   rationale for that record's classification and chosen action, and the
   actual Hinglish message text that was composed and sent.
5. Show one record that hit a stopping rule and was escalated instead of
   retried forever, and one record where the LLM call failed and fell back to
   rules, both are the "graceful failure" the bar is looking for.
6. Run the load generator (`scripts/`) against the deployed system, show real
   p50/p95/p99 latency and sustained throughput, then show the same run
   against the minikube deployment with HPA scaling pods under load.

### 12a. What is real and what is simulated (say this out loud)

Two different things in this system get loosely called "fake", and only one
of them would be a problem if a judge found it. Stating the difference
plainly is better than being asked.

**The World Simulator is a simulator on purpose, and that is not a
concession.** When the Executor decides to retry a payment, something has to
answer "succeeded" or "failed". In production that is a bank. In a hackathon
there is no bank, so `demo/world-simulator` stands in for one, holding a
sealed ground truth the decision path provably cannot read
(`test/integrity/ground_truth_isolation_test.go`). It lives under `demo/`
rather than `services/` precisely so the boundary is visible in the directory
structure. This is what makes outcomes *measurable* against a known answer
instead of merely observed, which is a stronger position than a real
integration would give us in the time available. Same for
`demo/notification-simulator`, which logs the message it would have sent.

**The dashboard's mock backend is Phase 0 scaffolding that has already done
its job.** `web/` was deliberately scaffolded against the written
`docs/API_GATEWAY.md` contract using mocked responses, specifically so UI
work could start in parallel with backend work instead of waiting on it
(`docs/PLAN.md` Phase 0). That was the right call and it worked. Connecting
it to the real Gateway was always Phase 5 Unit H, not a decision being
revisited here.

The only thing worth stating explicitly is the difference in *kind* between
the two stand-ins, because both get loosely called "mock" and they are not
the same thing. `demo/world-simulator` substitutes for a third party we
cannot have (a bank), permanently, by design, and it is what makes outcomes
measurable. `web/src/lib/mockEngine.ts` substitutes for *our own backend*,
temporarily, as scaffolding, and it is retired as the default the moment
Unit H lands. Nothing in the demo runs on it: the demo runs with
`VITE_API_BASE_URL` set against a real Gateway, the frontend renders whatever
the backend computed and computes nothing itself, and every number on screen
came through the pipeline.

Mock mode stays supported after Unit H, because being able to develop the
dashboard without the whole stack running is genuinely useful and losing it
would be a regression. It just stops being the default.

**Live updates.** `docs/ARCHITECTURE.md` §6a specifies a WebSocket push
relayed from Reporting's `StreamBatchUpdates`, and that remains the design.
It is scheduled last in Phase 5 rather than first, because the dashboard
already refetches on a 2 second interval and every aggregate on the page is
driven by that refetch, not by the socket. The socket feeds only the
scrolling event log. So the fallback is invisible on stage while the push
path is the more expensive item remaining (a server-streaming RPC, Kafka
consumption in Reporting, and a gRPC-stream-to-WebSocket bridge in the
Gateway). Ordering, not a downgrade: if it lands, beat 2 is a genuine push.

## 13. Open questions

- Which LLM provider(s) back the diagnosis call, deliberately deferred, cost
  and rate limits need real evaluation first. The design already supports
  this being deferred: the Classifier calls a priority-ordered provider
  chain (candidate primary/secondary providers, falling back to rules) behind
  a swappable interface, and the load generator defaults to a synthetic
  (no real API calls) mode so cost isn't burned during throughput testing.
  See `docs/ARCHITECTURE.md` section 5. Decide actual provider(s) once
  cost/rate-limit numbers are in hand.
- ~~How much of the retry-cap logic should mirror real RBI/NPCI mandate rules
  vs. a simplified stand-in we state explicitly as an assumption?~~
  **Answered 2026-08-29, see §11a.** Two real rules are now cited and
  enforced in the guardrail layer (TRAI TCCCPR contact-hour window, RBI
  e-mandate 24 hour pre-debit lead time), with the remaining simplifications
  named explicitly rather than left implied. NPCI per-mandate presentation
  limits remain a stand-in.
- Exact latency/throughput NFR numbers in section 10 are starting targets,
  need to be replaced with real measured numbers once the first load test
  runs, and re-confirmed after the minikube/HPA deployment.
