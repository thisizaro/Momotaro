# SPEC.md (classifier): Phase 1 implementation spec

**Status**: ready to implement. Written 2026-08-23 against `main` at the
commit that merged the Decision Engine Phase 1 depth work (PR #11).

**Who this is for**: the agent picking up `docs/PLAN.md` Phase 1's
"Classifier: rules-only for now" item. This document is the task brief: what
to build, what not to build, what already exists, and what must be requested
from someone else rather than changed here. It does not replace the
repo-level docs, it tells you which parts of them apply and resolves the
places where they look like they disagree.

**Contract note**: `proto/classifier/v1/classifier.proto` remains the single
source of truth for the API. Nothing in this file overrides it. Where this
file describes behaviour, it describes behaviour *behind* that contract.

---

## 1. Read before you write code

In this order. The parenthetical is why it matters for this task
specifically, not a summary.

1. `AGENTS.md` (repo root) and `services/classifier/AGENTS.md` (ownership
   boundaries: what you may and may not touch).
2. `docs/ENGINEERING.md` in full. Non-negotiable for this task:
   - §1 TDD, and what it means here.
   - §3 context and deadlines on every outbound call.
   - §5 config validated at startup, fail fast.
   - §11 Definition of Done, the gate for ticking a `docs/PLAN.md` box.
   - §14 one job per file, one job per function. This service currently has
     a single `server.go` doing everything; that is the walking skeleton, not
     the target shape.
3. `docs/ARCHITECTURE.md` §5 (diagnosis: the hybrid rules/LLM design, the
   provider chain, circuit breakers, cost safety) and §5a (where the
   economics live, and the cause-aware retry policy table). §5b for
   `ComposeNudge` context only, since that RPC is out of scope here.
4. `docs/PRD.md` §2a (the two-trust-level split: guardrails deterministic,
   diagnosis bounded-LLM).
5. `proto/classifier/v1/classifier.proto` and `proto/common/v1/common.proto`.
   The enums in `common.proto` are your entire output vocabulary.
6. `docs/DECISIONS.md` and `docs/INCIDENTS.md`, skim for anything touching
   classification or the raw event payload.

---

## 2. Scope

### In scope (this task)

- A real deterministic rules engine replacing the hardcoded response: given a
  record, produce a `RootCauseBucket`, one `ActionType` from the closed menu,
  a human-readable rationale, and an honest confidence.
- The `Provider` chain *skeleton*: an internal interface, an ordered chain
  built from config, per-rung hop recording, and the deterministic rules
  engine as the final rung that always answers.
- `force_rules_only` honoured on the request.
- The service restructured per `ENGINEERING.md` §14.
- Unit tests covering the full mapping table and every error path.

### Explicitly out of scope

Do not build these. Each is its own `docs/PLAN.md` item in a later phase, and
building it early makes a large PR that is hard to review and blocks nobody.

| Thing | Where it belongs | Why not now |
|---|---|---|
| Any real LLM provider (Anthropic, OpenAI, ...) | Phase 3 | Provider choice is an open question in `docs/PRD.md` §13, still pending cost/rate-limit evaluation. `PLAN.md` Phase 1 says "the LLM provider-chain interface stubbed but not wired to a real provider yet". |
| Circuit breakers | Phase 3, its own PLAN item | Needs a real provider to breaker. Build the chain so a breaker *wraps a rung* later without restructuring, and stop there. |
| `ComposeNudge` | Phase 5 (Hinglish nudge composition) | No caller exists. Decision Engine never calls it in Phase 1. Leave it returning `codes.Unimplemented`. |
| Prometheus metrics (`llm_fallback_total`, `llm_call_duration_seconds`, `llm_circuit_state`) | Phase 4 | These come from the shared gRPC interceptor work across every service, not per-service hand-wiring. See §9 below for what to do instead. |
| Economics / EV scoring / `ClosedUneconomic` | Phase 2, and it lives in the **Decision Engine**, not here | `ARCHITECTURE.md` §5a is emphatic: the model recommends, deterministic economics in the Decision Engine decides. A classifier that priced actions would break the trust model the whole design rests on. |
| Retry caps, contact caps, cooldowns, escalation triggers | Decision Engine, always | `ARCHITECTURE.md` §5, last bullet: "Guardrails never move downstream of the LLM's judgment." Your output is a recommendation. |
| Cause-aware retry *timing* (salary window, backoff) | Phase 2, Decision Engine | You emit the bucket; the Decision Engine turns bucket into `due_at`. This is why you need no clock (§8). |

### The hard boundary: no ground truth

`migrations/00001_initial_schema.sql` states it on the `ground_truth` table
itself, and `ARCHITECTURE.md` §5a calls it "the integrity rule,
non-negotiable": **the Classifier must have no query path to
`GROUND_TRUTH`.** A classifier that can see which records are recoverable
makes every accuracy number in the demo meaningless. In Phase 1 this is free
to honour, because the service needs no database connection at all (§8).
Do not add one.

---

## 3. Where things stand today

`services/classifier/` currently contains the walking-skeleton stub:

- `internal/server/server.go`: `Classify` returns a hardcoded
  `TRANSIENT_BANK` / `RETRY` / confidence 1.0 response for every record,
  regardless of input. `ComposeNudge` returns `Unimplemented`.
- `cmd/main.go`: correct and essentially finished. Config via
  `config.LoadCommon`, fail-fast, recovery and require-deadline
  interceptors, graceful shutdown. You will need to extend it to build the
  chain and pass a logger into `server.New`, but do not restructure it.
- `internal/server/server_test.go`: three of its four tests survive in
  spirit. **`TestClassifyIsHardcodedRegardlessOfInput` must be deleted.** It
  asserts the classification does *not* vary by input, which is the exact
  property this task removes. Deleting it is correct, not a shortcut; note
  the deletion in the PR description so a reviewer sees it was deliberate.

### What the caller actually sends today

This matters more than the proto's field list, and it is the single easiest
thing to get wrong. `services/decision-engine/internal/engine/clients.go`
builds the request as:

```go
&classifierv1.ClassifyRequest{Record: record}
```

That is all. Concretely:

- `history` is **always empty**. Nothing populates it.
- `instrument_history` is **always empty**. Nothing populates it.
- `force_rules_only` is **always false**. Nothing sets it.
- `record` is populated from the `raw.events` payload
  (`services/decision-engine/internal/engine/rawevent.go`), so
  `id`, `batch_id`, `type`, `amount_paise`, `currency`, `failure_code`,
  `instrument_ref`, `created_at` all arrive. `instrument_ref` may be empty
  (it is nullable in the schema).

**Therefore**: your rules engine must produce a correct answer from
`failure_code` and `type` alone. Read `history`/`instrument_history` if
present (defensively, they are the documented inputs and Phase 2/3 will fill
them), but never require them. A rules engine that degrades when history is
absent is fine; one that misclassifies or errors is not.

**Do not fix this by populating history yourself.** Two paths exist and
neither is yours to take unilaterally:

- The Decision Engine reads `INTERVENTION_ATTEMPT` and fills the request.
  This matches the proto's design intent and is the recommended fix.
- The Classifier queries `INTERVENTION_ATTEMPT` itself.
  `ARCHITECTURE.md` §10a does list Classifier as a *reader* of that table,
  so this is permitted, but it adds a database dependency to a currently
  stateless service and duplicates a read the caller is better placed to do.

Either way it is a cross-service change. Per
`services/classifier/AGENTS.md`, **stop and propose it**, do not build it.
It is not blocking: Phase 1's mapping does not need history.

---

## 4. What to build: the rules engine

### 4.1 Input normalisation

`failure_code` is a raw string from an upstream rail. It arrives inconsistent
in this repo already: Go tests and `docs/API_GATEWAY.md` use
`BANK_TIMEOUT`, `INSUFFICIENT_FUNDS`, `HARD_DECLINE`; `web/src/lib/mockEngine.ts`
uses `bank_timeout`, `insufficient_funds`, `expired_instrument`. Real rails
are worse.

Normalise before lookup: trim whitespace, uppercase, and collapse `-` and
spaces to `_`. Do this in one named function so it is testable on its own and
obvious to the reader. Do not scatter `strings.ToUpper` through the lookup.

### 4.2 Failure code to bucket

The bucket vocabulary is closed (`common.proto` `RootCauseBucket`). This
table is the Phase 1 mapping. Codes are post-normalisation.

| Normalised `failure_code` | Bucket |
|---|---|
| `BANK_TIMEOUT`, `RAIL_CONGESTION`, `ISSUER_UNAVAILABLE`, `GATEWAY_TIMEOUT`, `TIMEOUT` | `TRANSIENT_BANK` |
| `INSUFFICIENT_FUNDS`, `LOW_BALANCE` | `INSUFFICIENT_FUNDS` |
| `HARD_DECLINE`, `EXPIRED_INSTRUMENT`, `EXPIRED_CARD`, `BLOCKED_INSTRUMENT`, `INVALID_INSTRUMENT`, `CARD_INVALID`, `DO_NOT_HONOUR` | `HARD_DECLINE` |
| `RISK_HOLD`, `FRAUD_REVIEW`, `SUSPECTED_FRAUD` | `RISK_HOLD` |
| `AUTH_REQUIRED`, `REAUTH_REQUIRED`, `MANDATE_REVOKED`, `MANDATE_PAUSED`, `USER_ACTION_NEEDED` | `USER_ACTION_NEEDED` |
| `CHECKOUT_ABANDONED`, `ABANDONED`, `ABANDONMENT` | `ABANDONMENT` |
| `INVOICE_OVERDUE`, `OVERDUE`, `PAST_DUE` | `OVERDUE` |

Add codes freely if you find real ones the demo generator will produce; the
table is meant to grow. Keep it as data (a `map[string]commonv1.RootCauseBucket`
literal), not a `switch` with fallthrough logic, so a reader can audit it at a
glance and a test can iterate it.

**A note on `INSUFFICIENT_FUNDS`, because the repo looks contradictory here.**
`web/src/lib/mockEngine.ts` maps `insufficient_funds` into a coarse
`transient` bucket. That is a display simplification in a mock with three
buckets total; it is not the system's model. `common.proto` gives
`INSUFFICIENT_FUNDS` its own bucket and `ARCHITECTURE.md` §5a gives it a
distinct retry policy (salary window, not short backoff), which only works if
the bucket is distinct. **Follow the proto and the architecture.** Do not
"correct" the classifier to match the web mock.

### 4.3 Unknown failure codes

An unrecognised code must not be guessed at. Fall back in this order:

1. If `record.type` is `RECORD_TYPE_CHECKOUT`, bucket `ABANDONMENT`.
2. If `record.type` is `RECORD_TYPE_INVOICE`, bucket `OVERDUE`.
   (For both: the record type carries genuine signal about the failure mode,
   independent of any rail code.)
3. Otherwise bucket `ROOT_CAUSE_BUCKET_UNSPECIFIED`, action `ESCALATE`,
   confidence `0.0`, and a rationale that names the unrecognised code
   verbatim so a human reading the audit trail can add it to the table.

An empty `failure_code` takes this same path. It is **not** an
`InvalidArgument` error: see §5.

This fallback ordering is a recommendation, not a locked decision. If you
have a better one, say so in the PR and record it in `docs/DECISIONS.md`
rather than changing it silently.

### 4.4 Bucket to recommended action and confidence

Phase 1 has no economics gate, so the recommendation is a direct function of
the bucket. Actions are `common.proto` `ActionType`.

| Bucket | Recommended action | Confidence | Reasoning to encode in the rationale |
|---|---|---|---|
| `TRANSIENT_BANK` | `RETRY` | 0.90 | rail was busy, funds were likely there, retry soon |
| `INSUFFICIENT_FUNDS` | `RETRY` | 0.80 | instrument is valid, balance is not there *yet*; timing is the Decision Engine's call |
| `HARD_DECLINE` | `NUDGE_METHOD_UPDATE` | 0.85 | a retry cannot succeed on a dead instrument, only a method update can (§5a) |
| `USER_ACTION_NEEDED` | `NUDGE_METHOD_UPDATE` | 0.70 | needs the customer to act; lower confidence because this bucket is the broadest |
| `RISK_HOLD` | `ESCALATE` | 1.00 | never auto-act around a risk decision (§5a) |
| `ABANDONMENT` | `NUDGE_REMINDER` | 0.80 | nothing failed technically, they just left |
| `OVERDUE` | `NUDGE_REMINDER` | 0.75 | no technical failure, a reminder is the whole intervention |
| `UNSPECIFIED` | `ESCALATE` | 0.00 | we do not know, so a human should |

Confidence must be in `[0.0, 1.0]`; assert that in a test over the whole
table. Keep the numbers as named constants next to the table with a one-line
comment each, in code rather than YAML: the checked-in YAML that
`ARCHITECTURE.md` §5a describes (`configs/intervention_costs.yaml`) is for
costs and recovery probabilities in Phase 2, a different thing from
classification confidence. Do not create that file here.

### 4.5 Rationale

Stored verbatim in `AUDIT_ENTRY.rationale` and shown to a judge. It is the
thing that makes the reasoning auditable, so it is not boilerplate.

- One or two sentences, plain English, naming the observed signal and the
  proposed action. `web/src/lib/mockEngine.ts`'s `RATIONALES` map is a good
  register to aim for.
- Must reference the actual input, at minimum the failure code, so two
  records with different codes do not produce identical text.
- Never empty. The e2e test asserts this, and so should yours.
- No invented amounts, dates, or figures. Interpolate from the record if you
  want to mention the amount; do not compose a number.

### 4.6 Source, and the `llm:claude` question

`ClassifyResponse.source` is the `common.proto` `Source` enum:
`SOURCE_LLM`, `SOURCE_RULES_FALLBACK`, `SOURCE_TEMPLATE_FALLBACK`.

`ARCHITECTURE.md` §5 describes `source` in prose as `llm:<provider>`, e.g.
`llm:claude`. That prose predates the enum and does **not** mean the enum
needs new values or a string field. Provider identity lives in
`ClassifyResponse.hops`, which exists precisely for this. Resolution:

- `source` is the coarse answer: which *kind* of thing answered.
- `hops` is the detail: which named rungs were tried, and what each returned.

In Phase 1, `source` is **always** `SOURCE_RULES_FALLBACK`, because the rules
engine is always what answers. `PLAN.md` Phase 1 states this outright, and
the e2e test asserts it. Do not propose a proto change for this.

### 4.7 The provider chain skeleton

Build the shape, not the providers.

- An unexported interface in this service's `internal/`, roughly:
  `Classify(ctx, *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error)`,
  plus a `Name() string` for hop recording. Keep it minimal and honest; do
  not add a `ComposeNudge` method nothing implements. Phase 5 can extend it.
- The rules engine implements that interface, named `rules`, and is always
  the last rung. Because it cannot fail, the chain always terminates in a
  valid answer, which is the property §5 is buying.
- The chain is an ordered slice of rungs built at startup from
  `LLM_PROVIDER_CHAIN` (already in `.env.example`, default `rules`). Walk it
  in order; a rung is tried only if the previous one errored or returned a
  response that fails validation.
- **Hop recording rule**: a hop is appended for every rung *actually
  attempted*, in order, per §5 ("Every rung actually attempted, in order, so
  the trail shows what was tried and not just what answered"). A rung that is
  configured-but-absent, or skipped because an earlier rung already answered,
  produces no hop. In Phase 1 that means exactly one hop:
  `{provider: "rules", result: "ok"}`.
- **Validation between rungs** is where the chain earns its keep later: a
  response naming a bucket or action outside the enum, or a confidence
  outside `[0,1]`, is a rung failure (`result: "schema_invalid"`), not an
  answer. Write that validation now, as its own function, and test it now.
  It is the only part of the chain that has real logic in Phase 1, and Phase
  3 depends on it being correct.
- An unknown name in `LLM_PROVIDER_CHAIN` must fail at **startup**, not at
  request time (`ENGINEERING.md` §5). A typo in a config value should stop the
  pod, not silently degrade every classification.
- Structure the loop so a rung can later be wrapped (by a circuit breaker, a
  timeout, a metric) without rewriting the loop. Do not build the wrapper.

### 4.8 `force_rules_only`

Skip every non-rules rung and answer from the rules engine directly. In Phase
1 this is behaviourally identical to the default path, since the chain is
rules-only. Implement and test it anyway: it is the load generator's
cost-safety switch (§5, "Cost-safety for load testing"), and Phase 3 gets it
for free if it exists now. Test that hops contain only `rules` when it is set.

---

## 5. Errors: what is `InvalidArgument` and what is not

Draw this line deliberately; getting it wrong turns unclassifiable records
into failed RPCs, which the Decision Engine retries three times and then
dead-letters. That would send a perfectly valid record to the DLQ.

`codes.InvalidArgument`, a caller bug:

- `record` is nil.
- `record.id` is empty (there is no correlation key, so nothing downstream
  can be logged or audited coherently).

**Not** an error, classify it and move on:

- `failure_code` empty or unrecognised. Route to the unknown-code path (§4.3)
  and return a normal response with a low confidence. This is a record we
  cannot diagnose well, not a malformed request.
- `history` / `instrument_history` empty. Normal today (§3).
- `amount_paise` zero or negative. Not the Classifier's business; the DB
  already enforces `> 0` and the Gateway validates it. Do not duplicate a
  guard you do not own.
- `instrument_ref` empty. Nullable in the schema.

Follow `ENGINEERING.md` §4 for error wrapping. Return gRPC status codes from
the handler, wrapped errors internally.

---

## 6. File layout

Per `ENGINEERING.md` §14. Names are suggestions; the shape is not. The point
is that the handler orchestrates and nothing else, and each file's name tells
you what is inside without opening it.

```
services/classifier/
  SPEC.md                        <- this file
  AGENTS.md                      <- unchanged
  cmd/main.go                    <- extend: build the chain, pass a logger
  internal/
    rules/
      buckets.go                 <- normalisation + the failure-code table
      actions.go                 <- bucket -> action + confidence
      rationale.go               <- rationale composition
      rules.go                   <- the Provider implementation, ties the above together
      *_test.go                  <- pure unit tests, table-driven
    provider/
      provider.go                <- the Provider interface + hop type helpers
      chain.go                   <- ordered walk, hop recording, rung failover
      validate.go                <- response validation between rungs
      chain_test.go              <- chain behaviour with fake rungs
    server/
      server.go                  <- gRPC handlers only: validate, delegate, respond
      server_test.go             <- handler-level tests
```

`server.go`'s `Classify` should read as a short list of steps. If it contains
a lookup table, a `strings.ToUpper`, or a rationale string, it is doing
someone else's job.

Only `provider` and `rules` need to be separate packages if you find that
clearer; a single `internal/classify` package with the same file split is also
fine and arguably simpler. Do not put everything in `internal/server`.

---

## 7. Tests

TDD per `ENGINEERING.md` §1: write these first. Everything here is a pure
unit test. **No integration build tag, no database, no Kafka, no broker.**
That is a real property of this service in Phase 1 and it is worth keeping:
these tests run in `make test` in milliseconds.

### Rules engine

- Table-driven over **every** entry in the failure-code map: correct bucket
  for each. Iterate the map itself so a new code cannot be added without a
  test covering it.
- Normalisation: `bank_timeout`, `BANK_TIMEOUT`, ` Bank-Timeout `, and
  `bank timeout` all reach `TRANSIENT_BANK`.
- Every bucket yields a non-`UNSPECIFIED` action, a confidence in `[0,1]`, and
  a non-empty rationale. Table-driven over the `RootCauseBucket` enum values
  so adding a bucket to the proto without handling it here fails a test.
- Unknown code with `RECORD_TYPE_CHECKOUT` yields `ABANDONMENT`; with
  `RECORD_TYPE_INVOICE` yields `OVERDUE`; with `RECORD_TYPE_PAYMENT` yields
  `UNSPECIFIED` + `ESCALATE` + confidence 0.
- Unknown code's rationale contains the offending code verbatim.
- Empty `failure_code` takes the unknown path and does not error.
- `RISK_HOLD` yields `ESCALATE`. Assert this one on its own with a comment
  citing §5a; it is a compliance-relevant behaviour, not just a table row.
- **Determinism**: the same request classified twice yields an identical
  response. Phase 2's re-run safety test depends on this holding.
- Two records with different failure codes yield different rationales.

### Provider chain

Use fake rungs; do not call anything real.

- Single rules rung: one hop, `{rules, ok}`, `source=SOURCE_RULES_FALLBACK`.
- A rung that errors is recorded as a hop with a failure result, and the next
  rung is tried.
- A rung returning an invalid response (bucket outside the enum, confidence
  `1.5`, action outside the menu) is treated as a rung *failure*, recorded as
  `schema_invalid`, and the next rung is tried. One test per invalid shape.
- Hops appear in attempt order.
- A rung that answers successfully stops the walk: later rungs produce no
  hops and are not invoked.
- The chain always terminates with a valid answer as long as `rules` is last.
- `force_rules_only=true` skips non-rules rungs entirely: assert the fake LLM
  rung was never invoked.
- Unknown provider name in config fails at construction time, not at request
  time.

### Handler

- Nil `record` yields `InvalidArgument`.
- Empty `record.id` yields `InvalidArgument`.
- A valid request returns a fully populated response: bucket set, action set,
  rationale non-empty, source set, at least one hop.
- Empty `history` and `instrument_history` are handled without error (this is
  the production path today, so test it explicitly rather than assuming).
- `ComposeNudge` still returns `Unimplemented`. Keep the existing test.

Run with `-race`. It is a Definition of Done item and this service will be
serving concurrent gRPC requests against a shared chain, so a rung holding
mutable state is a real risk worth catching.

---

## 8. Config, clock, database

**Config.** Everything you need already exists in `.env.example`:

```
LLM_PROVIDER_CHAIN=rules
LLM_TIMEOUT=2s
ANTHROPIC_API_KEY=
OPENAI_API_KEY=
```

Read `LLM_PROVIDER_CHAIN` with `config.Loader.CSV`, defaulting to `rules`,
and validate every name at startup. `LLM_TIMEOUT` is a Phase 3 concern; you
may load and validate it now (harmless, and it documents intent) or leave it
alone. Do not read the API keys: nothing uses them yet, and a service that
requires a key it never calls is a startup failure waiting to happen.

If you genuinely need a new environment variable, add it to `.env.example`
**in the same PR**, with a comment matching the surrounding style. Note that
`.env.example` is *not* union-merged (only `docs/PLAN.md`,
`docs/DECISIONS.md`, `docs/INCIDENTS.md` are), so a concurrent edit there can
conflict. Keep the diff to the lines you actually need.

**Clock.** You do not need one. The Phase 1 rules engine is a pure function
of its input, with no time-based behaviour: the salary-window and backoff
timing that `ARCHITECTURE.md` §5a describes turns into `due_at`, which the
Decision Engine computes (`engine.dueAtFor`). Do not inject a clock you never
call. `ENGINEERING.md` §2's rule is "never call `time.Now()` in business
logic", which is satisfied here by having no time in the business logic at
all. Phase 3's provider calls need a context deadline, not a clock.

**Database.** You do not need one. Do not add `POSTGRES_DSN` or a pool. See
§2's ground-truth boundary, and §3 on why the history gap is not yours to
close.

---

## 9. Logging and metrics

**Logging.** `internal/platform/logger` is the only logger. Pass a
`*slog.Logger` into `server.New` (it currently takes nothing) and correlate
with `logger.ForRecord(log, recordID, batchID)`. Useful keys already exist:
`KeyBucket`, `KeySource`, `KeyProvider`, `KeyError`. Log at least:

- one line per classification at `Info`: record id, bucket, action, source,
  confidence;
- a `Warn` when a record takes the unknown-code path, including the code, so
  a gap in the table is visible in logs rather than only in an audit trail;
- a `Warn` per failed rung, with provider name and result.

Note that request-scoped logging via interceptor is Phase 4
(`docs/PLAN.md` Phase 0's interceptor item says so explicitly), so
`logger.From(ctx)` will return a default logger, not a request-scoped one.
Pass the logger in through the constructor; do not build the interceptor.

**Metrics.** Do not add Prometheus wiring. `ENGINEERING.md` §11 item 3 asks
for "the relevant metric exported", and §13's metric list includes
`llm_fallback_total` and friends, but §13 is explicit that these arrive "via
a shared gRPC interceptor (not hand-added per handler)", and that is Phase 4.
The Audit Service set the precedent this repo now follows: its invariant
verifier logs violations and defers metric export to Phase 4 rather than
hand-rolling an exporter (see its entry in `docs/PLAN.md`). Do the same, and
say so in your PLAN.md note so the deferral is deliberate and visible rather
than looking like an oversight.

---

## 10. Work needed outside this service

Checked against `main` as of this writing. Short version: **nothing is
blocking, and no migration or proto change is required.**

### Migrations: none needed

`migrations/00001_initial_schema.sql` already carries everything
classification writes into. `AUDIT_ENTRY` has `rationale`, `source`, and
`message_text`; `RECORD_STATE` has `root_cause_bucket`;
`INTERVENTION_ATTEMPT` has `message_text` and `message_source`. You write none
of these yourself (the Decision Engine does, transactionally, per §10a), but
the columns exist so nothing you produce has nowhere to land.

> **Stale note worth flagging, not fixing here.** `docs/PLAN.md`'s Phase 5
> Hinglish item says it "needs a small additive migration for the two new
> columns". Migration `00001` already added `intervention_attempt.message_text`
> and `.message_source`. Whoever picks up Phase 5 should verify before writing
> a migration. Do not edit that PLAN.md line as part of this task; mention it
> in your PR so the orchestrator can decide.

### Proto: none needed

`classifier.proto` and `common.proto` cover every field this task produces.
Resist two tempting changes:

- Adding a provider-name string to `Source` or `ClassifyResponse`. Provider
  identity belongs in `hops` (§4.6).
- Adding fields for history the caller does not populate. The fields exist
  already; the gap is in the caller (§3).

If you conclude a proto change is genuinely required, **stop and propose it**
per `services/classifier/AGENTS.md`. Proto changes are their own PR, merged
before any code depending on the new shape, per `ARCHITECTURE.md` §9.

### `internal/platform/`: none needed

`config` has `CSV`/`Duration`/`Float`/`Str`, `logger` has the keys you need,
`interceptors` already provides recovery and require-deadline, `shutdown`
already works. Nothing to add. Same "stop and propose" rule if you disagree.

### Cross-service items to raise, not build

1. **`ClassifyRequest.history` and `instrument_history` are never
   populated** (§3). Recommend the Decision Engine fill them from
   `INTERVENTION_ATTEMPT`. Not blocking for Phase 1.
2. **Confidence threshold enforcement does not exist.**
   `classifier.proto` documents `confidence` as "Below the configured
   threshold the record is escalated rather than acted on", and
   `ARCHITECTURE.md` §5 puts that decision in the Decision Engine. Today
   `decideAfterClassify` in
   `services/decision-engine/internal/engine/state.go` ignores `confidence`
   entirely. So a confidence-0.0 unknown-code classification is currently
   only escalated because *you* recommend `ESCALATE`, not because a threshold
   caught it. Emit honest confidence values regardless; the guardrail will
   work when the Decision Engine adds it. Raise this as a Decision Engine
   item (it fits naturally with Phase 2's guardrail work). **Do not implement
   the threshold here**: a classifier that escalates on its own confidence is
   the model deciding, which is exactly the trust inversion `PRD.md` §2a
   forbids.
3. **`docs/PLAN.md`'s stale Phase 5 migration note** (above).

Raise all three in the PR description. If any turns out to be a real design
problem rather than a gap, `ENGINEERING.md` §13 applies: stop and say so.

---

## 11. What breaks if you get this wrong

Check these before opening the PR.

- **`test/e2e/walking_skeleton_test.go` posts `failure_code: "BANK_TIMEOUT"`**
  and asserts the record reaches `RECOVERED` through
  `NEW -> RETRY_SCHEDULED -> RETRYING -> RECOVERED`, with
  `entries[0].Source == SOURCE_RULES_FALLBACK` and a non-empty rationale.
  So `BANK_TIMEOUT` **must** map to `TRANSIENT_BANK` and recommend `RETRY`,
  and `source` must stay `SOURCE_RULES_FALLBACK`. If you change either, you
  have broken the end-to-end proof, not just a test.
- **`TestClassifyIsHardcodedRegardlessOfInput` will fail** and should be
  deleted (§3).
- **The Decision Engine's fakes are not affected.**
  `services/decision-engine/internal/engine/testhelpers_test.go` uses its own
  `fakeClassifier`, so its tests do not exercise your code. Green
  decision-engine tests are not evidence your change works; the e2e test is.
- **Returning an error for an unclassifiable record sends it to the DLQ.**
  `HandleMessage` retries `Classify` three times and then dead-letters. Re-read
  §5 before returning any new error code.

---

## 12. Definition of Done for this task

`ENGINEERING.md` §11, made concrete. All of it, before ticking the PLAN.md box.

1. Tests written first and passing, including `-race`.
2. Error paths tested, not just the happy path (§7's handler and chain cases).
3. Structured logs with `record_id` correlation. Metric export deliberately
   deferred to Phase 4, and *said so* in the PLAN.md note (§9).
4. `ctx` plumbed through the chain with a deadline on any outbound call.
   Phase 1 makes none, so this is satisfied by plumbing `ctx` and not
   swallowing it.
5. Graceful shutdown: already handled in `cmd/main.go`, do not regress it.
6. Config validated at startup, including an unknown provider name failing
   fast (§4.7).
7. `gofmt` clean, no new lint failures.
8. Money: nothing here touches money. `amount_paise` is read-only input.
9. Docs updated where behaviour diverged (§13 below).
10. Structure per §14: `server.go` orchestrates, the rules table lives in its
    own file, no god-`Server`.

---

## 13. Verify, then update the docs

```bash
make check                                   # fmt, vet, proto-lint, build, unit tests
go test -race ./services/classifier/...      # this service, explicitly, with -race
make test-integration                        # brings up the stack, runs integration + e2e
```

`make test-integration` is the one that matters most: it runs the real
classifier binary in the end-to-end pipeline. Run it more than once; this repo
has already been bitten twice by tests that pass on the first run (see
`docs/INCIDENTS.md`).

Then, in the same PR:

- **`docs/PLAN.md`**: tick the Classifier box. Follow the established style:
  a note under the item stating what was built, what was deliberately
  excluded and why (providers to Phase 3, circuit breakers to Phase 3,
  `ComposeNudge` to Phase 5, metrics to Phase 4), and the file split. Add a
  `(unplanned, ...)` line for anything you had to do that was not in the plan.
  This file is union-merged, so tick your own box directly.
- **`docs/DECISIONS.md`**: an entry for each real decision. At minimum: the
  failure-code-to-bucket table and where its values came from; the
  unknown-code fallback ordering; the `source`-vs-`hops` resolution of §4.6;
  why the service stays stateless.
- **`docs/INCIDENTS.md`**: anything that cost you real time. `ENGINEERING.md`
  §12 is not optional, and "what broke and what you did about it" is
  explicitly assessed on this project.
- **`services/classifier/AGENTS.md`**: only if behaviour diverged from what it
  says. It currently says nothing that this task contradicts.
- **This file**: if you discover something that would have saved you an hour,
  add it. The next agent picking up Phase 3 reads this.

Branch `svc/classifier/rules-engine` per the root `AGENTS.md` convention.
Small PR, no AI attribution in the commit message or PR description, and
`grep -i "claude\|co-authored\|generated with"` your commit message before
pushing.
