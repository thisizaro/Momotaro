# Phase 2 Implementation Plan: Durability, Safety & Economics

This is the working breakdown of `docs/PLAN.md` Phase 2. `PLAN.md` stays the
one-line checklist; this file explains what each item actually is, in an order
that can be built.

## How to use this document

Every unit below is **independently completable**. Each one names its
dependencies, the exact files it owns, its branch name, and how to prove it
works. A unit whose dependencies are merged can be handed to an agent in its
own worktree without coordination.

Read the **Collision notes** before running two units at once. Two units that
name the same file must not run in parallel, even in separate worktrees, because
the conflict surfaces at merge time instead of at write time.

**Phase goal in one sentence**: make the agent's spending decisions defensible
and its safety guarantees provable under failure.

Phase 1 proved a record can flow through the pipeline. Phase 2 makes it flow
*only when worth it* and *correctly when things break*.

---

## Status at a glance

| Unit | What | Status | Blocks |
|---|---|---|---|
| A | Guardrails: retry budget, contact cap, cooldown, recovery window | **merged** | E, H |
| B | Cost model and recovery priors | **merged** | D |
| C | `GROUND_TRUTH` isolation test | **merged** | nothing |
| D | Economics scorer, `Scoring`, `ClosedUneconomic` | **merged** | E, F, G, M |
| E | Retry loop: re-entry to `Scoring` after a failed attempt | **merged** | F, H, M |
| F | Cause-aware retry timing | **merged** | nothing |
| G | EV snapshot persisted per attempt | not started | nothing |
| H | Batch correctness invariants | not started | nothing |
| I | Idempotency proven end to end | **merged** | nothing |
| J | Re-run safety | **merged** | nothing |
| K | Crash safety | not started | nothing |
| L | Scheduler fake-clock test | **merged** | nothing |
| M | Delete the `TEMPORARY` state machine edges | not started | nothing |

**9 of 13 merged. 4 remaining.** H and M are unblocked. G is complete but
not yet merged (two branches: `infra/record-state-ev-snapshot`,
`svc/executor/ev-snapshot`).

Units A, B and C map to three `PLAN.md` checkboxes. D and E together are the
"economics scorer" checkbox. M is cleanup that Phase 2 unlocks and that
`statemachine.go` explicitly asks for.

---

## Dependency graph

```
  A (merged) ─┐
  B (merged) ─┴─> D ──> E ──┬──> F
                            ├──> H
                            └──> M
                     G  I  J  K  L   (independent, start any time)
```

**The critical path is D -> E.** Everything else either sits on it or is free.

Units G, I, J, K and L depend on nothing and can start immediately, in
parallel, today.

---

## Unit D: Economics scorer

**Status**: merged, PR #28.
**Depends on**: A, B (both merged).
**Branch**: `svc/decision-engine/economics-scorer`.

### What it is

The part that makes this an agent rather than a retry loop. For every action
the guardrails permit, compute expected value and pick the best. If nothing
has positive expected value, close the record as deliberately not worth
chasing.

### Why it matters

It is the answer to the judge question "does the LLM decide how money is
spent?". It does not. The Classifier says *what went wrong*; the numbers
decide *what to do about it*.

### LLD

```
services/decision-engine/internal/economics/
  config.go   Load(costsPath, priorsPath) (*Model, error)
  score.go    Model.Score(action, bucket, attemptNo, amountPaise) Score
              Model.Best(permitted []Candidate, bucket, amountPaise) (Score, bool)

  Candidate { Action ActionType; AttemptNo int }
  Score     { Action; LiftBps int; PRecovery float64; EVPaise float64; CostPaise int64 }
```

`EV = P(recovery) * amount_at_risk - direct_cost - indirect_cost`

Two things that are easy to get wrong and are already handled:

- The prior table holds **lift**, not absolute recovery rate. Scoring against
  absolute rates would credit the agent with recoveries that would have
  happened anyway.
- `Best` requires **strictly positive** EV. An action that exactly breaks even
  is not worth the operational risk.

### Definition of done

Merged, CI green including the integration and e2e tiers.

---

## Unit E: Retry loop, re-entry to Scoring

**Status**: merged.
**Depends on**: D merged.
**Branch**: `svc/decision-engine/retry-loop`.
**Files owned**: `services/decision-engine/internal/engine/state.go`,
`scheduler.go`, `store.go`, and their tests.

### What it is

Today a failed attempt escalates. `ARCHITECTURE.md` §7 says it should return to
`Scoring`, so the record is re-priced with one more attempt spent and a lower
recovery probability. An action worth trying on attempt 1 can then be judged
not worth trying on attempt 3.

### Why it matters, and why it is not optional

