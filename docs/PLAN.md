# Build Plan (Momotaro): priority list, phase by phase

This is a living checklist, not a spec. Reorder, insert, split, or drop items
freely as priorities change, that is the entire point of this document. The
full detail for any item lives in the doc/section it points to
(`PRD.md`, `ARCHITECTURE.md`, `AGENTS.md`), this file only tracks what to do
and in what order, never the how or the why.

Convention: `[ ]` open, `[x]` done. Each item ends with `→ detail: <doc §>`.

> **Agents: tick your own boxes here.** This file uses git's `merge=union`
> driver (see `.gitattributes`), so two agents ticking different boxes
> merge cleanly instead of conflicting. Two rules: only tick a box when the
> item meets the Definition of Done in `docs/ENGINEERING.md` §11, and add
> or tick lines rather than reordering or restructuring, union merge handles
> additions well and rewrites badly. Same applies to `docs/DECISIONS.md`.

> **Deliberately parked work lives in `docs/BACKLOG.md`**, not here as a
> forever-unchecked box. Check there before assuming something unchecked
> below was simply forgotten.

> **Learning companion**: `docs/CONCEPTS.md` explains, at a conceptual
> level (not code), every pattern and technology this project applies and
> why. Grows as the project does; read it to understand what was built,
> not just that it was built.

## Phase 0: Foundations & scaffold

**Phase 0 is deliberately sequential and must be finished and merged before
agents are allocated services.** Every item here is a shared foundation. Fan
out too early and you get six different `Clock` implementations and six
config loaders to reconcile. The one exception is `web/`, which can start
immediately against `docs/API_GATEWAY.md` using mocked responses.

- [x] Repo init: rename to `Momotaro`, `git init`, remote set, root
      `.gitignore` (Go binaries, `.env`, `web/node_modules`; **not**
      `proto/gen/`, that is committed) and root `.dockerignore`.
      Also `.gitattributes` with `merge=union` on `PLAN.md`/`DECISIONS.md`.
      Pushed to origin.
- [x] Single root `go.mod`, module path fixed (e.g.
      `github.com/thisizaro/Momotaro`), Go version pinned
      → `ARCHITECTURE.md` §2a
- [x] Top-level layout created: `services/` (7 product services, each with
      `cmd/`, `internal/`, `Dockerfile`, `AGENTS.md`), `demo/` (same shape,
      hackathon-only stand-ins), `internal/platform/`, `proto/`,
      `migrations/`, `web/` → `AGENTS.md` "Layout", `ARCHITECTURE.md` §2a, §3b
- [x] Scaffold script in `scripts/` that generates that structure. Run
      **once** by the orchestrator and committed, not run per agent
- [x] **Contracts first, before any service code.** `buf` pinned,
      `buf.yaml` + `buf.gen.yaml` committed, `.proto` for API Gateway,
      Ingestion, Classifier, Executor, World Simulator
      (`RecoveryActionPort`), Notification Simulator (`NotificationPort`),
      Reporting (including `StreamBatchUpdates`), and Decision Engine
      (`ReportDelayedOutcome`). Conventions in §9: one file per service,
      versioned package, dedicated Request/Response per RPC, enum zero
      values, `int64` paise for money
      → `ARCHITECTURE.md` §9, §2a, §3b, §6a
- [x] `proto/gen/` generated and committed; `buf lint` + `buf breaking`
      wired into CI → `ARCHITECTURE.md` §9
- [x] `migrations/` set up with a pinned tool (goose or golang-migrate),
      first migration includes `BATCH`, `RECORD.batch_id`,
      `RECORD_STATE.due_at`, and the `UNIQUE (record_id, attempt_number)`
      constraint on `INTERVENTION_ATTEMPT`, none of it bolted on later
      → `ARCHITECTURE.md` §10, §11, §12a
- [x] `docs/ENGINEERING.md` written and its Definition of Done adopted as
      the gate for every checkbox in this file (referenced from this file's
      header and from `AGENTS.md`). Each agent still has to actually read it.
- [x] Shared internal packages, part 1: `Clock` (with controllable Fake),
      structured logger with fixed correlation keys, fail-fast config
      loader. All tested under `-race`
      → `ENGINEERING.md` §2, §5, §9
- [x] Shared internal packages, part 2: gRPC interceptors
      (metrics/tracing/recovery/deadline), graceful-shutdown helper, and the
      `kafkax`/`pgx` helpers. Library choices are already pinned in each
      package's doc.go; recovery + deadline interceptors and the shutdown
      helper should land with the first service, the rest in Phase 4.
      `UnaryServerRecovery` + `UnaryServerRequireDeadline` wired into every
      service's gRPC server, `UnaryClientDefaultDeadline` into every
      inter-service gRPC client (api-gateway→ingestion,
      decision-engine→classifier/executor); metrics, tracing and
      request-scoped logging remain Phase 4
      → `ENGINEERING.md` §3, §6, `ARCHITECTURE.md` §8a, §13
- [x] `docker-compose.yml` for local dev: Postgres, Redis, Kafka
      (single-broker/KRaft is fine), plus a Kafka UI (redpanda console or
      kafdrop) for inspecting topics by hand → `ARCHITECTURE.md` §3, §8
- [x] `.env.example` checked in, real `.env` gitignored, includes the static
      demo API key → `AGENTS.md` "Secrets and config", `ARCHITECTURE.md` §17
- [x] Basic CI: GitHub Actions, build + unit test on every push/PR,
      path-filtered per service once services exist → `AGENTS.md`
      "Branching and CI conventions"
- [x] `AGENTS.md` "Testing conventions" and "Secrets and config" sections
      in place before any service code lands
- [x] `web/` scaffolded (Vite + React + TS + Tailwind) against the already-written
      `docs/API_GATEWAY.md` contract, can start in parallel with backend
      work using mocked responses → `web/AGENTS.md`
- [x] One `Dockerfile` per service, multi-stage, build context = repo root,
      one binary per image. Verify each image builds before fan-out
      → `ARCHITECTURE.md` §2a
- [x] Per-service `AGENTS.md` written from a shared template: what this
      service owns, which tables it may write vs. only read (§10a), its
      proto as interface source of truth, what it must not touch, and how
      to request a change from another service
- [x] **Walking skeleton**: one record end to end through all 7 services
      with everything hardcoded (fixed classification, stub outcome, no
      worker pool, no scheduler, no economics) reaching `Recovered` with an
      audit row. This proves integration *before* any agent builds depth,
      and is the single biggest de-risking step in the plan. Proven by
      `test/e2e/walking_skeleton_test.go` (`go test -tags e2e ./test/e2e/...`
      against `make up` + `make migrate-up`): api-gateway, ingestion,
      decision-engine, classifier, executor and audit run as real built
      binaries, one record posted via `POST /v1/batches` reaches
      `RECORD_STATE_RECOVERED`, verified both in Postgres and via a live
      `Audit.GetRecordAudit` call. Reporting and the World Simulator/webhook
      path are still open, see Phase 1.

## Phase 1: Core pipeline skeleton (prove the loop end to end)

- [x] API Gateway: real Go service, static API key check, basic rate
      limiting, and both entry points routed to Ingestion over gRPC:
      `POST /v1/batches` (demo/backfill) and
      `POST /v1/webhooks/payment-failed` (production shape, one event at a
      time) → `ARCHITECTURE.md` §0a, §3a, `docs/API_GATEWAY.md`.
      Rate limiting is a single token-bucket middleware
      (`RATE_LIMIT_RPS`/`RATE_LIMIT_BURST`, disabled when either is <= 0),
      applied before auth so an over-limit caller cannot even reach the key
      check.
