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
      *(Not yet pushed to the remote.)*
- [ ] Single root `go.mod`, module path fixed (e.g.
      `github.com/thisizaro/Momotaro`), Go version pinned
      → `ARCHITECTURE.md` §2a
- [ ] Top-level layout created: `services/` (7 product services, each with
      `cmd/`, `internal/`, `Dockerfile`, `AGENTS.md`), `demo/` (same shape,
      hackathon-only stand-ins), `internal/platform/`, `proto/`,
      `migrations/`, `web/` → `AGENTS.md` "Layout", `ARCHITECTURE.md` §2a, §3b
- [ ] Scaffold script in `scripts/` that generates that structure. Run
      **once** by the orchestrator and committed, not run per agent
- [ ] **Contracts first, before any service code.** `buf` pinned,
      `buf.yaml` + `buf.gen.yaml` committed, `.proto` for API Gateway,
      Ingestion, Classifier, Executor, World Simulator
      (`RecoveryActionPort`), Notification Simulator (`NotificationPort`),
      Reporting (including `StreamBatchUpdates`), and Decision Engine
      (`ReportDelayedOutcome`). Conventions in §9: one file per service,
      versioned package, dedicated Request/Response per RPC, enum zero
      values, `int64` paise for money
      → `ARCHITECTURE.md` §9, §2a, §3b, §6a
- [ ] `proto/gen/` generated and committed; `buf lint` + `buf breaking`
      wired into CI → `ARCHITECTURE.md` §9
- [ ] `migrations/` set up with a pinned tool (goose or golang-migrate),
      first migration includes `BATCH`, `RECORD.batch_id`,
      `RECORD_STATE.due_at`, and the `UNIQUE (record_id, attempt_number)`
      constraint on `INTERVENTION_ATTEMPT`, none of it bolted on later
      → `ARCHITECTURE.md` §10, §11, §12a
- [x] `docs/ENGINEERING.md` written and its Definition of Done adopted as
      the gate for every checkbox in this file (referenced from this file's
      header and from `AGENTS.md`). Each agent still has to actually read it.
- [ ] Shared internal packages built once, before services need them, so
      seven agents don't write seven versions: `Clock` interface,
      structured logger, config loader with fail-fast validation, gRPC
      interceptors (metrics/tracing/recovery), graceful-shutdown helper
      → `ENGINEERING.md` §2, §3, §5, §6, §9
- [ ] `docker-compose.yml` for local dev: Postgres, Redis, Kafka
      (single-broker/KRaft is fine), plus a Kafka UI (redpanda console or
      kafdrop) for inspecting topics by hand → `ARCHITECTURE.md` §3, §8
- [ ] `.env.example` checked in, real `.env` gitignored, includes the static
      demo API key → `AGENTS.md` "Secrets and config", `ARCHITECTURE.md` §17
- [ ] Basic CI: GitHub Actions, build + unit test on every push/PR,
      path-filtered per service once services exist → `AGENTS.md`
      "Branching and CI conventions"
- [x] `AGENTS.md` "Testing conventions" and "Secrets and config" sections
      in place before any service code lands
- [ ] `web/` scaffolded (framework choice open) against the already-written
      `docs/API_GATEWAY.md` contract, can start in parallel with backend
      work using mocked responses → `web/AGENTS.md`
- [ ] One `Dockerfile` per service, multi-stage, build context = repo root,
      one binary per image. Verify each image builds before fan-out
      → `ARCHITECTURE.md` §2a
- [ ] Per-service `AGENTS.md` written from a shared template: what this
      service owns, which tables it may write vs. only read (§10a), its
      proto as interface source of truth, what it must not touch, and how
      to request a change from another service
- [ ] **Walking skeleton**: one record end to end through all 7 services
      with everything hardcoded (fixed classification, stub outcome, no
      worker pool, no scheduler, no economics) reaching `Recovered` with an
      audit row. This proves integration *before* any agent builds depth,
      and is the single biggest de-risking step in the plan

## Phase 1: Core pipeline skeleton (prove the loop end to end)

- [ ] API Gateway: real Go service, static API key check, basic rate
      limiting, and both entry points routed to Ingestion over gRPC:
      `POST /v1/batches` (demo/backfill) and
      `POST /v1/webhooks/payment-failed` (production shape, one event at a
      time) → `ARCHITECTURE.md` §0a, §3a, `docs/API_GATEWAY.md`
- [ ] Ingestion: gRPC `SubmitBatch(records[]) -> {batch_id}` and
      `SubmitEvent(record) -> {record_id}`, both converging on the same
      `raw.events` publish path so nothing downstream can tell them apart.
      Creates the `BATCH` row, stamps `batch_id` on every message
      → `ARCHITECTURE.md` §0a, §3, §4, §10