**Without the loop, the guardrail caps never bind.** Nothing retries twice, so
no record ever approaches a cap of 3. That makes Unit H's headline claim, "zero
stopping-rule violations", trivially true and therefore worthless. The loop is
what turns Phase 2 from nominal into real.

It is also what makes the decaying priors do any work. Right now every scoring
decision happens at attempt 1.

### LLD

Change `decideAfterExecute` so a failed attempt returns to `Scoring` rather
than `Escalated`:

```
FAILURE + budget remains  -> Scoring       (re-price with attemptNo + 1)
FAILURE + budget spent    -> Escalated     (guardrails refuse everything)
SUCCESS                   -> Recovered
PENDING  + nudge          -> Nudged
```

Re-entering `Scoring` must reuse the same decision path as Unit D rather than
duplicating it. Extract the body of `Engine.decide` so both the New path and
the re-entry path call one function.

The scheduler then needs to write the same multi-step transitions the New path
writes, so `store.go` needs a re-scoring equivalent of `scheduleNew`.

**Termination is the thing to prove.** A loop that re-scores forever is worse
than the escalate-on-failure behaviour it replaces. Two independent stops
exist and both must be tested: the guardrail caps (compliance) and the priors
falling to zero past the deepest modelled attempt (economics).

### Definition of done

- A record that fails its first retry is re-scored, not escalated
- A record that exhausts its retry budget escalates, and does so via the
  guardrails refusing rather than via a hardcoded branch
- A test drives a record through the full loop to a terminal state and asserts
  it terminates
- The audit trail shows every hop

### Collision notes

Touches the same three files as Unit F. **Do not run E and F in parallel.**

---

## Unit F: Cause-aware retry timing

**Status**: merged.
**Depends on**: E merged.
**Branch**: `svc/decision-engine/cause-aware-timing`.
**Files owned**: a new `schedule.go` in `internal/engine`, plus `engine.go`'s
`dueAtFor`.

### What it is

Replace the single fixed `RetryDelay` with timing that follows the root cause.

| Bucket | Timing | Why |
|---|---|---|
| `INSUFFICIENT_FUNDS` | next salary window, 1st to 7th | the money genuinely is not there yet |
| `TRANSIENT_BANK` | minutes, short backoff | funds were there, the rail was busy |
| `HARD_DECLINE` | never retry | a retry cannot succeed, only a method update can |
| `RISK_HOLD` | never retry, escalate | never auto-retry around a risk decision |

### Why it matters

It is the single most demo-legible feature in the project. "It waits for payday
instead of burning an attempt tomorrow" lands with a judge in one sentence.

### LLD

```
func retryDueAt(bucket RootCauseBucket, attemptNo int, now time.Time) *time.Time
```

Pure function, injected clock, no I/O, table-driven tests.

Two traps:

- **The salary window needs real date arithmetic.** If today is the 3rd, the
  window is open now. If it is the 28th, the next window is the 1st of next
  month. Month boundaries and year rollover both need tests.
- **`DEMO_TIME_SCALE` must apply**, or a salary-window retry never fires inside
  a demo. The existing `cfg.Scale()` is the mechanism.

Hard-decline and risk-hold rows are belt-and-braces: the priors already give
them zero probability, so the scorer would not choose a retry anyway. Encode
them regardless, and say in a comment that this is a second independent stop.

### Definition of done

Table-driven tests covering each bucket, both salary-window branches, a month
boundary, and a year rollover. Scaling verified. Red/green proof that a test
can actually fail (timeScale division and salary-window boundary both proven).

---

## Unit G: EV snapshot persisted per attempt

