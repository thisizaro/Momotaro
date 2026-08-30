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
| B | Synthetic batch generator | merged | nothing |
| C | World Simulator (real) | merged | A |
| D | Executor: wire real World/Notification Simulator clients | merged | C |
| E | Hinglish nudge composition (Classifier) | not started | A (for the caller; the Classifier-side work itself is independent) |
| F | Reporting Service | in progress: unary RPCs merged, `StreamBatchUpdates` deferred | nothing strictly, more useful once B/C produce real data |
| G | API Gateway: report/records/audit routes + WebSocket relay | not started | F |
| H | Dashboard: wire to real Gateway | not started | G |
| I | Razorpay's real error codes as the failure vocabulary | merged | nothing |
| J | Compliance guardrails (TRAI contact hours, RBI mandate lead time) | merged | nothing |
| K | Baseline comparison in Reporting | not started | F |
| L | Surface stored-but-invisible decision provenance | not started | G (routes), H (UI) |
| M | Persist EV candidate ranking + guardrail refusal reasons | merged | nothing |
| N | Correct three stale claims in checked-in files | merged | nothing |
| **O** | **Freeze the API Gateway contract** | **merged** | **nothing. Blocked G, H and the whole frontend track. Done first.** |

**Units I to N were added 2026-08-29**, after the actual judging rubric was
read and recorded in `PRD.md` §0. They are not scope creep: I, J, M and N
touch code no other unit is writing and can run fully in parallel, K rides
inside F, and L rides inside G/H. See "What the rubric changed" below.

## Ordering, and the one dependency that is not real

The naive read of the table is a single chain C → D → F → G → H, which does
not fit the time available. It is not actually a chain.

**Reporting (F) does not depend on the World Simulator (C).** Reporting reads
Postgres. The Executor's existing scripted stub already produces recovered,
escalated and nudged records with real `cost_paise`, so every aggregate F
computes has data to compute over today. C makes those outcomes *realistic*
and unparks the ~70% of records that currently sit in `NUDGED` forever
waiting for a delayed outcome nobody delivers, which matters enormously for
whether the demo numbers look sane, but F can be built, tested and merged
against the stub in parallel with C.

That gives four independent tracks rather than one chain:

```
FIRST     O (freeze docs/API_GATEWAY.md)   <- gate, ~1h, blocks G and all frontend

then, fully parallel:
  track 1   C (World Simulator) -> D (Executor wiring)
  track 2   F (Reporting, unary RPCs first) -> K (baseline)
  track 3   E (Hinglish nudge composition)
  track 4   I, J, M, N   (independent, no shared files with the above)
  track 5   FRONTEND: F1 -> F2 -> F3 -> F4, entirely inside web/**

converging
  G (Gateway routes) after F, then track 5's F3 has live data to render
```

Track 5 needs no backend unit to finish before it starts, because mock mode
means the UI can be built and verified against the frozen contract before any
route exists. It only needs the routes to be *live* at demo time.

**Build F's unary RPCs before `StreamBatchUpdates`.** `GetBatchReport` and
`ListBatchRecords` are what close the rubric's one real gap. The streaming
RPC, plus Kafka consumption in Reporting, plus the Gateway's
gRPC-stream-to-WebSocket bridge, is the single most expensive item left in
the phase, and the dashboard's own 2 second refetch already drives every
aggregate on the page (the socket feeds only the scrolling log). Reasoning
and the honesty framing in `PRD.md` §12a. Same for G: the four routes split
cleanly into three cheap ones and one expensive one.

**`GET /v1/records/{id}/audit` is the cheapest genuinely-live thing in the
project.** It needs only the Audit service, which is fully implemented,
registered and tested. It needs no Reporting service at all. If everything
else slips, this one route makes demo beat 4 real.

## File ownership, for running agents in parallel

Phase 5 is the first phase where a **dedicated frontend agent** makes sense,
because `web/` is the one part of this system whose entire interface to
everything else is a written HTTP contract. That makes it genuinely parallel
work, on one condition stated below.

| Track | Owns | Must not touch |
|---|---|---|
| **Frontend** | `web/**` | everything else |
| Backend, per unit | its own `services/<name>/**` or `demo/<name>/**` | `web/**`, other services' trees |
| Shared, coordinated | `proto/`, `migrations/`, `docs/API_GATEWAY.md` | see below |

Both tracks may append to `docs/INCIDENTS.md` and tick boxes in
`docs/PLAN.md`; both use git's `merge=union` driver so concurrent edits merge
cleanly rather than conflicting.

**The condition: `docs/API_GATEWAY.md` must be frozen before either track
starts.** This is Unit O, and it is a genuine gate rather than paperwork. The
frontend agent builds against that document and nothing else (`web/AGENTS.md`
says so and has since Phase 0). The Gateway agent implements that document.
If it is ambiguous when they start, they will each resolve the ambiguity
differently, in isolation, correctly by their own lights, and the mismatch
surfaces at integration time when there is no time left to fix it. The
contract is currently ambiguous in at least six places (Unit O lists them),
so this is not hypothetical.

After the freeze, the two tracks genuinely do not interact. The frontend
agent can rebuild every screen if it wants to, and as long as it renders the
frozen contract, the Gateway agent never needs to know.

## Unit O: freeze the API Gateway contract

**Status**: merged 2026-08-29. **Depends on**: nothing. **Blocked**: G, H, and
the whole frontend track, now unblocked. **Actual size**: about an hour, as
estimated.

**What it is**: `docs/API_GATEWAY.md` is the interface between the frontend
track and the backend track, and it is not currently precise enough to build
against from two directions. Six concrete gaps, all found by auditing the doc
against the frontend's actual calls and the Reporting proto:

1. **`GET /v1/batches` (list batches) is called by the dashboard and is in
   neither the doc nor the Gateway.** `web/src/lib/api.ts` calls it on mount
   and sets the active batch from `list[0].batch_id`, so against a live
   Gateway there is no batch id, and report, records and updates all stay
   empty forever. This is the single hardest live-mode blocker. Spec it and
   implement it, or change the discovery flow to use the id returned by
   submit. Speccing it is better: a judge will want to look at more than one
   batch.
2. **Money field names drift between doc and proto.** The doc says
   `at_risk_amount` / `recovered_amount`; the proto says `at_risk_paise` /
   `recovered_paise`. Standardise on the `_paise` suffix everywhere, matching
   `docs/ENGINEERING.md` §8's integer-paise rule. A field named `amount` that
   holds paise is exactly how a float creeps in later.