- [ ] Decision Engine: consumes `raw.events` via a **keyed worker pool**
      (not one-at-a-time) with contiguous-prefix offset commits, calls
      Classifier + Executor via gRPC, owns the state machine, writes
      `RECORD_STATE` and `AUDIT_ENTRY` **in one transaction**
      → `ARCHITECTURE.md` §4, §7, §8a, §10a
- [ ] Decision Engine scheduler worker: `due_at` polling with
      `FOR UPDATE SKIP LOCKED`, so cooldowns and cause-aware retry timing
      actually fire. Nothing time-based works without this
      → `ARCHITECTURE.md` §7a
- [ ] DLQ path: bounded processing retries, then publish to
      `raw.events.dlq` with the failure reason and commit the offset, so one
      poison record cannot wedge a partition → `ARCHITECTURE.md` §8b
- [ ] Classifier: rules-only for now (`source=rules_fallback` always), the
      LLM provider-chain interface stubbed but not wired to a real provider
      yet → `ARCHITECTURE.md` §5, `PRD.md` §2a
- [ ] Executor: **insert-before-execute** idempotency against the
      `UNIQUE (record_id, attempt_number)` constraint (the durable
      guarantee), Redis `SETNX` as the fast path only, calls a minimal
      **stub** outcome source (fixed/scripted responses) via the
      `RecoveryActionPort`/`NotificationPort` interfaces, not the full
      `demo/world-simulator` yet, just enough to prove the state machine
      reaches a terminal state → `ARCHITECTURE.md` §11, upgraded in Phase 5
- [ ] Audit Service: serves a record's audit trail, and runs the continuous
      invariant verifier exporting `stopping_rule_violation_total` and
      `incomplete_audit_trail_total`. It does **not** write audit rows, the
      owning service does that transactionally → `ARCHITECTURE.md` §10a
- [ ] End-to-end smoke test: `POST /v1/batches` with a handful of records,
      watch them reach `Recovered`/`Escalated`, confirm the audit trail is
      complete → `PRD.md` §7, §8

Note: the full World Simulator (hidden ground truth, probabilistic
outcomes, delayed-callback scheduling) is deliberately deferred to Phase 5,
not built here. A stub keeps Phase 1 about proving the pipeline shape, not
the demo's realism. Same for the dashboard and the Gateway's WebSocket
relay, both need Reporting to exist first, so they land in Phase 5 too.

## Phase 2: Durability, safety & economics

- [ ] Idempotency proven end-to-end (duplicate Kafka delivery and duplicate
      gRPC retry both handled safely) → `ARCHITECTURE.md` §11
- [ ] Retry budgets, cooldowns, contact caps enforced with automated tests
      → `PRD.md` §11
- [ ] `configs/intervention_costs.yaml` and the `P(recovery)` prior table
      checked in, with the assumed values documented and defensible
      → `ARCHITECTURE.md` §5a
- [ ] Economics scorer in Decision Engine: `Scoring` state, EV computed per
      allowed action, `ClosedUneconomic` terminal state when no action has
      positive EV → `ARCHITECTURE.md` §5a, §7, `PRD.md` §2b
- [ ] Cause-aware retry scheduling (salary-window for insufficient_funds,
      short backoff for bank_timeout, no retry for hard_decline/risk_hold)
      → `ARCHITECTURE.md` §5a
- [ ] `cost_paise` + EV snapshot persisted per attempt, so net recovered is
      computed from real logged spend, not estimated
      → `ARCHITECTURE.md` §10
- [ ] Test asserting the Decision Engine has **no** query path to
      `GROUND_TRUTH`, the integrity rule from `ARCHITECTURE.md` §5a
- [ ] Correctness invariant tests over a batch run: zero stopping-rule
      violations, 100% audit trail completeness → `PRD.md` §9, §10
- [ ] Re-run safety test: replay the same batch twice, confirm identical
      outcome (no double-processing) → `ARCHITECTURE.md` §11
- [ ] Crash-safety test: kill the Decision Engine mid-batch, restart, assert
      no record lost and no audit gap (this is what the transactional write
      and contiguous-prefix commits exist to guarantee)
      → `ARCHITECTURE.md` §8a, §10a
- [ ] Scheduler test with a fake clock: a record parked with a future
      `due_at` fires exactly once when the clock advances past it, and is
      never double-claimed by two concurrent pods
      → `ARCHITECTURE.md` §7a, `ENGINEERING.md` §2

## Phase 3: Reasoning layer

- [ ] LLM provider(s) decided (cost/rate-limit evaluation still open, see
      `AGENTS.md` locked decisions) and wired into the provider chain
      → `ARCHITECTURE.md` §5
- [ ] Fallback path deliberately tested (simulate timeout/error per
      provider, confirm the chain falls through correctly and every hop
      tried is recorded) → `ARCHITECTURE.md` §5
- [ ] Circuit breaker per provider, with a test proving that a sustained
      provider outage does **not** make every record pay the full timeout
      → `ARCHITECTURE.md` §5
- [ ] Rationale stored and retrievable from the audit trail for a full
      record → `PRD.md` §2a

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
