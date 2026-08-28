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
> **`docs/PHASE3_IMPLEMENTATION.md`** (7 units, A to G). That document also
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
- [ ] Fallback path deliberately tested (simulate timeout/error per
      provider, confirm the chain falls through correctly and every hop
      tried is recorded) → `ARCHITECTURE.md` §5,
      `docs/PHASE3_IMPLEMENTATION.md` Unit C
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
- [ ] Rationale stored and retrievable from the audit trail for a full
      record → `PRD.md` §2a
      Note (found while planning Phase 3): the rationale half of this is
      already done and has been since Phase 1 (decision-engine `store.go`
      writes it, audit `store.go` reads it back, `GetRecordAudit` returns it,
      two tests assert it). The part genuinely missing is
      `ClassifyResponse.hops`, which the chain computes and nothing persists,
      so the trail cannot show *which* provider answered or what the ones
      before it did. Replaced by `docs/PHASE3_IMPLEMENTATION.md` Unit E
      (migration + proto + code, three sequenced PRs).
- [ ] (unplanned, found while planning Phase 3) populate
      `ClassifyRequest.history` and `instrument_history` from the Decision
      Engine. Both have been empty since Phase 1
      (`services/classifier/SPEC.md` §3 documented it and §10 item 1 raised it
      as a cross-service item), so a model rung would see exactly the two
      inputs the rules table sees and could not improve on it. Without this,
      the whole reasoning layer is decorative
      → `docs/PHASE3_IMPLEMENTATION.md` Unit F
- [ ] (unplanned, found while planning Phase 3) enforce the classification
      confidence threshold in the Decision Engine. `classifier.proto`
      documents it, `ARCHITECTURE.md` §5 assigns it here, and both
      `engine/state.go` and `engine/engine.go` already carry comments saying a
      low-confidence classification is a safety call that bypasses pricing,
      but no code reads `confidence`. Harmless while it is a table constant,
      a real gap once a model produces it
      → `docs/PHASE3_IMPLEMENTATION.md` Unit G

## Phase 4: Observability

- [ ] Prometheus metrics in every service via a shared gRPC interceptor plus
      Kafka consumer lag exporter → `ARCHITECTURE.md` §13
- [ ] OpenTelemetry tracing across gRPC + Kafka hops, `record_id` as trace id
      → `ARCHITECTURE.md` §13
- [ ] Structured logging correlated by `record_id`/`trace_id`
      → `ARCHITECTURE.md` §13
- [ ] Alertmanager rules (consumer lag, LLM fallback rate, stopping-rule
      violation) → `ARCHITECTURE.md` §13
- [ ] Grafana dashboards, per-service and business metrics
      → `ARCHITECTURE.md` §13

## Phase 5: Demonstration realism

- [ ] Synthetic batch generator: realistic failure codes/amounts, seeded
      hidden ground-truth recoverability profile per record, written
      straight into `BATCH`/`RECORD`/`GROUND_TRUTH` → `ARCHITECTURE.md` §6, §10
- [ ] World Simulator upgraded from Phase 1's stub to the full probabilistic
      outcome model reading ground truth, including the Redis-backed
      delayed-outcome queue and its `ReportDelayedOutcome` callback into
      Decision Engine for nudge-type actions → `ARCHITECTURE.md` §6
- [ ] Reporting Service: at-risk amount, gross + **net** recovered
      (after logged intervention spend), cost per rupee recovered,
      uneconomic-closed count/value shown separately from escalations,
      recovery rate by bucket/intervention, classification accuracy vs.
      ground truth, all scoped by `batch_id`, plus the `StreamBatchUpdates`
      server-streaming gRPC method
      → `PRD.md` §8, §9, §2b, `ARCHITECTURE.md` §10, §6a
- [ ] API Gateway: WebSocket relay (`WS /v1/batches/{batch_id}/live`) wired
      to `StreamBatchUpdates` → `ARCHITECTURE.md` §6a, `docs/API_GATEWAY.md`
- [ ] Dashboard: recovered amount/rate, record table, one record's audit
      drill-down, live feed via the WebSocket above, built only against
      `docs/API_GATEWAY.md` → `PRD.md` §1, `web/AGENTS.md`
- [ ] Hinglish nudge composition: `Classifier.ComposeNudge` reusing the
      existing provider chain and circuit breakers, static Hinglish
      template per bucket as fallback, output length-capped and validated
      (no model-invented amounts or dates), text stored on
      `INTERVENTION_ATTEMPT.message_text` and surfaced in the audit trail.
      Needs a small additive migration for the two new columns
      → `ARCHITECTURE.md` §5b, `PRD.md` §1 feature 5
- [ ] Demo-time scale factor (config knob, `ARCHITECTURE.md` §17) wired
      through cooldowns and World Simulator's delay/tick timing, so a live
      demo run finishes in minutes, not hours

## Phase 6: Load testing & performance validation

- [ ] `scripts/loadgen` built, synthetic mode default (no real LLM calls)
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
