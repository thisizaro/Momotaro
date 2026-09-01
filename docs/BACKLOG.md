# Backlog (Momotaro)

Work that is deliberately **not now**: sized up, reasoned about, and
parked on purpose rather than forgotten. Distinct from `docs/PLAN.md`
(what we're building, phase by phase) and `docs/DECISIONS.md` (what we
chose and why). This file is "we will do this, later, and here is why not
now."

Append-only in spirit, same as `DECISIONS.md`/`INCIDENTS.md`. When an item
here gets picked up, move its status to **in progress**, and once merged,
delete it from this file (its story lives in `PLAN.md`/the relevant
`PHASEn_IMPLEMENTATION.md` from then on) rather than leaving a stale
"done" marker here.

---

## OpenTelemetry tracing (Phase 4 Unit F)

**Status**: deferred, not started.
**Decided**: 2026-08-29.
**Where to pick this up**: `docs/PHASE4_IMPLEMENTATION.md` Unit F already
has the shape of it (gRPC interceptor propagates trace context on every
call, `record_id` forced as the trace id, Kafka producers/consumers
inject/extract trace context from message headers since Kafka does not do
this on its own). `internal/platform/interceptors/doc.go` also already
names it as belonging there.

<!-- Reviewer-suggested and research-derived items live in the section
     "Considered and deliberately not now (2026-08-29)" at the bottom of
     this file. They are deliberately NOT in docs/PLAN.md so they stay out
     of every session's working context until someone chooses to pick one
     up. -->


**Why deferred rather than built now**: it is the single hardest piece of
Phase 4 — every hop through `raw.events`/`raw.events.dlq`/`audit.events`
needs manual header injection/extraction, on top of a new trace backend
(Jaeger or Tempo) added to the observability stack and a new interceptor
wired into every service. That is a bigger lift than any other Phase 4
unit. And its payoff overlaps a lot with something that already exists:
`GetRecordAudit` plus the `ProviderHop` list already gives a complete,
ordered account of everything that happened to one record across every
service (Phase 2/3 work), which covers most of what a demo would use
tracing to show. What tracing adds on top is flame-graph-style
cross-service timing, genuinely nice, not currently blocking anything.

**Decision**: build Phase 5 (demo realism, the actual thing standing
between this project and a working demo) first, then come back.

## Cite the compliance rule in the audit reason (Phase 5 Unit J follow-up)

**Status**: not started.
**Decided**: 2026-08-29, while implementing Unit J.

Unit J's two compliance guardrails (TRAI contact-hour window, RBI mandate
lead time, `docs/PRD.md` section 11a) are implemented as pure functions
(`contactHourWindow`, `mandateLeadTimeFloor` in
`services/decision-engine/internal/engine/schedule.go`) that correctly
change a record's `due_at`, tested against fixed instants and adversarially
verified. What is not yet wired in is the audit `reason` string actually
naming the rule when it fires (e.g. *deferred to 10:00 IST per TRAI
contact-hour window*): today the reason recorded alongside the transition
still describes the scoring decision, not a timing adjustment made after
it.

**Where to pick this up**: the same `scoreAndRoute` -> `scheduleNew`/
`recordRescore` plumbing Unit M reworks to persist the EV ranking and
guardrail refusal reasons. Thread a "why this due_at" string through the
same change rather than adding a second plumbing pass. If Unit M ships
without covering it, this stays open on its own.

## Persist a queryable trace for dead-lettered records (Phase 5 Unit F follow-up)

**Status**: not started.
**Decided**: 2026-08-29, while implementing Unit F.