**Status**: not started, and **unblocked right now**.
**Depends on**: nothing. The proto fields are merged (PR #27).
**Branch**: `svc/executor/ev-snapshot`.
**Files owned**: `services/executor/internal/attempt/store.go`,
`services/decision-engine/internal/engine/clients.go`.

### What it is

`INTERVENTION_ATTEMPT` has held `ev_score_at_decision` and
`p_recovery_at_decision` since the first migration and **nothing has ever
written them**. The Decision Engine now computes both. Pass them on the
`ExecuteRequest` and have the Executor persist them.

### Why it matters

The priors get recalibrated over time, so recomputing an old attempt's expected
value later gives today's answer, not the one that authorised the spend.
Storing the snapshot is what lets the trail answer "why did you think this was
worth spending?".

### LLD

Decision Engine: populate `EvScoreAtDecision` and `PRecoveryAtDecision` on the
`ExecuteRequest`. The winning `economics.Score` already carries both; it needs
threading from `decide` to the execute call.

Executor: add both columns to the insert in `attempt/store.go`. The Executor
**never recomputes them**; it records what it is told. The Decision Engine is
the only service that scores.

### Definition of done

An integration test asserting both columns are non-null and match what the
Decision Engine computed, after a real execution.

### Collision notes

Touches `clients.go`, which Units E and F do not. Safe to run alongside either.

---

## Unit H: Batch correctness invariants

**Status**: not started.
**Depends on**: E merged. Meaningless before it.
**Branch**: `test/batch-invariants`.
**Files owned**: a new file in `test/e2e/`.

### What it is

Run a batch and assert the two headline correctness claims from `PRD.md` §9 and
§10: **zero stopping-rule violations**, and **100% audit trail completeness**.

### Why it matters, and why the dependency is strict

These are the numbers a judge checks. Written before Unit E, the test passes
because nothing ever retries twice, so no cap can be violated. It would be a
green test that proves nothing, which is the exact failure mode this repo has
hit three times already.

### LLD

Submit a batch shaped to push records **towards** their caps: several records
in buckets that retry, with amounts large enough to stay economic across
attempts. Drive to terminal states, then assert:

- no record exceeded `MAX_RETRIES` retry attempts in `INTERVENTION_ATTEMPT`
- no record exceeded `MAX_CONTACTS` contacts
- no two contacts for one record are closer together than `CONTACT_COOLDOWN`
- every record reached a terminal state
- `Audit.VerifyInvariants` reports zero violations for the batch

**Prove the test can fail.** Temporarily raise a cap so a violation is
reachable, confirm red, then revert. Without that, this is an assertion that
nothing bad happened in a run where nothing could have.

### Collision notes

Shares `test/e2e/harness_test.go` with Units I, J and K. See the shared hazard
below.

---

## Unit I: Idempotency proven end to end

**Status**: merged.
**Depends on**: nothing.
**Branch**: `test/idempotency-e2e`.
**Files owned**: a new file in `test/e2e/`.

### What it is

Prove the same event arriving twice never charges or messages a customer twice,
across the whole pipeline rather than at the Executor alone.

Two delivery paths, both real:

1. **Duplicate Kafka delivery**: publish the same `raw.events` message twice.
2. **Duplicate gRPC retry**: call `Execute` twice with the same
   `(record_id, attempt_number)`.

### Why it matters

The Executor's durable guard is already tested in isolation. What is untested
is that the *pipeline* preserves it: that nothing upstream renumbers an
attempt or creates a second record row.

### LLD

The guarantee is `UNIQUE (record_id, attempt_number)` with insert-before-
execute. Assert **one** `INTERVENTION_ATTEMPT` row and **one** side effect,
and that the second call returns `already_executed`.

### Definition of done

Both paths tested, with the assertion on row count rather than on the response
alone. A duplicate that returns the right answer while writing two rows is
still a bug.

---

## Unit J: Re-run safety

**Status**: merged.
**Depends on**: nothing.
**Branch**: `test/rerun-safety`.
**Files owned**: a new file in `test/e2e/`.

### What it is

Submit the same batch twice and confirm identical outcomes with no
double-processing.

### Why it matters

This is the demo-rehearsal safety net. Re-running a batch is exactly what
happens when a demo is repeated, and it must not double-count recovered
revenue.

### LLD

Distinguish two cases explicitly, because they are different guarantees:

- **Same `batch_id` resubmitted**: idempotent, nothing new is created.
- **New `batch_id`, same records**: a genuinely new batch. Decide and document
  which the API promises; do not leave it implied.

Assert on final states, `INTERVENTION_ATTEMPT` counts, and total spend.

---

## Unit K: Crash safety

**Status**: not started, **unblocked now**.
**Depends on**: nothing.
**Branch**: `test/crash-safety`.
**Files owned**: a new file in `test/e2e/`.

### What it is

Kill the Decision Engine mid-batch, restart it, and assert no record is lost
and no audit trail has a gap.

### Why it matters

This is what the transactional write and the contiguous-prefix Kafka commits
exist to guarantee. Until something kills a process, both are claims rather
than facts.

### LLD

The harness already starts services as subprocesses, so a hard kill is
available. Steps: submit a batch, wait until partially processed, `SIGKILL`
the Decision Engine, restart it, wait for completion.

Assert: every record reaches a terminal state, every trail is complete with no
missing hop, and no record was processed twice.

`SIGKILL`, not `SIGTERM`. A graceful shutdown proves the graceful path, which
is not the interesting one.

---

## Unit L: Scheduler fake-clock test

**Status**: merged.
**Depends on**: nothing.
**Branch**: `svc/decision-engine/scheduler-clock-test`.
**Files owned**: `services/decision-engine/internal/engine/scheduler_test.go`.

### What it is

A record parked with a future `due_at` fires **exactly once** when the clock
passes it, and is never claimed by two concurrent pods.

### Why it matters

`FOR UPDATE SKIP LOCKED` is the mechanism the whole time-based half of the
system rests on. Double-claiming means charging a customer twice.

### What actually shipped

Two tests: `TestSchedulerFiresOnceWhenFakeClockPassesDueAt` uses
`clock.Fake` for the "fires exactly once" half.
`TestSchedulerConcurrentSchedulersClaimExactlyOnce` races **25** concurrent
`Scheduler`s (not 2) against one due row, via a fake Executor
(`attemptRecordingExecutor`) that mirrors the real Executor's
insert-before-execute contract, including its graceful handling of a
duplicate claim (catches SQLSTATE 23505, returns `AlreadyExecuted` rather
than an error).

Both numbers came from adversarial review, not the original draft: with
only 2 racers, removing `FOR UPDATE OF rs SKIP LOCKED` from `claimDue`
entirely still passed 5/5 runs, because true database-level overlap between
two racers was rare in practice. Raising to 25 racers gave a ~60% catch
rate over 20 runs against the same deliberately-broken code, with 0/20
false positives against correct code. Separately, the first version of the
fake Executor did not mirror the real duplicate-claim handling, so when
contention was forced high enough to actually double-claim, the Scheduler's
retry loop waited on a fake clock nothing in the test advances and the test
**hung** until its timeout instead of failing with a clear assertion,
worse than a silent pass since it reads as an infra problem in CI, not a
locking bug. Fixed before merge.

### LLD

Use the injected `clock.Fake`. Two assertions:

1. **Fires exactly once.** Park a record ahead of the clock, tick past it,
   assert one claim.
2. **Never double-claimed.** Run schedulers concurrently against one due
   record and assert exactly one claims it.

For the concurrency half, a single green run is not evidence. Use the
`GOMAXPROCS=2` loop from `ENGINEERING.md` §1. `-race` does not catch ordering
bugs, and this repo has two incidents proving it.

### Collision notes

Owns `scheduler_test.go`, which Unit E also touches. **Do not run L and E in
parallel.**

---

## Unit M: Delete the TEMPORARY state machine edges

**Status**: not started.
**Depends on**: E merged.
**Branch**: `svc/audit/remove-temporary-edges`.
**Files owned**: `services/audit/internal/server/statemachine.go`.

### What it is

Three edges in the Audit state machine are marked `TEMPORARY` with comments
saying to delete them once `Scoring` exists: `New -> Recovered`,
`New -> RetryScheduled`, `New -> NudgeScheduled`. Every record now routes
through `Scoring`, so none of the three should be produced any more.

### Why it matters

They are holes in the invariant verifier. While they exist, a record that
skipped the economics gate would pass verification silently. The comments
already promise this cleanup; leaving it undone makes the docs a lie.

### LLD

Delete the three edges, run the full e2e suite, and confirm nothing regresses.
If something still produces one of them, **that is a bug in the producer**, not
a reason to keep the edge.

Do this **after** E, not after D. The re-entry path may legitimately produce
transitions the New path does not.

---

## Parallelization guide

**Start immediately, in parallel, no coordination:** G, I, J, K, L.

**After D merges:** E.

**After E merges:** F, H, M, all three in parallel.

The tempting mistake is starting H early because it sounds like a test. It is
meaningless before E.

## Shared hazards

**`test/e2e/harness_test.go` is shared by H, I, J and K.** Adding a *new file*
to `test/e2e/` is safe. Changing the harness is not. Either give one agent
ownership of all four units in sequence, or route every harness change through
the lead. Unit K almost certainly needs a harness change, since killing and
restarting a single service is not something the harness supports today.

**`docs/PLAN.md`, `docs/DECISIONS.md` and `docs/INCIDENTS.md` are safe to edit
concurrently.** They use git's `merge=union` driver. Append only, never
restructure, and note that GitHub's web merge does **not** apply the driver, so
a conflicting PR must be merged locally.

**Every unit must state, honestly, whether its tests can fail.** The recurring
failure in this repo has not been agents refusing work, it has been green that
was not evidence: a test that passed three runs in four by chance, a hardcoded
`true`, a fabricated fixture, and a mutation harness that reported errors as
successes. The cheap defence is mechanical: break the code on purpose, confirm
the test goes red, revert, and paste the real output.

## Definition of done, for every unit

`docs/ENGINEERING.md` §11 is the gate. The item most often skipped here is
**item 3, the exported metric**. No service exports metrics yet, so that half
lands in Phase 4 with the rest of observability. Say so in the PR rather than
quietly ticking the box.
