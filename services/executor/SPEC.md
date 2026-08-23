# SPEC.md (executor): Phase 1 implementation spec

**Status**: ready to implement. Written 2026-08-23 against `main` at the
commit that merged the Decision Engine Phase 1 depth work (PR #11).

**Who this is for**: the agent picking up `docs/PLAN.md` Phase 1's Executor
item. This document is the task brief: what to build, what already works and
must not be rewritten, and what has to be requested from someone else rather
than changed here.

**Read §3 before you write anything.** This service is further along than the
Classifier was; roughly half the Phase 1 item is already built and tested.
The single biggest way to waste a day here is to rewrite the idempotency
guard that already works.

**Contract note**: `proto/executor/v1/executor.proto` remains the single
source of truth for the API. Nothing here overrides it.

---

## 1. Read before you write code

1. `AGENTS.md` (repo root) and `services/executor/AGENTS.md`.
2. `docs/ENGINEERING.md` in full. Load-bearing for this task:
   - §1 TDD, and "do not mock what you own".
   - §3 context and deadlines on every outbound call. This service will gain
     two real outbound gRPC calls in Phase 5, and the ports you build now are
     where those deadlines land.
   - §8 **anything that touches money**. This is the service that touches
     money. Idempotency must be proven by a test that delivers the same
     action twice and asserts one effect.
   - §11 Definition of Done, §14 one job per file.
3. `docs/ARCHITECTURE.md` §11 (two-layer idempotency, and why Redis alone is
   not acceptable), §3b (ports and adapters: `RecoveryActionPort` /
   `NotificationPort`), §5a (the cost model, for what the stub's costs should
   look like), §6 (World Simulator, for what the ports become in Phase 5),
   §10a (table ownership).
4. `proto/executor/v1/executor.proto`, `proto/worldsim/v1/worldsim.proto`,
   `proto/notifier/v1/notifier.proto`. The last two are the port contracts
   you are standing in for.
5. `docs/DECISIONS.md`, the entry on "Executor's idempotency guard is
   insert-then-update, not insert-with-final-outcome". That decision is the
   foundation of the existing code and §4.3 below changes one detail of it.

---

## 2. Scope

`docs/PLAN.md` Phase 1 states the item as:

> Executor: **insert-before-execute** idempotency against the
> `UNIQUE (record_id, attempt_number)` constraint (the durable guarantee),
> Redis `SETNX` as the fast path only, calls a minimal **stub** outcome
> source (fixed/scripted responses) via the
> `RecoveryActionPort`/`NotificationPort` interfaces, not the full
> `demo/world-simulator` yet, just enough to prove the state machine reaches
> a terminal state.

### In scope

- The two ports as real Go interfaces, with action-type routing between them
  (§4.1). Today there is one undifferentiated `OutcomeFunc`.
- A **scripted** stub behind each port: per-action outcomes, real
  `cost_paise`, a `failure_code` on failure, and `PENDING` plus `resolves_at`
  for nudges (§4.2). Today the stub always returns SUCCESS at zero cost,
  which cannot prove the state machine reaches anything but `Recovered`.
- Fixing the `PENDING` collision the above introduces (§4.3). **Read this
  even if you skip everything else**; it is a latent double-execution and
  hang risk, not a style point.
- The Redis `SETNX` fast path (§4.4), subject to the dependency question in
  §10 which you must resolve with the orchestrator before writing it.
- Restructuring per `ENGINEERING.md` §14: `server.go` is currently one file
  holding validation, SQL, and orchestration.

### Explicitly out of scope

| Thing | Where it belongs | Why not now |
|---|---|---|
| `demo/world-simulator` and `demo/notification-simulator` implementations | Phase 5 | Both are empty scaffolds today (`cmd/main.go` with no gRPC server). PLAN.md says "not the full `demo/world-simulator` yet". You build the *port*; Phase 5 builds the adapter behind it. |
| Real gRPC dialling of those two services | Phase 5 | Nothing is listening. Your stub is an in-process implementation of the port interface. |
| The delayed-outcome queue and `ReportDelayedOutcome` | Phase 5 | `DecisionEngine.ReportDelayedOutcome` has no server-side implementation and no caller. Return `PENDING` with `resolves_at`; do not build the callback. |
| Ground truth (probabilistic outcomes read from `GROUND_TRUTH`) | Phase 5, and it lives in **World Simulator**, never here | See the hard boundary below. |
| Economics, EV scoring, `configs/intervention_costs.yaml` | Phase 2, Decision Engine | Your costs are plausible constants in code for now (§4.2). Do not create that YAML file; Phase 2 owns it. |
| Retry budgets, contact caps, cooldowns | Decision Engine, always | You execute what you are told to execute, exactly once. You never decide whether an action is allowed. |
| Prometheus metrics | Phase 4 | Shared interceptor work. See §9. |
| Writing `AUDIT_ENTRY` rows | See §10, item 2 | Genuinely ambiguous in the docs, and the recommendation is **do not**. Read that item before deciding. |

### The hard boundary: no ground truth

`migrations/00001_initial_schema.sql` says it on the table, `worldsim.proto`
says it in its package comment, and `ARCHITECTURE.md` §5a calls it
non-negotiable: **only the World Simulator and Reporting's accuracy scorer
may read `GROUND_TRUTH`.** The Executor is on the decision path. It must have
no query path there.

This is precisely why the stub is *scripted* rather than *probabilistic*: a
realistic outcome model requires the answer key, so it belongs behind the
port in `demo/world-simulator` (Phase 5), not in `services/executor`. A
scripted stub in this service is not a shortcut, it is the correct side of the
boundary.

---

## 3. Where things stand today: what already works

**Do not rewrite any of this.** It is correct, tested, and it is the durable
guarantee the whole design rests on.

`services/executor/internal/server/server.go` already implements:

- **Insert-before-execute.** `Execute` inserts the `intervention_attempt`
  row, claiming the `UNIQUE (record_id, attempt_number)` slot, *before*
  calling the outcome source. Only a successful insert proceeds to the side
  effect.
- **Unique-violation recognition.** SQLSTATE `23505` is detected via
  `errors.As` on `*pgconn.PgError` (not string matching), and routes to
  `awaitExistingAttempt`.
- **Replay of the recorded outcome.** A duplicate returns the original
  outcome with `already_executed=true`, never re-executing.
- **The concurrent-race window.** `awaitExistingAttempt` polls while the
  existing row is still un-resolved, so a duplicate arriving between the
  original's insert and its outcome update does not read a torn value. The
  poll uses the injected clock.
- **Validation.** Missing `record_id`, non-positive `attempt_number`, and
  `ACTION_TYPE_UNSPECIFIED` all return `InvalidArgument` before any side
  effect.
- **Injected outcome source.** `OutcomeFunc` is injected precisely so tests
  can count executions.

`internal/server/server_test.go` already covers the redelivery case, the
8-goroutine concurrent-duplicate case, independent attempt numbers, and
validation. These tests are the money-safety proof required by
`ENGINEERING.md` §11 item 8. **Keep every one of them passing.** If your
refactor requires changing what they assert, you have probably changed
behaviour, not structure; stop and re-read.

`cmd/main.go` is correct: fail-fast config, Postgres pool, recovery and
require-deadline interceptors, graceful shutdown. Extend it to construct the
ports; do not restructure it.

### What is missing, concretely

1. One undifferentiated `OutcomeFunc`, not the two ports §3b names.
2. `StubOutcome` always returns `SUCCESS` at cost 0. Every record therefore
   reaches `Recovered`, so the failure and nudge branches of the Decision
   Engine's state machine have never run end to end.
3. `cost_paise` is always 0, so "net recovered" cannot be a measurement.
4. `failure_code` is always `''`, so the response field the proto documents
   as "becomes the next classification's input" carries nothing.
5. `resolves_at` is never set.
6. No Redis fast path.
7. `message_text` is written from the request but the caller never sends one
   (§5).

### What the caller actually sends today

From `services/decision-engine/internal/engine/clients.go`:

```go
&executorv1.ExecuteRequest{
    RecordId:      recordID,
    BatchId:       batchID,
    ActionType:    action,
    AttemptNumber: attemptNumber,
    AmountPaise:   amountPaise,
}
```

Note what is absent: **`Message` is never set.** The proto says it is
"required for nudge actions, already composed and interpolated by the
Decision Engine", but `ComposeNudge` is Phase 5 and nothing composes anything
yet. So your `NotificationPort` stub must handle an empty message without
erroring. Log a `Warn` when a nudge arrives with no text (it is a real gap,
just not yours), and proceed.

Also note `AttemptNumber` is `c.AttemptCount + 1` from the claimed record, so
it starts at 1 and increases. `AmountPaise` arrives but Phase 1 has little
use for it (§4.2); do not force a use.

---

## 4. What to build

### 4.1 The two ports

`ARCHITECTURE.md` §3b: "The Executor never calls 'the World Simulator' or
'the Notification Simulator' by name in its own code, it depends on two small
interfaces."

Define both in this service's `internal/`, shaped to mirror the proto
contracts they will later be backed by:

- **`RecoveryActionPort`**, mirroring `worldsim.SimulateOutcome`: given a
  record id, action type, and attempt number, return outcome, whether the
  answer is immediate, `resolves_at`, and a failure code.
- **`NotificationPort`**, mirroring `notifier.SimulateSend`: given a record
  id, channel, and message, return whether it was sent and the cost in paise.

Keep the Go signatures close to the proto messages, so the Phase 5 adapter is
a thin translation rather than a redesign. Use `commonv1` types (`ActionType`,
`Outcome`) directly; do not invent a parallel enum.

**Routing by action type**, and this is the part with actual logic in it:

| `ActionType` | Port | Shape of the outcome |
|---|---|---|
| `RETRY` | `RecoveryActionPort` | immediate: `SUCCESS` or `FAILURE` |
| `NUDGE_METHOD_UPDATE`, `NUDGE_REMINDER` | `NotificationPort`, then translate | the *send* succeeds or fails; the customer's reaction does not arrive inside the RPC, so the outcome is `PENDING` with `resolves_at` |
| `ESCALATE`, `NONE` | neither | see below |

`ESCALATE` and `NONE` are the interesting cases and the docs do not settle
them. Reasoning: the Decision Engine's `decideAfterClassify` sends an
escalated record straight to `ESCALATED` without scheduling anything, so
today the scheduler never calls Execute with `ESCALATE`. But `Execute` is a
public RPC and must answer coherently if it happens.

**Recommended**: treat both as a no-side-effect action. Record the attempt
row (so the trail is honest that the call happened), call no port, return
`OUTCOME_SUCCESS` at cost 0 for `NONE`, and `OUTCOME_FAILURE` with
`failure_code="ESCALATED_NO_AUTOMATED_ACTION"` for `ESCALATE`, since escalation
is not something the Executor can accomplish. Whatever you choose, choose it
deliberately, test it, and record it in `docs/DECISIONS.md`. Do not leave it
falling through a `default` branch by accident.

Route in one small named function that maps action to port, so the routing is
readable in one place and testable without touching the database.

### 4.2 The scripted stub

Two stub implementations, one per port, living behind the interfaces. They
replace `StubOutcome`. Keep the injectability: the existing tests depend on
being able to count executions, and `ENGINEERING.md` §8 requires it.

**Scripted, not random.** Determinism matters for two reasons: Phase 2 has a
re-run safety test that replays a batch and asserts an identical outcome, and
a flaky stub makes the e2e test flaky. Derive the outcome deterministically
from the request, for example from `attempt_number` and `action_type`, and
document the script in a comment. If you want variety across records, derive
it from a stable hash of `record_id`, never from `math/rand` without a seed
you control, and never from the clock.

**A concrete script that exercises the whole state machine** (adjust if you
have a better one, but make sure every branch below is reachable):

| Action | Attempt | Outcome | Cost (paise) | Failure code |
|---|---|---|---|---|
| `RETRY` | 1 | `SUCCESS` | 200 | |
| `RETRY` | 2+ | `FAILURE` | 200 | `BANK_TIMEOUT` |
| `NUDGE_REMINDER` | any | `PENDING` + `resolves_at` | 25 (SMS) | |
| `NUDGE_METHOD_UPDATE` | any | `PENDING` + `resolves_at` | 60 (WhatsApp) | |

Attempt 1 of a retry succeeding is what keeps the e2e test green (§11). Costs
should be plausible and cited: `ARCHITECTURE.md` §5a describes per-action
`direct_cost` in paise (SMS, WhatsApp, retry attempt fee), and
`notifier.proto` notes WhatsApp costs more than SMS. Keep them as named
constants with a one-line comment each, in code. **Do not create
`configs/intervention_costs.yaml`**; that file and its `indirect_cost` term
belong to Phase 2's economics work, which is a different thing from a stub's
direct cost.

`cost_paise` must be non-negative (the DB enforces `>= 0`) and must be
persisted on the attempt row and returned in the response. It is the input to
"net recovered", so it is not decorative.

**`resolves_at`** is computed from the injected clock plus a configured delay,
scaled by `DEMO_TIME_SCALE` via `cfg.Scale()` like every other wall-clock
value in this repo. Note honestly that nothing consumes it yet: the Decision
Engine's `decideAfterExecute` looks only at `outcome`, and the scheduler parks
a `PENDING` nudge with `due_at` NULL because `ReportDelayedOutcome`'s caller
is Phase 5. Set it anyway, because it is the correct answer to the question
the proto asks, and Phase 5 needs it. Mention in your PLAN.md note that it is
produced but not yet consumed, so the gap is visible rather than looking like
an oversight.

### 4.3 The `PENDING` collision: read this one carefully

This is the trap in this task, and it is created by §4.2, not present today.

`intervention_attempt.outcome` is `TEXT NOT NULL`, so the row cannot be
inserted without an outcome value. The existing code therefore inserts
`OUTCOME_PENDING` as a *claim marker* and updates it to the real outcome
afterwards (see the `docs/DECISIONS.md` entry). `awaitExistingAttempt` polls
while the stored value is `OUTCOME_PENDING`, meaning "the original request is
still working".

The moment nudges start returning a genuine `OUTCOME_PENDING`, that marker
becomes ambiguous, with two real consequences:

1. **A redelivered nudge request polls until its deadline expires.** The row
   legitimately says `PENDING` forever (until Phase 5's delayed callback), so
   `awaitExistingAttempt` never sees it resolve. The caller sees a deadline
   error instead of `already_executed=true`. Under the Decision Engine's
   bounded retry that means three timeouts and then a dead letter, for a
   record that executed perfectly.
2. **A crashed original leaves a permanently un-resolvable row.** Already
   true today in principle; nudges make it routine.

**Fix, and it needs no migration**: use `OUTCOME_UNSPECIFIED` as the claim
marker instead of `OUTCOME_PENDING`. The column is unconstrained `TEXT`, the
enum's zero value means exactly "unset" by the repo's own proto convention
(`common.proto`: "every enum has an `_UNSPECIFIED = 0` so 'unset' is
distinguishable from a deliberately-set first value"), and nothing else reads
this column yet (the Audit service reads `audit_entry` only; Reporting does
not exist). So:

- Insert with `outcome = 'OUTCOME_UNSPECIFIED'` to claim the slot.
- Update to the real outcome, which may legitimately be `OUTCOME_PENDING`.
- `awaitExistingAttempt` polls while the value is `OUTCOME_UNSPECIFIED`, and
  returns as soon as it is anything else, `PENDING` included.

Test this explicitly: a redelivered nudge whose original returned `PENDING`
must come back promptly with `already_executed=true` and `outcome=PENDING`,
not hang. That test is the whole point of this section.

**Also bound the poll.** An unbounded loop waiting on a row a crashed pod
will never update is a hang dressed up as patience. Cap it (a few hundred
milliseconds is generous given a synchronous stub) and return a clear error
when it expires, rather than relying on the caller's deadline. Update the
`docs/DECISIONS.md` entry to record the marker change and the bound.

### 4.4 The Redis `SETNX` fast path

**Resolve §10 item 1 with the orchestrator before writing this.** It needs a
new module dependency, which is not yours to add unilaterally.

If approved, the rules from `ARCHITECTURE.md` §11 are absolute:

- Redis is an **optimisation only**. `SETNX idem:{record_id}:{attempt_number}`
  short circuits an obvious duplicate without a database round trip.
- The Postgres unique constraint remains the actual guarantee. "If the two
  ever disagree, Postgres wins. An agent implementing the Executor must not
  treat the Redis check as sufficient on its own."
- Therefore: **a Redis outage must not fail a request.** If Redis is
  unreachable, times out, or returns garbage, log it and fall through to the
  durable path. A fast path that can take the service down is worse than no
  fast path. Test this: with a deliberately broken Redis client, every
  existing idempotency test must still pass unchanged.
- TTL slightly longer than max processing time (§10a). Put the actual number
  in `.env.example` with a comment, do not hardcode it.

Honest assessment, worth raising rather than swallowing: in Phase 1 this
buys nothing measurable. The durable guard is already correct, the stub is
synchronous and fast, and the load testing that would show the saved round
trip is Phase 6. It adds a dependency, a new failure mode, and a
"disagreement" case to reason about. A defensible alternative is to build it
in Phase 6 alongside the load testing that justifies it, and note the
deferral in PLAN.md. **Raise this with the orchestrator; do not decide it
alone in either direction.** If you do defer it, the PLAN.md item is not fully
done and the checkbox should say so explicitly rather than being ticked.

---

## 5. Errors: what fails the RPC and what does not

The existing validation is right; extend it in the same spirit.

`codes.InvalidArgument`, a caller bug, before any side effect:

- `record_id` empty (already).
- `attempt_number <= 0` (already).
- `action_type == ACTION_TYPE_UNSPECIFIED` (already).

**Not** an error:

- Empty `message` on a nudge. Log a `Warn` and proceed (§3). The Decision
  Engine does not compose messages yet.
- `amount_paise` zero. Not your guard to duplicate.
- A port reporting a failed action. `OUTCOME_FAILURE` with a `failure_code` is
  a successful RPC reporting an unsuccessful action. These are different
  things and conflating them is the most consequential error in this section:
  the Decision Engine's scheduler treats an RPC error as a retryable
  infrastructure fault (three attempts, then dead letter) but treats a
  `FAILURE` outcome as a decision input that escalates the record. Return an
  error only when you genuinely could not execute.

A port returning a transport error (Phase 5, when they are real gRPC calls)
*is* an RPC error. Keep the two paths distinguishable in your port signatures:
`(result, error)`, where a business failure lives in `result` and only
infrastructure failure lives in `error`.

**Foreign key note**: `intervention_attempt.record_id` references
`record(id)`, so an unknown `record_id` fails the insert with a foreign key
violation, not a unique violation. Today that surfaces as a wrapped generic
error. Consider mapping it to `codes.NotFound`, which is more useful to a
caller than an opaque internal error, and test it.

---

## 6. File layout

Per `ENGINEERING.md` §14. Shape matters, names are negotiable.

```
services/executor/
  SPEC.md                        <- this file
  AGENTS.md                      <- unchanged
  cmd/main.go                    <- extend: construct the ports, pass config
  internal/
    ports/
      ports.go                   <- RecoveryActionPort, NotificationPort interfaces
      route.go                   <- action type -> which port, one job
      stub.go                    <- the scripted stub implementations
      cost.go                    <- per-action cost constants, cited
      *_test.go                  <- pure unit tests, no database
    attempt/
      store.go                   <- the claim insert, the outcome update, the replay read
      idempotency.go             <- unique-violation detection, the bounded await
      redis.go                   <- the SETNX fast path, IF approved (§4.4, §10)
    server/
      server.go                  <- gRPC handler: validate, claim, execute, record, respond
      validate.go                <- request validation
      server_test.go             <- the existing integration tests, kept green
```

The routing and stub packages must be **database-free and clock-injected**,
so their tests are pure unit tests that run in `make test` without the stack
up. That is a genuine improvement over today, where every executor test needs
Postgres.

`server.go`'s `Execute` should read as: validate, claim the attempt, execute
via the routed port, record the outcome, respond. If it contains a cost
constant or a `switch` on action type, that logic is in the wrong file.

---

## 7. Tests

TDD per `ENGINEERING.md` §1. Two tiers, and the split is deliberate.

### Pure unit tests, no build tag, no database

New, and the reason for the §6 layout.

- **Routing**: each `ActionType` reaches the expected port. Table-driven over
  the whole `ActionType` enum so a new action cannot be added to the proto
  without a decision here.
- **The script**: retry attempt 1 succeeds; retry attempt 2 fails with a
  failure code; each nudge type returns `PENDING` with a non-zero
  `resolves_at` and its documented cost.
- **Determinism**: the same request twice yields byte-identical results. This
  is what Phase 2's re-run safety test depends on.
- **Costs**: every action's cost is non-negative; nudge costs are non-zero
  (a free SMS would quietly make the economics meaningless in Phase 2);
  WhatsApp costs more than SMS, per `notifier.proto`.
- **`resolves_at`** is computed from the injected clock, not `time.Now()`.
  Prove it with a `clock.Fake`: advance it and assert the value moves.
- **`ESCALATE` and `NONE`** hit no port at all. Assert with a port fake that
  records calls.

### Integration tests, `//go:build integration`

Extend `internal/server/server_test.go`. Keep every existing test passing.

- **The `PENDING` redelivery case** (§4.3). A nudge executes, returns
  `PENDING`; a redelivered identical request returns promptly with
  `already_executed=true` and `outcome=PENDING`. Assert it returns fast:
  a test that only checks the value would pass even if the poll spun for
  seconds. Bound it with a short context and assert no deadline error.
- **Persistence**: `cost_paise`, `failure_code`, and `message_text` all land
  on the `intervention_attempt` row with the values returned in the response.
  Read them back from Postgres, not from the response object.
- **Failure outcome**: a `FAILURE` returns a non-empty `failure_code`, is
  persisted, and does **not** return an RPC error (§5).
- **Idempotency across outcome kinds**: repeat the existing redelivery and
  concurrent-duplicate tests for a nudge action (which now returns `PENDING`)
  and for a failing retry, not just the succeeding retry. The guard must be
  outcome-agnostic, and today only the success path is covered.
- **Unknown `record_id`** behaves as decided in §5.
- **Redis fall-through**, if §4.4 is approved: with an unreachable Redis, all
  idempotency tests still pass. This is the test that makes the fast path
  safe to ship.

Run with `-race`. The concurrent-duplicate test is the one that matters and
it already exists; keep it.

---

## 8. Config, clock, Redis

**Clock.** Already injected, keep it. `resolves_at` and the poll interval
both go through it. Never `time.Now()` (`ENGINEERING.md` §2).

**Scale.** Apply `cfg.Scale()` to the nudge resolution delay, the same way
the Decision Engine applies it to `RETRY_DELAY`/`NUDGE_DELAY`. A demo-time
scale factor that skips this service would leave nudges resolving on a real
clock while everything else compressed, which is exactly the bug
`DEMO_TIME_SCALE` exists to prevent.

**New config.** Likely needed:

- a nudge resolution delay (the `resolves_at` offset), scaled;
- the attempt-await bound from §4.3;
- if §4.4 is approved: the Redis idempotency key TTL. `REDIS_ADDR` already
  exists in `.env.example`.

Add each to `.env.example` **in the same PR**, with a comment in the
surrounding style. That file is *not* union-merged (only `docs/PLAN.md`,
`docs/DECISIONS.md`, `docs/INCIDENTS.md` are), so keep the diff to the lines
you need. Validate at startup and fail fast (`ENGINEERING.md` §5).

**Database.** Already connected and correct. You own `INTERVENTION_ATTEMPT`
and write nothing else (§10a). Read `RECORD` if you must; do not write it.

---

## 9. Logging and metrics

**Logging.** Already using `logger.ForRecord`. Keep and extend. Log at least:

- the claim, the execution, and the replay-on-duplicate (all present today);
- cost and outcome on every execution (`KeyCostPaise`, `KeyOutcome` exist);
- a `Warn` on a nudge with an empty message;
- a `Warn` on a Redis fall-through, if §4.4 lands.

**Metrics.** Do not add Prometheus wiring. `ARCHITECTURE.md` §13 is explicit
that per-method metrics arrive "via a shared gRPC interceptor (not hand-added
per handler)", which is Phase 4, and `intervention_spend_paise_total` belongs
with Phase 2's economics. The Audit Service set the precedent this repo
follows: log now, export in Phase 4. State the deferral in your PLAN.md note
so it reads as deliberate.

---

## 10. Work needed outside this service

### Migrations: none needed

`intervention_attempt` already has `cost_paise`, `message_text`,
`message_source`, `failure_code`, `ev_score_at_decision`, and
`p_recovery_at_decision`. The §4.3 claim-marker change needs no migration
because `outcome` is unconstrained `TEXT NOT NULL`. Verify this yourself
against `migrations/00001_initial_schema.sql` before concluding otherwise.

Note `message_source` exists and nothing writes it. It is Phase 5's (whether
the nudge text was LLM-generated or templated, per §5b). Leave it NULL; do
not invent a value.

### Proto: none needed

`executor.proto` already has every response field this task populates,
including `resolves_at`, `already_executed`, and `failure_code`. The port
contracts in `worldsim.proto` and `notifier.proto` are what your Go
interfaces mirror. If you conclude a proto change is required, **stop and
propose it** per `services/executor/AGENTS.md`; proto changes are their own
PR, merged first (`ARCHITECTURE.md` §9).

### Items to raise, not build

**1. Redis needs a new module dependency. This is a blocker for §4.4.**

`github.com/redis/go-redis/v9` is **not** in `go.mod`, and there is no
`internal/platform/redisx` package. `internal/platform/pgx/doc.go` names
go-redis as the pinned choice, but nobody has added it. So the fast path
requires either a new `internal/platform/` package or a direct dependency
inside this service, and `services/executor/AGENTS.md` puts
`internal/platform/` squarely in "stop and propose".

Three options, for the orchestrator to pick:

- **Defer the fast path to Phase 6**, alongside the load testing that would
  justify it. Cheapest, and costs no correctness (§4.4). Tick the PLAN.md
  item with the deferral stated explicitly.
- **A new `internal/platform/redisx`**, consistent with how `kafkax` and
  `pgx` are handled, and reusable by Phase 2's cooldown/retry-budget keys and
  Phase 5's delayed-outcome queue, both of which need Redis anyway. Best
  long-term shape, but it is an `infra/` PR by someone with that mandate, not
  an executor PR.
- **go-redis directly inside `services/executor`.** Fastest to write, but it
  guarantees a second agent duplicates it within two phases.

Recommendation: propose `internal/platform/redisx` as a separate `infra/`
task, and defer the fast path until it exists. Do not add the dependency
yourself.

**2. Should the Executor write `AUDIT_ENTRY` rows? Recommendation: no.**

`ARCHITECTURE.md` §10a's ownership table says `AUDIT_ENTRY` is "Written by
Decision Engine **and Executor**, transactionally with their own state
changes". The Executor currently writes none. Before adding them, note:

- `audit_entry.from_state` and `to_state` are both `NOT NULL`, and the
  Executor does not own `RECORD_STATE` and does not know the record's states.
  It has nothing truthful to put there.
- The Decision Engine's `recordOutcome` already writes the outcome entry
  transactionally, including `attempt_number` and `cost_paise`, so the trail
  is complete without an Executor entry.
- `test/e2e/walking_skeleton_test.go` asserts **exactly 3** audit entries. A
  fourth breaks it.

So the Executor's honest contribution to history is the
`intervention_attempt` row, and the audit trail is the Decision Engine's,
written transactionally with the state change it describes. That reading is
consistent with §10a's actual rule ("the service that owns a state change
writes both"), since the Executor owns no state change. **Recommendation: do
not write audit entries.** Record the reasoning in `docs/DECISIONS.md` so the
next reader does not re-litigate it, and flag the ownership table's wording
as worth a clarifying edit by whoever owns the docs.

**3. Pre-existing bug, verified, not yours to fix: the invariant verifier
rejects every record the pipeline currently produces.**

`services/audit/internal/server/statemachine.go`'s `allowedTransitions` has
no `NEW -> RETRY_SCHEDULED` or `NEW -> NUDGE_SCHEDULED` edge. It has
`NEW -> SCORING`, `NEW -> ESCALATED`, and a temporary `NEW -> RECOVERED`. But
the Decision Engine's Phase 1 code writes `NEW -> RETRY_SCHEDULED` on every
classified record, because `Scoring` is Phase 2 and does not exist.

Verified against the live database on 2026-08-23:

```
RECORD_STATE_NEW -> RECORD_STATE_RETRY_SCHEDULED       | 6
RECORD_STATE_RETRY_SCHEDULED -> RECORD_STATE_RETRYING  | 6
RECORD_STATE_RETRYING -> RECORD_STATE_RECOVERED        | 6
RECORD_STATE_NEW -> RECORD_STATE_RECOVERED             | 2
```

So `VerifyInvariants` currently reports `impossible_transitions = 6`, an
invariant that `ARCHITECTURE.md` §13 says must stay at zero and is alerted on
at critical severity. It went unnoticed because the two halves merged from
different agents at nearly the same time, and because `GetRecordAudit`
hardcodes `TrailComplete: true` rather than computing it, so the e2e test's
`TrailComplete` assertion is vacuous and passes regardless.

**Why this is in your spec**: your scripted stub will start producing
`RETRYING -> ESCALATED` and nudge-path edges for the first time. When you run
invariant checks and see non-zero counts, **you did not cause them.** Do not
"fix" it by editing the Audit service or the state machine; both are outside
`services/executor`. Raise it. It is a two-line fix in the Audit service
(add the two edges, with the same TEMPORARY comment style the existing
`NEW -> RECOVERED` edge carries) plus a real `TrailComplete` computation, and
it belongs to whoever owns Audit.

**4. `audit_entry.message_text` is read but never written.**

`ARCHITECTURE.md` §5b says the composed nudge text is stored so "the demo can
show the actual message inside a record's audit trail". The Audit service
reads `audit_entry.message_text`, but nothing writes that column: the message
lands on `intervention_attempt.message_text` (yours) instead. Phase 5 will
need the Decision Engine's `recordOutcome` to carry it across, or the Audit
service to join `intervention_attempt`. Not blocking, worth raising.

Raise all four in the PR description.

---

## 11. What breaks if you get this wrong

- **`test/e2e/walking_skeleton_test.go` must stay green.** It posts one
  `BANK_TIMEOUT` record and asserts it reaches `RECOVERED` via
  `NEW -> RETRY_SCHEDULED -> RETRYING -> RECOVERED`, with exactly 3 audit
  entries and `AttemptNumber == 1` on the last. Concretely: **a `RETRY` at
  `attempt_number = 1` must return `SUCCESS`**, and you must not add a fourth
  audit entry. Everything in §4.2's script is built around keeping this true.
- **Returning an RPC error instead of a `FAILURE` outcome dead-letters a
  healthy record.** The Decision Engine's scheduler retries an error three
  times, then publishes to `raw.events.dlq` and leaves the record in
  `RETRYING`. Re-read §5.
- **The `PENDING` collision hangs redelivered nudges** (§4.3). This is the
  one that will not show up until a nudge is redelivered, which is exactly
  when you least want to be debugging it.
- **Breaking an existing idempotency test means you broke money safety**, not
  a test. `ENGINEERING.md` §8 and §11 item 8. Stop and re-read rather than
  adjusting the assertion.
- **The Decision Engine's tests will not catch any of this.** Its
  `fakeExecutor` never calls your code. Green decision-engine tests are not
  evidence; `make test-integration` is.

---

## 12. Definition of Done

`ENGINEERING.md` §11, made concrete.

1. Tests written first and passing, including `-race`.
2. Error paths tested: port failure, unknown record, empty nudge message,
   Redis unreachable (if built).
3. Structured logs with `record_id` correlation. Metric export deliberately
   deferred to Phase 4, and said so in the PLAN.md note.
4. `ctx` with a deadline on every outbound call. Phase 1's ports are
   in-process, so this means plumbing `ctx` into them and honouring it in the
   bounded await, ready for Phase 5's real gRPC calls.
5. Graceful shutdown: already handled, do not regress.
6. Config validated at startup, including any new value from §8.
7. `gofmt` clean, no new lint failures.
8. **This service touches money**: idempotency proven by a test that delivers
   the same action twice and asserts one effect. The existing tests do this;
   extend them to the nudge and failure paths (§7), because today only the
   success path is proven.
9. Docs updated where behaviour diverged.
10. Structure per §14: `server.go` orchestrates, costs and routing live in
    their own files, port stubs are database-free and unit-testable.

---

## 13. Verify, then update the docs

```bash
make check                                        # fmt, vet, proto-lint, build, unit tests
go test -race ./services/executor/...             # unit tier, no stack needed
make test-integration                             # stack up, integration + e2e
go test -race -tags integration ./services/executor/...
```

Run `make test-integration` more than once. This repo has been bitten twice
already by tests that passed on the first run (see `docs/INCIDENTS.md`), and
one of those was a concurrency race in a shared-database test much like
yours.

Then, in the same PR:

- **`docs/PLAN.md`**: tick the Executor box, or tick it with an explicit note
  if the Redis fast path was deferred (§10 item 1). Follow the established
  style: what was built, what was deliberately excluded and why (real
  simulators to Phase 5, delayed-outcome callback to Phase 5, economics to
  Phase 2, metrics to Phase 4), and the file split. Add `(unplanned, ...)`
  lines for anything not in the plan. Union-merged, so tick directly.
- **`docs/DECISIONS.md`**: at minimum the claim-marker change from §4.3 (and
  update the existing insert-then-update entry rather than contradicting it),
  the `ESCALATE`/`NONE` routing decision, the cost constants and their
  source, the audit-entry decision from §10 item 2, and the Redis outcome.
- **`docs/INCIDENTS.md`**: anything that cost you real time.
  `ENGINEERING.md` §12 is not optional.
- **`services/executor/AGENTS.md`**: only if behaviour diverged from it.
- **This file**: add anything that would have saved you an hour. Phase 5's
  agent reads it next.

Branch `svc/executor/ports-and-idempotency` per the root `AGENTS.md`
convention. Small PR, no AI attribution in the commit message or PR
description, and `grep -i "claude\|co-authored\|generated with"` your commit
message before pushing.