3. **Enum wire spelling is undecided.** Does a state arrive as
   `RECORD_STATE_RETRY_SCHEDULED` (protojson's default) or `RetryScheduled`
   (what the frontend's lookup maps currently assume)? Nobody has chosen.
   Whichever is picked, the frontend's `Record<>` maps must be total over it,
   because they are exhaustive lookups that render `undefined` rather than
   erroring. Same question for `RecordType`, `Outcome` and `RootCauseBucket`.
4. **The frontend knows three root-cause buckets; `common.proto` has seven.**
   Four buckets currently produce blank labels and undefined colours. The
   contract should list the closed vocabulary explicitly so the frontend can
   be complete over it rather than discovering members at runtime.
5. **WebSocket auth mechanism is unspecified and the two sides disagree.**
   The frontend sends the key as a subprotocol
   (`new WebSocket(wsUrl, [API_KEY])`); the Gateway's middleware reads the
   `X-API-Key` header, which browsers cannot set on a WebSocket handshake.
   Pick one (subprotocol or a query parameter) and write it down. This
   currently fails on first connect.
6. **`POST /v1/batches` request body disagrees.** The frontend posts
   `{count: 80}`; the Gateway requires `{source, records: [...]}` and returns
   400 on the current payload. Decide whether the Gateway grows a
   generate-N-synthetic-records mode (convenient for a demo button, and
   `scripts/batchgen` already has the generation logic) or the frontend
   builds real record arrays. The first is better for the demo.

**Also fold in** the response fields Units K and L will need, so they are
specced once rather than appended twice: `net_recovered_paise`,
`intervention_spend_paise`, `cost_per_rupee_recovered`,
`closed_uneconomic_count`/`_paise`, `processing_failure_count`, the
`ClassificationAccuracy` block including the confusion map, the baseline
comparison block, `AuditEntry.hops[]`, `trail_complete`, and the
`VerifyInvariants` summary.

**Definition of done**: every endpoint the frontend calls appears in the doc
with an exact request and response shape; every field name matches what the
Gateway will actually emit; the enum wire spelling is stated once and
referenced everywhere; and `web/AGENTS.md` still truthfully says the doc is
the only thing a frontend agent needs to read. All met, see `docs/API_GATEWAY.md`
(now marked FROZEN) and the resolutions below.

**Resolutions, one per gap above**:

1. Added `GET /v1/batches`, newest first, backed by a new `ListBatches` RPC.
   Put it on **Ingestion, not Reporting**: Ingestion already owns the
   `batch` table and already has a live Postgres connection, Reporting is
   still a stub, gating this route behind the largest unbuilt unit in the
   phase would be a mistake caught in review. While unbacked, the primary
   generate-and-watch flow doesn't need it at all (`submitBatch` already
   returns the new `batch_id` directly); it's only the "browse an
   already-seeded batch" flow that has to wait, and the doc says to show
   that explicitly (a disabled control, not a silent permanent loader).
2. Every money field in the doc now ends in `_paise` and matches
   `reporting.proto`'s own names exactly (`at_risk_paise`,
   `recovered_paise`, `net_recovered_paise`, etc).
3. Picked the full proto constant string (`"RECORD_STATE_RETRY_SCHEDULED"`,
   not `"RetryScheduled"`) for every enum, over hand-writing a second
   spelling. Reasoning: the wire value then greps straight back to
   `common.proto` with no translation table on either side to drift out of
   sync. Stated once in the doc's new "Closed vocabularies" section rather
   than repeated per endpoint.
4. That same "Closed vocabularies" section lists every member of every enum
   (all 7 `RootCauseBucket` values included), so the frontend's lookup maps
   can be exhaustive over the real vocabulary from the start rather than
   discovering members at runtime. `failure_code` is called out explicitly
   as an open string, not a closed enum, so it gets a fallback, never an
   exhaustive table.
5. Picked the WebSocket subprotocol (what the frontend already sends) over
   a query parameter, specifically because a query parameter would put the
   API key in server access logs and browser history by default and a
   subprotocol does not. The Gateway side needs to switch from checking
   `X-API-Key` to checking the negotiated subprotocol on this one route.