`reporting.v1.BatchReport.processing_failure_count` cannot be computed
today: it returns `0` unconditionally
(`services/reporting/internal/server/server.go`). A record whose
processing keeps failing for a non-transient reason is dead-lettered
(`services/decision-engine/internal/engine/dlq.go`), published to Kafka's
`raw.events.dlq`, and left in whatever `RECORD_STATE` it was claimed
into, by design (`docs/PLAN.md`'s DLQ entry: "not written as a
`RecordState` value, none exists for it"). Reporting reads Postgres only,
never Kafka, as a source of numbers (its own package doc), so there is
currently no persisted signal it can query.

**Where to pick this up**: this is a Decision Engine change, not a
Reporting one. The natural fix is for `deadLetterPublisher.Publish` (or
its three call sites in `engine.go`/`scheduler.go`) to also write a row
Reporting can count, most likely reusing `audit_entry` with a sentinel
`reason`/`actor` rather than adding a new table, since every other
processing-failure-adjacent event already lives there. Whoever picks this
up should propose the exact shape first (`docs/ARCHITECTURE.md` section
10a's ownership table needs no new table if `audit_entry` is reused, but
does if a new one is added), per `AGENTS.md`'s "a migration is always its
own PR" rule.

## Production-grade hardening pass

**Status**: not started, not yet scoped in detail.
**Decided**: 2026-08-29 (the user's own framing: "after \[Phase 5's
important stuff\] are done we will wire things up to make proper
production grade").

This project's `docs/PRD.md`/`docs/ARCHITECTURE.md` are already written
for a real system, not a toy, but a hackathon build still takes shortcuts
a genuine production deployment would not accept. Once Phase 5 (demo
realism) and whatever else turns out to matter for the demo are done,
this is the pass to come back and close that gap deliberately rather than
by accident. Not scoped item-by-item yet; when this gets picked up, the
first step is an audit against `docs/ARCHITECTURE.md`/`docs/ENGINEERING.md`
for exactly this kind of gap: static demo `API_KEY` instead of real auth
(`.env.example`), no TLS between services, no real Alertmanager
notification channel (`docs/PHASE4_IMPLEMENTATION.md` Unit D), Postgres/
Redis/Kafka running as single instances with no HA, secrets in a `.env`
file rather than a secrets manager, and whatever else that audit turns up
that isn't already tracked here.

---

# Considered and deliberately not now (2026-08-29)

Everything below was sized, argued about, and parked in the same pass that
produced Phase 5 Units I to N. Three external reviews plus a research pass
over Razorpay's own published material and the actual judging rubric
(`docs/PRD.md` §0) generated far more ideas than two days can hold. The ones
worth doing became Units I to N. These are the rest.

**This section exists so nobody re-derives these from scratch, and so nobody
picks one up thinking it was forgotten.** Two groups: things genuinely worth
building later, and things deliberately rejected with the reason, which is
the more useful half.

## Worth doing, in the order I would pick them up

### 1. `ROOT_CAUSE_BUCKET_INDETERMINATE` as a first-class bucket

**Why it matters**: "the payment failed" and "we do not know what happened to
the payment" are different facts, and treating the second as the first is how
a recovery agent creates a duplicate charge. Razorpay's published error list
has real codes for exactly this: `payment_pending`, `verification_failed`,
`invalid_response_from_gateway`, `payment_timed_out`.

**Why not now**: Unit I already closes the dangerous half of this by refusing
to map indeterminate codes to a bucket whose policy is an automatic retry.
The full version wants a new proto enum value, entries in
`recovery_priors.yaml` and the action table, generator support, and honestly a
reconciliation step (ask the rail what actually happened before deciding),
which is a real feature rather than a bucket.

**Worth knowing**: the *principle* is already implemented and tested
elsewhere. The Executor refuses to re-run an unresolved claim and returns
`Aborted`, and `DECISIONS.md` 2026-08-23 records the reasoning verbatim:
re-running "could double-charge a real payment rail", and inventing an outcome
"would put a fiction in the audit trail". That is a strong answer at a panel
today, with no further code.

### 2. Expected revenue at risk

`Σ amount × P(permanent loss)`, using probabilities already persisted in
`record_state.p_recovery_at_decision`. ₹10 lakh at 95% recoverable and ₹10
lakh at 8% recoverable are very different business situations and the current
`at_risk_paise` cannot tell them apart. One SQL expression and one tile inside
Reporting. Genuinely cheap; parked only because it is additive polish on a
service that does not exist yet.

### 3. Correlated outage scenario in `scripts/batchgen`

A `-scenario bank-outage` flag that concentrates a slice of the batch on one
issuer and one failure code inside a short time window. The existing
per-bucket reporting would then show a systemic spike with no new reporting
code, which is most of the value of the "merchant-level intelligence" idea two
reviewers raised, for a fraction of the work. Makes the demo narrative better
("this is not 80 unrelated customer problems, this is one bank having a bad
twenty minutes").

### 4. Per-rung LLM timeouts

The one change that would let Gemini back into the default chain honestly.
`DECISIONS.md` 2026-08-28 has the measurements: Groq p50 ~570ms versus Gemini
p50 ~3.01s, and no single `LLM_TIMEOUT` serves both inside the Decision
Engine's 5s `CALL_TIMEOUT`. A small change to `provider.Config`. The rung is
already built, unit-tested and confirmed against the live API, so this is
purely about making a three-rung chain fit a real budget.

### 5. `StreamBatchUpdates` and the WebSocket relay -- resolved, see below

**Resolved 2026-08-30**: reached inside Phase 5 after all (Unit F built
`StreamBatchUpdates`, Unit G built the Gateway's WebSocket relay). The
frontend/Gateway subprotocol-vs-header mismatch this item warned about was
checked directly and found already correct on the frontend side; the
Gateway now checks the subprotocol too (`services/api-gateway/internal/
httpapi/live.go`). The unbounded `updates` array / no-reconnect gap this
item also named is still real and not yet addressed -- that part alone
would need its own future item if picked up, but is now a small, isolated
`App.tsx` concern rather than blocking anything backend-side.

### 6. Phase 6 load testing

Not new, already in `PLAN.md`, noted here only because `PRD.md` §10's latency
and throughput figures are explicitly "starting targets" that no load test has
ever validated. If a panel asks "did you measure that", the honest answer today
is no. Replacing those numbers with measured ones is the single cheapest way
to make §10 defensible.

### 7. Classify Postgres "no rows" errors as dead-letter-worthy in `decision-engine.HandleMessage`

Found 2026-08-30 (`docs/INCIDENTS.md`, same date): a single Kafka message
whose `record_id` has no matching Postgres row crashes the entire
`decision-engine` process and wedges its Kafka partition permanently
(restart just replays and re-crashes on the same message), rather than
being dead-lettered like every other per-record failure the handler already
knows how to route around. `kafkax.ConsumeKeyed`'s own doc comment states
the intended contract explicitly -- a handler error should mean an
infrastructure failure, not a per-record one -- but four call sites in
`engine.HandleMessage` (`recordStateExists`, `loadAttemptRows`,
`loadInstrumentHistory`, `loadAttemptHistory`) don't follow it: they
propagate any Postgres error unclassified, including `pgx`'s "no rows"
case, which is a per-record problem, not an infra one.

**The fix, scoped**: at each of those four call sites, check
`errors.Is(err, pgxpkg.ErrNoRows)` (the same pattern `services/ingestion/
internal/server/store.go` already uses) and route to the dead-letter queue
with a clear reason (e.g. "record not found, likely an orphaned Kafka
message") instead of returning the raw error. Every other Postgres error
(genuine connectivity failures) stays fatal, unchanged -- that part of the
current behavior is correct and should not be touched.

Not fixed reactively when found: this needs tests-first like everything
else in this codebase (`docs/ENGINEERING.md` section 1), specifically a
test that publishes a `raw.events` message for a `record_id` Postgres does
not have and asserts it gets dead-lettered rather than crashing the
consumer, for all four call sites, before touching the implementation.

### 8. Isolate the scheduler's claim-due-work query per test, or make the affected tests tolerant of it

Found 2026-08-30 (`docs/INCIDENTS.md`, same date): `decision-engine`'s
scheduler tests -- most visibly `TestSchedulerFiresOnceWhenFakeClockPassesDueAt`
and `TestSchedulerDeadLettersAfterExecuteRetriesExhausted` -- have failed
twice in CI within one hour, on two unrelated PRs (one touched only
`Makefile`/`scripts/`, the other only `docs/INCIDENTS.md`), while passing
every time locally. Root cause, already known and accepted as a tradeoff
(`docs/AGENTS.md` testing conventions): the scheduler's claim-due-work
query runs system-wide, no per-test scoping, so a background scheduler
loop from one test in the same binary can claim a record a *different*
test just seeded before that test gets to check it. Rare locally, common
enough on CI's more contended runner to be a recurring nuisance rather
than a one-off.

**Two possible directions, not yet chosen between**: (a) give each test's
scheduler its own isolated view somehow (a `WHERE` clause scoped to a
test-run marker, mirroring how Kafka topics are already isolated per
test), or (b) accept the shared-query design as correct (it may be
load-bearing for realistic concurrency testing elsewhere) and instead make
the *specific* assertions in the affected tests tolerant of a record
having already been claimed by something else, the same way other
system-wide-query tests in this codebase already assert relative
order/presence rather than exact state. Whoever picks this up should read
both existing incident entries first and decide which fits before writing
any code.

## Deliberately rejected, with the reason

Kept because "we considered it and here is why not" is a better answer at a
panel than not having thought about it. Sources were three external design
reviews (2026-08-29).

- **Merchant AI copilot** (natural-language analytics over Reporting),
  **LLM planner** replacing or competing with the EV scorer, and a
  **cross-batch learning loop**. All three rejected on the same ground, and it
  is a scoring ground rather than a taste one: "AI Judgment: whether AI tools,
  LLMs, or agents were applied appropriately **instead of forcing unnecessary
  tech stacks**" is an explicit criterion (`PRD.md` §0). These add model
  surface without adding judgment. The planner is actively worse than what
  exists, since the deterministic scorer choosing among a guardrail-permitted
  menu is the stronger answer to "does the LLM decide how to spend money". The
  learning loop is the worst of the three: with one demo batch there is nothing
  to learn from, and a plotted improvement curve that is not real would be the
  exact dishonesty this project has avoided everywhere else.
- **Dynamic payment routing / multi-acquirer failover.** Genuinely how real
  gateways work, and Razorpay's own Optimizer does it. Rejected on size, not
  merit: it needs an acquirer/route concept in the schema, the World
  Simulator, the priors table and the action space. That is a phase, not a
  task.
- **Bank health intelligence service.** Same reason, plus it wants real-time
  external signal this system has no source for. Item 3 above gets a slice of
  the same demo value for far less.
- **Customer recovery profiles** (per-customer preferred channel, response
  history, best retry window). Good idea, and `instrument_history` already
  gives the classifier a thin version of it. The full version needs customer
  identity, which this system deliberately does not model: `record.
  instrument_ref` is by its own schema comment never the instrument itself,
  and there is no customer entity at all.
- **Predictive prevention** (predict the failure before it happens). The
  strategically most interesting suggestion received, and out of scope by
  definition: this track is recovery, the pipeline starts at a failure event,
  and prevention needs pre-transaction signal the system never sees.
- **Fraud/risk scoring inside recovery.** `RISK_HOLD` already escalates and is
  never auto-retried, which is the safety-critical half. A real risk score
  needs velocity, device and chargeback signals that do not exist here.
- **Five separate frontends on ports 3000 to 3004** (one review proposed a
  merchant dashboard, an AI inspector, an audit console, an infrastructure
  console and a simulator controller as separate apps). Rejected outright:
  five builds and five things to break on stage, against one app that already
  exists and works. Anything worth showing becomes a tab or a panel in
  `web/`, which is what Unit L does.
- **Probability calibration curves.** Legitimate and genuinely interesting,
  since `recovery_priors.yaml` is honest that its values are assumptions.
  Rejected for now because the file itself already states the blocker: honest
  calibration needs a randomised `NONE` holdout arm, "nothing in the design
  does that today", and a calibration plot drawn without one would be
  measuring the priors against a world generated from a different set of
  priors. Unit K's baseline comparison gets the defensible part of this value
  without the methodological hole.

## Runtime control of `DEMO_TIME_SCALE`

**Status**: considered 2026-08-31, deliberately not doing. **Not a "later"
item so much as a "this does not work" item**, recorded so nobody proposes it
again without reading the arithmetic.

**The idea**: expose demo speed as a control in the UI, so a presenter could
slow the system down mid-demo and let judges watch a retry actually wait.

**Why it is expensive.** `DEMO_TIME_SCALE` is read once at startup and baked
into config structs passed by value. Runtime mutability needs a
concurrency-safe config holder, every read site routed through it, and an
endpoint, across at least the decision engine and the world simulator. Worse,
the scale is applied at the moment `due_at` is *computed and written*, so
records already scheduled hold absolute timestamps and would not move. Making
them move means bulk-rewriting pending `due_at` values by a ratio, which
destroys the meaning of "this was scheduled for the next salary window". The
honest version needs a real logical clock, which is a large change (and is
also what the recovery-window guardrail would need to be demonstrable in a
compressed demo, see `docs/INCIDENTS.md` 2026-08-31).

**Why it would not work even if built, which is the part that settles it.**
The waits this system schedules span nearly five orders of magnitude:

```
at DEMO_TIME_SCALE=300000:
  RETRY_DELAY 30s              ->  0.000s   instant
  CONTACT_COOLDOWN 24h         ->  0.288s   a blink
  salary window (worst ~31d)   ->  8.928s   watchable

spread between the two extremes: 89,280x
```

**No single knob can make both ends watchable.** Slow it until a 30 second
retry backoff is visible and the salary window becomes hours. Speed it up
until the salary window is quick and the retry backoff was already instant. A
slider would only move which one you cannot see.

**What was done instead.** Phase 5.5 Units AA and AB surface `due_at` as a live
countdown and plot pending work on a time axis. The salary window is already
watchable at ~9 real seconds; it simply was not displayed. That delivers the
whole demo intent for about two hours of additive, zero-risk work, and the
retry backoff being instant is fine: nobody needs to watch an exponential
backoff, they need to see that an empty account is deliberately left alone
until payday.