- [x] (unplanned, found while starting Ingestion depth) `ENGINEERING.md`
      had no guidance on file/function modularity, and the walking-skeleton
      handlers show why that matters: added §14 ("one job per file, one job
      per function") and Definition of Done item 10 → `ENGINEERING.md` §14
- [x] (unplanned, found while implementing `SubmitEvent`) the initial
      schema had no column for the `idempotency_key` dedup that
      `SubmitEvent`'s proto contract already documented. Added migration
      `00002_record_idempotency_key.sql`: nullable `record.idempotency_key`
      plus a partial unique index → `ARCHITECTURE.md` §11, §12a
- [x] Ingestion: gRPC `SubmitBatch(records[]) -> {batch_id}` and
      `SubmitEvent(record) -> {record_id}`, both converging on the same
      `raw.events` publish path so nothing downstream can tell them apart.
      Creates the `BATCH` row, stamps `batch_id` on every message
      → `ARCHITECTURE.md` §0a, §3, §4, §10.
      `SubmitEvent` attaches to a shared "rolling" batch
      (`ROLLING_BATCH_SOURCE`, found-or-created) rather than a batch per
      event, and deduplicates on `idempotency_key` against the new
      `record.idempotency_key` column (migration `00002`), returning the
      original record instead of creating a duplicate on a webhook retry.
      Split into `validate.go`/`record.go`/`store.go`/`events.go`/`server.go`
      per `ENGINEERING.md` §14 (one job per file); `server.go` is
      orchestration only.
- [x] (unplanned, needed by the keyed worker pool below) added
      `kafkax.Consumer.ConsumeKeyed`: dispatch-by-key-hash across a bounded
      worker pool with contiguous-prefix offset commits. Built in
      `internal/platform/kafkax` since it is generic Kafka consumption
      infrastructure, not Decision Engine business logic; `kafkax.go`'s own
      doc comment anticipated this landing here → `ARCHITECTURE.md` §8a
- [x] (unplanned, needed by the scheduler below) added
      `record_state.pending_action` (migration `00003`): the state alone
      does not say which nudge subtype was scheduled, so the scheduler had
      nothing to resume from → `ARCHITECTURE.md` §7a
- [x] Decision Engine: consumes `raw.events` via a **keyed worker pool**
      (not one-at-a-time, `kafkax.ConsumeKeyed` above) with contiguous-prefix
      offset commits, calls Classifier via gRPC, owns the state machine,
      writes `RECORD_STATE` and `AUDIT_ENTRY` **in one transaction**
      → `ARCHITECTURE.md` §4, §7, §8a, §10a.
      Phase 1 scope deliberately excludes the `Scoring`/`ClosedUneconomic`
      economics gate (Phase 2): `HandleMessage` classifies and schedules
      only, it never calls Execute directly, every action (including retry)
      goes through the scheduler below so the diagram's actual shape holds.
      A classify failure retries a bounded number of times then dead-letters
      rather than blocking the partition. Split into
      `state.go`/`store.go`/`clients.go`/`dlq.go`/`engine.go`/`scheduler.go`
      per `ENGINEERING.md` §14.
- [x] Decision Engine scheduler worker: `due_at` polling with
      `FOR UPDATE SKIP LOCKED`, so cooldowns and cause-aware retry timing
      actually fire. Nothing time-based works without this
      → `ARCHITECTURE.md` §7a.
      Claims a due record (Scheduled -> Retrying/Nudged, its own audit
      entry) in one transaction, then calls Execute outside it so a slow
      downstream call never holds the row lock. A failed nudge/retry
      escalates (no retry budget yet, Phase 2); a `PENDING` nudge outcome
      parks in `Nudged` with `due_at` left `NULL`, since
      `ReportDelayedOutcome`'s caller (World Simulator) is Phase 5.
- [x] DLQ path: bounded processing retries, then publish to
      `raw.events.dlq` with the failure reason and commit the offset, so one
      poison record cannot wedge a partition → `ARCHITECTURE.md` §8b.
      Applied at both failure points: `HandleMessage`'s classify call and
      the scheduler's Execute call, each retried up to 3 times before
      dead-lettering. Not written as a `RecordState` value (none exists for
      it); left in whatever state it was claimed into, since a DLQ'd record
      is a processing failure, not a considered decision like `Escalated`.
- [x] Classifier: real deterministic rules engine
      (`services/classifier/internal/rules`) replacing the walking-skeleton
      hardcoded response. Normalises `failure_code` (trim, uppercase,
      collapse `-`/spaces to `_`), maps it to one of the seven
      `RootCauseBucket` values via a checked-in table, and derives a
      recommended action, an honest confidence and a rationale naming the
      actual input from the bucket. An unrecognised or empty code falls
      back to `record.type` (`CHECKOUT` → `ABANDONMENT`, `INVOICE` →
      `OVERDUE`, otherwise `UNSPECIFIED` + `ESCALATE` + confidence 0),
      never guessed at, and is not an `InvalidArgument` (an unclassifiable
      record is still a valid one). Provider chain **skeleton** built
      (`internal/provider`): an ordered list of rungs resolved from
      `LLM_PROVIDER_CHAIN` at startup, an unknown name failing fast rather
      than at request time, per-rung hop recording, and inter-rung
      response validation (a bucket/action outside the enum or a
      confidence outside `[0,1]` is a rung failure, `schema_invalid`, not
      an answer). The rules engine is always the last rung and cannot
      fail, so the chain always terminates in a valid answer.
      `force_rules_only` skips every non-rules rung (the load generator's
      cost-safety switch). `source` stays `SOURCE_RULES_FALLBACK` always in
      Phase 1, since the rules engine is always what answers; provider
      identity lives in `hops`, not a new proto field →
      `ARCHITECTURE.md` §5, §5a, `PRD.md` §2a. Explicitly deferred: any
      real LLM provider and circuit breakers (Phase 3); `ComposeNudge`
      (Phase 5, no caller exists yet); Prometheus metrics (Phase 4's
      shared gRPC interceptor work, not hand-wired per service; logs a
      `Warn` per failed rung and per unknown-code classification instead);
      economics/EV scoring and cause-aware retry timing (Decision Engine,
      Phase 2). Restructured per `ENGINEERING.md` §14: `internal/rules`
      (the failure-code table, the action/confidence table, rationale
      composition, the rules `Provider`), `internal/provider` (the
      `Provider` interface, the chain walk, hop recording, response
      validation), `internal/server` (handler only: validate, delegate,
      log). Stays entirely stateless: no Postgres connection, no clock,
      `ANTHROPIC_API_KEY`/`OPENAI_API_KEY` stay unread since nothing calls
      them yet. Deleted `TestClassifyIsHardcodedRegardlessOfInput`
      (walking skeleton only, asserted the exact property this task
      removes).
- [x] Executor: **insert-before-execute** idempotency against the
      `UNIQUE (record_id, attempt_number)` constraint (the durable
      guarantee), Redis `SETNX` as the fast path only, calls a minimal
      **stub** outcome source (fixed/scripted responses) via the
      `RecoveryActionPort`/`NotificationPort` interfaces, not the full
      `demo/world-simulator` yet, just enough to prove the state machine
      reaches a terminal state → `ARCHITECTURE.md` §11, upgraded in Phase 5.
      The two ports are now real Go interfaces (`internal/ports`) shaped to
      mirror `worldsim`/`notifier` protos, so Phase 5's adapter is a thin
      translation; routing by action type lives in one place, and `ESCALATE`
      reports failure rather than a false success since handing a record to a
      human is not something the Executor can do. The stub is **scripted, not
      random** (retry attempt 1 succeeds, 2+ fail, nudges go out and return
      `PENDING`), so both branches of the post-execute state machine are
      reachable and Phase 2's re-run safety test stays possible. Real
      per-action costs land on the attempt row, which is what makes "net
      recovered" a measurement. `resolves_at` is produced but not yet
      consumed: its caller (`ReportDelayedOutcome`) is Phase 5. Redis fast
      path deferred, see the scope-decision note below. Split into
      `ports/`, `attempt/` and `server/` per `ENGINEERING.md` §14, with the
      ports layer database-free so its tests need no stack.
- [x] (unplanned, found while writing the Executor's scripted stub) reusing
      `OUTCOME_PENDING` as the "claim taken, still working" marker on
      `INTERVENTION_ATTEMPT` became ambiguous the moment a nudge legitimately
      resolved to `PENDING`: a redelivered nudge would poll for an answer not
      arriving until Phase 5, blow its deadline and be dead-lettered despite
      having executed perfectly. Now claimed with `OUTCOME_UNSPECIFIED`
      (the enum's "unset", per `common.proto`'s own convention) and the wait
      is bounded, so an abandoned claim is reported rather than guessed at.
      No migration needed → `ARCHITECTURE.md` §11, `docs/INCIDENTS.md`
- [x] Audit Service: serves a record's audit trail, and runs the continuous
      invariant verifier exporting `stopping_rule_violation_total` and
      `incomplete_audit_trail_total`. It does **not** write audit rows, the
      owning service does that transactionally → `ARCHITECTURE.md` §10a.
      `VerifyInvariants` scans RECORD_STATE + AUDIT_ENTRY (optionally scoped
      to one batch) for two invariants that are meaningful today: an
      incomplete trail (a state with no audit entry, or one whose last entry
      disagrees with it) and an impossible transition (a broken chain or an
      edge outside the state machine, `services/audit/internal/server/
      statemachine.go`). `stopping_rule_violations` is always zero: no
      retry/contact caps exist yet (Phase 2), so there is nothing to check
      against, deliberately not faked. A background `Watcher`
      (`watch.go`) calls the exact same `VerifyInvariants` path on a
      configurable interval (`INVARIANT_CHECK_INTERVAL`) so the check runs
      without anyone asking, logging violations rather than exporting
      Prometheus metrics yet, that wiring is Phase 4's shared gRPC
      interceptor work across every service, not Audit-only. Refactored the
      existing `GetRecordAudit` into the same `store`/`statemachine`/
      `verify`/`watch` split per `ENGINEERING.md` §14.
- [x] (unplanned, the Phase 0 CI box claimed this but it was never built)
      the Docker matrix had no path filtering, so every PR rebuilt all nine
      service images even when it changed only documentation. Now a `changed`
      job computes the matrix from the diff
      (`.github/scripts/changed-services.sh`) and `docker` is skipped
      entirely when nothing that reaches an image changed. Fails safe: shared
      inputs every image embeds (`go.mod`, `internal/platform/`, `proto/`,
      `.github/`) rebuild everything, and so does an undeterminable diff
      base → `AGENTS.md` "Branching and CI conventions"
- [x] (unplanned, found while writing `services/executor/SPEC.md`) the
      invariant verifier above rejected every record the Phase 1 pipeline
      produces. Its state machine had no `NEW -> RETRY_SCHEDULED` or
      `NEW -> NUDGE_SCHEDULED` edge, because `ARCHITECTURE.md` §7's diagram
      routes everything through `Scoring`, which is the Phase 2 economics
      gate and does not exist, so Decision Engine schedules straight out of
      `NEW`. Confirmed against the live database, then fixed by adding both
      edges with the same `TEMPORARY` marking as the existing
      `NEW -> RECOVERED` skeleton edge. Also made
      `GetRecordAudit.trail_complete` an actual computation: it was
      hardcoded `true`, so every assertion on it, including the e2e test's,
      proved nothing, and one pre-existing Audit test turned out to be
      asserting a completeness it never checked over a fabricated
      `UNSPECIFIED -> NEW` entry the system never writes. See
      `docs/INCIDENTS.md` 2026-08-23 → `ARCHITECTURE.md` §7, §10a
- [x] (unplanned, found while building `scanRecords`) `WHERE $1 = '' OR
      r.batch_id = $1::uuid` fails on Postgres for an empty `$1`: SQL does
      not short-circuit `OR`, so the `::uuid` cast runs and errors even when
      the empty-string branch would have matched. Fixed by comparing
      `batch_id::text = $1` instead of casting the parameter. See
      `docs/INCIDENTS.md`.
- [x] End-to-end smoke test: `POST /v1/batches` with a handful of records,
      watch them reach `Recovered`/`Escalated`, confirm the audit trail is
      complete → `PRD.md` §7, §8.
      `test/e2e/smoke_test.go` submits seven records through the real HTTP API
      and asserts each settles where its own failure code implies: retries and
      insufficient-funds reach `Recovered`, a risk hold and an unrecognised
      code reach `Escalated` without anything being executed, and a dead
      instrument and an abandoned checkout park in `Nudged` awaiting Phase 5's
      delayed-outcome callback. Per record it checks the trail chains, starts
      at `NEW`, carries the classifier's rationale, and ends where the record
      actually is; per batch it calls `Audit.VerifyInvariants` and requires
      zero incomplete trails, zero impossible transitions and zero
      stopping-rule violations, which is `PRD.md` §9/§10's claim checked by
      the service whose job it is rather than re-derived by the test. Also
      asserts logged spend is non-zero wherever an intervention actually ran.
      The stack bring-up is now a shared harness (`test/e2e/harness_test.go`)
      rather than duplicated. Note the stub's "attempt 2+ fails" branch is
      deliberately not reachable end to end: Phase 1 has no retry budget, so
      no record ever gets a second attempt, and faking one to exercise the
      branch would test the fake rather than the pipeline. It is covered by
      unit tests instead → `docs/INCIDENTS.md`
- [x] (unplanned, found by the smoke test above on its first run) the nudge
      path records a `Nudged -> Nudged` audit entry, because the scheduler
      claims a due nudge into `Nudged` and the send's `PENDING` outcome maps
      there too. The verifier rejected it. Allowed the edge rather than
      dropping the entry, since that entry carries the attempt number and the
      send's real cost, and losing it would put a hole in the spend history.
      See `docs/INCIDENTS.md` → `ARCHITECTURE.md` §7

Note: the full World Simulator (hidden ground truth, probabilistic
outcomes, delayed-callback scheduling) is deliberately deferred to Phase 5,
not built here. A stub keeps Phase 1 about proving the pipeline shape, not
the demo's realism. Same for the dashboard and the Gateway's WebSocket
relay, both need Reporting to exist first, so they land in Phase 5 too.

## Phase 2: Durability, safety & economics

- [x] Idempotency proven end-to-end (duplicate Kafka delivery and duplicate
      gRPC retry both handled safely) → `ARCHITECTURE.md` §11
- [x] Retry budgets, cooldowns, contact caps enforced with automated tests
      → `PRD.md` §11
      Enforced in the Decision Engine between classification and scheduling,
      the fixed order in `ARCHITECTURE.md` §5a. Counters are derived from
      `INTERVENTION_ATTEMPT` rather than stored, and evaluated in Postgres
      rather than a cache, for the reason in `DECISIONS.md` (2026-08-24).
      Also covers the recovery window and the automatic downgrade to
      "needs human" that `PRD.md` §11 requires so a record never loops.
      Scope note on the Definition of Done (`ENGINEERING.md` §11 item 3):
      structured logs carry the recommended action, the scheduled action and
      the guardrail reason, but no Prometheus metric is exported, because no
      service exports one yet. That lands with the rest of observability in
      Phase 4, which already carries the stopping-rule-violation alert.
      The economics scorer then picks from the permitted set instead of the
      current fall back to escalation.
- [x] `configs/intervention_costs.yaml` and the `P(recovery)` prior table
      checked in, with the assumed values documented and defensible
      → `ARCHITECTURE.md` §5a
      Both files carry a provenance tag on every number
      (`[SOURCED]`/`[ASSUMPTION]`/`[UNVERIFIED]`) with the derivation or URL
      inline, so each is arguable input by input rather than taken on trust.
      `intervention_costs.yaml` also reconciled against
      `services/executor/internal/ports/cost.go`, which had drifted from it
      on two of three constants (`docs/DECISIONS.md` 2026-08-24); the Go
      constants now match the YAML and a test
      (`cost_reconciliation_test.go`) keeps them from silently diverging
      again. Remaining `[UNVERIFIED]` figures (the Gupshup BSP markup; the
      NPCI NACH rate, sourced from a processor citing NPCI rather than
      npci.org.in directly) are named in that same DECISIONS.md entry as
      needing firming up, not resolved by this checkbox.
- [x] Economics scorer in Decision Engine: `Scoring` state, EV computed per
      allowed action, `ClosedUneconomic` terminal state when no action has
      positive EV → `ARCHITECTURE.md` §5a, §7, `PRD.md` §2b
      Selection is by expected value over the whole permitted menu, not by
      the Classifier's `recommended_action`, so the model contributes the
      bucket and the numbers decide. Escalation deliberately bypasses
      economics: a risk hold is a safety call, and pricing it would imply it
      were negotiable.
      Re-entry to `Scoring` after a failed attempt (`ARCHITECTURE.md` §7)
      is now built too, as Unit E in `docs/PHASE2_IMPLEMENTATION.md`. A
      failed attempt is re-priced with one more attempt spent rather than
      escalated, so the guardrail caps finally bind and the decaying priors
      do real work. Termination is proven against both independent stops:
      the guardrails refusing a capped action, and the priors falling to
      zero past the deepest modelled attempt. Both were verified by
      breaking them on purpose and confirming the tests go red.
- [x] Cause-aware retry scheduling (salary-window for insufficient_funds,
      short backoff for bank_timeout, no retry for hard_decline/risk_hold)
      → `ARCHITECTURE.md` §5a
- [x] `cost_paise` + EV snapshot persisted per attempt, so net recovered is
      computed from real logged spend, not estimated
      → `ARCHITECTURE.md` §10
- [x] Test asserting the Decision Engine has **no** query path to
      `GROUND_TRUTH`, the integrity rule from `ARCHITECTURE.md` §5a.
      Widened to cover all three decision-path services (Decision Engine,
      Classifier, Executor), since §5a states the rule for "the decision
      path," not one service. `test/integrity/ground_truth_isolation_test.go`,
      unit tier, no build tag, parses each service's Go source with
      `go/parser`/`go/ast` so comments are never matched, only real
      identifiers and string literals.
- [x] Correctness invariant tests over a batch run: zero stopping-rule
      violations, 100% audit trail completeness → `PRD.md` §9, §10
- [x] Re-run safety test: replay the same batch twice, confirm identical
      outcome (no double-processing) → `ARCHITECTURE.md` §11
- [x] Crash-safety test: kill the Decision Engine mid-batch, restart, assert
      no record lost and no audit gap (this is what the transactional write
      and contiguous-prefix commits exist to guarantee)
      → `ARCHITECTURE.md` §8a, §10a
- [x] Scheduler test with a fake clock: a record parked with a future
      `due_at` fires exactly once when the clock advances past it, and is
      never double-claimed by two concurrent pods
      → `ARCHITECTURE.md` §7a, `ENGINEERING.md` §2
- [x] Delete the `TEMPORARY` state machine edges (`NEW -> RECOVERED`,
      `NEW -> RETRY_SCHEDULED`, `NEW -> NUDGE_SCHEDULED`) added at
      `docs/PLAN.md` above for the Phase 1 pipeline, now that every record
      routes through `Scoring` → `docs/PHASE2_IMPLEMENTATION.md` Unit M

## Phase 3: Reasoning layer

> Working breakdown, dependency graph and per-unit LLDs:
> **`docs/PHASE3_IMPLEMENTATION.md`** (8 units, A to H). That document also
> lists ten flaws found in the four checkboxes below before starting, the most
> important being: the "rationale stored and retrievable" item is already done
> and the part actually missing is `ClassifyResponse.hops`, which nothing
> persists; and an LLM rung would currently receive exactly the same two
> inputs as the lookup table it replaces, because
> `ClassifyRequest.history`/`instrument_history` are never populated. Read it
> before picking up any item here.

- [x] LLM provider(s) decided (cost/rate-limit evaluation still open, see
      `AGENTS.md` locked decisions) and wired into the provider chain
      → `ARCHITECTURE.md` §5, `docs/PHASE3_IMPLEMENTATION.md` Units A, B
      Chain is `groq,gemini,rules`, Groq running `openai/gpt-oss-20b` at
      `reasoning_effort: low`, chosen for **guaranteed constrained decoding**
      (`json_schema` + `strict: true` is token-level, so the model cannot emit
      a bucket outside the enum) and for being the fastest inference measured.
      Unit A first hardened the chain the rungs plug into: the terminal-rung
      invariant is now enforced at startup rather than assumed, and each rung
      is bounded by `LLM_TIMEOUT` with a `CHAIN_RESERVE` held back so a hanging
      provider can no longer consume the caller's whole deadline and get a
      classifiable record dead-lettered.
      Two scope notes. **Gemini is built, unit-tested and confirmed working
      against the live API, but held OUT of the default chain** on measured
      latency: Groq p50 ~570ms / max 688ms versus Gemini p50 3.01s / max
      6.19s, and no single `LLM_TIMEOUT` serves both without either making
      Gemini decorative or overrunning the Decision Engine's 5s
      `CALL_TIMEOUT`. Re-enabling it honestly needs per-rung timeouts. And the
      **default `LLM_PROVIDER_CHAIN` everywhere stays `rules`**, including the
      e2e harness, so no automated tier ever spends quota; the live chain is
      opt-in. Metric export deferred to Phase 4 as everywhere else.
      Full reasoning and the measurements in `docs/DECISIONS.md` 2026-08-28.
- [x] Fallback path deliberately tested (simulate timeout/error per
      provider, confirm the chain falls through correctly and every hop
      tried is recorded) → `ARCHITECTURE.md` §5,
      `docs/PHASE3_IMPLEMENTATION.md` Unit C
      Seven unit-tier tests against fakes in `provider/fallback_test.go`,
      one per failure mode (timeout, transport error, HTTP 5xx, HTTP 429,
      invalid JSON, out-of-vocabulary enum, open circuit), each asserting
      the exact hop result string, plus a two-LLM-rung case (groq fails,
      gemini answers) proving hop order and `SOURCE_LLM`. Uses
      `services/classifier/internal/llm`'s real `RateLimitedError`/
      `StatusError` types as the fake rung's error value rather than local
      stand-ins, so the test is tied to the vocabulary the classifier
      actually produces.
      e2e half in `test/e2e/fallback_test.go`: the real classifier binary,
      pointed at an `httptest.Server` that always returns 500, submits one
      record and asserts the audit trail shows `SOURCE_RULES_FALLBACK` with
      hops `[{groq,error},{rules,ok}]`. Needed a real cross-unit fix first
      (`docs/INCIDENTS.md` 2026-08-28): the Decision Engine's
      `LLM_SAMPLE_RATE` (Unit H) sends `force_rules_only=true` at its
      default of `0.0`, which filters `groq` out of the chain before it is
      ever called, so the test's own "was the fake server even hit" guard
      caught its first version silently proving nothing. Fixed by extending
      the harness (`startStackWithEnv`, additive, the other seven callers of
      `startStack` unchanged) to also set `LLM_SAMPLE_RATE=1.0` for this
      test's Decision Engine process.
- [x] Circuit breaker per provider, with a test proving that a sustained
      provider outage does **not** make every record pay the full timeout
      → `ARCHITECTURE.md` §5, `docs/PHASE3_IMPLEMENTATION.md` Unit D
      Per-pod, in-memory, closed/open/half-open, wrapping a rung and itself a
      `Provider` so `chain.go`'s walk needed no change. `NewBreaker` refuses to
      wrap the `rules` rung: an open breaker in front of the rung that cannot
      fail would leave the chain with no answer at all.
      **HTTP 429 is a first-class case, not just another failure**: it opens on
      the first one rather than after `LLM_BREAKER_THRESHOLD`, takes its
      cooldown from `Retry-After` when the provider sends one, and records its
      own `rate_limited` hop. On a capped free tier throttling is the failure
      most likely to actually fire, and threshold-counting would pay four more
      doomed calls first. Gemini's live rate limit sent no `Retry-After` at
      all, so the fallback branch is exercised in practice, not just in test.
      Per Flaw 6 the tests assert **call counts and hop results, never
      wall-clock time**. The half-open "exactly one trial" property is raced
      with 25 goroutines; a first version caught deliberately broken locking
      only 4 times in 20 runs, and holding the trial in flight took that to
      20/20 with 0/20 false positives (`docs/INCIDENTS.md` 2026-08-28).
      Metric export (`llm_circuit_state`) deferred to Phase 4; a `Warn` on
      every state change is the compensating control.
- [x] Rationale stored and retrievable from the audit trail for a full
      record → `PRD.md` §2a
      Done as `docs/PHASE3_IMPLEMENTATION.md` Unit E, in three sequenced PRs
      (#31 migration, #32 proto, #33 code). `audit_entry.provider_hops` now
      carries which provider rungs were actually tried, encoded as
      `provider:result` pairs, and `GetRecordAudit` returns them on
      `AuditEntry.hops`. The codec is shared infrastructure
      (`internal/platform/hopcodec`) rather than per-service, because the
      writer and reader are different services and a divergent delimiter would
      corrupt an audit row instead of failing a build.
      Note (found while planning Phase 3): the rationale half of this is
      already done and has been since Phase 1 (decision-engine `store.go`
      writes it, audit `store.go` reads it back, `GetRecordAudit` returns it,
      two tests assert it). The part genuinely missing is
      `ClassifyResponse.hops`, which the chain computes and nothing persists,
      so the trail cannot show *which* provider answered or what the ones
      before it did. Replaced by `docs/PHASE3_IMPLEMENTATION.md` Unit E
      (migration + proto + code, three sequenced PRs).
- [x] (unplanned, found while planning Phase 3) populate
      `ClassifyRequest.history` and `instrument_history` from the Decision
      Engine. Both had been empty since Phase 1
      (`services/classifier/SPEC.md` §3 documented it and §10 item 1 raised it
      as a cross-service item), so a model rung would see exactly the two
      inputs the rules table sees and could not improve on it.
      → `docs/PHASE3_IMPLEMENTATION.md` Unit F
      Two new store queries: `loadAttemptRows` (this record's own attempts,
      oldest first) and `loadInstrumentHistory` (up to ten most recent
      attempts on other records sharing the instrument, excluding this
      record's own rows). Both load before `Classify` is called, not after
      like the guardrails' aggregate counters, since they are its inputs.
      Deliberately not merged with `loadAttemptHistory`: that query serves
      the guardrails' counts and cooldown timestamp, this serves the
      classifier's prompt rows, and combining them would couple a compliance
      check to a prompt-shaping read.
      A brand new record has no rows of its own yet, since `Classify` is
      only ever called once per record before any attempts exist in the
      current architecture; `history` is real plumbing for a future
      re-classification path rather than something today's flow exercises,
      and the tests seed rows directly to prove the plumbing works. All six
      e2e tests unchanged, since the rules engine ignores both fields.
- [x] (unplanned, found while planning Phase 3) enforce the classification
      confidence threshold in the Decision Engine. `classifier.proto`
      documents it, `ARCHITECTURE.md` §5 assigns it here, and both
      `engine/state.go` and `engine/engine.go` already carried comments
      saying a low-confidence classification is a safety call that bypasses
      pricing, but no code read `confidence`. Harmless while it was a table
      constant, a real gap once a model produces it
      → `docs/PHASE3_IMPLEMENTATION.md` Unit G
      New `CLASSIFY_CONFIDENCE_THRESHOLD`, default `0.0` (validated `[0,1]`
      at startup), checked in `Engine.decide` before the existing
      recommended-action-escalate check: below it, a classification is
      escalated (`directPath`, bypassing economics entirely) with reason
      "classification confidence below threshold". Default `0.0` escalates
      nothing, since every rules-engine confidence value is `> 0`, so this
      is provably a no-op until deliberately configured, and all six e2e
      tests stayed unaffected.
      The one interaction worth recording: the rules engine's unknown-code
      path always returns confidence `0.0` **and** recommends `ESCALATE`
      (`rules/actions.go`), so once a threshold above `0.0` is set, both the
      new check and the pre-existing recommended-action check are satisfied
      by the same record. The reason string names whichever is the real
      cause rather than whichever branch runs first: an unknown-code record
      still gets "classifier recommended escalation", never "classification
      confidence below threshold", so the audit trail can tell "we do not
      recognise this failure code" from "the model was unsure" -- proven by
      a dedicated integration test asserting both reason strings land on
      the right record. The re-entry path (`scheduler.go`'s
      `handleFailedAttempt`) never re-classifies, so it has no fresh
      confidence to check; `scoreAndRoute`'s doc comment now says so, so
      nobody adds a check there against a stale or zero value.
- [x] (unplanned, found while planning Phase 3) `LLM_SAMPLE_RATE` and the
      checked-in demo/dev config profiles. `PRD.md` §12 calls for a
      50-to-100-record demo batch; Groq's free tier is 30 RPM. Those two
      numbers do not fit, and without a gate the failure is not graceful:
      the first thirty records get a model, the rest get rate limited in an
      order decided by scheduling luck, and there is no way to know in
      advance which record a judge picks has a real rationale behind it.
      → `docs/PHASE3_IMPLEMENTATION.md` Unit H
      `ClassifyRequest.force_rules_only` already existed as a per-request
      field with the classifier already honoring it (Unit A); nothing had
      ever set it. The Decision Engine now decides per record, via
      `hash(record_id) % 10000 < rate*10000`, deliberately not `rand`:
      re-run safety (`test/e2e/rerun_safety_test.go`) asserts identical
      outcomes on replay, and a random draw would make that guarantee
      conditional on a config value. Default `0.0`, validated in `[0,1]` at
      startup, so every existing test and every default run stays free.
      `configs/demo.env` (`LLM_SAMPLE_RATE=0.15`, ~15 live calls per 100
      records, comfortably under quota) and `configs/dev.env`
      (`LLM_SAMPLE_RATE=1.0`, every manually-submitted record gets a live
      rationale) checked in, alongside `.env.example` and one new row in
      `ARCHITECTURE.md` §17. No `MODE=demo|dev|prod` enum: the profiles set
      the same individually named knobs everything else uses
      (`DEMO_TIME_SCALE`, `LLM_PROVIDER_CHAIN`), so the code never learns
      what "demo" means and production stays a real, individually
      overridable code path rather than something that only runs on a
      judge's laptop.

## Phase 4: Observability

> Working breakdown and per-unit notes: **`docs/PHASE4_IMPLEMENTATION.md`**
> (6 units, A to F). Units are ordered easiest first; tracing (Unit F) is
> held pending a go/no-go decision rather than built speculatively, since
> Kafka does not propagate trace context on its own.

- [x] Prometheus metrics in every service via a shared gRPC interceptor
      plus Kafka consumer lag exporter → `ARCHITECTURE.md` §13,
      `docs/PHASE4_IMPLEMENTATION.md` Units A, B
- [ ] **Deferred, see `docs/BACKLOG.md`**: OpenTelemetry tracing across
      gRPC + Kafka hops, `record_id` as trace id → `ARCHITECTURE.md` §13
- [ ] Structured logging correlated by `record_id` (already true
      everywhere via `logger.ForRecord`) / `trace_id` (blocked on the
      item above) → `ARCHITECTURE.md` §13
- [x] Alertmanager rules (consumer lag, LLM fallback rate, stopping-rule
      violation) → `ARCHITECTURE.md` §13, `docs/PHASE4_IMPLEMENTATION.md` Unit D
- [x] Grafana dashboards, per-service and business metrics
      → `ARCHITECTURE.md` §13, `docs/PHASE4_IMPLEMENTATION.md` Unit E

## Phase 5: Demonstration realism

> Working breakdown, dependency graph, and a pre-planning audit of what
> was already built versus still a stub: **`docs/PHASE5_IMPLEMENTATION.md`**
> (8 units, A to H). Read it before picking up any item here — it found a
> real prerequisite gap this checklist doesn't mention (Decision Engine had
> no gRPC server at all) and several items far more built than their
> checklist wording implies (the Hinglish migration already shipped in
> Phase 0; the dashboard is essentially already built and just needs
> wiring).

- [x] Decision Engine gRPC server (`ReportDelayedOutcome`), the
      prerequisite World Simulator's delayed-outcome callback and any
      nudge-composition caller both need, not itself a `PLAN.md` line item
      before the Phase 5 audit found the gap
      → `docs/PHASE5_IMPLEMENTATION.md` Unit A
- [x] Synthetic batch generator: realistic failure codes/amounts, seeded
      hidden ground-truth recoverability profile per record, written
      straight into `BATCH`/`RECORD`/`GROUND_TRUTH` (table already exists,
      Phase 0) → `ARCHITECTURE.md` §6, §10, `docs/PHASE5_IMPLEMENTATION.md` Unit B
- [x] World Simulator upgraded from Phase 1's stub to the full probabilistic
      outcome model reading ground truth, including the Redis-backed
      delayed-outcome queue and its `ReportDelayedOutcome` callback into
      Decision Engine for nudge-type actions → `ARCHITECTURE.md` §6,
      `docs/PHASE5_IMPLEMENTATION.md` Unit C
- [x] Executor: wire real World/Notification Simulator clients, replacing
      Phase 1's in-process scripted stubs, with no change to
      `internal/server`. Also builds `demo/notification-simulator` for
      real (it was still a stub), and restructures the nudge path to
      actually ask the recovery port whether/when the customer reacts,
      not just send the message: without this, Unit C's delayed-outcome
      mechanism is never invoked and every nudge would still sit in
      `NUDGED` forever. **Missing from this checklist since Phase 5 was
      first drafted** (the intro above says "8 units, A to H" but this
      line never existed; `docs/PHASE5_IMPLEMENTATION.md`'s own unit table
      always had a D row) → `ARCHITECTURE.md` §3b, §6,
      `docs/PHASE5_IMPLEMENTATION.md` Unit D
- [x] Reporting Service: at-risk amount, gross + **net** recovered
      (after logged intervention spend), cost per rupee recovered,
      uneconomic-closed count/value shown separately from escalations,
      recovery rate by bucket/intervention, classification accuracy vs.
      ground truth, all scoped by `batch_id`, plus the `StreamBatchUpdates`
      server-streaming gRPC method
      → `PRD.md` §8, §9, §2b, `ARCHITECTURE.md` §10, §6a,
      `docs/PHASE5_IMPLEMENTATION.md` Unit F
- [x] API Gateway: report/records/audit GET routes plus a WebSocket relay
      (`WS /v1/batches/{batch_id}/live`) wired to `StreamBatchUpdates`, plus
      the `GET /v1/batches` list endpoint (backed by a new `ListBatches` RPC
      on Ingestion, its own proto PR first) → `ARCHITECTURE.md` §6a,
      `docs/API_GATEWAY.md`, `docs/PHASE5_IMPLEMENTATION.md` Unit G
- [ ] **[FRONTEND]** Dashboard: recovered amount/rate, record table, one
      record's audit drill-down, live feed, built only against
      `docs/API_GATEWAY.md`. `web/` is a Phase 0 scaffold, built early
      against the written contract so UI work would not wait on the backend.
      Not the final UI and not precious. This item is rebuilding against the
      frozen contract, not preserving what is there
      → `PRD.md` §1, `web/AGENTS.md`,
      `docs/PHASE5_IMPLEMENTATION.md` "The frontend track" F1/F2
- [x] Hinglish nudge composition: `Classifier.ComposeNudge` reusing the
      existing provider chain and circuit breakers, static Hinglish
      template per bucket as fallback, output length-capped and validated
      (no model-invented amounts or dates), text stored on
      `INTERVENTION_ATTEMPT.message_text` and surfaced in the audit trail.
      The two columns this needs already exist (`migrations/
      00001_initial_schema.sql`); no new migration required
      → `ARCHITECTURE.md` §5b, `PRD.md` §1 feature 5,
      `docs/PHASE5_IMPLEMENTATION.md` Unit E
- [x] Demo-time scale factor (config knob, `ARCHITECTURE.md` §17) wired
      through cooldowns (already done, Phase 2/3) and World Simulator's
      delay/tick timing (folded into Unit C, not a separate item: World
      Simulator has no timing logic to retrofit until it exists). Stale
      checkbox found and corrected 2026-08-30: `DEMO_TIME_SCALE` has been
      wired since Unit C merged (`config.Common.DemoTimeScale`, applied in
      `demo/world-simulator/internal/server/server.go`'s `scale` field and
      `services/decision-engine/cmd/main.go`), this line was just never
      ticked

### Added 2026-08-29, after reading the actual judging rubric

> **Ownership, for running agents in parallel.** Items tagged **[FRONTEND]**
> live entirely in `web/**` and belong to a dedicated frontend agent that
> reads only `web/AGENTS.md` and `docs/API_GATEWAY.md`. Everything else is
> backend and must not touch `web/`. The two tracks are genuinely
> independent **once `docs/API_GATEWAY.md` is frozen**, which is Unit O and
> is a real gate, not paperwork: the contract is currently ambiguous in six
> places, and two agents resolving the same ambiguity in isolation will
> resolve it differently. Full ownership table and the frontend track
> breakdown are in `docs/PHASE5_IMPLEMENTATION.md`.

- [x] **Freeze `docs/API_GATEWAY.md`.** All six gaps resolved: added
      `GET /v1/batches`; standardised every money field on the `_paise`
      suffix; picked the full proto enum string as the wire spelling for
      every enum, stated once in a new "Closed vocabularies" section;
      listed all 7 root-cause buckets (and every other enum's full member
      list) so nothing is missing; picked the WebSocket subprotocol over a
      query param for the live-feed auth workaround; and gave
      `POST /v1/batches` a `count` form for the demo generate button,
      explicitly documented as never carrying `GROUND_TRUTH` (only
      `scripts/batchgen` may write that table). Also folded in the
      `baseline_comparison` shape Unit K will need and the
      `GET /v1/batches/{batch_id}/invariants` route Unit L will need, so
      neither has to touch this doc again. Two small proto additions are
      flagged as still needed before Units G/K/L can fully implement
      against it (`ListBatches`, `SubmitBatchRequest.count`,
      `AuditEntry.ev_score_at_decision`/`p_recovery_at_decision`), left for
      whoever picks up that proto PR rather than done here
      → `docs/PHASE5_IMPLEMENTATION.md` Unit O, `docs/DECISIONS.md`

The Track 03 text and the general evaluation criteria are now recorded
verbatim in `PRD.md` §0. Scored against them, the project has exactly one
gap ("measured money recovered across a batch", i.e. Unit F) and several
capabilities that are built but invisible. These items close that, and are
ordered by value per hour. Detail for each is in
`docs/PHASE5_IMPLEMENTATION.md` Units I to N.

- [x] Adopt Razorpay's **published** payment error codes as the classifier's
      failure-code vocabulary, kept the invented one as aliases so nothing
      broke. The one behavioural change: codes meaning "outcome unknown"
      (including the existing `GATEWAY_TIMEOUT`/`TIMEOUT`) no longer
      auto-retry, they escalate instead, to avoid retrying a payment that
      may have already succeeded → `docs/PHASE5_IMPLEMENTATION.md` Unit I.
      Also fixes a Unit H blocker: `web/src/lib/format.ts`'s
      `FAILURE_CODE_LABELS` is keyed lowercase and renders blank against real
      uppercase codes, so this vocabulary has to be settled either way
- [x] Compliance guardrails with citations: TRAI TCCCPR contact-hour window
      and RBI e-mandate 24 hour pre-debit lead time, enforced as a floor on
      MANDATE retry timing → `PRD.md` §11a, `docs/PHASE5_IMPLEMENTATION.md`
      Unit J. Closes `PRD.md` §13's open question and the one row in
      `ARCHITECTURE.md` §17 whose justification column did not defend
      itself. Citing the specific rule in the audit reason string is
      tracked separately in `docs/BACKLOG.md`, folded into Unit M's
      plumbing rather than done twice
- [x] Baseline comparison in Reporting: what a naive "retry everything three
      times, nudge everything" policy would have recovered against the same
      sealed ground truth, so "measured money recovered" is a result rather
      than a number → `docs/PHASE5_IMPLEMENTATION.md` Unit K
- [ ] Surface decision provenance the system already stores but never shows:
      provider hop chain, net recovered and cost per rupee, uneconomic-closed
      as its own tile, the classification confusion matrix, and a live
      `VerifyInvariants` result. Backend half is the Gateway routes; the UI
      half is **[FRONTEND]** F3 → `docs/PHASE5_IMPLEMENTATION.md` Unit L
- [ ] **[FRONTEND]** Error handling, which does not exist anywhere in `web/`
      today: no `.catch` on any call, no error boundary, no empty state, and
      a 2 second `setInterval` with no try/catch that will throw an unhandled
      rejection every two seconds behind a blank page if the backend 404s.
      The difference between degrading visibly and dying silently on stage
      → `docs/PHASE5_IMPLEMENTATION.md` "The frontend track" F2
- [x] Persist the full EV candidate ranking and the per-action guardrail
      refusal reasons, both previously computed and discarded, so the audit
      trail can answer "why not the alternatives". New `audit_entry.decision_trace`
      column (migration 00006), attached only to the audit row where a
      scoring decision actually happened
      → `docs/PHASE5_IMPLEMENTATION.md` Unit M
- [x] Correct three stale claims in checked-in files that currently accuse
      the codebase of defects it does not have. Done in the same PR that
      recorded the rubric audit, before this checklist item existed as its
      own line, the box was just never ticked
      → `docs/PHASE5_IMPLEMENTATION.md` Unit N

### Demo-readiness remediation, found 2026-08-31 by running the product end to end

> Detail and root causes in `docs/PHASE5_IMPLEMENTATION.md` "Demo-readiness
> remediation", incidents in `docs/INCIDENTS.md` 2026-08-31. P and Q are
> confirmed defects; T is deliberately blocked until P is fixed.

- [x] **Unit P: stop scaling `RecoveryWindow`.** `DEMO_TIME_SCALE=300000`
      compresses the 7-day window to 2.016s, and it is compared against real
      wall-clock record age, so 73 of 100 records were escalated for "recovery
      window closed" before economics ever priced them. Every other scaled
      duration is a future wait and stays scaled
      → `docs/PHASE5_IMPLEMENTATION.md` Unit P
- [x] **Unit Q: make `configs/demo.env` actually apply.** The Makefile's
      `include .env` overrides the environment, so sourcing the profile is
      silently discarded. Add a `PROFILE` variable, and correct the profile's
      `LLM_PROVIDER_CHAIN` to `groq,rules` per `DECISIONS.md` 2026-08-28
      → `docs/PHASE5_IMPLEMENTATION.md` Unit Q
- [x] **Unit R: `make demo-up` / `make demo-down`**, plus correct `README.md`'s
      demo section, which currently documents a method Unit Q proves does
      nothing → `docs/PHASE5_IMPLEMENTATION.md` Unit R
- [x] **Unit S: surface `decision_trace`.** Written by Unit M, populated, and
      read by nothing: no proto, no route, no component. The EV candidate
      ranking and guardrail refusal reasons are the "every money action
      explainable" artifact and are currently invisible
      → `docs/PHASE5_IMPLEMENTATION.md` Unit S. **Done**: added a typed
      `DecisionTrace` message to `AuditEntry` rather than passing through
      the raw JSONB column as a string; documented it on the frozen
      `docs/API_GATEWAY.md` contract first, per that document's own "fix it
      here first" rule; wired the Audit store and Gateway to read and map
      it; and built an always-visible "why this action" panel under each
      decision entry, ranked candidates first (winner marked, derived by
      value rather than by parsing `reason`), guardrail-blocked actions in
      their own separate section → `docs/DECISIONS.md` 2026-09-02.
- [ ] **Unit T: re-measure after P, then decide on the three modelling
      questions** (memoryless outcomes reward spam; one
      `wrong_action_probability` for every wrong action; escalation costs
      Rs 18 and recovers nothing). Not actionable against a batch where 73% of
      records never reached the scorer
      → `docs/PHASE5_IMPLEMENTATION.md` Unit T

## Phase 5.5: Demo control surface and Razorpay integration depth

> Working breakdown and per-unit detail: **`docs/PHASE5_5_IMPLEMENTATION.md`**
> (6 units, U to Z). Added 2026-08-31, after running the product end to end
> showed that Phase 5 made the system *work* without making it *legible*.
>
> Two tracks that share no files: U, V, W, X (the control surface) and Z, Y
> (Razorpay depth). U is first regardless, it is a correctness defect.

- [x] **Unit U: dead-letter unprocessable records instead of crashing.** One
      Kafka message referencing a deleted record kills the decision-engine
      permanently, and every restart dies on the next poisoned offset. A
      missing record is a permanent data condition being reported as a
      transient infrastructure error, so `ConsumeKeyed` treats it as fatal
      instead of dead-lettering. Violates `PRD.md` §10 ("no poison record
      stalls the pipeline"). Includes `make demo-reset` and a README warning
      that `make test-integration` poisons a live demo stack's topic
      → `docs/PHASE5_5_IMPLEMENTATION.md` Unit U, `docs/INCIDENTS.md` 2026-08-31
- [x] **Unit V: extract batchgen's generation logic into an importable
      package.** It is `package main` today, so nothing can reuse the
      synthetic-record model. Pure refactor, no behaviour change. Landed as
      `internal/platform/syntheticgen`, same precedent as `kafkax` and
      `hopcodec`; `scripts/batchgen` kept its CLI flags and Postgres/Kafka
      writes and became a thin caller
      → `docs/PHASE5_5_IMPLEMENTATION.md` Unit V
- [x] **Unit W: `/v1/demo/*` control API**, flag-gated behind
      `DEMO_CONTROLS_ENABLED` (default false, routes not registered at all
      when off, a 404 rather than a 403) and proxied through the Gateway to
      World Simulator, so neither the dashboard-talks-only-to-the-Gateway
      rule nor the only-`demo/`-writes-`GROUND_TRUTH` rule is broken.
      `POST /v1/demo/batches` (scenario presets `normal`/`bank-outage`/
      `salary-day`/`dead-cards`, each a distribution over
      `internal/platform/syntheticgen`, using only real Razorpay failure
      codes), `GET /v1/demo/scenarios`, `GET /v1/demo/world`, and
      `POST /v1/demo/inject-poison`. Batch seeding is implemented on World
      Simulator, not the Gateway, since only a `demo/` component may write
      `GROUND_TRUTH`; the Gateway only proxies over gRPC and gained no
      database handle. Downtime control (`POST /v1/demo/downtime`) is
      explicitly deferred to Unit Y, which owns the payload shape
      → `docs/PHASE5_5_IMPLEMENTATION.md` Unit W
- [x] **[FRONTEND] Unit X: the demo control panel**, replacing the "Generate
      Sample Data" button rather than patching it. That button is Phase 0
      leftover: two of its five failure codes no longer exist since Unit I, and
      because it goes through the public API its batches can never carry ground
      truth, so accuracy and the baseline comparison are both absent from the
      most obvious control on the screen. New `DemoControlPanel`, reached from
      a Dashboard/Demo Controls tab in the header: seed a batch (scenario
      picker populated from `GET /v1/demo/scenarios`, count, optional seed;
      selects the new batch in the dashboard on success), World Simulator
      state (`GET /v1/demo/world`, never surfaced anywhere before this), a
      poison-record injector, and a plain-English explanation of the World
      Simulator distinct from the dashboard's own mock mode. Detects a
      disabled `DEMO_CONTROLS_ENABLED` (all four routes 404 identically) via a
      `DemoControlsDisabledError` and shows a "start the stack with
      PROFILE=demo" message instead of a broken panel. All four routes mocked
      in `mockEngine.ts` per `web/AGENTS.md`, honestly labelled as browser-side
      fabrication since only the World Simulator can write real `GROUND_TRUTH`
      → `docs/PHASE5_5_IMPLEMENTATION.md` Unit X
- [x] **Unit Y: payment-downtime webhooks and outage-aware retry.** Razorpay
      publishes `payment.downtime.started`/`.updated`/`.resolved` with
      severity, method and scheduled windows. Consume them and defer retries
      into a known issuer outage, as a guardrail alongside TRAI and RBI. This
      is the "bank health" capability two reviews asked for, except it is
      Razorpay's own published signal rather than an invented one.
      **Shipped, deliberately scoped down.** New route
      `POST /v1/webhooks/payment-downtime` matched to Razorpay's real
      documented payload (nested `payload["payment.downtime"]["entity"]`,
      unix-seconds `begin`/`end`, nullable `end`, open-string `severity`, a
      varying `instrument` shape resolved via `instrument_schema`), signature
      verification deliberately NOT done (Unit Z's job). A new
      `payment_downtime` table (migration 00008), owned by the Decision
      Engine, holds one row per Razorpay downtime id. `guardrails.go` gained
      `applyDowntimeGuardrail`, kept separate from `applyGuardrails` so the
      existing guardrail rules and their tests are untouched: it blocks
      RETRY only, never nudges, with reason
      `bank downtime active: <method> <instrument>, severity <severity>`.
      Unlike the other guardrails a downtime-blocked retry that is still
      genuinely worth running is DEFERRED (scheduled normally) rather than
      escalated; resuming on `.resolved` needs nothing beyond the next
      guardrail check reading the row fresh, since nothing was ever put to
      sleep. A safety-valve cap (`downtimeMaxUnresolvedHold`, 6h) keeps an
      unplanned outage from stranding a record if `.resolved` never arrives.
      **Scoped out**: pre-empting an already-scheduled retry's `due_at` at
      claim time (so one attempt can still fire into an outage that started
      after the record was scheduled, self-correcting on its next
      re-score), and severity-tiered treatment (medium vs high). Full
      reasoning in `docs/DECISIONS.md`
      → `docs/PHASE5_5_IMPLEMENTATION.md` Unit Y
- [x] **Unit Z: webhook signature verification and the four-field error
      taxonomy.** Verify `X-Razorpay-Signature` (HMAC-SHA256 over the RAW
      body, verified before decoding, `hmac.Equal` for constant-time
      comparison) on both webhook routes, and accept Razorpay's four
      additional error fields (`error_code`, `error_description`,
      `error_source`, `error_step`, `error_reason`) alongside the existing
      `failure_code`. **Shipped.** Signature verification is one shared
      middleware (`verifyWebhookSignature`, `services/api-gateway/internal/
      httpapi/signature.go`) wrapping both `payment-failed` and
      `payment-downtime`, backed by a new `internal/platform/webhooksig`
      package (`Sign`/`Verify`) so `services/api-gateway` and
      `scripts/loadgen` share one implementation of "valid" rather than two
      that could drift. `WEBHOOK_SECRET` is required at startup, same
      requiredness as `API_KEY`: an unset secret is a startup failure, not a
      per-request bypass, because a Gateway that started anyway would accept
      every request into these routes only to fail-closed on literally all
      of them. `WEBHOOK_SECRET_PREVIOUS` is optional, for rotation.
      `scripts/loadgen` now signs its own traffic (`-webhook-secret` /
      `$WEBHOOK_SECRET`), so `make loadgen` keeps working with no bypass on
      the Gateway side. The four new fields are additive on
      `common.v1.Record`/`ingestion.v1.NewRecord` (migration 00009 adds the
      matching nullable `record` columns) and flow through to the
      classifier's rules engine: `bucketForErrorTaxonomy`
      (`services/classifier/internal/rules/buckets.go`) is consulted only
      when `failure_code` itself is unrecognised, matching `error_step`
      containing "AUTHENTICATION" to `USER_ACTION_NEEDED` and "AUTHORIZATION"
      paired with a systemic `error_source` (bank/gateway/network/razorpay/
      issuer) to `TRANSIENT_BANK`, the worked example this unit was scoped
      around. Both fields stay open string vocabularies (substring matching,
      no closed enum) since Razorpay's own docs say the possible values vary
      by payment method. **Scoped out** (see `docs/DECISIONS.md`): switching
      `payment-failed` to Razorpay's real nested payload shape (kept the
      existing flat wire body, additive fields only); surfacing the new
      fields in `web/`, Reporting or Audit (stored and used by the
      classifier only); a closed enum or validation for `error_source`/
      `error_step`; any change to the LLM prompt or provider chain.
      **This closes P2.**
      → `docs/PHASE5_5_IMPLEMENTATION.md` Unit Z

- [x] **Unit AA: surface `due_at` through the stack.** A record's scheduled
      time appears nowhere: not in the Reporting proto, not in
      `docs/API_GATEWAY.md`, not in `web/`. So cause-aware retry timing, the
      most distinctive behaviour this system has, is made correctly, recorded
      correctly, and shown to nobody. Additive field, existing indexed column,
      no migration. Render as a live countdown, not a timestamp
      → `docs/PHASE5_5_IMPLEMENTATION.md` Unit AA
- [x] **[FRONTEND] Unit AB: timeline view.** Pending records plotted on a time
      axis by `due_at`, grouped by root cause, so insufficient-funds records
      visibly cluster at the salary window while bank timeouts fire
      immediately. Turns the scheduling policy from a claim in a doc into a
      shape on a chart → `docs/PHASE5_5_IMPLEMENTATION.md` Unit AB

## Phase 5.6: Demo readiness

> **Prioritised list with full detail: `docs/DEMO_READINESS.md`.** Found on
> 2026-09-02 by running the product end to end and reading the dashboard in a
> real browser, rather than by planning. Everything there is either a confirmed
> defect or a capability that already exists in the backend and cannot be seen.
>
> Work it top to bottom. P0 items are demo-breaking or embarrassing; P1 items
> are already built and merely invisible, which makes them the best value per
> hour in the project.

- [x] **P0, ~10h. Complete 2026-09-02, all five units merged.** AC nudge
      messages written to a column nothing reads (134 composed messages
      unreachable through the API), fixed by writing `message_text` onto the
      outcome audit entry in the same transaction (#100); AD seed the World
      Simulator, **partially fixed** (#99, then #104): rolls now derive from
      a `roll_key` stored on GROUND_TRUTH rather than a random uuid, and
      generation, ground truth and economics reproduce exactly, but the TRAI
      contact-hour guardrail on a 300000x-compressed clock plus live LLM
      sampling still move 9 records in 100. A seed does not reproduce a run
      end to end, see `docs/DEMO_READINESS.md` Unit AD;
      AE LLM messages leak internal enum names into customer copy, fixed with
      a validator built from the generated proto enum maps, not just a prompt
      instruction (#98); AF infinite skeleton loaders when no batch exists,
      fixed with a real three-way empty/loading/loaded state and a shared
      `EmptyState` component (#101); AG a red "Disconnected" badge on a
      healthy system, which turned out to be three defects rather than a
      label bug, fixed with reconnect backoff and honest close-code handling
      (#101) → `docs/DEMO_READINESS.md`
- [x] **P1, ~16h. Complete 2026-09-03, all four units done. This closes
      P1.** Unit S surface `decision_trace` (the "why not the
      alternatives" table, still the highest-value single item), **done**
      2026-09-02, see `docs/PLAN.md`'s own Unit S line above and
      `docs/DECISIONS.md` 2026-09-02; AH historical
      timeline plus real-vs-relative time, **done** 2026-09-02: `RecordSummary`
      gained `first_action_at`/`last_action_at`, `docs/API_GATEWAY.md` edited
      first per the Unit S precedent, timeline split into a Live/History
      toggle with a real-vs-simulated-time axis on History, see
      `docs/DEMO_READINESS.md` Unit AH and `docs/DECISIONS.md` 2026-09-02;
      AI confidence-based LLM routing with
      a quota-exhausted banner, **done** 2026-09-03, see `docs/PLAN.md`'s own
      Unit AI line below; AJ live production stream via a webhook CLI,
      which fills the dead event panel and makes the no-answer-key distinction
      explanatory, **done** 2026-09-03: the CLI landed as `scripts/loadgen`
      (reserved since Phase 6 for exactly this job, unbuilt until now, so
      this closes that gap rather than adding a third generator), posting
      steady, `syntheticgen`-drawn traffic at `/v1/webhooks/payment-failed`
      with no ground truth by design; the dashboard's Classification
      Accuracy panel and `BaselineComparisonCard` now share one explanation
      (`web/src/lib/groundTruth.ts`) for a batch with none, using
      `BatchSummary.source` already on the wire, no `docs/API_GATEWAY.md`
      change needed → `docs/DEMO_READINESS.md` Unit AJ, `docs/DECISIONS.md`
      2026-09-03
- [x] **[FRONTEND] Unit AN: redesign the record drawer, show real time
      against simulated time.** Added after the rest of Phase 5.6 was
      planned. Widened `max-w-lg` (512px) to `max-w-3xl` (768px), the
      container Unit S's decision panel and the audit trail had already
      outgrown; sticky header so a long trail never scrolls the record id,
      amount and state out of view; the trail redrawn with a real spine,
      one line with a node per entry coloured by `to_state`; repetitive
      metadata (`actor`, `source: SOURCE_UNSPECIFIED`) demoted to quiet
      text while the two sources that vary, `SOURCE_LLM` and
      `SOURCE_RULES_FALLBACK`, get a small badge. Each entry now also shows
      what its timestamp represents in the 7-day recovery window and the
      elapsed gap since the previous entry in both real and simulated
      terms, reusing `web/src/lib/demoTime.ts` (Unit AH) rather than a
      second implementation, with a new `formatSimulatedGap` function
      added test-first. No backend change → `docs/DEMO_READINESS.md` Unit
      AN, `docs/DECISIONS.md` 2026-09-02.
- [x] **[FRONTEND] Unit AO: refine and make the timeline interactive.**
      Added after Unit AH shipped, on review of the working feature.
      `HistoryTimeline` gave every record its own thin sub-row inside its
      bucket band, a small-multiples/Gantt layout instead of one shared row
      per bucket, so a dense bucket's connector lines (28 Abandonment
      records, 16 Hard Decline) no longer merge into a solid band. The
      connector is now a neutral slate rather than the bucket colour, so
      the state colour on the marker is the only meaningful hue; the
      `circle size = amount at risk` caption's contrast was fixed
      (`text-slate-300` to `text-slate-500`). Added click-to-filter:
      clicking a bucket row isolates it (the other buckets collapse to a
      one-line summary rather than vanishing, so switching focus stays one
      click), clicking an outcome in the legend filters to it, both
      compose, and hovering a record highlights its line and marker while
      dimming the rest. A filtered-empty combination renders the shared
      `EmptyState` rather than a blank panel, with the active-filter chips
      and a clear-filters control staying visible above it. The record
      area is capped at a max height with internal scrolling rather than
      growing unboundedly. No backend change was needed. Also fixed in the
      same unit: `DecisionTracePanel`'s EV value no longer wraps
      (`whitespace-nowrap`), and `RecordDrawer` suppresses a rationale box
      that repeats the immediately previous entry's rationale verbatim,
      keeping it when it differs or when the repeat is not consecutive →
      `docs/DEMO_READINESS.md` Unit AO, `docs/DECISIONS.md` 2026-09-02.
- [x] **[FRONTEND] Unit AP: restore information density to the timeline.**
      Added 2026-09-03 after the user reviewed Unit AO's shipped per-record
      Gantt directly and rejected it as the default: "too much scrolling and
      so gapped... the initial view of the last one was better, it gave a
      better idea in one view". `HistoryTimeline` now opens on a Compact
      view, one fixed-height row per bucket with jittered points (Unit AH's
      original layout, recovered from git history rather than reinvented),
      so the chart's height never depends on record count and needs no
      scrolling at typical batch density (80-100 records, 7 buckets). Unit
      AO's actual fixes are kept, not reverted: the neutral connector
      colour, the caption contrast fix, click-a-bucket-to-isolate,
      click-a-legend-outcome-to-filter (composing), hover-to-highlight, and
      the filter chips. Unit AO's per-record layout survives as an opt-in
      "Per-record" toggle beside "Compact", reached deliberately rather than
      shown first, which is what the user suggested directly ("an option to
      see the gantt chart"). Search was added (record id or amount, both
      substring-matched against what the drawer and this chart already
      display), narrowing the view to the match through the same isolate
      mechanism the other filters use, with an honest empty state naming
      the query when nothing matches. `amountRadius`
      (`web/src/lib/timelineGeometry.ts`) gained optional bounds parameters
      so Compact's taller row and Per-record's thin sub-row each get a
      marker scale tuned to their own row height rather than sharing one.
      No backend change was needed. → `docs/DEMO_READINESS.md` Unit AP,
      `docs/DECISIONS.md` 2026-09-03.
- [x] **Unit AI: route LLM calls by confidence, keep `LLM_SAMPLE_RATE` as a
      ceiling, and surface quota exhaustion.** Done 2026-09-03. `clients.go`
      now always asks the deterministic rules engine first
      (`force_rules_only=true`, free, cannot fail) and only places a second,
      live-model call when that answer's `Confidence` is below the new
      `LLM_ROUTE_CONFIDENCE_THRESHOLD` (default 0.0, so an unconfigured
      deployment routes nothing to a live model, same shape as
      `LLM_SAMPLE_RATE`'s own zero default); `configs/demo.env` sets it to
      0.80, chosen by reading `rules/actions.go`'s actual confidence table
      rather than a round number. The old hash-of-`record_id` `sampledForLLM`
      (Phase 3 Unit H) is deleted; `LLM_SAMPLE_RATE` is reinterpreted as a
      running-ratio ceiling (`llm_budget.go`'s `llmBudget`, ratio computed
      over every classified record, not only the ambiguous ones) rather than
      a selector, because the Decision Engine consumes `raw.events` as a
      stream and never sees a whole batch to rank before deciding. An
      ambiguous record denied by the ceiling still gets the rules answer, now
      tagged with a `sample_budget:exhausted` hop. Reporting counts, per
      batch, every record whose first classification carries a
      `rate_limited`, `circuit_open` or `exhausted` hop
      (`services/reporting/internal/server/exhaustion.go`) as
      `llm_quota_exhausted_count` on `BatchReport`, documented in
      `docs/API_GATEWAY.md` before implementation per its own frozen-doc
      rule, proxied through the Gateway field-for-field, and rendered as a
      quiet slate `LlmQuotaBanner` (hidden entirely at 0) rather than an
      alarm. Decision ordering is unchanged: routing only changes which
      records the model gets to see, `engine.go`'s `decide` still reads only
      `Bucket` off the winning response → `docs/DEMO_READINESS.md` Unit AI,
      `docs/DECISIONS.md` 2026-09-03.
- [x] **P2, ~11h. Complete, closed 2026-09-03.** Unit Y Razorpay
      payment-downtime webhooks; Unit Z webhook signature verification and
      the four-field error taxonomy → `docs/PHASE5_5_IMPLEMENTATION.md`,
      `docs/DEMO_READINESS.md`
- [x] **P3, ~6h, done 2026-09-03.** AK `/help` page from the frozen contract
      2026-09-03: GET /v1/help content negotiates, a browser gets a
      rendered FastAPI-/docs-shaped page (native <details> accordion, no
      JavaScript), everything else keeps the JSON, both built from one
      helpRoutes slice; AL misleading labels, **done** 2026-09-03: two of
      three confirmed live and fixed ("In flight / lost" renamed to "Not
      recovered", the structurally-always-zero "Escalations" tally
      removed rather than relabelled), the third (confusion matrix
      showing only 3 of 7 buckets) checked and found not real, the panel
      is genuinely scrollable; AM read-only config panel, see its own
      line below
      → `docs/DEMO_READINESS.md`
- [x] **AM read-only config panel, done 2026-09-03.** New `GetAgentConfig`
      RPC on the Decision Engine's own gRPC service returns the guardrail
      and LLM-routing values it already loaded and validated at startup;
      `GET /v1/demo/config` proxies it through the Gateway, gated on
      `DEMO_CONTROLS_ENABLED` like every other `/v1/demo/*` route, rather
      than the Gateway re-reading `os.Getenv` and risking drift. Shown as a
      new "Agent configuration" section on the Demo Controls page, grouped
      into time compression, retry/contact limits and LLM routing, marked
      fixed at startup. `.env.example` recounted fresh: **60** variables,
      not the 56 the original brief stated; `LLM_PROVIDER_CHAIN` left out
      as Classifier-owned, out of scope for a contained change
      → `docs/DEMO_READINESS.md`, `docs/DECISIONS.md` 2026-09-03

## Phase 6: Load testing & performance validation

- [x] `scripts/loadgen` built, synthetic mode default (no real LLM calls).
      Built 2026-09-03 for Unit AJ (`docs/DEMO_READINESS.md`), a narrower
      scope than the rest of this phase: steady traffic through the public
      webhook API with no ramp/peak profile and no latency measurement, see
      the three items below, which remain unclaimed and explicitly skipped
      for the hackathon per this doc's own P1 status note
      → `ARCHITECTURE.md` §5, §14
- [ ] Baseline/ramp/peak load profile run against the docker-compose stack
      → `ARCHITECTURE.md` §14
- [ ] Real p50/p95/p99 latency and sustained throughput measured, used to
      replace the starting targets in `PRD.md` §10
- [ ] Worker pool size calibrated against measured throughput, and
      `raw.events` partition count set above expected pod count so the
      demo never hits the partition ceiling
      → `ARCHITECTURE.md` §8a, §12

## Phase 7: Kubernetes / minikube deployment

- [ ] Dockerfile per service → `ARCHITECTURE.md` §15
- [ ] k8s manifests/Helm chart, headless Services for gRPC client-side load
      balancing, HPA on Classifier + Executor → `ARCHITECTURE.md` §12, §15
- [ ] Infra deps (Kafka/Postgres/Redis) deployed in minikube
      → `ARCHITECTURE.md` §15
- [ ] Load test re-run against the minikube deployment, HPA scaling observed
      live → `ARCHITECTURE.md` §14, §15, `PRD.md` §12

## Phase 8: Demo rehearsal & polish

- [ ] Full demo script (`PRD.md` §12) rehearsed end to end, timing checked
- [ ] Dashboard/judge-facing polish
- [ ] Out-of-scope list (`ARCHITECTURE.md` §16) double-checked so nothing is
      claimed in the demo that wasn't actually built
- [ ] **Live failure injection rehearsed**, scripted so it can be triggered
      on stage without improvising. "Failure Recovery: how you identified
      system failures at runtime and engineered graceful fallbacks" is an
      explicit judging criterion (`PRD.md` §0) and this is the only beat that
      answers it by *doing* rather than describing. Two candidates, both
      already backed by passing tests, so the work is rehearsal and a script,
      not new code:
      (a) SIGKILL the Decision Engine mid-batch and show no record lost and no
      audit gap, the property `test/e2e/crash_safety_test.go` already proves;
      (b) raise `LLM_SAMPLE_RATE` to deliberately trip Groq's real rate limit
      and show a genuine on-stage failover with real `rate_limited` hops in
      the audit trail, which `configs/demo.env` already anticipates in its own
      comments → `PRD.md` §12 beat 5
- [ ] Pick the drill-down record in advance. LLM sampling is a deterministic
      hash of `record_id` (`DECISIONS.md` 2026-08-28), so which records get a
      live model call is knowable before the demo rather than luck. Beat 4
      should land on a record that actually has an LLM rationale, and beat 5
      on one that actually fell back → `PRD.md` §12 beats 4, 5
- [ ] Confirm `VITE_API_BASE_URL` is set so the dashboard is serving live
      data rather than the Phase 0 scaffold, and that every number on screen
      came through the pipeline → `PRD.md` §12a