6. `POST /v1/batches` now accepts either `records` (explicit, unchanged) or
   `count` (for the demo generate button), with the ground-truth boundary
   stated explicitly: a `count`-submitted batch never gets a `GROUND_TRUTH`
   row (only `scripts/batchgen` may write that table), so its report has no
   `accuracy` or `baseline_comparison` block, same as real production
   traffic. This is not a small addition, `scripts/batchgen`'s generation
   logic is currently `package main` and not importable, so it needs
   extracting into a shared package first. **And it is worth flagging louder
   than a spec footnote**: the dashboard's own generate button already calls
   this exact form (`web/src/lib/api.ts`'s `submitBatch(80)`), so pressing
   the most obvious button on screen today would, once this route is
   backed, produce a batch with neither of this phase's two headline
   numbers. A demo run that needs the accuracy story should seed with
   `scripts/batchgen` ahead of time and select that batch via the new
   `GET /v1/batches`, rather than pressing generate, and Unit H/F1 should
   consider relabelling the button so nobody presses it by reflex mid-demo.

**A review pass on this unit's own PR caught two real defects, corrected
before merge**: the doc had made three claims about JSON zero-value
handling (`recovered_delta_paise` always present, `rationale` always
present, `from_state` sometimes absent) that cannot all be true under one
consistent marshaling rule, and never stated which rule applied; fixed by
adding an explicit Wire convention (hand-written structs, no `omitempty`
anywhere, matching the one live route that had drifted from this,
`submitBatchResponse.Rejected`, now fixed in the same PR). Separately, the
claim that `from_state` is ever absent was simply wrong:
`audit_entry.from_state` is `NOT NULL`, and every record's first transition
is `RECORD_STATE_NEW -> RECORD_STATE_SCORING`, never an unspecified state
(`docs/INCIDENTS.md` 2026-08-23 already documents that nothing in the
system writes that). A frontend agent building the "first entry has no
`from_state`" branch the old draft implied would have shipped dead code.

Two follow-on items were surfaced but deliberately left for the proto PR that
actually implements them, since a contract-freeze pass isn't the place to
touch `.proto` files: `AuditEntry` needs `ev_score_at_decision` and
`p_recovery_at_decision` added before Unit L's provenance UI can show them
(the data already exists on `INTERVENTION_ATTEMPT`, just not surfaced yet),
and `SubmitBatchRequest` needs the `count` field Ingestion will act on.

## The frontend track, in order

All of this lives in `web/**` and is the dedicated frontend agent's work.
None of it blocks or is blocked by backend units after Unit O, because the
contract is frozen and mock mode means the UI can be built and verified
before the routes exist.

**F1. Rebuild against the frozen contract (was Unit H).** `web/` is a Phase 0
scaffold, deliberately: it was built early against the written contract so UI
work would not wait on the backend. It is not the final UI and should not be
treated as precious. Update `types.ts` and `api.ts` to the frozen contract,
make every lookup map in `format.ts` total over the real vocabularies, fix
the submit body and the WebSocket auth, and update `mockEngine.ts` to emit
the same shapes so mock mode keeps working.

**F2. Add error handling, which currently does not exist anywhere.** There is
no `.catch` on any call, no error boundary, no empty state and no "batch not
found". `loadBatchData()` runs inside a 2 second `setInterval` with no
try/catch, so a live backend returning a 404 produces an unhandled rejection
every two seconds behind a permanently blank page. This is the difference
between a demo that degrades visibly and one that dies silently on stage.

**F3. Build the panels for capabilities the backend already has (Unit L's UI
half).** Provider hop chips in the drawer, net recovered and cost per rupee
tiles, uneconomic-closed as its own tile distinct from escalated, the
classification confusion matrix, a live `VerifyInvariants` "0 violations
across N records" tile, a `trail_complete` badge, and the Unit K baseline
comparison. Each is small; collectively they are what makes the system's
actual sophistication visible. See Unit L for what each one is and why it
matters.

**F4. Demo polish.** Whatever the rehearsal (Phase 8) shows is confusing,
plus making sure the drill-down record chosen in advance looks good.

**Note for whoever writes the frontend agent's prompt**: point it at
`web/AGENTS.md` and `docs/API_GATEWAY.md` and stop there. It does not need
`docs/ARCHITECTURE.md`, and telling it about Kafka, gRPC or the state machine
would be actively unhelpful, since none of that is reachable from where it
works. That constraint has been the arrangement since Phase 0 and it is what
makes this track cleanly separable.

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

## Unit B: Synthetic batch generator

**Status**: merged.
**Depends on**: nothing.

**What it is**: `scripts/batchgen`, a CLI that seeds a batch of revenue-
at-risk records straight into `BATCH`/`RECORD`/`GROUND_TRUTH`, bypassing
Ingestion entirely (Ingestion's proto has no field for the hidden answer
key, by design, and only this tool may ever write `GROUND_TRUTH`,
`docs/ARCHITECTURE.md` §6), then publishes each one to `raw.events` so
the real pipeline (Classifier, Decision Engine, Executor) picks it up
exactly as if a real webhook had arrived. Distinct from Phase 6's
`scripts/loadgen`, which submits through the real HTTP API for throughput
testing and can never carry ground truth.

**Design**: `scripts/batchgen/profile.go` holds all the pure generation
logic (no I/O, unit-testable). `bucketProfiles` is a hidden recovery
model per root-cause bucket, seeded from `docs/ARCHITECTURE.md` §6's own
worked examples (`TRANSIENT_BANK` 0.80, `HARD_DECLINE` 0.15) and
extrapolated on the same logic for the rest, deliberately **not** derived
from the rules engine's own action table (`services/classifier/internal/
rules/actions.go`): that table says what the agent decides to do, this
says how reality actually responds, and collapsing them into one table
would make classification accuracy tautological instead of a real
measurement. A per-record `unrecoverableChance` (6%) models the "this one
is genuinely unrecoverable" case regardless of bucket, and a
`misleadingCodeChance` (12%) deliberately gives some records a hidden
`true_bucket` that diverges from what the failure code's naive
rule-table lookup would say, within the same record-type family — without
this, ground truth would always agree with the rules engine by
construction and the accuracy metric Reporting will compute (Unit F)
would be meaningless. A shared instrument-ref pool gives roughly 30% of
PAYMENT/MANDATE records a repeated `instrument_ref`, so Classifier's
`instrument_history` feature (Phase 3 Unit F) has real, varied data to
reason about in a demo instead of every record looking isolated.

**Files**:
- `scripts/batchgen/profile.go` (new): `bucketProfiles`, the code pools
  per record type, `generateRecord` (the pure entry point), instrument-ref
  pooling, and a log-uniform amount distribution (~Rs.50 to Rs.75,000,
  skewed toward smaller common transaction sizes).
- `scripts/batchgen/profile_test.go` (new, untagged): every generated
  record satisfies the schema's own CHECK constraints; a `CHECKOUT`/
  `INVOICE` record's hidden bucket never leaves its product family;
  divergence from the "obvious" bucket happens at roughly the configured
  rate, not never and not always; a fixed seed reproduces byte-identical
  output; instrument-ref sharing only applies to PAYMENT/MANDATE and
  actually repeats across records, not just draws unique values.
- `scripts/batchgen/main.go` (new): CLI (`-dsn`, `-brokers`, `-topic`,
  `-count`, `-source`, `-seed`, mirroring `scripts/migrate`'s flag
  conventions), the Postgres writes, and a hand-kept mirror of
  `services/ingestion/internal/server.RawEvent`'s wire shape (there is no
  proto for `raw.events`, `docs/ARCHITECTURE.md` §9 applies to gRPC
  contracts, not this internal topic).

**Verification**: `go build/vet/test ./...`, `gofmt -l .` clean.
Adversarially broke `pickDivergentBucket` (let it pick any bucket
regardless of record-type family) and confirmed the family test failed
with a concrete cross-family example, then reverted. Live-verified against
a freshly reset stack (`make down-clean`, `make up`, migrate, all six
services): `go run ./scripts/batchgen -count 40 -seed 7` produced a batch
whose `record`/`ground_truth` row counts matched exactly, was fully
consumed by Decision Engine's real consumer group (confirmed via `kafka-
consumer-groups.sh --describe`, lag 0 across all 12 partitions), and
reached a fully realistic final-state distribution with no manual
intervention: 28 nudges parked awaiting delayed outcomes, 6 recovered via
retry, 3 correctly escalated (`RISK_HOLD`), 3 retries still in flight.

---

# What the rubric changed (2026-08-29)

The Track 03 text and the general evaluation criteria were located and
recorded verbatim in `PRD.md` §0. Three things follow that were not obvious
before, and Units I to N exist because of them.

**1. There is exactly one hole, and it is Unit F.** Scored clause by clause,
this system already does "detects revenue at risk", "determines the right
intervention", "bounded recovery workflow", "stopping rules" and "an audit
trail". The clause it cannot currently demonstrate is **"measured money
recovered across a batch"**, because `services/reporting/` is a 41 line stub.
Every `PRD.md` §9 headline metric is unimplemented. That reframes Unit F from
"one of eight units" to "the deliverable", and everything else to supporting
evidence.

**2. Adding AI surface is a risk, not a hedge.** "AI Judgment: whether AI
tools, LLMs, or agents were applied appropriately **instead of forcing
unnecessary tech stacks**" is a scored criterion. So proposals to add a
natural-language merchant copilot, an LLM planner competing with the EV
scorer, or a cross-batch learning loop are not neutral additions with upside
only. They add model surface without adding judgment, and the scorer is a
better answer to "how does this decide" than a planner would be. Deliberately
not built. This also raises the stakes on Unit E: with `ComposeNudge`
unbuilt, the *only* LLM use in the system is classification, which
`DECISIONS.md` (2026-08-22) already concedes the rules engine does nearly as
well. Generation is the honest justification for having a model at all.

**3. The artefact is defended at a panel, not only demoed.** Shortlisted
builders go straight to a technical interview. That makes `DECISIONS.md` and
`INCIDENTS.md` first-class deliverables rather than internal hygiene, and it
makes Unit N (stale claims in checked-in files) worth an hour: right now
`configs/intervention_costs.yaml` accuses this codebase of computing its
headline metric against the wrong cost model, and that accusation is false.

## Unit C: World Simulator (real)

**Status**: merged. **Depends on**: A (its `ReportDelayedOutcome` RPC is
the callback target). **Rough size**: a full day, matching the estimate
(the largest single unit in the phase: a new Postgres+Redis+gRPC-client
service from a 41-line stub).

**What it is**: `docs/ARCHITECTURE.md` section 6 in full: `SimulateOutcome`
rolls a record's hidden `GROUND_TRUTH` profile against the action taken,
answering a retry immediately and a nudge as `PENDING` with the real
answer delivered later via a Redis-backed delayed-outcome queue and a
background poller calling `DecisionEngine.ReportDelayedOutcome`. This
unparks the ~70% of records that previously sat in `NUDGED` forever
(Phase 1's stub never resolved a nudge) and is what makes the accuracy and
recovery numbers Unit F reports actual measurements rather than a scripted
happy path.

**Design**:
- Mirrors `services/audit/`'s file split (`docs/ENGINEERING.md` section
  14): `store.go` (the one read-only query, joining `record` for the
  original `failure_code` and `ground_truth` for the hidden profile),
  `bucket.go` and `outcome.go` (pure roll logic, no I/O), `queue.go` (the
  Redis sorted set), `poller.go` (the background delivery loop), `server.go`
  (gRPC orchestration only).
- **`isCorrectAction` needs its own copy of the classifier's bucket→action
  table**, since cross-service code is gRPC only and
  `services/classifier/internal/rules` is private to that service. This is
  a deliberate, small duplication, not an oversight, same precedent as
  `scripts/batchgen/profile.go`'s `ObviousBucket` table: both carry a "must
  stay in sync with the classifier's table" comment.
- **A failed retry reuses the record's own original `failure_code`**
  (`record.failure_code`, read once at ingestion, never updated) rather
  than inventing a new per-attempt failure-code model `GROUND_TRUTH` does
  not carry. Simple and defensible: "the same underlying reason struck
  again."
- **Each `SimulateOutcome` call re-rolls independently**; nothing
  remembers a prior attempt's result. Matches the plain-English model in
  section 6 ("resolves on retry with 80% probability") rather than
  inventing a decay curve the data does not encode.
- **A nudge with a zero `response_delay_seconds` resolves immediately**
  instead of scheduling a zero-delay Redis entry pointlessly, which is the
  "usually `PENDING`" the proto comment allows for.
- **The Redis member format extends section 6's example
  (`record_id:attempt_number:outcome`) with a fourth field,
  `failure_code`**: `ReportDelayedOutcomeRequest` accepts one, and
  `scheduler.go`'s `ResumeNudge` already documents it as informational
  (logged, never decision-driving), so carrying it through costs nothing
  and keeps a failed nudge's audit trail as informative as a failed
  retry's.
- **`queue.due()` is not perfectly atomic** (`ZRANGEBYSCORE` then one
  `ZREM` per member, not a single Lua script): World Simulator runs its
  poller as one goroutine in one instance, so there is no concurrent
  poller to race against, and `ReportDelayedOutcome` is already
  at-least-once and idempotent-safe downstream (a duplicate is discarded,
  not an error). Revisit if this service is ever run with more than one
  replica.
- **`deliver` retries a failed `ReportDelayedOutcome` call up to 3 times**
  (`maxDeliverAttempts`, mirroring `scheduler.go`'s `executeWithRetry`
  exactly) before logging the outcome as lost. The entry is already
  removed from Redis by the time `due()` returns it, so an exhausted
  retry is a real, visible loss (logged at `Error`), not a silent one;
  acceptable for a DEMO ONLY component that a real deployment deletes
  entirely (section 3b).
- Every wall-clock delay goes through `config.Common.Scale`
  (`DEMO_TIME_SCALE`), same as every other service. The poller's own tick
  interval is deliberately **not** scaled (`WORLDSIM_POLL_INTERVAL`,
  default 1s): scaling an already-small interval down further would make
  polling impractically frequent, and a small fixed interval already
  catches a compressed `resolves_at` promptly, mirroring
  `services/decision-engine`'s own `SchedulerConfig.PollInterval`.
- New dependency: `github.com/redis/go-redis/v9`. This is the first real
  Redis client in the codebase (every other mention of Redis elsewhere is
  a deliberate "not Redis" comment); no `internal/platform/redis` wrapper
  was added since World Simulator is the only consumer today, consistent
  with not building shared infrastructure ahead of a second need.
- Wired into local dev and observability the same way every service is:
  `make run-world-simulator` (fixed ports 9202/9203), a `world-simulator`
  scrape target in `deploy/observability/prometheus.yml.tmpl`, and
  `DECISION_ENGINE_ADDR`/`WORLDSIM_POLL_INTERVAL` added to `.env.example`.
  Verified live, not just by inspection: ran the service against the real
  docker-compose stack, made a real `SimulateOutcome` call, confirmed the
  Redis sorted set held the correctly-formatted scheduled entry, and
  confirmed `requests_total`/`request_duration_seconds` appeared on
  `/metrics` with the right method and status labels.

**Definition of done additions**: 22 tests against real Postgres and real
Redis (`go test -tags=integration`): the roll logic's correct-vs-wrong
action selection, the mirror table, immediate retry success/failure
(including that the response's `failure_code` is empty on success),
wrong-action probability actually applying, nudge scheduling and its
exact Redis payload, the zero-delay-resolves-immediately edge,
`DEMO_TIME_SCALE` actually scaling `resolves_at`, not-found/invalid-argument
edges, the queue's schedule/due/remove cycle including a malformed
member, and the poller's delivery, retry-then-succeed, and
give-up-after-max-attempts paths. Adversarially verified five times:
inverted which probability applies to the correct vs. wrong action
(caught by three separate tests); removed the zero-delay guard, which
initially did **not** get caught because the test exercising it used
`ESCALATE` instead of a real nudge action type and so never touched
`isNudge` at all — fixed the test itself to use `NUDGE_METHOD_UPDATE`,
re-broke the code, confirmed the corrected test now failed, then
reverted; swapped which branch of the immediate response carries
`failure_code`, caught by the retry-success test. All reverted, confirmed
green.

**Found and flagged, not fixed here**: `demo/notification-simulator` is
still a 41-line stub, same as World Simulator was before this unit. Unit
D ("Executor: wire real World/Notification Simulator clients") may
therefore need to implement the Notification Simulator for real, not only
wire a client to something already built — its own job is much smaller
(log what would have been sent; nothing is really delivered, per its
proto's own comment) but it is not yet done, and D's scope should account
for that rather than assume it is.

## Unit D: Executor wired to real World/Notification Simulator clients

**Status**: merged. **Depends on**: C. **Rough size**: a full day (larger
than "swap two stubs": also built the Notification Simulator for real,
and restructured the nudge path).

**What it is**: `services/executor/internal/ports/stub.go`'s two
Phase 1 in-process stubs replaced with real gRPC clients dialling
`demo/world-simulator` (Unit C) and `demo/notification-simulator`, with no
change to `internal/server` (`docs/ARCHITECTURE.md` section 3b's whole
point: the Executor depends on two small interfaces, never on either
simulator by name).

**A larger discovery while implementing, not scope creep**: closing this
unit properly required two things beyond "wire two clients":

1. **`demo/notification-simulator` was still a 41-line stub**, same as
   World Simulator was before Unit C. Built for real: logs what would have
   been sent and prices it by channel, matching `StubNotification`'s
   existing behaviour exactly, now over a real gRPC boundary. No
   Postgres, no Redis: this service holds no state and answers from its
   own checked-in pricing table alone. Its channel prices are a small,
   deliberate duplication of `services/executor/internal/ports/cost.go`'s
   `smsCostPaise`/`whatsappCostPaise` (cannot import that private package;
   same precedent as `scripts/batchgen`'s `ObviousBucket` and
   `demo/world-simulator`'s `correctActionFor`).
2. **`route.go`'s `nudge()` never called `RecoveryActionPort` at all**,
   only `NotificationPort`. Sending and reacting are different questions:
   `SimulateSend` answers "did the channel deliver this", but only
   something holding a recoverability model (World Simulator, backed by
   `GROUND_TRUTH`) can answer "does this customer, specifically, pay
   after being asked". Without also calling `SimulateOutcome` for a
   nudge, Unit C's entire delayed-outcome mechanism is dead code in
   production: nothing would ever call
   `DecisionEngine.ReportDelayedOutcome` for a nudge, and every nudge
   would keep sitting in `NUDGED` forever, the exact problem Unit C's own
   stated goal is to fix. `nudge()` now sends the message, and only if it
   was actually delivered, asks the recovery port whether/when the
   customer is expected to react, using its `resolves_at` and `Outcome`
   directly. A zero-delay profile resolves immediately rather than being
   forced into `PENDING`, mirroring `retry()`'s existing
   immediate/deferred split for symmetry.

**Design**:
- `services/executor/internal/ports/grpc.go` (new): `WorldSimRecovery` and
  `NotificationSimAdapter`, thin translators from the generated gRPC
  client interfaces to `RecoveryActionPort`/`NotificationPort`. `CostPaise`
  for a retry is injected here (`retryCostPaise`), not read from World
  Simulator's response: the proto carries no cost field by design (cost is
  a checked-in constant, not something "reality" reports back). A nudge's
  recovery-port call costs `0` on this port; its real cost is the
  notification's, reported separately.
- `Router` lost its `clock.Clock` and `nudgeResolveDelay` fields entirely:
  once `nudge()` sources `resolves_at` from the recovery port, nothing in
  `Router` needs a clock any more. Removed rather than left unused
  (top-level instructions: no dead fields).
- `NUDGE_RESOLVE_DELAY` is retired, not deleted, in `.env.example`: kept,
  unread, so an existing local `.env` does not break, matching the
  project's own precedent for retired config (see the LLM breaker
  section there).

**Definition of done additions**: `route_test.go` rewritten for the new
nudge behaviour (recovery port called after a successful send, not called
after a failed one, a recovery-port error surfaces, an immediate answer
passes through unforced) plus new coverage for both real adapters
(`grpc_test.go`, faking the generated gRPC client interfaces directly, the
same pattern `services/decision-engine`'s tests use to fake the Executor
client) and the new Notification Simulator (`server_test.go`). One
pre-existing integration test (`TestExecuteRedeliveredPendingNudgeReplaysPromptly`)
needed updating: it used a zero-value `countingRecovery{}`, which used to
be harmless for a nudge (the recovery port was never called) and became a
nil `resolves_at` once it was. Adversarially verified three times: forced
every nudge outcome to `Immediate` (never `PENDING`), caught; called the
recovery port unconditionally rather than only after a successful send,
caught by two tests; broke the retry-only cost injection to apply to every
action, caught. All reverted, confirmed green. **Verified live against
the real stack**: ran all three services (executor, world-simulator,
notification-simulator) together, executed a real `NUDGE_METHOD_UPDATE`
and a real `RETRY` through Executor's `Execute` RPC, confirmed the
correct channel/cost/outcome/`resolves_at` at every hop, confirmed the
Redis delayed-outcome entry was scheduled correctly, and confirmed
`requests_total` appeared on all three services' `/metrics` with the
right labels.

**CI caught something local verification did not: the whole `test/e2e`
suite broke.** Every e2e test failed at startup, because the harness
(`test/e2e/harness_test.go`) never learned about the two new required
Executor settings and never started either simulator. Fixed properly, not
worked around:
- `startStackWithEnv` now builds and starts `demo/world-simulator` and
  `demo/notification-simulator` too, wires `DECISION_ENGINE_ADDR` into
  the former and `WORLD_SIMULATOR_ADDR`/`NOTIFICATION_SIMULATOR_ADDR`
  into Executor's env, and waits on both new ports before any test runs.
  `buildBinary` gained a `pkgDir` parameter (`"services"` vs `"demo"`,
  docs/ARCHITECTURE.md section 2a's repo layout) since it had hardcoded
  `services/` for every prior caller.
- A deeper problem underneath the startup failure: every e2e test seeds
  or submits its own record directly, and none of them ever wrote a
  `GROUND_TRUTH` row, because nothing before this unit ever needed one.
  World Simulator requires one to answer at all. Seeded one per record
  in every affected test (`walking_skeleton_test.go`, `smoke_test.go`,
  `idempotency_test.go`, `batch_invariants_test.go`,
  `crash_safety_test.go`, `fallback_test.go`, `rerun_safety_test.go`),
  with values chosen to reproduce each test's own pre-existing assumption
  deterministically (`recovery_probability=1.0` where the old stub's
  script meant "always succeeds"; `0.0` where Unit H's fabricated history
  means "this one real attempt must fail"; a large sentinel
  `response_delay_seconds` for the two NUDGE smoke cases, since World
  Simulator always answers `PENDING` for a nudge with a positive scaled
  delay regardless of the roll, docs/ARCHITECTURE.md section 6).
- **A real race, caught by running the full suite with `-race` locally
  before pushing again, not by CI.** Records submitted through the real
  HTTP path get their id back only after submission, so ground truth for
  them can only be seeded afterward, racing the scheduler's first claim.
  Under the default `retryDelay="1s"` (which, at `DEMO_TIME_SCALE=300000`,
  collapses to microseconds, leaving only the ~300ms scheduler poll
  interval as real buffer), `-race`'s overhead was enough to lose that
  race once: a `BANK_TIMEOUT` record got dead-lettered because World
  Simulator answered `NotFound` before the seed landed. Fixed the same
  way Units H/K already establish for their own seeding: every test using
  the tight default now passes `"3000000s"` instead of `"1s"` (~10s real),
  buying comfortable margin without changing any assertion. Confirmed
  by two clean `-race` runs of the whole suite afterward, including one
  via the exact command CI runs (`make test-integration`'s
  `go test -race -count=1 -tags='integration e2e' ./...`).
- `idempotency_test.go` did not need either fix: its ground truth is
  seeded before the record is ever published, race-free by construction,
  and its second subtest calls Executor's gRPC port directly, bypassing
  the scheduler entirely.

## Unit I: Razorpay's published error codes as the failure vocabulary

**Status**: merged. **Depends on**: nothing. **Rough size**: 2 hours, as
estimated.

**What it is**: `services/classifier/internal/rules/buckets.go`'s
`failureCodeToBucket` table is invented. `BANK_TIMEOUT`, `RAIL_CONGESTION`,
`ISSUER_UNAVAILABLE` and `EXPIRED_INSTRUMENT` are plausible and are not
Razorpay's. Razorpay publishes the real vocabulary at
`https://razorpay.com/docs/errors/payments/list/`, with an error reason and a
`source` (customer, business, gateway, razorpay) for each.

**Why it is worth doing before the demo data is generated**: the judges work
on this taxonomy. A classifier whose input vocabulary is the platform's own
published error list, cited, is a materially different claim from one that
invented a plausible list. It also lets the table carry a `[SOURCED]`
provenance tag, matching the discipline already used in
`configs/intervention_costs.yaml` and `configs/recovery_priors.yaml`.

**It also unblocks Unit H.** `web/src/lib/format.ts`'s `FAILURE_CODE_LABELS`
is keyed lowercase (`bank_timeout`) while real codes arrive uppercase, so
every failure-code cell in the drawer renders blank against live data. The
vocabulary has to be settled during Unit H regardless. Do it once, properly.

**Design**:
- Keep every existing key as an alias. `normalizeFailureCode` already
  uppercases and collapses separators, so Razorpay's `insufficient_funds`
  already resolves to the existing `INSUFFICIENT_FUNDS` key for free. Several
  codes map with no work.
- Add the real codes, at minimum: `bank_not_available`,
  `bank_technical_error`, `issuer_technical_error`, `gateway_technical_error`,
  `payment_declined_due_to_high_traffic`, `psp_not_available`,
  `upi_app_technical_error` (all → `TRANSIENT_BANK`);
  `insufficient_funds`, `transaction_limit_exceeded`,
  `transaction_daily_limit_exceeded`, `credit_limit_exceeded` (→
  `INSUFFICIENT_FUNDS`); `card_expired`, `card_declined`,
  `debit_instrument_blocked`, `card_number_invalid`, `bank_account_invalid`,
  `invalid_vpa` (→ `HARD_DECLINE`); `authentication_failed`, `incorrect_otp`,
  `otp_expired`, `otp_attempts_exceeded`, `mandate_creation_failed`,
  `mandate_creation_declined`, `reqauth_mandate_not_acknowledged` (→
  `USER_ACTION_NEEDED`); `payment_risk_check_failed` (→ `RISK_HOLD`);
  `payment_cancelled`, `payment_session_expired` (→ `ABANDONMENT`).
- **Do not map the indeterminate codes to `TRANSIENT_BANK`.** This is the one
  behavioural change in the unit, not a rename. `payment_timed_out`,
  `payment_pending`, `verification_failed` and
  `invalid_response_from_gateway` all mean *we do not know whether the bank
  succeeded*, which is not the same as "it failed, retry soon". Retrying a
  payment that actually succeeded is a duplicate charge. The current table
  maps `GATEWAY_TIMEOUT`/`TIMEOUT` to `TRANSIENT_BANK`, i.e. straight into
  that trap. Until a reconciliation path exists (parked, `BACKLOG.md`), route
  these to `USER_ACTION_NEEDED` or escalation rather than an automatic retry,
  and say why in the rationale.
- Update `scripts/batchgen/profile.go`'s code pools to draw from the real
  codes, so the demo batch looks like real Razorpay traffic.
- Carry Razorpay's `source` field into the rationale where it is known
  (`source: gateway` on `bank_not_available` means systemic, not the
  customer's fault, so retrying is right and contacting them is not). One
  line, and it reads as genuine domain awareness.

**Definition of done additions**: the existing table test iterates the map, so
extend it to assert every new code resolves and that the four indeterminate
codes specifically do *not* resolve to a bucket whose policy is an automatic
retry. Met: `TestIndeterminateCodesNeverAutoRetry` covers all six codes that
must not auto-retry (the four new ones plus the two reclassified existing
ones, `GATEWAY_TIMEOUT`/`TIMEOUT`), `TestIndeterminateRationaleDoesNotClaimRiskReview`
checks the override wording, `TestNewRazorpayCodesResolve` and
`TestLowercaseRazorpayCodeResolvesViaNormalization` cover a representative
sample of the new table entries. Adversarially verified: temporarily
reverted `GATEWAY_TIMEOUT` to `TRANSIENT_BANK` and confirmed
`TestIndeterminateCodesNeverAutoRetry` failed with the exact expected
message before reverting the break.

**One scope decision made while implementing**: the design bullet above
about carrying Razorpay's `source` field into the rationale text at runtime
was scaled back to source-tagged comments in the table itself
(`// [SOURCED] Razorpay, source: gateway/bank...`). Surfacing `source`
dynamically per code would need restructuring the map from
`map[string]RootCauseBucket` to a richer per-code struct threaded through
`bucketForCode`/`composeRationale`, a larger change for a "reads as
domain-aware" nicety rather than a correctness requirement. The comments
still make the domain awareness visible to anyone reading the code or
reviewing the table, which was the actual goal.

## Unit J: compliance guardrails, cited

**Status**: merged. **Depends on**: nothing. **Rough size**: 2 to 3 hours, as
estimated.

**What it is**: the track asks for "compliant escalation" and this system used
the word without naming a rule. `PRD.md` §11a now specifies two real ones and
their limits. This unit enforces them.

**Why it is cheap**: the shape already exists twice over. The guardrail layer
already computes a permitted action set inside the same transaction as the
state change it gates, and `services/decision-engine/internal/engine/
schedule.go`'s `retryDueAt` already does exactly this kind of calendar
arithmetic for the `INSUFFICIENT_FUNDS` salary window. Both new rules are the
same pattern.

**Design**:
- **Contact-hour window (TRAI TCCCPR).** A new pure function alongside
  `retryDueAt`, same signature style (takes `now`, returns a time, no I/O, no
  Clock): given a proposed `due_at` for a *customer-contacting* action
  (`NUDGE_METHOD_UPDATE`, `NUDGE_REMINDER`), return it unchanged if it falls
  inside 10:00 to 21:00 IST, otherwise the next window open. `RETRY` is not a
  customer contact and is unaffected. Scaled by `DEMO_TIME_SCALE` like every
  other timing knob, so a demo does not stall for nine hours.
- **Mandate pre-debit lead time (RBI).** A floor, not an offset: a `RETRY` on
  a `RECORD_TYPE_MANDATE` record cannot be scheduled sooner than
  `RETRY_MANDATE_LEAD_TIME` (default 24h) from now. The salary-window
  calculation may push it later; nothing may pull it earlier. Note the
  interaction: for `INSUFFICIENT_FUNDS` mandates both rules apply and the
  later of the two wins.
- Both produce an audit `reason` naming the rule, so the trail shows
  *deferred to 10:00 IST per TRAI contact-hour window* rather than an
  unexplained delay.
- Config validated at startup like every other guardrail
  (`GuardrailConfig.Validate`), because `INCIDENTS.md` 2026-08-24 is the
  entry about a zero-valued safety config silently escalating every record.

**Watch out for**: the timezone. IST is fixed at UTC+5:30 with no DST, so
`time.FixedZone` is correct and simpler than a tzdata lookup, and a distroless
runtime image may not carry tzdata anyway. Write the test against fixed
instants, not `time.Now()`.

**Definition of done additions**: a nudge due at 03:00 IST is deferred to
10:00 and not dropped; a nudge due at 14:00 is untouched; a mandate retry
requested 5 minutes out is floored to 24 hours; a mandate retry whose salary
window already lands 20 days out is *not* pulled back to 24 hours. All four
covered by `TestContactHourWindow`, `TestDueAtForDefersOutsideContactHours`,
`TestRetryDueAtFloorsMandateRetriesToTheLeadTime`, and
`TestRetryDueAtMandateFloorNeverPullsALaterDateEarlier`. Adversarially
verified: broke the contact-hour close boundary to an off-by-one (`<=` for
`<`) and confirmed the boundary test caught it; separately made the mandate
floor apply unconditionally and confirmed the non-mandate guard test caught
it; both reverted and confirmed green.

**One design bullet scoped out, deliberately, not silently**: "the audit
reason names the rule" (e.g. *deferred to 10:00 IST per TRAI contact-hour
window*) is not yet wired in. The two timing functions
(`contactHourWindow`, `mandateLeadTimeFloor`) are pure and correctly change
`due_at`, which is itself provable and tested, but the audit `reason`
string written alongside a transition still describes the *scoring*
decision (e.g. "best expected value: ..."), not a timing adjustment made
after that decision. Wiring a "why this due_at, specifically" citation
through to the audit entry touches the same `scoreAndRoute` ->
`scheduleNew`/`recordRescore` plumbing Unit M is about to rework to persist
the EV ranking, so it is picked up there rather than threaded twice. Tracked
in `docs/BACKLOG.md` if Unit M does not end up covering it.

## Unit F: Reporting Service

**Status**: in progress. `GetBatchReport` and `ListBatchRecords` merged;
`StreamBatchUpdates` deferred. **Depends on**: nothing strictly, more
useful once B/C produce real data. **Rough size**: half a day for the
unary half, done; streaming is its own, more expensive unit.

**What it is**: the one clause the rubric audit found completely
unimplemented ("measured money recovered across a batch"). Mirrors
`services/audit/` almost exactly, since it is the closest existing
service in shape: pure gRPC reader, no Kafka in this pass, Postgres only,
same `New(pool)`/`store` split (`docs/ENGINEERING.md` section 14).

**Design**:
- `store.go` holds every SQL statement, `server.go` is orchestration only.
  `loadHeadline` computes the top-level counts and sums in one query
  (`record` LEFT JOIN `record_state`); `interventionSpend` is a second,
  separate query against `intervention_attempt` rather than folded into
  the same join, because joining a one-to-many table into the headline
  aggregate would multiply `record` rows per attempt and corrupt every
  other count and sum in that query.
- `by_root_cause` and `by_intervention` are each their own `GROUP BY`
  query. A record with no `record_state` row yet (not yet classified) has
  no bucket and is excluded from `by_root_cause`, not counted as an
  unknown bucket: it is not yet evidence for or against any bucket's
  recovery rate.
- `by_intervention`'s `recovered_paise` attributes a record's full amount
  to whichever attempt actually succeeded for it, by joining
  `intervention_attempt` to `record` filtered on
  `outcome = 'OUTCOME_SUCCESS'`. This cannot double-count: a record
  recovers via exactly one successful attempt (success terminates it,
  `ARCHITECTURE.md` section 7), so there is exactly one matching row per
  recovered record.
- **Accuracy is nil, not zeroed, when the batch has no `GROUND_TRUTH`.**
  `docs/API_GATEWAY.md`'s own contract is explicit: "a missing key means
  no answer key exists, distinct from a real zero." `classificationAccuracy`
  returns `nil` when the confusion-count query returns zero rows (real
  traffic, no answer key), never a populated struct with zeroed fields.
- `ListBatchRecords`' `page_token` is a stringified integer offset.
  `docs/API_GATEWAY.md` only requires it be opaque, not that it encode
  anything cleverer, and batches in this system are demo-scale (tens to
  low hundreds of records); a real keyset pagination scheme would be
  solving a problem this system does not have yet.
- `cost_per_rupee_recovered` and every bucket/intervention rate are
  computed with an explicit zero-denominator guard returning `0`, not
  `+Inf` or `NaN`, since neither round-trips through JSON on the Gateway.

**A real gap found while implementing, not papered over**:
`processing_failure_count` cannot be computed from Postgres at all with
the current schema. A dead-lettered record (`services/decision-engine/
internal/engine/dlq.go`) is published to Kafka's `raw.events.dlq` and left
in whatever `RECORD_STATE` it was claimed into; no table, column, or
audit row marks it as a processing failure specifically, by design
(`docs/PLAN.md`'s DLQ entry: "not written as a `RecordState` value, none
exists for it"). Reporting's own package doc says it reads Postgres only,
never Kafka, as a source of numbers, so there is currently no honest way
to report this figure as anything but `0`. Returns `0` with a code
comment explaining why, and tracked as a real gap in `docs/BACKLOG.md`
rather than silently guessed at. The eventual fix belongs to whoever owns
the Decision Engine's DLQ path (writing a queryable trace at
dead-letter time), not to Reporting alone.

**Definition of done, this pass**: `GetBatchReport` and `ListBatchRecords`
covered by 10 tests against real Postgres (`go test -tags=integration`),
including the headline aggregate math, both `GROUP BY` breakdowns, the
ground-truth accuracy/confusion computation and its deliberate absence
without ground truth, pagination advancing across pages, and the
not-found/invalid-argument edges. Adversarially verified: inverted
`net_recovered_paise`'s subtraction to addition and confirmed the
headline test caught the wrong number; separately removed the
zero-confusion-rows nil guard and confirmed the ground-truth-absence
test caught a populated-but-empty `Accuracy` appearing where it must not.
Both reverted, confirmed green. `docs/PLAN.md`'s checkbox stays unticked
until `StreamBatchUpdates` also lands, per its own Definition of Done.

## Unit K: baseline comparison in Reporting

**Status**: not started. **Depends on**: F. **Rough size**: 3 to 4 hours.

**What it is**: "measured money recovered" is a number. "Measured money
recovered, versus what a conventional policy would have recovered on the same
records" is a result. This is the difference between demonstrating the agent
ran and demonstrating it was worth running.

**Design, and the honest version of it**: compute the counterfactual
**analytically**, not by running the batch twice. `GROUND_TRUTH` already
carries `recovery_probability`, `wrong_action_probability` and
`true_bucket` per record. Reporting is already one of only two services
permitted to read that table, and its accuracy scorer already joins against
it. So in the same scorer, evaluate a fixed naive policy over the same
records: **retry every record up to three times, nudge every record once,
no economics, no guardrails beyond a hard attempt cap.** Report gross
recovered, intervention spend and net recovered for both.

**The expected result is the interesting one**, and it is worth predicting in
advance so the number is not read as a bug: the blind policy will likely
recover *similar gross* while spending several times more, because it pays to
chase `HARD_DECLINE` and `RISK_HOLD` records whose priors are zero for a
reason. That is precisely the argument for EV selection, and it turns
`ClosedUneconomic` from "the agent gave up" into "the agent saved this much",
which is what `PRD.md` §12 beat 3a promises and currently has no number
behind.

**The honesty requirement, non-negotiable**: both figures are evaluated in a
world we authored. The claim is "this policy beats a blind one **under our
modelled world**", not "we recover N% more real money". Say so in the report
payload's own field documentation and on the dashboard tile, not only in this
doc. This project has tagged every assumption in `configs/*.yaml` with
`[SOURCED]`/`[ASSUMPTION]`/`[UNVERIFIED]`; the same standard applies here, and
a panel will respect the caveat far more than it would respect an unqualified
number.

**Useful external anchors, since they let the baseline be checked against
something we did not choose**: Razorpay publishes that automated retry systems
recover "15-20% of failed transactions, adding 3-5 percentage points to
overall payment success rate", and that for subscriptions smart retry tools
recover "up to 57% of initially failed payment attempts". If our modelled
baseline lands wildly outside that range, the model is wrong and that is worth
knowing before a judge finds it. Also worth having ready as a closing line:
"a 5-percentage-point improvement translates to ₹5 lakhs in recovered revenue
for every ₹1 crore in monthly GMV", which converts a rate delta into rupees at
merchant scale, with their citation.

## Unit L: surface what is already stored but invisible

**Status**: not started. **Depends on**: G (routes), H (UI). **Rough size**:
half a day for all of it.

**What it is**: an audit found several capabilities that are fully built,
persisted, and have no route or no UI. Each is a small component in a design
language `web/src/components/` already establishes. Collectively they are the
difference between having built something and showing it.

- **Provider hop chain.** `AuditEntry.hops[]` records every rung actually
  attempted with results from a closed set (`ok`, `error`, `timeout`,
  `rate_limited`, `schema_invalid`, `circuit_open`, `deadline_exhausted`).
  The drawer has no field for it and currently conveys fallback with a single
  string. `common.proto`'s own comment makes the point: `source` alone "reads
  the same whether the primary answered first try or timed out and a failover
  covered for it". This is the graceful-fallback evidence the rubric asks for
  and it is invisible today. Render it as a chip row in the drawer's
  Classification block.
- **Net recovered and cost per rupee.** Both are in `BatchReport` already,
  with `net_recovered_paise` flagged in the proto as "THE headline number".
  `MetricsGrid` shows gross only. Two tiles.
- **Uneconomic closed, as its own tile.** The proto deliberately separates
  `closed_uneconomic_count`/`_paise` from `escalated_count`, because "a human
  should decide" and "we decided this was not worth it" are different
  outcomes. The UI collapses them.
- **The confusion matrix.** `ClassificationAccuracy.confusion` already
  specifies a predicted-to-true map that shows *where* it was wrong. The UI
  shows one scalar percentage.
- **A live invariants tile.** `Audit.VerifyInvariants` is implemented and
  returns `stopping_rule_violations`, `incomplete_audit_trails`,
  `impossible_transitions` and `records_checked`. There is no Gateway route
  and no UI. A green "0 violations across 80 records checked" tile is roughly
  an hour and it is the difference between *claiming* `PRD.md` §9's
  correctness invariants and *measuring* them in front of the judge, by the
  service whose job that is.
- **`trail_complete`** has no badge in the drawer.

**Also in scope, because Unit H trips over them**: the frontend's lookup maps
are exhaustive `Record<>` types over the wrong vocabulary and will render
blanks rather than errors against live data. Three buckets where the proto has
seven; `RecordType` missing `CHECKOUT` and `INVOICE`; `Outcome` spelled
`failed` where the proto says `OUTCOME_FAILURE`; and no decision anywhere
about whether states arrive as `RetryScheduled` or
`RECORD_STATE_RETRY_SCHEDULED`. Settle the wire spelling once, in
`docs/API_GATEWAY.md`, and make the frontend total over it.

**And fix the two live-mode breakages found in the same audit**: the
WebSocket sends the API key as a subprotocol (`new WebSocket(wsUrl,
[API_KEY])`) while the Gateway checks the `X-API-Key` header, so auth fails on
first connect; and `submitBatch` posts `{count: 80}` while the Gateway
requires `{source, records: [...]}`, so the button returns 400 against a real
backend. Neither shows up in mock mode.

## Unit M: persist the EV ranking and the guardrail refusal reasons

**Status**: merged. **Depends on**: nothing. **Rough size**: 3 hours, as
estimated, plus the migration and threading through three persistence call
sites (`scheduleNew`, `recordRescore`, `applyResumedOutcome`, the last one
not originally named here since it is `ResumeNudge`'s own re-scoring path).

**What it is**: "Every money action explainable" is the house standard
(`PRD.md` §0). Today the trail explains the *winner* and nothing else. The
data needed to explain the losers is computed and thrown away, twice:

- `economics.Model.Best` (`services/decision-engine/internal/economics/
  score.go`) loops candidates, keeps the argmax, and returns one `Score`.
  Sub-zero-EV candidates are `continue`d before they are ever compared, so
  they leave no trace at all.
- The guardrail layer separately computes `guardrailVerdict.blocked
  map[ActionType]string`, a per-action *refusal reason*, which is also
  dropped after being used to filter the permitted set.

Together those two are a complete answer to "why this action and not the
others", and both are discarded microseconds after being computed.

**Design, as built**: `economics.Model.ScoreAll` scores every permitted
candidate and returns the full ranking, unfiltered, in the same order as
its input. `Best` is redefined in terms of it (`Best` calls a new
`BestOf(scores)` that `ScoreAll`'s result also feeds), specifically so the
two can never independently disagree about the winner, byte-identically
proven by `TestBestAgreesWithScoreAllsOwnMaximum`. `scoreAndRoute`
(state.go) now returns a fifth value, `DecisionTrace{Candidates, Blocked}`,
built from the same `ScoreAll` call already needed to find the winner (not
a second, wasted computation): `Candidates` is the full ranking, `Blocked`
is the guardrail verdict's own `blocked` map, exposed rather than recomputed.

Threaded through three persistence call sites, all three needing the same
JSON encoding (`store.encodeDecisionTrace`, mirrors `encodedHops`'s
fail-open shape: an encoding failure logs and stores NULL rather than
losing the whole transaction): `scheduleNew` (the New path),
`recordRescore` (a failed attempt's re-entry to Scoring), and
`applyResumedOutcome` (the same re-entry, reached via the delayed-outcome
`ResumeNudge` path instead). One additive column, migration 00006,
`audit_entry.decision_trace JSONB`.

**Attached to exactly one audit row per decision, not every row**:
`scoringPath`/`rescoringPath` always produce two `stateStep`s, and only the
second (`Scoring -> X`) followed an actual comparison; attaching the trace
to both, or to `directPath`'s single escalation-bypass step (`New ->
Escalated`, which never reaches scoring at all), would be either redundant
or a false claim that a comparison happened. Each insert loop checks
`step.From == RECORD_STATE_SCORING` and attaches the trace only there.

**Did not change how the winner is chosen**: `Best`'s selection logic
(strictly-positive EV, first-in-order tie-break) moved to `BestOf` verbatim,
proven behavior-preserving by every pre-existing `economics` test passing
unchanged, plus a dedicated equivalence test. A real Postgres integration
test (`TestHandleMessageSchedulesRetryPersistsDecisionTrace`) confirms the
column round-trips through the actual database: NULL on the `New ->
Scoring` row, populated with every candidate (including the winner, with
positive EV) on the `Scoring -> RetryScheduled` row. Adversarially verified
twice: inverted the `step.From == SCORING` check and confirmed the
integration test caught the trace landing on the wrong row; separately
dropped the last candidate from `ScoreAll`'s loop and confirmed the
coverage test caught the gap. Both reverted, confirmed green.

**Why it was worth it**: a table showing `retry +₹340 / whatsapp +₹120 /
sms −₹15 / none 0 → chose retry`, or `retry blocked: attempt cap reached (3
of 3)`, is a far stronger explainability artefact than any amount of model
rationale prose, because it shows the deterministic part doing the deciding.
That is the answer to "so does the LLM decide how to spend money?", made
visual.

## Unit N: correct three stale claims in checked-in files

**Status**: merged. **Depends on**: nothing. **Rough size**: 1 hour.

Each of these is a checked-in file asserting something about this codebase
that is no longer true. All three were verified against the source on
2026-08-29, and fixed in the same PR that recorded the rubric audit itself,
before this list existed as its own tracked unit. They matter more than
their size because they sit in the files a panel is most likely to read
closely, and each is a self-accusation.

1. **`configs/intervention_costs.yaml`'s `executor_reconciliation` block says
   `agrees_today: false`**, claiming `whatsappCostPaise` is 60 (should be 14)
   and `retryCostPaise` is 200 (should be 25).
   `services/executor/internal/ports/cost.go` actually reads 14 and 25, and
   `cost_reconciliation_test.go` has guarded them since 2026-08-24. As it
   stands, the most judge-legible config file in the repo states that the
   headline "net recovered" metric is computed against a different cost model
   than the one that authorised the spend. It is not. Update the block to
   `agrees_today: true` with the date, and keep the defect list that is still
   genuinely open (the `CHANNEL_EMAIL` default branch, `StubRecovery`
   charging retry cost on both branches, `SimulateOutcomeResponse` carrying no
   cost field).
2. **`configs/recovery_priors.yaml` says the ESCALATE priors are "currently
   mostly unreachable"** because §7's state machine has no
   `Scoring -> Escalated` edge. That edge exists, at
   `services/audit/internal/server/statemachine.go:32`, added 2026-08-27 (see
   `INCIDENTS.md`). Correct the note.
3. **`configs/README.md` does not exist**, and both YAML files reference it
   seven times ("Why these are two files is argued in `configs/README.md`",
   "See configs/README.md, 'The MDR gap'"). Either write it or remove the
   references. Writing it is better: the two things it is cited for (why costs
   and priors are separate files, and the MDR gap in the net-recovered
   formula) are both genuinely worth a paragraph, and the MDR gap in
   particular is a known understatement in the headline metric that is better
   documented than discovered.

Also stale and worth the same pass: **`ARCHITECTURE.md` §10's ERD for
`GROUND_TRUTH`** shows a `readable_by` column that was never created and omits
`wrong_action_probability` and `response_delay_seconds`, both of which the
World Simulator actually needs and Unit C will be reading.
