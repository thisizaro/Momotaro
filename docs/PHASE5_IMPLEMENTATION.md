# Phase 5 Implementation Plan: Demonstration Realism

This is the working breakdown of `docs/PLAN.md` Phase 5. `PLAN.md` stays the
one-line checklist; this file explains what each item actually is, what a
pre-planning audit found already built versus still a stub, and an order
it can be built in.

Same contract as `docs/PHASE2_IMPLEMENTATION.md`/`docs/PHASE3_IMPLEMENTATION.md`/
`docs/PHASE4_IMPLEMENTATION.md`. Every unit below is independently
completable and names its dependencies.

**Phase goal in one sentence**: make the system demoable against real,
plausible data end to end, not just provably correct on hand-picked test
records.

**Where this sits**: Phases 0 through 4 are complete and merged (Phase 4's
Unit F, tracing, deferred to `docs/BACKLOG.md`). A pre-planning audit
(2026-08-29) found more already built than `docs/PLAN.md`'s checklist
implies for some items, and one real prerequisite gap `PLAN.md` doesn't
mention at all. See "Audit findings" below before assuming any item's
scope from the checklist alone.

| Unit | What | Status | Depends on |
|---|---|---|---|
| A | Decision Engine gRPC server (`ReportDelayedOutcome`) | merged | nothing |
| B | Synthetic batch generator | not started | nothing |
| C | World Simulator (real) | not started | A |
| D | Executor: wire real World/Notification Simulator clients | not started | C |
| E | Hinglish nudge composition (Classifier) | not started | A (for the caller; the Classifier-side work itself is independent) |
| F | Reporting Service | not started | nothing strictly, more useful once B/C produce real data |
| G | API Gateway: report/records/audit routes + WebSocket relay | not started | F |
| H | Dashboard: wire to real Gateway | not started | G |

## Audit findings (2026-08-29, before any unit was picked up)

A survey of the actual repo state, done before committing to a build
order, found:

1. **Decision Engine had no gRPC server at all**, not even a stub. Both
   World Simulator's delayed-outcome callback (Unit C) and any nudge-
   composition caller (Unit E) need to call *into* Decision Engine, so
   this was a bigger prerequisite than `docs/PLAN.md`'s Phase 5 bullets
   imply. This is Unit A, done first for exactly that reason.
2. **The Hinglish migration `PLAN.md` calls for is already done.**
   `intervention_attempt.message_text`/`message_source` shipped in
   `migrations/00001_initial_schema.sql`, not a new additive migration.
   `PLAN.md`'s line describing this is stale.
3. **`GROUND_TRUTH` already exists** with the right columns
   (`true_bucket`, `recovery_probability`, `wrong_action_probability`,
   `response_delay_seconds`), including two World Simulator needs that
   `PLAN.md`'s own item-1 phrasing doesn't mention. Unit B has a real
   target schema today.
4. **The dashboard (`web/`) is essentially already built**: real
   components, a working mock backend, and a WebSocket client already
   written against `docs/API_GATEWAY.md`'s documented contract. Unit H is
   "wire to the real Gateway and reconcile contract drift," not "build a
   dashboard" — a very different effort estimate than the checklist
   implies.
5. **A real gap, not a stale doc**: `web/src/lib/api.ts` already calls
   `GET /v1/batches` to list batches for the selector dropdown, but
   `docs/API_GATEWAY.md` never specs that endpoint, only per-batch ones
   that already require a `batch_id` you'd need to have discovered some
   other way. Needs resolving when Unit G is picked up: add the endpoint
   and document it, or change the dashboard's discovery flow.
6. **Unit B and Phase 6's `scripts/loadgen` are different tools.**
   `loadgen` submits via the real HTTP API for throughput testing and can
   never carry ground truth (Ingestion's API has no such field); Unit B
   writes directly into `batch`/`record`/`ground_truth` in Postgres,
   bypassing Ingestion entirely, since only it can seed the sealed answer
   key. Worth keeping distinct rather than merging into one script.

## Unit A: Decision Engine gRPC server

**Status**: merged.
**Depends on**: nothing.

**What it is**: `proto/decisionengine/v1/decisionengine.proto`'s one RPC,
`ReportDelayedOutcome` — resuming a record parked in `NUDGED`, awaiting an
outcome that resolves after the request that started it already returned
(a customer acting on a nudge hours later). Decision Engine had zero gRPC
surface before this; everything it did arrived on `raw.events`. This unit
gives it a second, narrow front door, and nothing else changes about how
it works.

**Design, in one paragraph**: a `NUDGED` record sits with no `due_at`
(nothing polls it), so this RPC is the only way it ever moves again.
`Scheduler.ResumeNudge` (`internal/engine/scheduler.go`) loads the
record's current claim-shaped snapshot, checks it is still `NUDGED` at
exactly the `attempt_number` being reported (a stale or duplicate report —
this RPC is at-least-once, `decisionengine.proto`'s own header comment —
must be discarded, not misapplied to the wrong attempt), then routes a
`SUCCESS` outcome straight to `Recovered` (`decideAfterExecute`, the same
function every synchronous success already uses) or a `FAILURE` outcome
back through `scoreAndRoute`, the exact re-entry to Scoring
`handleFailedAttempt` already uses for a synchronous execute failure
(`docs/ARCHITECTURE.md` §7) — so the async and sync paths cannot disagree
about what a failed attempt means.

**The one piece of new subtlety `handleFailedAttempt` didn't need**: two
copies of the same delayed-outcome report can arrive genuinely
concurrently (redelivery), with no prior "claim" transition serialising
them the way `claimDue` already serialises the scheduler's own poll loop.
`store.applyResumedOutcome` takes a `SELECT ... FOR UPDATE` row lock and
re-verifies eligibility inside the same transaction that writes, so a
loser of that race is discarded rather than double-applied. Proven with a
25-goroutine concurrency test
(`TestResumeNudgeConcurrentReportsApplyExactlyOnce`); with the lock
removed, that same test failed 11-12 out of 25 races applying twice, on
every run — see "Verification" below.

**Files**:
- `services/decision-engine/internal/engine/store.go`: `loadNudged`
  (reads a record's current claim-shaped snapshot; `found=false` only when
  the record doesn't exist at all, so a caller can tell "stale report for
  a record that moved on" from "this record_id was never real") and
  `applyResumedOutcome` (the locked, transactional write).
- `services/decision-engine/internal/engine/scheduler.go`: `Scheduler.
  ResumeNudge`, the orchestration — decides `SUCCESS`/`FAILURE`/discard,
  calls `scoreAndRoute` for the `FAILURE` case exactly like
  `handleFailedAttempt` does, and calls the new store methods.
- `services/decision-engine/internal/server/server.go` (new package):
  `Server` implementing `decisionenginev1.DecisionEngineServiceServer`.
  Validates the request (non-empty `record_id`, positive `attempt_number`,
  a real `outcome`), delegates to `Scheduler.ResumeNudge` via a narrow
  `resumer` interface (so a test can fake it), translates the result. A
  discarded report (`Applied=false`) is a normal response, not a gRPC
  error, since discarding is the expected outcome for redelivered or
  late-arriving reports.
- `services/decision-engine/cmd/main.go`: a `net.Listen` on `GRPC_PORT`
  (previously configured but unused — Decision Engine had never opened
  any port), the standard three interceptors (recovery, deadline,
  metrics, Phase 0/4), registers the new server, wired into the existing
  multi-goroutine supervision (`consumeErr`/`schedulerErr` gain a third
  sibling, `grpcServeErr`) and into `shutdown.Close`.
- `services/decision-engine/internal/engine/resume_nudge_integration_test.go`
  (new, `integration` tagged): success, re-score-not-escalate on failure,
  stale attempt number discarded, non-`NUDGED` state discarded, unknown
  record discarded, and the 25-goroutine concurrency proof.
- `services/decision-engine/internal/server/server_test.go` (new,
  untagged): request validation, delegation, discard-is-not-an-error, and
  error propagation, all against a fake `resumer`.

**Verification**: `go build/vet/test ./...`, `gofmt -l .` all clean.
`go test -tags integration ./services/decision-engine/...` and the full
`go test -tags e2e ./test/e2e/...` suite both green, including the
walking skeleton (which now also shows decision-engine's `"grpc server
listening"` log line it never had before). Adversarially removed the `FOR
UPDATE` lock and ran the concurrency test five times: 11-12 of 25
concurrent reports applied on every single run, versus exactly 1 with the
lock restored, confirmed clean across three more runs after reverting.
Adversarially broke the gRPC handler's discard path (`Applied=false`
returned as a `codes.Internal` error instead of a normal response) and
confirmed the corresponding test failed with the exact expected message.

**A real bug found only by live-testing the running binary, not this
unit's own test suite**: `loadNudged`'s first version scanned
`pending_action` into a plain `string`, which panics on the real `NULL`
that column legitimately holds once a record is past `NUDGED` (a
`RECOVERED` record has no pending action, `nullIfUnspecified`, store.go).
The suite's own `TestResumeNudgeDiscardsWhenNotInNudgedState` didn't
catch it, because its fixture helper (`seedScheduled`, shared with
`scheduler_test.go`) writes the literal string `"ACTION_TYPE_UNSPECIFIED"`
rather than real SQL `NULL`. Found by dialing the actual running
`decision-engine` binary with a throwaway gRPC client after seeding a
record directly in Postgres, then calling `ReportDelayedOutcome` twice —
the second call, against the now-`RECOVERED` record, crashed. Fixed
(`*string`, nil-checked) and the test now forces real `NULL` via a raw
`UPDATE ... SET pending_action=NULL`, confirmed to fail before the fix and
pass after. Full account: `docs/INCIDENTS.md` 2026-08-29.
