# Decision log (Momotaro)

Append-only. Newest entries at the bottom. **Agents may append directly**,
this file uses git's `merge=union` driver (see `.gitattributes`) so
concurrent appends merge cleanly instead of conflicting.

Log a decision here when it is load-bearing: something a future agent could
reasonably contradict if they did not know it had been settled. Include the
*why*, not just the what. Do not log routine implementation choices.

Quick reference for what was decided lives in `AGENTS.md` "Locked
decisions"; the full reasoning lives in `docs/PRD.md` and
`docs/ARCHITECTURE.md`. This file is the chronology.

- 2026-08-22: Track 4 scrapped, building Track 3 only.
- 2026-08-22: Backend language is Go.
- 2026-08-22: Diagnosis is hybrid rules+LLM, not pure rules and not pure LLM.
- 2026-08-22: Inter-service comms inside the cluster is gRPC-only; Kafka
  narrowed to two pub/sub topics (`raw.events`, `audit.events`); everything
  else that used to be modeled as a Kafka round trip (classify, execute)
  became a direct gRPC call.
- 2026-08-22: Kubernetes/minikube deployment is a final-phase deliverable,
  not a starting point.
- 2026-08-22: NFR targets and observability/alerting requirements added to
  PRD and Architecture (latency, throughput, correctness invariants,
  metrics/tracing/logging/alerting).
- 2026-08-22: LLM provider decision deferred (cost/rate-limits need
  evaluation first). Classifier design changed from "one LLM + rules
  fallback" to a priority-ordered multi-provider chain ending in rules
  fallback, decided behind a swappable interface so this doesn't block
  building the rest of the pipeline.
- 2026-08-22: Branching/CI confirmed as trunk-based: short-lived branch per
  service/agent, PR into main, CI runs build+tests scoped to touched
  services via path filters.
- 2026-08-22: `docs/PLAN.md` added as the living, freely-reorderable
  phase-by-phase build plan (Phase 0 through Phase 8). Testing conventions
  and secrets/config handling added to this file since Phase 0 depends on
  both being settled before service code lands.
- 2026-08-22: Project named **Momotaro**.
- 2026-08-22: Clarified that the product runs continuously off webhooks in
  production, with batch submit as the demo/backfill path, both permanent
  and converging on the same pipeline (`ARCHITECTURE.md` §0a added). Also
  pinned down that the user is a Razorpay *merchant*, not Razorpay staff.
  Architecture diagram redrawn to put the simulators outside the cluster
  boundary, where the third parties they stand in for actually live.
- 2026-08-22: Intervention economics added as a **core** feature (PRD §2b,
  Architecture §5a): expected-value action selection, `Scoring` state and
  `ClosedUneconomic` terminal state in the state machine, cause-aware retry
  timing, per-attempt cost logging, and net-recovered as the headline
  metric. Built in Phase 2. Carries an explicit integrity rule that the
  decision path never reads the sealed ground truth.
- 2026-08-22: All em dashes removed from docs per standing style rule.
- 2026-08-22: Distributed-systems hardening pass, eight fixes, all in
  Architecture: (A) added the Decision Engine scheduler worker, §7a, without
  which no time-based work ever fired; (B) keyed parallel consumption with
  contiguous-prefix offset commits, §8a, the previous design capped
  throughput ~50x under the NFR; (C) circuit breakers per LLM provider, §5;
  (D) fixed the dual-write problem, §10a, audit entries now written
  transactionally with state changes, Postgres is the sole source of truth,
  Kafka demoted to notification, and the Audit Service repurposed to
  continuous invariant verification; (E) DLQ for poison messages, §8b;
  (F) durable two-layer idempotency, §11; (G) migrations discipline and
  explicit table ownership, §12a and §10a; (H) clock injection, mandated in
  the new `docs/ENGINEERING.md`. Also documented that Decision Engine
  scaling is partition-bound so the HPA demo targets Classifier/Executor.
- 2026-08-22: Repo structure decided (`ARCHITECTURE.md` §2a): single root
  `go.mod`, each service self-contained under `services/<name>/` with its
  own `cmd/`, `internal/`, and `Dockerfile`, one image and one Deployment
  per service, shared code confined to `internal/platform/`. Service
  isolation is enforced by Go's `internal/` visibility rule (cross-service
  import = compile error) rather than by module boundaries, which are a
  distribution concern and would have cost a committed `go.work` kept in
  sync across six machines plus multi-module Docker build contexts. Docker
  build context is the repo root. Extracting a service to its own module
  later remains mechanical.
- 2026-08-22: gRPC/proto toolchain standardised on pinned `buf` with
  `buf lint` + `buf breaking` in CI, plus naming/versioning/field-number
  conventions (`ARCHITECTURE.md` §9), so the proto-PR-first rule is
  mechanically enforced instead of remembered.
- 2026-08-22: Operational rules for concurrent agents: agents stay inside
  their assigned service, and a **walking skeleton** (one record through
  all 7 services, everything hardcoded) is now a Phase 0 gate before any
  agent builds depth. (This entry originally also made `docs/PLAN.md` and
  the decision log orchestrator-only; **superseded** later the same day by
  the union-merge decision below, which removed the need for that
  bottleneck.)
- 2026-08-22: Added `docs/ENGINEERING.md` as mandatory reading before any
  code, covering TDD, clock injection, context deadlines, error handling,
  fail-fast config, graceful shutdown and probes, concurrency bounds, money
  handling (integer paise, never floats), logging, PR hygiene, and a
  Definition of Done that gates every PLAN.md checkbox.
- 2026-08-22: Final gap-closing pass before build start: API Gateway
  confirmed as a real service (`services/api-gateway`), added to Phase 1.
  Main app (`services/`, 7 services) structurally separated from
  hackathon-only stand-ins (`demo/`: world-simulator, notification-simulator)
  wired through a port/interface. `web/` added for the dashboard, which
  talks only to the Gateway, contract captured in new `docs/API_GATEWAY.md`.
  `BATCH`/`batch_id` added to the schema so reports scope to one demo run.
  Delayed nudge outcomes get a concrete mechanism: a Redis-backed delayed
  queue inside World Simulator that calls back into Decision Engine via
  gRPC when due. Live dashboard updates decided as WebSocket-via-Gateway,
  relaying a new Reporting server-streaming gRPC method, not polling.
  Gateway auth decided as a static shared API key, not real user/session
  auth, deliberately, documented alongside other real-world-vs-hackathon
  simplifications in `docs/ARCHITECTURE.md` §17.
- 2026-08-22: Repo initialised as `Momotaro`, single `main` branch, remote
  `github.com/thisizaro/Momotaro`. Decision log split out of `AGENTS.md`
  into this file, and both this file and `docs/PLAN.md` given git's
  `merge=union` driver via `.gitattributes`. Reason: with several agents
  working concurrently, both files are touched by nearly every PR, which
  would guarantee a merge conflict every time under the default driver.
  Union merge keeps both sides' added lines, so agents can tick their own
  checkboxes and append their own decisions with no orchestrator
  bottleneck. Tradeoff accepted: two agents editing the *same* line yields
  a duplicated line, which is trivial to delete and far better than
  constant conflicts. Consequence: agents must add to these files, never
  reorder or restructure them.
- 2026-08-22: Added `docs/ORCHESTRATION.md` holding the multi-agent
  sequencing rule (Phase 0 is sequential and must merge before fan-out,
  `web/` and `scripts/` excepted), the suggested service allocation, and
  reusable templates for the per-agent prompt and the per-service
  `AGENTS.md` boundary contract.
- 2026-08-22: **No AI attribution in commit messages or PR descriptions**,
  anywhere, by anyone. No `Co-Authored-By: Claude`, no "Generated with",
  no equivalent trailer for any tool. This overrides whatever an agent's
  tooling does by default. The three existing commits were rewritten with
  `git filter-branch` to strip the trailer, backup refs deleted and objects
  pruned, before anything was pushed. Rule recorded in
  `docs/ENGINEERING.md` §10, `AGENTS.md` branching conventions, and the
  agent prompt template in `docs/ORCHESTRATION.md` so it reaches every
  agent at the point it matters.
- 2026-08-22: Added `docs/INCIDENTS.md` (union-merged, agents append
  directly) recording what broke and what we did about it. Reason: the
  hackathon judging criteria explicitly assess "Failure recovery: what
  broke, and what you did about it", and that story cannot be reconstructed
  credibly the night before. Distinct from this file, which records what we
  chose rather than what we got wrong. Wired into `ENGINEERING.md` §12
  (fixing a bug is now three parts: regression test, fix, entry) and the
  agent prompt template.
- 2026-08-22: Hinglish nudge composition added as a feature
  (`ARCHITECTURE.md` §5b), implemented as a second RPC on the Classifier
  (`ComposeNudge`) rather than a new service or inline in the Executor, so
  every LLM call in the system stays behind one provider chain, one set of
  circuit breakers, and one cost-safety switch. Reason for adding it at all:
  it is a listed track direction, it had silently dropped out of the docs,
  and generation is a far cleaner "right tool in the right place"
  justification than classification, which our own rules fallback can do
  nearly as well. Guardrails unchanged: the model writes wording only, never
  whether or whom to contact, and amounts/dates are interpolated by us so a
  model cannot invent a figure in a message about money. Static Hinglish
  templates per bucket are the fallback. Text only, no TTS: "voice" is a
  listed direction but is real work for little added credit.
- 2026-08-22: Infrastructure library choices pinned in the platform package
  docs so nine services cannot diverge: **franz-go** for Kafka (pure Go, no
  cgo so distroless runtime images keep working, and it exposes the
  per-partition fetch control the keyed worker pool needs; sarama makes that
  awkward and confluent-kafka-go needs cgo), **pgx v5 with pgxpool** for
  Postgres (binary protocol, real access to `FOR UPDATE SKIP LOCKED` which
  the scheduler worker depends on), **go-redis v9** for Redis, and **goose**
  for migrations, used as a pinned library via `scripts/migrate` rather than
  a separately installed binary so every agent runs the identical migrator.
- 2026-08-22: Kafka topics are created explicitly by a `kafka-init` container
  with `auto.create.topics.enable=false`. Auto-creation would turn a
  topic-name typo into an empty topic that silently never receives anything,
  which is a miserable bug to find. `raw.events` gets 12 partitions,
  deliberately above expected pod count, so the Decision Engine's consumer
  parallelism is never capped by the partition ceiling during the scaling
  demo (`ARCHITECTURE.md` §12).
- 2026-08-22: CI split by concern (lint, build+test, proto, docker matrix,
  integration) rather than one monolithic job, so concurrent small PRs from
  several agents do not queue behind each other's compile time. Three
  enforcement gates worth naming: `go test -race` is mandatory (a suite that
  only passes serially hides a real bug in a system with a keyed worker pool
  and a concurrent scheduler); `buf breaking` runs on PRs so an incompatible
  proto change fails in CI instead of surprising another agent mid-branch;
  and a check that regenerating protos produces no diff, which catches both a
  stale `proto/gen/` and a service PR that quietly edited protos. The
  integration job runs on merge only, since it needs the full stack up.
- 2026-08-22: Walking skeleton implementation decisions (`PLAN.md` Phase 0's
  last item), scoped deliberately to the smallest thing that proves the
  pipeline shape:
  - **`raw.events` payload is a hand-written JSON struct (`RawEvent`), not a
    proto.** The topic sits entirely inside the cluster, between Ingestion
    (producer) and Decision Engine (consumer); `ARCHITECTURE.md` section 9's
    proto discipline governs gRPC contracts, and nothing there requires an
    internal Kafka payload to be one too. The type is duplicated by hand in
    both services' `internal/` trees (Go's `internal/` rule means neither can
    import the other's copy) with a doc comment pointing at its mirror. If
    this schema grows past a handful of fields, promote it to a real proto
    under `proto/` rather than hand-syncing further.
  - **Decision Engine's walking-skeleton flow collapses `New` straight to
    `Recovered` or `Escalated`**, skipping `Scoring`/`RetryScheduled`/
    `Retrying` (`ARCHITECTURE.md` section 7). Those states exist for the
    economics scorer and the scheduler worker, both explicitly out of scope
    here (`PLAN.md`); collapsing them is not a shortcut on the real state
    machine, it is the correct shape for a build that has neither component
    yet. The audit entry still records the logical `New -> {Recovered,
    Escalated}` transition so the trail reads correctly once those states are
    reintroduced.
  - **Executor's idempotency guard is insert-then-update, not insert-with-
    final-outcome.** `intervention_attempt.outcome` is NOT NULL, so the
    walking skeleton inserts the row as `OUTCOME_PENDING` (claiming the
    `UNIQUE(record_id, attempt_number)` slot) before calling the outcome
    stub, then updates it. A redelivered request that loses the insert race
    polls briefly for the row to leave `PENDING` rather than assuming
    success, so a genuine concurrent duplicate cannot observe a torn read.
    Proven under `-race` with concurrent duplicate delivery
    (`services/executor/internal/server/server_test.go`).
  - **`raw.events` topic and the Decision Engine's consumer group are
    overridable via `RAW_EVENTS_TOPIC`/`RAW_EVENTS_CONSUMER_GROUP` env vars**,
    defaulting to the production names. Added specifically so the
    integration test can run against an isolated scratch topic instead of
    the shared one; see `INCIDENTS.md` for why that matters in practice, not
    just in theory.
  - **`POST /v1/batches`'s request body uses short record-type names**
    (`PAYMENT`/`MANDATE`/`CHECKOUT`/`INVOICE`) rather than the internal proto
    enum spelling (`RECORD_TYPE_PAYMENT`, ...), since this is the one shape
    an external caller (the demo load generator, a judge with curl) actually
    types. `docs/API_GATEWAY.md` updated to match, this was previously
    unspecified ("array of synthetic records").
  - Proven end to end by `test/e2e/walking_skeleton_test.go`
    (`go test -tags e2e ./test/e2e/...`), which builds and runs the six real
    service binaries as subprocesses against the docker-compose infra rather
    than wiring their `internal/` packages together in one test process: no
    single directory has import access to more than one service's
    `internal/` tree by construction (`ARCHITECTURE.md` section 2a), so an
    in-process wiring test was never an option here, and the subprocess
    shape is closer to how the system actually runs (`make up` + one
    binary per service) besides.
- 2026-08-23: Tests are split by infrastructure need, not by scope. Anything
  that dials real Postgres or Kafka sits behind `//go:build integration`
  (`internal/platform/pgx`, `internal/platform/kafkax`, and the audit,
  decision-engine, executor and ingestion server tests); `test/e2e` stays
  behind `e2e`. `go test ./...` on a bare checkout therefore runs only tests
  that need nothing, and CI's `build-test` job is honest about what it
  covers. `make test-integration` brings the stack up and runs the rest with
  both tags. Rejected the alternative of skipping when infra is unreachable:
  a test that silently skips in CI is a test you do not have, and you would
  never notice it stopped running. Consequence to keep in mind: the
  DB-touching service tests now only run in the integration job, so that job
  runs on pull requests too rather than merges only.
- 2026-08-23: Closed out Phase 0's last box, `internal/platform/interceptors`.
  Built only what `doc.go` said should land now: `UnaryServerRecovery`
  (panic → `codes.Internal` instead of a dead pod), `UnaryServerRequireDeadline`
  (server-side, rejects a call with `codes.InvalidArgument` if it arrives
  with no context deadline — turns `ENGINEERING.md` §3's rule into something
  verified on receipt, not just remembered at each call site), and
  `UnaryClientDefaultDeadline` (client-side, applies a default deadline only
  when the caller's context does not already carry one). Metrics, tracing,
  request-scoped logging, and the `round_robin` dialing helper stay Phase 4,
  per the doc's own status note. Wired into all four gRPC servers
  (classifier, executor, audit, ingestion) and both inter-service gRPC
  clients (api-gateway→ingestion, decision-engine→classifier/executor); the
  full test suite including `test/e2e` still passes, since every existing
  call site already set an explicit deadline before this landed.
- 2026-08-23: API Gateway's "basic rate limiting" (`docs/PLAN.md` Phase 1) is
  a single global token bucket (`golang.org/x/time/rate`), not per-key or
  per-IP. Reason: this system has no concept of a per-caller identity to key
  on, the API key is one static shared secret by design
  (`ARCHITECTURE.md` §17), so a per-key bucket would just be the global
  bucket with extra bookkeeping. Configured via `RATE_LIMIT_RPS`/
  `RATE_LIMIT_BURST`, either <= 0 disables it. Applied before the auth check
  in the middleware chain, so an over-limit caller is rejected without even
  reaching the key comparison. Also added `POST /v1/webhooks/payment-failed`
  → `Ingestion.SubmitEvent`, completing the Phase 1 API Gateway item; the
  walking skeleton had only wired `POST /v1/batches`.
- 2026-08-23: Added migration `00002_record_idempotency_key.sql`: a nullable
  `record.idempotency_key` column plus a partial unique index (`WHERE
  idempotency_key IS NOT NULL`). Reason: `proto/ingestion/v1/ingestion.proto`
  already specifies `SubmitEventRequest.idempotency_key` and
  `SubmitEventResponse.deduplicated` ("two submissions with the same key are
  the same event, so a webhook retry cannot create a duplicate record"), but
  the initial schema had nothing to check that against. Nullable and
  partial-unique because `SubmitBatch` records never carry one, batch
  submission has no webhook redelivery to guard against, and NULLs must not
  collide with each other. Additive only, own migration, ahead of the
  Ingestion service PR that depends on it, per `ARCHITECTURE.md` §12a.
- 2026-08-23: Ingestion's `SubmitEvent` "implicit rolling batch"
  (`proto/ingestion/v1/ingestion.proto`'s comment, otherwise unspecified
  anywhere) is one long-lived `BATCH` row per environment, found by a fixed
  `source` value (`ROLLING_BATCH_SOURCE`, `"webhook"` in production) and
  created lazily on first use, not time-windowed (e.g. not one per day).
  Reason: Phase 1 has no reporting need for finer-grained production
  batching yet, and a single rolling batch is the smallest thing that
  satisfies "every record is reportable." Revisit if/when Phase 5 reporting
  wants production records grouped by time window instead. Two concurrent
  first-ever calls can race and create two rolling batches; accepted as
  benign (no record is lost or double-counted, later calls converge on the
  older row by `ORDER BY created_at`), not worth a DB constraint for.
  `rollingBatchSource` is a constructor parameter on
  `services/ingestion/internal/server.New`, the same way `topic` already
  was, so tests use an isolated value instead of colliding with each other
  or accumulating junk in the production rolling batch.
- 2026-08-23: Added `ENGINEERING.md` section 14, "one job per file, one job
  per function", and a matching Definition of Done item (section 11, #10).
  Reason: the walking-skeleton gRPC handlers (e.g. Ingestion's `SubmitBatch`)
  accumulate validation, SQL, and Kafka publishing into one long method
  because the proto-generated signature is the only thing shaping them, and
  nothing in this file said otherwise. Applies to every service, not just
  the one that prompted it, so it landed here rather than as an unwritten
  convention one agent applies and the rest don't know about.
- 2026-08-23: Added `kafkax.Consumer.ConsumeKeyed`, the keyed worker pool
  `ARCHITECTURE.md` §8a specifies, ahead of the Decision Engine depth work
  that needs it. Built in `internal/platform/kafkax` rather than inside
  `services/decision-engine`, since dispatch-by-key-hash with
  contiguous-prefix offset commits is generic Kafka consumption
  infrastructure, not Decision Engine business logic, and `kafkax.go`'s own
  doc comment already anticipated this landing here ("deliberately not the
  keyed worker pool... that lands with the Decision Engine's depth work").
  `Consume` (one-at-a-time) is unchanged and still used by any consumer that
  doesn't need the pool. Contract: `handler` must resolve every record's
  fate itself (success or routed to a DLQ) and return nil; a non-nil error
  is treated as an infrastructure failure and stops the whole loop, mirroring
  `Consume`'s existing contract. See `docs/INCIDENTS.md`
  2026-08-23 for a real race this surfaced during testing (commits must run
  on a context that outlives the shutdown signal that triggers them).
- 2026-08-23: Decision Engine's Phase 1 depth deliberately excludes the
  `Scoring`/`ClosedUneconomic` economics gate and any retry budget/cap:
  those are explicit Phase 2 items in `docs/PLAN.md`. Consequences of that
  scoping, worth stating so a later agent does not read them as bugs: (1)
  `HandleMessage` never calls Execute, every action including a plain retry
  goes through the scheduler on a fixed Phase 1 delay
  (`RETRY_DELAY`/`NUDGE_DELAY`, both scaled by `DEMO_TIME_SCALE`), so the
  state diagram's actual shape (New -> Scoring -> RetryScheduled -> ...)
  holds even with Scoring itself not built yet; (2) a failed retry or nudge
  escalates immediately rather than re-scoring and trying again, since
  there is no budget to check against yet; (3) `ACTION_TYPE_NONE` (the
  honest home for which is `ClosedUneconomic`) escalates for the same
  reason. A record whose classify or execute call keeps failing is retried
  a bounded number of times (`maxClassifyAttempts`/`maxExecuteAttempts` = 3)
  then published to `raw.events.dlq`, per `ARCHITECTURE.md` §8b; it is left
  in whatever state it was claimed into rather than moved to `Escalated`,
  since the architecture explicitly treats a DLQ'd record as a processing
  failure, distinct from a considered decision.
- 2026-08-23: Fixed a real cross-package test race surfaced while testing
  the scheduler: `go test ./...` runs packages concurrently, and the
  scheduler's due-record claim query is intentionally unscoped (a
  system-wide poll is its actual job), so it could claim a record belonging
  to `test/e2e`'s walking-skeleton run against the same shared Postgres.
  Scheduler tests now assert only state scoped to the record_id they
  seeded, never a shared fake's global call count. See `docs/INCIDENTS.md`
  2026-08-23.
- 2026-08-23: Classifier's Phase 1 failure-code-to-bucket table
  (`services/classifier/internal/rules/buckets.go`) is taken directly from
  `SPEC.md` section 4.2, kept as a `map[string]commonv1.RootCauseBucket`
  literal rather than a switch so it can be audited at a glance and
  iterated by a test. `INSUFFICIENT_FUNDS` keeps its own bucket rather than
  being folded into a coarse "transient" one the way
  `web/src/lib/mockEngine.ts`'s three-bucket display mock does:
  `ARCHITECTURE.md` §5a gives it a distinct salary-window retry policy,
  which only works if the bucket stays distinct. The web mock is a display
  simplification, not the system's model; do not "correct" the classifier
  to match it.
- 2026-08-23: Classifier's unknown-failure-code fallback
  (`rules.fallbackBucket`, `SPEC.md` section 4.3) orders on `record.type`
  before giving up: `RECORD_TYPE_CHECKOUT` → `ABANDONMENT`,
  `RECORD_TYPE_INVOICE` → `OVERDUE`, anything else (including an empty
  `failure_code`) → `ROOT_CAUSE_BUCKET_UNSPECIFIED` + `ACTION_TYPE_ESCALATE`
  + confidence 0. This is a recommendation, not a locked decision (SPEC.md
  leaves it open for a later agent to improve once real rail-code data from
  the demo generator shows which unrecognised codes actually appear). An
  unrecognised or empty code is never `InvalidArgument`: `SPEC.md` section 5
  is explicit that turning an unclassifiable record into a failed RPC sends
  it to the DLQ after three retries, for a record that was never actually
  malformed.
- 2026-08-23: `ClassifyResponse.source` is the coarse answer, which kind of
  thing answered (`SOURCE_LLM` or `SOURCE_RULES_FALLBACK`); `hops` is the
  detail, which named rung answered and what every attempted rung returned.
  `ARCHITECTURE.md` §5's prose (`llm:claude`, `rules_fallback`) predates the
  `Source` enum and does not mean `Source` needs provider-name string
  values; provider identity lives entirely in `ProviderHop.provider`. The
  chain (`internal/provider/chain.go`), not each rung, decides `Source`,
  from whether the winning rung's name equals `provider.RulesName`: this
  keeps individual `Provider` implementations from needing to know about
  the coarse/fine split at all. With `LLM_PROVIDER_CHAIN=rules` the only
  configured rung in Phase 1, `source` is always `SOURCE_RULES_FALLBACK`,
  which is what `test/e2e`'s walking-skeleton test asserts.
- 2026-08-23: The Classifier stays entirely stateless in Phase 1: no
  Postgres connection, no clock, and the `ANTHROPIC_API_KEY`/
  `OPENAI_API_KEY` env vars are read by nobody. Reasons, per `SPEC.md`
  sections 2 and 8: it has no query path to `GROUND_TRUTH`, non-negotiable
  per `ARCHITECTURE.md` §5a and easiest to guarantee by simply holding no
  database handle at all; the Phase 1 rules engine is a pure function of
  its input with no time-based behaviour, the salary-window/backoff timing
  `ARCHITECTURE.md` §5a describes becomes `due_at`, computed by the
  Decision Engine, not here; and a service that requires a credential it
  never calls is a startup failure waiting to happen, so the keys stay
  unread until Phase 3 actually wires a provider that uses one.
- 2026-08-23: Classifier's provider chain skeleton (`internal/provider`)
  validates a rung's response (bucket/action outside their enum's defined
  values, confidence outside `[0,1]`) against the generated `_name` maps
  rather than a hand-written allow-list, so a bucket or action added to the
  proto later is covered automatically instead of silently passing.
  `force_rules_only` filters the configured chain down to whichever rung is
  named `provider.RulesName` ("rules") rather than special-casing index 0
  or "the last rung": in Phase 1 that is the same rung either way, but it
  stays correct once Phase 3 inserts real LLM rungs ahead of it. Confidence
  values (`rules/actions.go`) are named Go constants with a one-line
  comment each, not the checked-in `configs/intervention_costs.yaml`
  `ARCHITECTURE.md` §5a describes for Phase 2 costs/probabilities:
  classification confidence and economics cost data are different things
  that happen to both be numbers, and do not belong in the same file.
- 2026-08-23: Deferred the Executor's Redis `SETNX` idempotency fast path
  out of Phase 1, and moved the Redis client itself into
  `internal/platform/` as its own tracked item. `ARCHITECTURE.md` §11 is
  unusually explicit that Redis is "an optimisation only" here and that the
  `UNIQUE (record_id, attempt_number)` constraint is "the actual
  guarantee", so deferring it costs no correctness: the durable guard is
  already built and proven by tests that deliver the same action twice,
  including 8 concurrent duplicates. What it does cost is a saved Postgres
  round trip on an obvious duplicate, which is a throughput claim that
  cannot be evaluated before Phase 6's load run exists, and which nothing
  in Phase 1 is close to needing. Against that, building it now meant
  either adding `github.com/redis/go-redis/v9` (absent from `go.mod`
  despite `internal/platform/pgx/doc.go` naming it as the pinned choice)
  privately inside `services/executor`, which guarantees a second agent
  duplicates it within two phases, since Phase 2's cooldown and
  retry-budget keys and Phase 5's delayed-outcome queue both need the same
  client; or creating `internal/platform/redisx` from inside a service PR,
  which the per-service `AGENTS.md` files put squarely in "stop and
  propose". So: `redisx` is now a Phase 2 item, where its first real
  consumer lives, and the Executor fast path is a Phase 6 item, where the
  measurement that justifies it lives. Recorded as an explicit
  scope-decision line in `docs/PLAN.md` Phase 1 as well, so the Executor
  checkbox is not later misread as having covered the Redis clause in its
  own description. Detail and the two non-negotiable rules for whoever
  builds it (Postgres wins any disagreement; an unreachable Redis falls
  through rather than failing the request) are in
  `services/executor/SPEC.md` §4.4 and §10.
- 2026-08-23: Fixed the invariant verifier's disagreement with the Phase 1
  pipeline by adding temporary state-machine edges rather than by changing
  the Decision Engine. `ARCHITECTURE.md` §7's diagram routes every record
  through `Scoring` before scheduling, but `Scoring` is the Phase 2
  economics gate, so Phase 1 schedules straight out of `NEW` and the
  verifier called all of it impossible. The alternative, making the Decision
  Engine write a `Scoring` transition it does not actually perform, would
  have put a fabricated state in the audit trail purely to satisfy a
  checker: the trail would then claim an economics decision happened when
  none did, which is exactly the kind of thing the trail exists to make
  impossible to fake. So `NEW -> RETRY_SCHEDULED` and
  `NEW -> NUDGE_SCHEDULED` are now marked `TEMPORARY` alongside the
  walking skeleton's `NEW -> RECOVERED`, all three to be deleted together
  when `Scoring` lands and stops them being produced. Related:
  `trail_complete` now runs the same `incompleteTrail`/
  `impossibleTransition` pair `VerifyInvariants` aggregates, instead of
  getting its own implementation, so the RPC's per-record answer and the
  system-wide metric can never disagree about what "complete" means. It was
  previously hardcoded `true`. See `docs/INCIDENTS.md` 2026-08-23.
- 2026-08-23: Implemented the Docker matrix path filtering that `AGENTS.md`
  had specified and the Phase 0 CI checkbox had claimed, but which was never
  actually built: every PR rebuilt all nine service images, including
  documentation-only ones. A `changed` job now computes the build matrix from
  the diff and `docker` is skipped when nothing reaching an image changed.
  Three decisions worth recording. First, it **fails safe in every ambiguous
  case**: a filter that wrongly skips a build lets a broken image reach
  `main`, while one that wrongly runs a build costs a minute, so shared
  inputs (`go.mod`, `internal/platform/`, `proto/`, `.github/`) rebuild
  everything, and an undeterminable diff base (a new branch, a force push, a
  missing commit) does too. Second, shared code has to rebuild *every* image
  rather than being filtered per service, because each Dockerfile builds with
  the repo root as its context and `COPY`s the whole tree
  (`ARCHITECTURE.md` §2a), so `internal/platform/` really is an input to all
  nine. Third, the logic lives in `.github/scripts/changed-services.sh`
  rather than inline YAML, and builds its JSON in plain shell rather than with
  `jq`, specifically so it can be run and tested locally against real commits
  instead of only by pushing and watching. It was verified that way before it
  ever ran on a runner: docs-only builds nothing, a single-service change
  builds one image, a `internal/platform/` change builds all nine, a
  migrations-only change builds nothing (no image contains migrations), and
  every fail-safe branch returns the full matrix. Deliberately left alone:
  `lint`, `build-test`, `proto` and `integration` still run on everything.
  They are the correctness gates, they are a small fraction of the wall
  clock, and narrowing them on the same day two real bugs were caught only by
  `integration` would be trading the wrong thing for a minute.
- 2026-08-23: Executor Phase 1 decisions. (1) The claim marker on
  `INTERVENTION_ATTEMPT.outcome` is now `OUTCOME_UNSPECIFIED` rather than
  `OUTCOME_PENDING`. The column is `NOT NULL` so the row cannot be claimed
  without some value, and the previous choice was safe only while no real
  outcome was ever `PENDING`. A nudge legitimately resolves to `PENDING`
  (Phase 5 answers it via `ReportDelayedOutcome`), so reusing it as the
  marker made "still working" and "sent, awaiting the customer"
  indistinguishable, and a redelivered nudge would have polled for an answer
  that was not coming, timed out, and been dead-lettered despite executing
  perfectly. The enum's zero value is the honest marker per `common.proto`'s
  own stated convention that `_UNSPECIFIED` means unset. No migration needed:
  the column is unconstrained `TEXT` and nothing else reads it yet.
  (2) The wait for a concurrently-in-flight original is **bounded** (500ms,
  a window that only ever covers one in-process port call) and an unresolved
  claim past that returns `Aborted` rather than waiting out the caller's
  deadline. A slot claimed but never resolved means the holder died
  mid-attempt, and the two tempting recoveries are both wrong: re-running
  could double-charge a real payment rail, and inventing an outcome would put
  a fiction in the audit trail. Reporting it is the only honest option.
  (3) `ESCALATE` reports `OUTCOME_FAILURE` with a failure code rather than
  success, because handing a record to a human is not something the Executor
  can accomplish and claiming otherwise would put a false success in the
  trail. `ACTION_TYPE_NONE` succeeds at zero cost, since deliberately doing
  nothing is a decision that was reached rather than a failure. Neither calls
  a port. In practice `ESCALATE` is unreachable today (`decideAfterClassify`
  escalates without scheduling), and it is handled because `Execute` is a
  public RPC, not because a caller exists.
  (4) The stub is **scripted, never random**: retry attempt 1 succeeds and
  2+ fail, nudges always send and return `PENDING`. Determinism is not
  fastidiousness here, it is what makes Phase 2's re-run safety test possible
  at all and what keeps the end-to-end test from flaking, and the script is
  chosen so both branches of the post-execute state machine are reachable
  without dice. Nothing in it reads the clock either, since a time-dependent
  stub is a time-dependent test.
  (5) The Executor still writes **no** `AUDIT_ENTRY` rows. §10a's ownership
  table lists it as a writer, but `audit_entry.from_state`/`to_state` are
  `NOT NULL` and the Executor neither owns nor knows `RECORD_STATE`, so it has
  nothing truthful to put there; the Decision Engine's `recordOutcome`
  already writes that transition transactionally, including the attempt
  number and cost. The Executor's honest contribution to history is the
  `INTERVENTION_ATTEMPT` row. That reading is consistent with §10a's actual
  rule, "the service that owns a state change writes both", since the
  Executor owns no state change. Worth a clarifying edit to that table by
  whoever owns the docs.
- 2026-08-23: Phase 1's smoke test asserts what the pipeline settles records
  into, not just that it produces something. Three choices worth recording.
  (1) Expectations are derived per failure code, and each case carries the
  chain it expects (`TRANSIENT_BANK -> RETRY -> claimed -> stub succeeds`), so
  a failure names which link broke rather than only reporting a wrong state.
  A test that asserted "reached some terminal state" would have passed while
  the classifier mapped everything to `ESCALATE`. (2) Batch-wide correctness
  is checked by calling `Audit.VerifyInvariants` scoped to the batch rather
  than by re-deriving the invariants in the test. That is the service's whole
  second job (`ARCHITECTURE.md` §10a) and duplicating its logic in a test
  would mean two implementations that could agree with each other while both
  being wrong. Scoping to the batch also keeps it immune to whatever else is
  in the shared database, which is the failure mode that has already bitten
  this repo twice. (3) `Nudged` counts as *settled* but not *terminal*, and
  the test cross-checks its own notion of terminal against the three states
  `common.proto` actually marks terminal, so drift between the test and the
  contract surfaces rather than passing quietly. Related: the Executor stub's
  "attempt 2+ fails" branch is deliberately not exercised end to end, because
  Phase 1 has no retry budget and so never schedules a second attempt;
  fabricating one would test the fabrication rather than the pipeline, so it
  stays covered by unit tests until Phase 2 makes it reachable honestly.
- 2026-08-24: `docs/PLAN.md`'s Phase 2 item for the `GROUND_TRUTH` isolation
  test named only the Decision Engine. Widened the implementation to cover
  all three decision-path services (Decision Engine, Classifier, Executor),
  since `ARCHITECTURE.md` §5a states the rule as applying to "the decision
  path," not to one service by name, and a guard watching only one of the
  three services a record passes through before money is spent is a weaker
  guarantee than the rule it exists to enforce. `services/reporting` and
  `demo/world-simulator` are the two services the ownership table in §10a
  permits to read `GROUND_TRUTH` and are deliberately excluded from the
  scan. Implementation is `test/integrity/ground_truth_isolation_test.go`:
  unit tier, no build tag (so it runs in `make test` on every CI pass, not
  only when docker is up), parses each service's Go source with
  `go/parser`/`go/ast` rather than grepping text, so a comment documenting
  the rule (there is already one in
  `services/executor/internal/ports/ports.go`) can never trip it, only a
  real identifier or string literal naming the table can. Known gap, stated
  rather than silently accepted: the scanner only reads source physically
  inside the three service directories, it does not follow an import
  transitively into `internal/platform` or elsewhere to check whether a
  called function reads `GROUND_TRUTH` internally under an innocuous name,
  nor does it catch a table name assembled at runtime (string
  concatenation, an environment variable, decoded bytes) rather than
  written as a literal in source.
- 2026-08-24: QA independently verified the `GROUND_TRUTH` isolation test
  above (red/green reproduced across all three services and four violation
  shapes, file counts confirmed, the comment-blindness confirmed against
  the real comment in `services/executor/internal/ports/ports.go`) and
  found one real gap in the original implementation: the scanner only
  looked at `.go` files. QA built a working exploit, a
  `queries/lookup.sql` file containing `SELECT truly_recoverable FROM
  ground_truth WHERE record_id = $1`, pulled into a Go file with
  `//go:embed`. The `.go` file itself names no `GROUND_TRUTH` anything,
  only a relative file path, so it compiled, the query shipped in the
  binary, and the test stayed green. Nothing in this repo uses
  `go:embed` today, so the hole was latent, not active, but moving SQL
  into `.sql` files is an entirely ordinary refactor and the original
  description ("parses every Go file") reasonably read as if this were
  already covered.

  Fixed by adding a second scanner,
  `scanNonGoTextFilesForGroundTruthReferences`, that plain-text-matches
  (normalized, same as the `.go` scanner's identifier/string check) any
  `.sql`, `.yaml`, `.yml`, `.json`, or `.tmpl` file under each of the three
  service directories. `.md` is deliberately excluded: both
  `services/classifier/SPEC.md` and `services/executor/SPEC.md` already
  carry a "hard boundary: no ground truth" section that names
  `GROUND_TRUTH` in prose, which is exactly the legitimate-documentation
  case the AST path exists to leave alone for `.go` files, and there is no
  AST for a `.md` file to make that same distinction. A plain text match
  over `.sql`/`.yaml`/`.yml`/`.json`/`.tmpl` also means a comment inside
  one of those files (a SQL `-- ` comment, for instance) is not
  distinguished from code the way a `.go` comment is; that is a real,
  stated trade, not an oversight, and is the honest cost of using a text
  match where no AST exists.

  Confirmed red-then-green the same way as the original test: a fixture
  reproducing QA's exact repro (`fetch.go` with `//go:embed
  queries/lookup.sql`, plus `queries/lookup.sql` containing the violating
  query) was scanned with only the old `.go`-only scanner first, which
  found zero violations, a real `t.Fatalf` failure demonstrating the gap
  with an actual assertion, not a compile error. After adding the new
  scanner and wiring it into `TestDecisionPathHasNoGroundTruthQueryPath`,
  the same fixture is caught (`TestNonGoTextScanCatchesGroundTruthInEmbeddedFile`).
  A companion test,
  `TestNonGoTextScanIgnoresUnlistedExtensions`, proves `.md`/`.txt` files
  carrying the same words are not flagged, protecting the two `SPEC.md`
  files above from becoming false positives.

  Today, zero `.sql`/`.yaml`/`.yml`/`.json`/`.tmpl` files exist in any of
  the three service directories, so the new scanner's file count is
  honestly zero in the real repo; that is logged explicitly in the test
  output as expected, not silently treated as a pass, and the scanner's
  ability to catch a real match in these file types is proven separately
  against the synthetic fixture, not by the real repo's current absence of
  such files.

  Evasions that still survive after this fix, stated plainly: any file
  extension not in the checked list (`.csv`, `.txt`, `.proto`, an
  extensionless file, or any future format), a table name assembled at
  runtime rather than written as a literal (string concatenation, an
  environment variable, base64 or otherwise encoded bytes), and the
  already-stated gap of following an import transitively into
  `internal/platform` or elsewhere to check whether a called function
  reads `GROUND_TRUTH` internally under an innocuous name. This fix closes
  the specific `go:embed`-shaped hole QA found; it does not turn the
  scanner into a general dependency or data-flow analysis, which was never
  the intent.
- 2026-08-24: Guardrail enforcement (retry budgets, contact caps, cooldowns)
  is Postgres-transactional, not Redis-backed, reversing the "retry/cooldown
  counters" clause of the Storage bullet in `AGENTS.md`, which has been
  amended rather than left to contradict this. The reversal is small but
  load-bearing, so the reasoning is recorded in full. `ARCHITECTURE.md` §11
  is explicit that Redis here is "an optimisation only", and the rule written
  into `services/executor/SPEC.md` §10 is that an unreachable Redis falls
  through rather than failing the request. That rule is correct for an
  idempotency *fast path*, where Postgres still holds the real guarantee. It
  is actively wrong for a *cap check*, where falling through means the cap is
  simply not enforced, and TTL expiry or eviction silently resets a counter
  besides. `PRD.md` §9 makes "zero stopping-rule violations" a headline
  metric and §10 says a violation should be paged on because it should be
  impossible; that claim cannot rest on a cache. So a cap is now evaluated
  inside the same transaction as the state change it gates, which is the same
  reasoning as §10a's rule that a state change and its audit entry are
  written together: a check performed outside the transaction that acts on it
  has a window, and a window is a bug. Two consequences worth stating.
  First, the counters are **derived** from `INTERVENTION_ATTEMPT`
  (`COUNT(*)` filtered by `action_type`) rather than stored as columns, so
  they cannot drift from the history that the audit trail is the source of
  truth for, no migration is needed, and in-flight attempts are counted
  correctly for free because the Executor inserts its attempt row before
  executing. `ARCHITECTURE.md` §10a already lists the Decision Engine as a
  permitted reader of that table, so this crosses no ownership boundary.
  Second, `internal/platform/redisx` is therefore **not** a Phase 2
  dependency at all; nothing in Phase 2's eleven items needs it. Its first
  real consumer is Phase 5's delayed-outcome queue alongside Reporting's
  cache-aside layer, and the Executor fast path stays a Phase 6 item where
  the load measurement that would justify it lives. Recorded because
  `docs/DECISIONS.md` (2026-08-23) had said redisx was a Phase 2 item while
  Phase 2's checklist never actually carried that checkbox, and this is the
  kind of gap that gets silently skipped.
- 2026-08-24: `configs/intervention_costs.yaml` reconciled against
  `services/executor/internal/ports/cost.go`, and the drift between them
  closed with a test rather than a one-time fix, because the two priced the
  same three things and disagreed.

  WHAT THE COST MODEL CONTAINS. `configs/intervention_costs.yaml` is the
  checked-in `direct_cost`/`indirect_cost` term source for section 5a's EV
  formula: `channels.*` (marginal per-message cost for SMS, WhatsApp
  Utility, email), `action_channel_policy` (which channel each nudge action
  uses, mirroring `route.go`'s `channelFor`), `actions.*` (`direct_cost_paise`
  and `indirect_cost_paise` per `ActionType`), and an
  `informational_not_in_formula` block recording Razorpay's success-fee MDR,
  which the section 5a formula cannot express today because it is
  conditional on success and proportional to amount rather than a flat
  unconditional cost. Every number in the file is one of three tags:
  `[SOURCED]` (published price, URL inline), `[ASSUMPTION]` (reasoned
  estimate, derivation shown inline so it can be argued input by input), or
  `[UNVERIFIED]` (believed roughly right, no citation obtained, treat as a
  placeholder). The file itself states which tag each number carries; this
  entry does not repeat every one, only the ones load-bearing for the
  reconciliation below.

  THE RECONCILIATION. The YAML already carried its own
  `executor_reconciliation` block naming the disagreement and the required
  fix, written by whoever produced the cost model before they were stopped.
  That block's numbers were verified against `cost.go` directly and matched
  the file's own comparison exactly:

  | Cost | YAML value (tag) | Go constant (was) | Agreed? | Fix |
  |---|---|---|---|---|
  | SMS, one message | `channels.sms_paise` = 25 paise [SOURCED] | `smsCostPaise` = 25 | Yes | none |
  | WhatsApp, one message | `channels.whatsapp_utility_paise` = 14 paise [SOURCED, rate-card citation caveat noted in file] | `whatsappCostPaise` = 60 | No, overstated 4.3x, and wrong direction (WhatsApp is cheaper than SMS on these sourced rates, not dearer) | `whatsappCostPaise` -> 14 |
  | Retry, one attempt | `actions.RETRY.direct_cost_paise` = 25 paise [SOURCED] (NPCI NACH switching fee, charged win or lose; Razorpay charges only on success so a failed retry has no gateway fee) | `retryCostPaise` = 200 | No, overstated 8x | `retryCostPaise` -> 25 |

  `services/executor/internal/ports/cost.go` was edited to match: both
  disagreeing constants now carry the YAML's sourced values, with comments
  pointing at the YAML and at the new test rather than restating the
  derivation. The YAML values themselves were not touched; the mandate was
  "the checked-in file is the source of truth, fix the code", not
  "re-derive the numbers".

  COLLATERAL FIX, NOT PART OF THE MANDATE. Lowering `whatsappCostPaise`
  below `smsCostPaise` broke `TestChannelCosts` in
  `services/executor/internal/ports/stub_test.go`, which asserted WhatsApp
  must cost more than SMS "because proto/notifier/v1 says it should be".
  That assumption was itself the thing the YAML's reconciliation block
  flags as backwards for India: `route.go`'s `channelFor` sends the
  higher-value nudge (`NUDGE_METHOD_UPDATE`) over WhatsApp on the theory
  that it is the pricier, better-read-rate channel, but on sourced Indian
  rates WhatsApp Utility is cheaper than SMS. The test's inequality
  assertion was removed; it now only checks every channel costs something
  positive. Actually changing which channel each nudge action prefers is a
  separate, undecided behavioural change (the YAML calls it a "RECOMMENDED
  CHANGE, owned by services/executor") and was deliberately not made here.

  THE DRIFT GUARD. `cost_reconciliation_test.go` (new, same package as
  `cost.go`) reads `configs/intervention_costs.yaml` off disk (path found by
  walking up from the test file's own `runtime.Caller` location, not by
  assuming a working directory) and asserts `smsCostPaise`,
  `whatsappCostPaise`, and `retryCostPaise` still equal the YAML's
  `channels.sms_paise`, `channels.whatsapp_utility_paise`, and
  `actions.RETRY.direct_cost_paise`. Parsing is a small targeted regex scan,
  not a YAML library: `go.mod` carries no YAML parser today and this task
  was explicitly out of bounds to add one for three integers. Verified by
  deliberately setting `whatsappCostPaise` back to 60 and re-running the
  test: it failed with `whatsappCostPaise vs channels.whatsapp_utility_paise:
  Executor constant = 60 paise, configs/intervention_costs.yaml = 14 paise`,
  then the constant was reverted and the test passed again.

  WHAT REMAINS `[UNVERIFIED]` OR OTHERWISE UNRESOLVED, so the next agent
  does not have to re-derive this from the YAML's comments:
  - The Gupshup per-message BSP markup (~10 paise, additive to the WhatsApp
    figure if a merchant is on a per-message BSP rather than a flat-fee
    one) is tagged `[UNVERIFIED]`: Gupshup's own pricing URL 404s. Not used
    by any reconciled constant today.
  - The RETRY NPCI NACH figures (20 paise off-us, 5 paise on-us) are sourced
    from a processor's documentation citing named NPCI circulars, not from
    npci.org.in directly, which returns HTTP 403 to automated fetch. The
    file itself flags this as "verify by hand before the demo".
  - The retry `indirect_cost_paise` (600, authorization-rate damage from a
    repeated decline) is `[ASSUMPTION] on the level, [SOURCED] on the
    anchors`: its most load-bearing input (issuer approval-rate degradation
    per additional decline, assumed 0.1 percentage points) has no published
    source at all. Not part of this reconciliation since `cost.go` has no
    indirect-cost concept yet; that is Phase 2's economics scorer's job.
  - `configs/intervention_costs.yaml` references `configs/README.md` four
    times (for why costs and priors are two files, for the MDR gap, for the
    escalation cost sensitivity) and that file does not exist in the repo.
    Not created as part of this task since it was not asked for and its
    scope is unclear; flagged here so it is not lost.
  - Not fixed, and out of scope here: `channelCostPaise`'s `default` branch
    charges `CHANNEL_EMAIL` the SMS rate; `StubRecovery` charges
    `retryCostPaise` on both its success and failure branches even though
    Razorpay's success-fee model implies those should differ; and
    `proto/worldsim/v1`'s `SimulateOutcomeResponse` carries no cost field at
    all, so Phase 5's real World Simulator swap has no source for retry
    cost unless it comes from this YAML. All three are named in the YAML's
    own `executor_reconciliation.defects_raised` list.
  - Not fixed, and flagged as a real finding: `docs/PRD.md` section 9's
    "net recovered" is gross recovered minus logged intervention spend, and
    the YAML's `informational_not_in_formula` block states plainly that
    Razorpay's success-fee MDR (roughly 2.36% to 3.42% of the recovered
    amount) is the single largest real cost of recovery and is currently
    absent from that headline number entirely, dwarfing every messaging
    cost reconciled above. Not this task's mandate to fix (it is a formula
    change, not a constant-agreement fix), but worth surfacing loudly:
    the headline metric under-costs recovery by roughly two orders of
    magnitude relative to messaging spend, independent of anything fixed
    here.
- 2026-08-26: `SubmitBatch` has no idempotency key and no dedup. Verified by
  inspection of `proto/ingestion/v1/ingestion.proto`
  (`SubmitBatchRequest` has no `batch_id` field and no idempotency key
  field), `services/ingestion/internal/server/server.go`
  (`SubmitBatch` calls `createBatch` unconditionally, no key lookup),
  and `services/api-gateway/internal/httpapi/handler.go` (the Gateway's
  `submitBatch` handler passes `SubmitBatchRequest` straight through with
  no additional dedup). Every call creates a brand-new `batch_id` and new
  `record` rows. This is the documented scope of the demo/backfill path
  (`ARCHITECTURE.md` section 0a): `SubmitBatch` was never designed to be
  idempotent. The only per-event idempotency guarantee in Ingestion is
  `SubmitEvent`'s `idempotency_key` field (the production webhook path).
  Unit J's e2e test (`test/e2e/rerun_safety_test.go`) asserts this actual
  behavior: two calls with identical content produce two distinct batch
  IDs, two distinct record IDs, and both process and settle independently.
   This is correct, not a gap. See `docs/PHASE2_IMPLEMENTATION.md` Unit J
   for the full test description.
- 2026-08-26: Salary-window boundary for cause-aware retry timing (Unit F)
  is days 1 through 7 of each calendar month, **inclusive on both ends**.
  Reason: the salary credit typically arrives on the 1st, but processing
  delays (bank holidays, batch processing windows) mean some credits land
  on the 2nd-3rd, and a merchant who gets paid on the 7th would be
  unfairly skipped if the window closed on the 6th. The window returns
  `now` when the current day is inside [1, 7] (no delay, the money should
  arrive imminently and the scheduler picks it up on the next tick). When
  day 8 or later, the next window is the 1st of the next calendar month
  at the same wall-clock time-of-day. Go's `time.Date` normalises month 13
  to January of the next year, which handles the December-to-January
  rollover automatically, but a test proves it rather than trusting the
  language spec. `retryDueAt` returns `nil` for HARD_DECLINE and
  RISK_HOLD (belt-and-braces: the priors already give them zero
  probability, so the scorer would not choose a retry anyway; encoding
  them as a second independent stop means a code path that somehow
  reaches retry scheduling for these buckets also produces no due_at
  rather than a nonsensical one). The function is pure (no I/O, takes
  `now` directly rather than an injected Clock), matches `dueAtFor`'s
  existing signature style, and lives in its own file
  (`schedule.go`).`DemoTimeScale` is threaded to both call sites via a
  new `TimeScale` field on `engine.Config` and `engine.SchedulerConfig`,
  populated from `config.Common.DemoTimeScale` in `main.go`.
- 2026-08-28: **LLM provider chain decided**, closing the deferral logged on
  2026-08-22 and the open question in `docs/PRD.md` section 13. The chain is
  `groq,gemini,rules`, with Groq running `openai/gpt-oss-20b` at
  `reasoning_effort: low`. Groq is primary for three reasons, in order of
  weight. (1) It is the only evaluated option giving **guaranteed constrained
  decoding**: `response_format: json_schema` with `strict: true` constrains
  the model at the token level, so it cannot emit a bucket outside
  `RootCauseBucket`. That support is narrow, only `gpt-oss-20b`,
  `gpt-oss-120b` and `Qwen 3.8 27B`; everything else on Groq, including
  `llama-3.1-8b-instant`, is best-effort. (2) Latency is a hard constraint
  here rather than a nicety, because the chain has to fit inside the Decision
  Engine's 5s `CALL_TIMEOUT` and `PRD.md` section 10's 3s p95 target; Groq
  measures roughly 1000 tok/s output, the fastest available. (3) It speaks
  the OpenAI chat-completions shape, so the client is standard.
  Gemini is the automatic failover, on the free tier, using **native
  `generateContent` with `responseSchema`**, deliberately NOT its
  OpenAI-compatibility endpoint: that endpoint exists but its tool calling
  does not follow the OpenAI schema, which is precisely the capability the
  fallback rung depends on. Gemini's compliance is best-effort and Google
  says so, which is fine because `provider/validate.go` is already the
  second gate and `services/classifier/SPEC.md` section 4.7 wrote it for
  exactly this case: a non-compliant answer becomes a `schema_invalid` hop
  and falls through.
  Cost was the trigger for revisiting this (Anthropic and OpenAI are not
  free) but is not the whole argument: on the merits, guaranteed constrained
  decoding plus the fastest inference is a better fit for a
  seven-way-classification-with-two-sentences workload than a frontier model
  would be. Both free tiers reserve the right to train on inputs; the data
  crossing that boundary is synthetic (World Simulator ground truth) and
  `record.instrument_ref` is by its own schema comment never the instrument
  itself, so no real payment PII leaves the system.
  Two consequences recorded so they are not rediscovered the hard way.
  **`LLM_TIMEOUT=2s` is now known to be a placeholder, not a measurement**:
  GPT-OSS are reasoning models, and `gpt-oss-20b` at high reasoning effort
  measures 3.05s time-to-first-token on Groq, which alone exceeds both that
  timeout and the 3s p95 target. `reasoning_effort: low` is the fix, and the
  timeout must be set from a real measurement in Unit B.
  **Free-tier rate limits are org-level, not per-key**: 30 RPM and 1,000
  requests/day on Groq's gpt-oss models, 10 to 15 RPM on Gemini. Against
  `PRD.md` section 12's 50-to-100-record demo batch these do not fit, which
  is why `LLM_SAMPLE_RATE` exists (`docs/PHASE3_IMPLEMENTATION.md` Unit H)
  and why the default `LLM_PROVIDER_CHAIN` everywhere, including the e2e
  harness, stays `rules`.
- 2026-08-28: **Rate limiting is a distinct circuit-breaker case, not just
  another failure.** A 429 opens the breaker immediately rather than after
  `LLM_BREAKER_THRESHOLD` consecutive failures, with the cooldown taken from
  `Retry-After` when the provider sends one. Reason: on a free tier, rate
  limiting is the failure mode most likely to actually fire, and plain
  consecutive-failure counting makes the pipeline pay five doomed calls
  before protecting itself, which is a visible multi-second stall during a
  demo. It is recorded as its own hop result (`rate_limited`) rather than
  `error`, because "we were throttled" and "the provider is broken" call for
  different operator responses and the audit trail should not blur them.
  This is also what makes a three-rung chain viable: the latency objection to
  `groq,gemini,rules` assumes each rung times out, but a rate-limited rung
  costs approximately nothing because no call is made.
- 2026-08-28: **No `MODE=demo|dev|prod` enum. Config profiles instead.**
  `configs/demo.env` and `configs/dev.env` set the existing individually
  named knobs (`DEMO_TIME_SCALE`, `LLM_PROVIDER_CHAIN`, `LLM_SAMPLE_RATE`);
  the code never learns what "demo" means. Reason: a single switch that
  silently changes many behaviours at once is how a demo stops being
  reproducible and how "does this work in production?" stops being
  answerable. `docs/ARCHITECTURE.md` section 17 already committed to the
  opposite pattern, a table of named knobs each with its documented
  real-world counterpart, and a mode enum would undercut it.
- 2026-08-28: **LLM sampling is deterministic, by hash of `record_id`**, not
  random. `LLM_SAMPLE_RATE` decides per record whether to spend a live model
  call, via the already-existing per-request `ClassifyRequest.
  force_rules_only` field. Determinism is not cosmetic: re-run safety
  (identical outcomes on replay) is a headline claim proven by
  `test/e2e/rerun_safety_test.go`, and a random sample would make that
  guarantee conditional on a config value. An FNV hash costs nothing and
  removes the question. Default is 0.0, so every default run and every
  existing test is provably free.
- 2026-08-28: **Gemini is built and proven but stays OUT of the default
  chain, on measured latency.** Unit B's live check (the `manual` build tag in
  `services/classifier/internal/llm/livecheck_test.go`) measured both rungs
  against real endpoints. Groq `gpt-oss-20b` at `reasoning_effort: low`, 16
  calls: min 237ms, p50 ~570ms, max 688ms. Gemini `gemini-2.5-flash`, 6 calls:
  min 2.38s, p50 3.01s, max 6.19s, roughly five times slower.
  There is no single `LLM_TIMEOUT` that serves both. Set it near Groq's
  profile (2s) and Gemini times out on essentially every call, making the rung
  decorative. Set it above Gemini's max (7s) and one rung alone exceeds the
  Decision Engine's entire 5s `CALL_TIMEOUT`, so a `groq,gemini,rules` chain
  where both fail cannot return inside the caller's deadline at all, which is
  the exact DLQ path Unit A exists to close. Even the favourable case does not
  fit: Groq rate-limited (instant 429, ~0ms) plus Gemini at 6.19s still
  overruns 5s, and `PRD.md` section 10's 3s p95 target is blown by Gemini's
  p50 on its own.
  So the default chain is **`groq,rules`**, and `LLM_TIMEOUT=2s` is now a
  measured value rather than the placeholder it was. This is exactly the
  escape hatch Unit B's definition of done specified in advance ("if the
  measured value will not fit two live rungs inside CALL_TIMEOUT, say so and
  drop the default chain to groq,rules rather than shipping a budget that only
  works on paper"), and it is the reason that clause was written.
  The Gemini rung is **not** deleted: it is implemented, unit-tested against
  `httptest`, and confirmed working against the live API. Re-enabling it is one
  config value. Getting it back into the default chain honestly needs
  **per-rung timeouts** rather than one `LLM_TIMEOUT` for the whole chain,
  which is a small change to `provider.Config` and a natural follow-up if the
  demo wants a vendor-to-vendor failover on stage.
- 2026-08-28: **Confidence, as produced by a real model, is close to useless
  as a safety signal in its raw form**, which is an input to Unit G rather
  than an argument against it. Across the live runs, every recognised failure
  code came back at 0.90 to 1.00 from both vendors, including one the model
  got arguably wrong. The single genuinely undiagnosable record initially came
  back from Groq as a **fabricated `HARD_DECLINE` at confidence 0.90** even
  though the prompt already instructed it to answer `UNSPECIFIED` and
  escalate. Strengthening that instruction (naming the failure mode and its
  consequence rather than just the desired behaviour) moved it to
  `UNSPECIFIED` + `ESCALATE` at **confidence 0.30**, which is the honest
  answer. Two things follow. A prompt instruction is not a guarantee, so the
  enum gates and `validate.go` remain the actual controls. And a
  `CLASSIFY_CONFIDENCE_THRESHOLD` set anywhere useful, say 0.5, would have
  caught nothing before the prompt fix and catches exactly the right record
  after it, so Unit G's value depends on prompt quality rather than being
  independent of it. Unit G should say so.
- 2026-08-28: **The two rungs disagree on `EXPIRED_INSTRUMENT`, and that is
  left standing rather than tuned away.** The rules table maps it to
  `HARD_DECLINE` (`SPEC.md` section 4.2); Groq consistently answers
  `USER_ACTION_NEEDED`; Gemini answers `HARD_DECLINE`. All three then
  recommend the same action, `NUDGE_METHOD_UPDATE`, so no spending decision
  changes. The bucket is not cosmetic though: it keys the recovery priors and
  the cause-aware retry timing, and Phase 5's reporting scores classification
  accuracy by bucket against ground truth. Both readings are defensible (an
  expired card is a dead instrument, and it is also something the customer
  must act on), and forcing the model onto the table's answer would defeat the
  point of a hybrid where the model contributes the bucket. Recorded so
  Phase 5's accuracy scorer treats a bucket disagreement between rungs as a
  known property rather than a bug, and so nobody quietly "fixes" the prompt
  to match the table.
- 2026-08-29: **Fixed local-dev ports per service, and a separate optional
  compose file for Prometheus** (`docs/PHASE4_IMPLEMENTATION.md` Unit C).
  `.env.example`'s `GRPC_PORT`/`METRICS_PORT` are documented as "defaults for
  running one at a time", which is fine for a single service but leaves
  Prometheus with nothing fixed to scrape once more than one runs together.
  `make run-<service>` targets now assign each service a distinct, permanent
  pair (ingestion keeps 9090/9091; the rest start at 9190 rather than
  immediately after, to stay clear of Kafka's own host port 9092), so all
  seven can run simultaneously without colliding, and Prometheus's scrape
  config (`deploy/observability/prometheus.yml`) can name real targets.
  App services are still not containers on the docker-compose network
  (that file's own header comment, unchanged), so Prometheus reaches them
  via `host.docker.internal` with `extra_hosts: host-gateway`, the
  documented portable mechanism for Docker Engine 20.10+ and Docker
  Desktop alike. The Prometheus container itself lives in a **new,
  separate** `docker-compose.observability.yml` rather than the base
  `docker-compose.yml`: `make test-integration` depends on `make up`
  bringing the base stack up, and no integration test asserts anything
  about metrics, so there is no reason for every CI integration run to
  also pull and start Prometheus. `make up-observability` layers both
  files together for a developer who wants it.
  **Verification gap, stated plainly**: every piece was confirmed except
  the actual `host.docker.internal` hop itself, which could not be
  exercised inside this session's own sandboxed dev environment (its Bash
  tool's network namespace has no route back from Docker's containers to
  the ports those same commands bind, confirmed by testing `host-gateway`,
  plain `host.docker.internal`, and the raw docker0 bridge address, all
  refused, even though `ss -ltn` shows every service correctly listening on
  `0.0.0.0`). What WAS confirmed directly: `docker compose config` accepts
  both files together, `make up-observability` starts Prometheus cleanly,
  and its own target list shows exactly the seven expected jobs. This is
  the standard, Docker-documented pattern and should work as designed on a
  real developer machine or in CI (native Linux Engine, no extra sandbox
  layer); a future agent or the user should confirm the actual scrape
  succeeds there rather than trusting this note alone.
  **Resolution (2026-08-29, same day, once the user confirmed on their own
  machine)**: it was not a sandbox artifact. `host.docker.internal` failed
  identically from the user's own browser hitting Prometheus's Targets
  page. Root cause: this machine's Docker is Docker Desktop on WSL2 in NAT
  networking mode, where `host.docker.internal` resolves to Docker
  Desktop's own internal VM, not the WSL2 distro `make run-<service>`
  actually binds ports on. `docs/INCIDENTS.md` 2026-08-29 has the full
  diagnosis. Fix: `prometheus.yml` is now a template
  (`prometheus.yml.tmpl`) with `HOST_IP_PLACEHOLDER` in place of the
  literal host, rendered by `make up-observability` into a gitignored
  `prometheus.generated.yml`. `HOST_IP` defaults to `host.docker.internal`
  (unchanged behaviour, and correct on native Linux Engine or Docker
  Desktop's mirrored networking mode) and is overridable
  (`make up-observability HOST_IP=$(hostname -I | awk '{print $1}')`) for
  setups like this one where that alias does not route to the right
  place. Confirmed working end to end: all six real services (`reporting`
  excepted, still an unimplemented stub) show `health: "up"` in
  Prometheus's own Targets page, and Grafana's dashboards show real,
  moving request-rate numbers from live traffic sent through the actual
  pipeline.
- 2026-08-29: **Two of Phase 4's three named alerts (`docs/PLAN.md`) had no
  metric to alert on**, so Unit D built them rather than silently declaring
  the alerts done against nothing. `docs/ARCHITECTURE.md` section 13 has
  committed to `llm_fallback_total` and `stopping_rule_violation_total`/
  `incomplete_audit_trail_total` since before Phase 4 existed; Unit A's
  gRPC interceptor never touched business-specific metrics like these.
  Added `llm_fallback_total` (classifier, incremented only when a real LLM
  rung was attempted and lost to rules, see `docs/INCIDENTS.md` 2026-08-29
  for why a naive version was wrong) and three gauges in the audit
  service's existing `Watcher` (`stopping_rule_violation_total`,
  `incomplete_audit_trail_total`, and `audit_impossible_transitions_total`,
  the third one beyond `ARCHITECTURE.md`'s named list -- the verifier
  already computes it, and shipping the other two without it would leave a
  detected violation with nowhere to be seen).
  **Gauges, not Counters, despite the `_total` naming convention.** All
  three names came from `ARCHITECTURE.md` before implementation, using
  Prometheus's own `_total` suffix convention that normally means a
  monotonic Counter. The actual semantics are a snapshot of the most
  recent scan, which can legitimately fall (a batch deleted, a bug fixed
  and re-verified), so a Gauge is the honest type. Kept the names exactly
  as already documented in three other files rather than renaming them to
  something more gauge-conventional (e.g. `..._current`): a name already
  used in `ARCHITECTURE.md`, `PLAN.md`, and `PHASE2_IMPLEMENTATION.md` is a
  more expensive thing to change than the naming-convention mismatch is
  worth, and `> 0` alerting works identically regardless of metric type.
  **The much longer remainder of `ARCHITECTURE.md` section 13's metric
  list is explicitly NOT built here** (LLM call latency, circuit-breaker
  state, DLQ depth, worker-pool saturation, scheduler timing, intervention
  spend). That is real, separate instrumentation work scattered across
  several services, not something that rides along with Unit D for free;
  it should get its own unit(s) in `docs/PHASE4_IMPLEMENTATION.md` when
  someone picks it up, rather than being assumed done because "metrics"
  sounds finished.
  **Alertmanager's one receiver is a deliberate no-op.** No real
  notification channel (Slack webhook, PagerDuty key, email) exists
  anywhere in this system's config today. Rather than inventing a fake
  destination, firing alerts are visible in Alertmanager's own UI and
  Prometheus's `ALERTS` metric, which is enough to prove the rules fire
  for a demo; wiring a real receiver is a small, separate addition for
  whenever an actual destination exists.
- 2026-08-29: **Phase 4 Unit F (OpenTelemetry tracing) is deferred, not
  skipped.** It is the hardest unit in Phase 4 (Kafka needs manual trace
  context propagation on every hop, plus a new trace backend), and
  `GetRecordAudit` plus `ProviderHop` already cover most of what a demo
  would use tracing to show. Phase 5 (demo realism) is the actual blocker
  toward a working demo and comes first. Tracked in the new
  `docs/BACKLOG.md`, along with a future "production-grade hardening"
  pass to happen after Phase 5's important items are done (a hackathon
  build takes shortcuts a real deployment would not: static demo
  `API_KEY`, no TLS between services, no real Alertmanager receiver,
  single-instance infra, secrets in `.env`). Not scoped item by item yet;
  see `docs/BACKLOG.md` for the starting audit list.
- 2026-08-29: **Phase 5 Unit A's `ReportDelayedOutcome` reuses
  `scoreAndRoute` for a `FAILURE` outcome, the same re-entry to Scoring
  `handleFailedAttempt` already uses for a synchronous execute failure**
  (`docs/ARCHITECTURE.md` §7), rather than inventing separate logic for
  the async case. A failed nudge outcome and a failed synchronous retry
  both mean the same thing operationally (an attempt was spent and
  failed, re-price with one fewer attempt of budget left), and the two
  paths must not be able to disagree about it.
  **The row lock (`SELECT ... FOR UPDATE`) in `applyResumedOutcome` is
  new, though — `recordOutcome`/`recordRescore` (the scheduler's own
  writes) do not need one.** The scheduler's poll-driven writes are
  already serialised by `claimDue`'s own claim transition (only one
  execution is ever in flight for a given record between polls); a
  `NUDGED` record has no such prior claim step, and this RPC is
  at-least-once, so two copies of the same report can arrive genuinely
  concurrently with no serialisation from anywhere else. Proven, not just
  argued: with the lock removed, a 25-goroutine concurrency test
  double-applied 11-12 times out of 25 on every run; with it, exactly 1,
  clean across repeated runs. Full account in
  `docs/PHASE5_IMPLEMENTATION.md` Unit A.
- 2026-08-29: **The judging rubric is now recorded verbatim in `docs/PRD.md`
  §0.** It had never been written down anywhere in this repo; the only
  fragment quoted anywhere was "Failure recovery: what broke, and what you
  did about it" in `docs/INCIDENTS.md`. Everything else was being triaged
  against our own memory of the track. Recorded because the last two days
  are pure triage and triage against a paraphrase is how the wrong thing
  gets built. Two consequences fall straight out of the text and are worth
  stating separately from the quote itself.
  **First, there is exactly one gap.** Clause by clause the system already
  satisfies "detects revenue at risk", "determines the right intervention",
  "bounded recovery workflow", "stopping rules" and "an audit trail". The one
  clause it cannot demonstrate is **"measured money recovered across a
  batch"**, because `services/reporting/` is a 41-line stub and every
  `PRD.md` §9 headline metric is therefore unimplemented. Phase 5 Unit F is
  not one of eight units, it is the deliverable; the rest is supporting
  evidence.
  **Second, "AI Judgment: whether AI tools, LLMs, or agents were applied
  appropriately instead of forcing unnecessary tech stacks" is a scored
  criterion**, which makes additional AI surface a risk rather than a hedge.
  Three external reviews on 2026-08-29 proposed a natural-language merchant
  copilot, an LLM planner selecting interventions, and a cross-batch learning
  loop. All three declined, reasons per item in `docs/BACKLOG.md`. The short
  version: the deterministic scorer choosing among a guardrail-permitted menu
  is a *better* answer to "does the LLM decide how to spend money" than a
  planner would be, so a planner would trade a strength for a weakness.
  Also noted: selection goes straight to a technical panel, so
  `DECISIONS.md` and `INCIDENTS.md` are first-class deliverables rather than
  internal hygiene, and depth that can be defended beats surface area that
  cannot.
- 2026-08-29: **"Compliant escalation" is now two cited rules rather than an
  assertion** (`docs/PRD.md` §11a, Phase 5 Unit J). The track text asks for
  compliant escalation and this project had been using the word without
  naming a regulation, while `PRD.md` §13 carried the gap as an open
  question and `ARCHITECTURE.md` §17's retry-cap row was the only row in that
  table whose justification column did not defend itself. Three documents
  pointing at the same hole. Closed with **TRAI TCCCPR 2018** (promotional
  commercial messages only 10:00 to 21:00 IST; transactional and service
  messages exempt) and the **RBI Digital Payments E-mandate Framework**
  (pre-transaction notification at least 24 hours before an auto-debit).
  Two judgement calls inside that. **We take the conservative reading of the
  TRAI category question**: a message about a payment the customer themselves
  initiated is defensibly a Service message and therefore exempt from the
  window, but all customer-contacting interventions respect 10:00 to 21:00
  IST anyway, because a recovery agent that can quietly message people at 3am
  is the exact failure mode a "bounded" workflow exists to prevent, and the
  cost of being wrong exceeds the cost of a deferred nudge. And **the RBI
  lead time is a floor, not an offset**: a mandate retry cannot be scheduled
  sooner than 24 hours out, the salary-window calculation may push it later,
  nothing may pull it earlier. Both are guardrails, so per §5a's fixed
  ordering they can only ever remove an option, never add one.
  Limits stated in §11a rather than left implied: no DND list checking (no
  telecom integration to check against), this system does not itself send the
  pre-debit notification (in the real flow that is the issuer's obligation,
  not the merchant's agent's), and NPCI per-mandate presentation limits
  remain the simplified stand-in §17 describes. Two real rules honoured
  precisely beats a vague claim of compliance.
- 2026-08-29: **The classifier's failure-code vocabulary moves to Razorpay's
  published error list** (Phase 5 Unit I). `buckets.go`'s table was invented:
  `BANK_TIMEOUT`, `RAIL_CONGESTION`, `ISSUER_UNAVAILABLE`,
  `EXPIRED_INSTRUMENT`. Plausible, and not the taxonomy the judges work on
  daily. Existing keys stay as aliases, and `normalizeFailureCode` already
  uppercases and collapses separators, so several real codes
  (`insufficient_funds` being the obvious one) resolve to existing keys with
  no work at all.
  **One behavioural change rides along and is the actual reason this is not
  cosmetic.** The current table maps `GATEWAY_TIMEOUT`/`TIMEOUT` to
  `TRANSIENT_BANK`, whose policy is "retry soon". Razorpay's real list
  distinguishes codes where the outcome is genuinely *indeterminate*
  (`payment_timed_out`, `payment_pending`, `verification_failed`,
  `invalid_response_from_gateway`) and "we do not know whether the bank
  succeeded" is not the same fact as "it failed". Retrying the first is how a
  recovery agent creates a duplicate charge. Those codes will not resolve to
  a bucket whose policy is an automatic retry. The full treatment (a real
  `INDETERMINATE` bucket plus a reconciliation step) is parked in
  `docs/BACKLOG.md`; the principle is already implemented and tested in the
  Executor, which refuses to re-run an unresolved claim (see 2026-08-23).
  Also settles a Unit H blocker either way: `web/src/lib/format.ts`'s
  `FAILURE_CODE_LABELS` is keyed lowercase and renders blank against real
  uppercase codes, so this vocabulary had to be decided before the dashboard
  could go live regardless.
- 2026-08-29: **"Measured money recovered" gets a baseline to be measured
  against** (Phase 5 Unit K). A recovery number on its own does not show the
  agent created value, since some of those records would have recovered under
  any policy. Reporting will compute, over the same sealed `GROUND_TRUTH`,
  what a naive "retry everything three times, nudge everything once, no
  economics" policy would have recovered, and report gross, spend and net for
  both.
  **Computed analytically, not by running the batch twice.** A live A/B needs
  a policy switch in the Decision Engine, a reproducible re-roll of the same
  world, and double the demo runtime, for a number the closed form already
  gives. **The honesty requirement is not optional and belongs in the payload
  and on the tile, not only in a design doc**: both figures are evaluated in a
  world we authored, so the claim is "this policy beats a blind one under our
  modelled world", never "we recover N% more real money". Same standard as the
  `[SOURCED]`/`[ASSUMPTION]`/`[UNVERIFIED]` tagging in `configs/*.yaml`.
  The expected result is worth predicting in advance so it is not misread as
  a bug: the blind policy should recover *similar gross at several times the
  spend*, because it pays to chase `HARD_DECLINE` and `RISK_HOLD` records
  whose priors are zero for a reason. That is the argument for EV selection
  stated as a measurement, and it is what finally puts a number behind
  `PRD.md` §12 beat 3a ("the money that decision saved"), which has been a
  promise with no figure behind it since it was written.
  External anchors to sanity-check the model against something we did not
  choose: Razorpay publishes that automated retry recovers "15-20% of failed
  transactions, adding 3-5 percentage points to overall payment success rate"
  and that subscription smart-retry recovers "up to 57%". A modelled baseline
  landing far outside that range means the model is wrong, and it is better to
  find that out before a judge does.
- 2026-08-29: **The dashboard's mock backend is a development mode, not a
  demo mode** (`docs/PRD.md` §12a). Recorded because the distinction is easy
  to blur and expensive to get caught blurring. `demo/world-simulator` and
  `demo/notification-simulator` stand in for third parties we cannot have in
  a hackathon (a bank, an SMS gateway); they live under `demo/` so the
  boundary is visible in the directory tree, they hold a sealed ground truth
  the decision path provably cannot read, and they are what make outcomes
  *measurable* rather than merely observed. That is a strength and gets said
  out loud. `web/src/lib/mockEngine.ts` is different in kind: 744 lines of
  browser-side JavaScript standing in for **our own backend**, convincing
  enough to be mistaken for one, which makes it a liability on stage rather
  than an asset. It stays a supported mode (`web/AGENTS.md`) because
  developing the dashboard without the stack running is genuinely useful. The
  demo runs against a real Gateway with `VITE_API_BASE_URL` set, and any
  panel that cannot be served live is **removed from the demo rather than
  shown on mock data**.
- 2026-08-29: **`StreamBatchUpdates` and the WebSocket relay are scheduled
  last in Phase 5, not dropped.** `ARCHITECTURE.md` §6a's push design stands
  and the earlier decision not to settle for polling is not reversed. What
  changed is ordering, on a fact nobody had noticed: the dashboard already
  refetches on a 2 second interval and **every aggregate on the page is
  driven by that refetch, not by the socket** (`App.tsx`'s `setInterval(
  loadBatchData, 2000)`). The socket feeds only the scrolling event log. So
  the fallback is invisible on stage, while the push path is the most
  expensive item left in the phase: a server-streaming RPC, Kafka consumption
  inside Reporting, and a gRPC-stream-to-WebSocket bridge in the Gateway,
  plus a latent auth bug (the frontend sends the key as a WebSocket
  subprotocol, the Gateway checks the `X-API-Key` header). Against that, the
  unary `GetBatchReport`/`ListBatchRecords` path is what closes the rubric's
  one actual gap. Build the cheap correct thing first, add the push if the
  time is there, and if it is not, `docs/BACKLOG.md` carries it rather than
  the demo quietly claiming it.
  Related and worth knowing for sequencing: **Reporting does not depend on the
  World Simulator.** Reporting reads Postgres, and the Executor's existing
  scripted stub already writes recovered/escalated/nudged rows with real
  `cost_paise`, so Unit F can be built and merged against the stub in parallel
  with Unit C. The apparent single chain C → D → F → G → H is not real, and
  treating it as real is the difference between finishing and not.
- 2026-08-29: **`docs/API_GATEWAY.md` is frozen (Phase 5 Unit O).** Three
  judgment calls made while resolving the six pre-freeze gaps, worth stating
  separately since they set precedent for every route built after this.
  **Enum wire spelling is the full proto constant string**
  (`"RECORD_STATE_RETRY_SCHEDULED"`), not a shortened frontend-style name.
  Rejected the alternative because a second spelling is a second thing that
  can drift from `common.proto`, with no test to catch it; the literal name
  greps straight back to the source enum. **WebSocket auth stays a
  subprotocol, not a query parameter.** The frontend already sends the key
  this way; the real choice was whether to change it, and a query parameter
  puts the API key in server access logs and browser history by default,
  a subprotocol does not. The Gateway side is the one that needs to change,
  from checking `X-API-Key` to checking the negotiated subprotocol.
  **`POST /v1/batches` grows a `count` form, and it never carries
  `GROUND_TRUTH`.** The demo generate button needs a one-field request, and
  `scripts/batchgen`'s own header comment is explicit that only it may write
  `GROUND_TRUTH`, directly to Postgres, specifically so the answer key is
  never reachable through a public API. Extending that boundary to the
  Gateway route would mean either the Gateway touching Postgres directly
  (`ARCHITECTURE.md` section 3a forbids this, protocol translation only) or
  Ingestion learning to write `GROUND_TRUTH` (breaking the "only this tool"
  invariant for a second tool). Neither is worth it for a demo button, so a
  `count`-submitted batch reports like real production traffic: no
  `accuracy`, no `baseline_comparison`, same as a real webhook batch. A demo
  run that needs the accuracy story is seeded with `scripts/batchgen` ahead
  of time and selected via the new `GET /v1/batches` instead.
  Two additive proto changes were identified but deliberately left for the
  PR that implements them rather than made here: `ListBatches` (backs the
  new list route) and `AuditEntry.ev_score_at_decision`/
  `p_recovery_at_decision` (needed before Unit L's provenance UI can render
  the EV snapshot per attempt, the data already exists on
  `INTERVENTION_ATTEMPT`, it just isn't surfaced through Audit yet).
- 2026-08-29: **The classifier's failure-code table now cites Razorpay's own
  published error list, and two existing codes changed behaviour** (Phase 5
  Unit I). `services/classifier/internal/rules/buckets.go`'s table was
  invented (`BANK_TIMEOUT`, `RAIL_CONGESTION`, etc, plausible but not what
  Razorpay actually returns). Every old key stays as a working alias; the
  new Razorpay codes are added with a `[SOURCED]` comment naming the
  platform's own `source` field where useful context (e.g. `bank_not_available`
  being gateway-caused, not the customer's fault).
  **The real change is that `GATEWAY_TIMEOUT` and `TIMEOUT` no longer
  auto-retry.** Both already existed in the table, mapped to
  `TRANSIENT_BANK`. A timeout means the outcome is genuinely unknown, not
  that the payment failed, and auto-retrying an unresolved payment risks a
  duplicate charge on one that may have already gone through. The same
  reasoning applies to four newly-added codes with the same shape
  (`payment_timed_out`, `payment_pending`, `verification_failed`,
  `invalid_response_from_gateway`). All six now resolve to `RISK_HOLD`,
  the only bucket in the table whose policy is a guaranteed escalation, with
  a rationale that explicitly explains the duplicate-charge risk rather than
  borrowing `RISK_HOLD`'s generic "held for risk review" wording, which would
  misdescribe a technical ambiguity as a fraud hold.
  A real `ROOT_CAUSE_BUCKET_INDETERMINATE` plus a reconciliation step (ask
  the rail what actually happened) is the honest full fix and stays parked
  in `docs/BACKLOG.md`; this unit closes the dangerous half (never
  auto-retry an unresolved outcome) without it. `scripts/batchgen/profile.go`'s
  code pools were updated to match, including moving `GATEWAY_TIMEOUT`'s
  `ObviousBucket` to `RISK_HOLD` so the "obvious bucket" a naive lookup would
  produce stays consistent with what the real classifier now does.
  Scope cut, deliberate: the design also considered surfacing Razorpay's
  `source` field dynamically in the composed rationale text; done instead as
  a comment on each table entry, since threading it through at runtime
  would restructure the map for a "reads as domain-aware" nicety rather than
  a correctness need.
- 2026-08-29: **Two compliance guardrails are enforced, not just cited**
  (Phase 5 Unit J). `docs/PRD.md` §11a named TRAI TCCCPR's contact-hour
  window and the RBI e-mandate framework's pre-debit lead time; this makes
  both real. `contactHourWindow` (schedule.go) defers a nudge's `due_at` to
  the next 10:00 IST open if it would otherwise land outside 10:00-21:00
  IST; `mandateLeadTimeFloor` refuses to schedule a `RECORD_TYPE_MANDATE`
  retry sooner than `RETRY_MANDATE_LEAD_TIME` (default 24h) from now.
  **The floor composes with the existing salary-window logic rather than
  replacing it**: `retryDueAt` computes the bucket-specific timing first
  (unchanged), then applies the mandate floor only if the record type is
  `MANDATE`, pulling an earlier date up to the floor but never pulling a
  later one down. A `MANDATE` record whose `INSUFFICIENT_FUNDS` salary
  window already lands three weeks out is untouched; one whose
  `TRANSIENT_BANK` timing would otherwise retry in 30 minutes is floored to
  24 hours.
  **Threading record type through required a schema-adjacent change**:
  `claimedRecord` (store.go) had no `RecordType` field, since nothing
  before this needed one. Added it, backed by `r.type` already present in
  `record`, populated by both `claimDue` and `loadNudged` (the latter
  matters because `ResumeNudge`'s failure path also calls `retryDueAt`).
  **IST is `time.FixedZone`, not a tzdata lookup**: fixed UTC+5:30, no DST,
  and a distroless runtime image may not carry tzdata at all.
  **Scope cut, tracked in `docs/BACKLOG.md`**: the audit `reason` string
  does not yet cite the specific rule when a deferral or floor actually
  fires (it still describes the scoring decision). Folded into Unit M's
  planned rework of the same plumbing rather than threaded twice.
- 2026-08-29: **The audit trail now records every candidate action considered
  and every action the guardrails refused, not just the winner** (Phase 5
  Unit M). `economics.Model.Best` used to discard every losing candidate
  the instant it was beaten, `continue`d before ever being compared, and
  `guardrailVerdict.blocked` was thrown away right after being used to
  filter the permitted set. Both are now captured in a new
  `DecisionTrace{Candidates, Blocked}` returned by `scoreAndRoute` and
  persisted as `audit_entry.decision_trace` (migration 00006, JSONB).
  **`Best` is now defined in terms of the new `ScoreAll`/`BestOf`, not a
  second copy of the same loop**: `ScoreAll` scores every candidate
  unfiltered, `BestOf` picks the winner from an already-scored slice, and
  `Best` is just `BestOf(ScoreAll(...))`. This was the deliberate design
  choice, not an afterthought: two independently-maintained selection loops
  could drift on which candidate wins, and the one thing this unit must
  never do is change that. Proven byte-identical with a dedicated
  equivalence test and confirmed by every pre-existing `economics` test
  passing unchanged.
  **The trace attaches to exactly one audit row per decision**: the step
  where `From == RECORD_STATE_SCORING`, since that is the one instant a
  real comparison happened; `scoringPath`/`rescoringPath`'s other step
  (entering Scoring) and `directPath`'s escalation-bypass step never ran an
  economics comparison and get NULL. Verified against real Postgres
  (`TestHandleMessageSchedulesRetryPersistsDecisionTrace`), and
  adversarially: inverting the `From == SCORING` check was caught by that
  same integration test; separately dropping a candidate from `ScoreAll`'s
  loop was caught by a dedicated coverage test. Both reverted.
  **Encoding failures are diagnostic, not load-bearing**: `encodeDecisionTrace`
  mirrors the existing `encodedHops` pattern exactly, logging and storing
  NULL on a JSON marshal failure rather than losing the transaction over
  data that exists to explain a decision, not to make one.
- 2026-08-29: **Reporting's two unary RPCs are merged; `StreamBatchUpdates`
  is deliberately deferred** (Phase 5 Unit F). This closes the rubric
  audit's one confirmed gap ("measured money recovered across a batch");
  the streaming half (a Kafka consumer plus a gRPC-stream-to-WebSocket
  bridge) is a separate, materially more expensive unit, and the
  dashboard's own polling already covers every number that matters
  without it, per `docs/PHASE5_IMPLEMENTATION.md`'s own ordering advice.
  `docs/PLAN.md`'s checkbox stays unticked until streaming lands too: the
  checklist item bundles both, and `docs/ENGINEERING.md` section 11 says
  not to tick a box before the whole item is done.
  **Built to mirror `services/audit/` deliberately, not by coincidence**:
  it is the closest existing service in shape, a pure Postgres reader with
  no Kafka consumption in this pass, so the same `New(pool)`/`store` split
  applied directly rather than inventing a new service skeleton.
  **A real, confirmed gap, not a guess**: `processing_failure_count`
  cannot be computed from Postgres today. A dead-lettered record is
  published to Kafka only and leaves no trace in any table by design
  (`services/decision-engine/internal/engine/dlq.go`), so Reporting,
  which reads only Postgres, has nothing to count. Returns `0`,
  documented in code and tracked as its own `docs/BACKLOG.md` entry
  scoped to whoever owns the Decision Engine's DLQ path, rather than
  silently reported as if it were a real measurement.
  **Accuracy is nil, never a populated zero, when a batch has no
  `GROUND_TRUTH`**: `docs/API_GATEWAY.md`'s own contract requires this
  distinction ("a missing key means no answer key exists, distinct from a
  real zero"), and it is adversarially verified: removing the nil guard
  was caught by `TestGetBatchReportOmitsAccuracyWithoutGroundTruth`.
  Verified against real Postgres, 10 tests
  (`services/reporting/internal/server/reporting_test.go`), covering the
  headline aggregate, both `GROUP BY` breakdowns, ground-truth accuracy
  and confusion, pagination across pages, and not-found/invalid-argument
  edges.
- 2026-08-30: **World Simulator is real, replacing Phase 1's stub** (Phase 5
  Unit C). `SimulateOutcome` now rolls a record's hidden `GROUND_TRUTH`
  profile against the action taken: a retry answers immediately, a nudge
  answers `PENDING` and its real answer is delivered later via a Redis
  sorted set (`wsim:delayed_outcomes`) and a background poller calling
  `DecisionEngine.ReportDelayedOutcome`. This unparks the ~70% of records
  that previously sat in `NUDGED` forever, since nothing ever resolved
  them, and is what makes Unit F's accuracy/recovery numbers actual
  measurements rather than a scripted happy path.
  **The correct-action mirror table is a deliberate small duplication**:
  `isCorrectAction` needs to know what the classifier would have
  recommended for a record's true bucket, but cannot import
  `services/classifier/internal/rules` (private to that service; cross-
  service code is gRPC only). Same precedent as
  `scripts/batchgen/profile.go`'s `ObviousBucket` table, and carries the
  same "must stay in sync" comment.
  **A failed retry reuses the record's own original `failure_code`**
  rather than inventing a per-attempt code model `GROUND_TRUTH` does not
  carry: "the same underlying reason struck again" is simple and
  defensible.
  **Each call re-rolls independently**, matching the plain-English
  probability model in `docs/ARCHITECTURE.md` section 6 rather than a
  decay curve the data does not encode.
  **The Redis member format extends the architecture doc's own example**
  (`record_id:attempt_number:outcome`) with a fourth field, `failure_code`,
  since `ReportDelayedOutcomeRequest` accepts one and it is already
  documented as informational downstream (`scheduler.go`'s `ResumeNudge`),
  so carrying it through costs nothing.
  **`queue.due()` is not perfectly atomic, by design**: a single poller
  goroutine in a single instance has nothing to race against, and
  `ReportDelayedOutcome` is already at-least-once/idempotent-safe
  downstream. Revisit only if this service ever runs with more than one
  replica.
  **`deliver` retries up to 3 times before logging a loss**, mirroring
  `scheduler.go`'s `executeWithRetry` exactly, rather than inventing a new
  resilience pattern.
  **New dependency, `github.com/redis/go-redis/v9`**: the first real Redis
  client in the codebase (every other Redis mention elsewhere is a
  deliberate "not Redis" comment). No shared `internal/platform/redis`
  wrapper added, since this is the only consumer today.
  **Verified live against the real stack, not only by test**: ran the
  service, made a real `SimulateOutcome` call, confirmed the Redis entry's
  exact format, and confirmed `requests_total` appeared on `/metrics`
  with correct labels.
  **Adversarial verification caught a bug in the test suite itself, not
  just the code**: the zero-delay-resolves-immediately test originally
  used `ESCALATE` as its action, which is never a nudge, so removing the
  zero-delay guard did not fail it (the guard was never reached). Fixed
  the test to use a real nudge action type, re-verified the break was now
  caught, then reverted. A reminder that a green adversarial-verification
  pass on a broken test proves nothing.
  **Found while implementing, not fixed here**:
  `demo/notification-simulator` is still an unimplemented stub. Unit D
  ("wire real World/Notification Simulator clients") may need to build it
  for real, not only wire a client to it, tracked in `docs/BACKLOG.md` if
  D does not end up covering it on its own.
- 2026-08-30: **Executor wired to real World/Notification Simulator
  clients, and the nudge path restructured to actually use World
  Simulator's answer** (Phase 5 Unit D). Closing this properly needed
  more than swapping two stubs.
  **`demo/notification-simulator` was still an unimplemented 41-line
  stub**, discovered while starting this unit (flagged in Unit C's own
  writeup beforehand). Built for real: logs what would have been sent and
  prices it by channel, matching `StubNotification`'s existing behaviour,
  now over a real gRPC boundary. Holds no state; no Postgres, no Redis.
  **`route.go`'s `nudge()` never called `RecoveryActionPort` at all**,
  only `NotificationPort` for the send. Without also calling
  `SimulateOutcome` for a nudge, Unit C's entire delayed-outcome mechanism
  is dead code in production: nothing would ever call
  `DecisionEngine.ReportDelayedOutcome` for a nudge, and every nudge would
  keep sitting in `NUDGED` forever. `nudge()` now sends the message, and
  only if delivered, asks the recovery port whether/when the customer
  reacts, using its `Outcome`/`resolves_at` directly rather than the
  router's old static `nudgeResolveDelay` constant. A zero-delay profile
  resolves immediately rather than being forced into `PENDING`, mirroring
  `retry()`'s existing immediate/deferred split.
  **`Router` lost its `clock.Clock` and `nudgeResolveDelay` fields**: once
  `resolves_at` comes from the recovery port, nothing in `Router` needed a
  clock any more, so both were removed rather than left as dead fields.
  **The two new adapters (`WorldSimRecovery`, `NotificationSimAdapter`,
  `ports/grpc.go`) inject cost themselves**: World Simulator's proto
  response carries no cost field by design, since cost is a checked-in
  constant, not something "reality" reports back. A nudge's recovery-port
  call costs `0` on this port; its real cost is the notification's.
  **`NUDGE_RESOLVE_DELAY` is retired, not deleted**, in `.env.example`,
  matching this project's existing precedent for retired config knobs.
  **One pre-existing integration test needed a real fix, not a stub
  update**: `TestExecuteRedeliveredPendingNudgeReplaysPromptly` used a
  zero-value `countingRecovery{}`, harmless before this unit (the recovery
  port was never called for a nudge) and wrong after (a nil `resolves_at`
  on a `PENDING` outcome), since the fake now needed a real scripted
  answer.
  **Verified live against the real stack**: ran all three services
  together, executed a real `NUDGE_METHOD_UPDATE` and a real `RETRY`
  through Executor, confirmed the correct channel/cost/outcome/
  `resolves_at` at every hop, the Redis delayed-outcome entry, and
  `requests_total` on all three `/metrics` endpoints.
  **`docs/PLAN.md` never had a checklist line for Unit D at all**, a gap
  since Phase 5 was first drafted (every other lettered unit had one).
  Added it now, ticked, rather than leaving the work permanently invisible
  in the human-facing checklist.
- 2026-08-30: **CI found what local verification for Unit D missed: the
  whole `test/e2e` suite broke**, and the fix needed to be the real one,
  not a workaround. Two independent gaps, both closed:
  **(1) The harness never learned about the two new required Executor
  settings and never started either simulator.** `test/e2e/harness_test.go`
  now builds and starts `demo/world-simulator` and
  `demo/notification-simulator` alongside the other six services.
  `buildBinary` gained a `pkgDir` parameter, since it had hardcoded
  `services/` for every prior caller and both new binaries live under
  `demo/`.
  **(2) Every e2e test seeds or submits its own record and none of them
  ever wrote a `GROUND_TRUTH` row**, because nothing before Units C/D
  ever needed one; World Simulator requires one to answer at all. Seeded
  one per record across all seven affected test files, with values chosen
  to reproduce each test's own pre-existing deterministic assumption
  (`recovery_probability=1.0` for "always succeeds" cases, `0.0` for Unit
  H's "this one real attempt must fail" case, a large sentinel
  `response_delay_seconds` for the two NUDGE smoke cases, since World
  Simulator answers `PENDING` for any nudge with a positive scaled delay
  regardless of the roll).
  **A real race was caught locally with `-race`, before pushing again, not
  left for CI to find twice.** HTTP-submitted records only get their id
  back after submission, so seeding ground truth for them races the
  scheduler's first claim; under the default `retryDelay="1s"` (which
  collapses to microseconds at `DEMO_TIME_SCALE=300000`, leaving only the
  ~300ms poll interval as real buffer), `-race`'s overhead was enough to
  lose that race once, dead-lettering a record. Fixed the same way Units
  H/K already establish: every test using the tight default now passes
  `"3000000s"` (~10s real) instead, confirmed clean across two full
  `-race` runs afterward, one via the exact command CI uses.
  **The lesson, stated plainly so it is not relearned**: verifying a unit
  live against the running stack proves the new code path works; it does
  not prove nothing else depended on the path being replaced. The e2e
  suite depended on the Executor's old stub being deterministic and
  ground-truth-free in ways neither this unit's own tests nor its live
  verification would ever exercise, because they never ran the existing
  test suite that did.
- 2026-08-30: **Reporting's baseline comparison (Phase 5 Unit K) evaluates
  the naive policy analytically, in expectation, not by sampling.** Since
  `GROUND_TRUTH.recovery_probability`/`wrong_action_probability` already
  are probabilities and no run-time randomness is needed, each of the
  naive policy's up to 4 attempts (3 retries, stopping at first success,
  then one nudge if all three failed) is modelled as an independent
  Bernoulli trial and summed to an expected recovered amount and an
  expected spend, matching World Simulator's own "each call re-rolls
  independently" semantics (`demo/world-simulator/internal/server/
  outcome.go`) without needing a `randSource` or a seed. The whole batch's
  gross/spend are summed as floats and rounded once at the end, not per
  record, so fractional-paise rounding cannot visibly drift on a large
  batch.
  **Two modelling choices were open and are now settled**: the naive
  policy's one nudge is `NUDGE_REMINDER`, not `NUDGE_METHOD_UPDATE`
  (a generic "please pay" message is what an undiagnosed system sends; a
  targeted "update your card" ask would itself be a diagnosis this policy
  deliberately lacks), and its channel cost is SMS, mirroring
  `services/executor/internal/ports/cost.go`'s own default for an
  unspecified channel. Both are named explicitly in
  `services/reporting/internal/server/baseline.go` rather than left
  implicit, since either choice measurably changes which buckets the naive
  policy gets "credit" for.
  **A third checked-in copy of the retry/SMS cost constants** now exists
  (`services/reporting/internal/server/baseline.go`'s
  `naiveRetryCostPaise`/`naiveNudgeCostPaise`), alongside the Decision
  Engine's YAML read and the Executor's own literal copy
  (`services/executor/internal/ports/cost.go`), because a cross-service
  import of `ports` is a compile error. Guarded by
  `TestReportingCostsMatchInterventionCostsYAML`, mirroring the Executor's
  own `cost_reconciliation_test.go` almost line for line.
  **The proto change shipped as its own PR (#62), merged before the
  Reporting PR that depends on it**, per `AGENTS.md`'s standing rule; both
  the message shape and the JSON example already existed in
  `docs/API_GATEWAY.md` from Unit O, written before either PR, so neither
  needed a design decision at implementation time, only building to what
  was already specced.
  **Verified live**: seeded a two-record batch directly in Postgres (one
  `TRANSIENT_BANK`, one `RISK_HOLD`, the same values as the hand-computed
  unit tests) and called the real running `services/reporting` binary over
  gRPC; the response matched the hand-computed numbers exactly
  (gross 99240, spend 131, net 99109 paise) and `requests_total` on
  `/metrics` incremented for the call.
- 2026-08-30: **Hinglish nudge composition (Phase 5 Unit E) mirrors the
  provider chain and circuit breaker rather than sharing them with
  Classify.** `provider.NudgeChain`/`NudgeBreaker` are separate types from
  `Chain`/`Breaker`, reusing every piece that does not depend on the
  request/response shape (`rungCtx`, the hop constants, `hopResultForError`,
  `sourceFor`, and the construction invariants extracted into
  `validateConfig`/`validateChainOrder`/`resolveRungs` so `NewChain` and
  `NewNudgeChain` share them) but each getting its own circuit state per
  named provider rather than one shared health signal for both Classify and
  ComposeNudge calls to, say, Groq. The shared-state version is arguably
  more correct (an outage is one outage, not two independently-discovered
  ones), but reworking the already-tested `Breaker` to expose that safely
  was judged a materially larger, riskier change than this unit's scope
  justified. Recorded as a deliberate tradeoff to revisit if a real outage
  ever shows the two breakers disagreeing about a provider's health.
  **The LLM request builders are new types, not the existing ones with a
  field left unset**: `groqNudgeRequest`/`geminiNudgeRequest` omit the
  JSON-schema constraint entirely, rather than reusing `groqRequest`/
  `geminiRequest` with `ResponseFormat`/`GenerationConfig` left as a zero
  value, because Go's `encoding/json` `omitempty` does not omit a
  non-pointer struct field regardless of its contents — reusing the
  existing types would have silently sent an empty, invalid schema
  constraint on every nudge request. Caught by a dedicated wire-shape test
  (`TestNudgeRequestCarriesNoResponseSchema`) before it shipped, not by a
  live failure.
  **The real amount is never written by the model or the template**: both
  write the literal token `{{AMOUNT}}`, substituted by `server.go` only
  after `provider.validateNudge` confirms the raw answer contains no digit
  outside that one placeholder occurrence. The token is duplicated
  byte-for-byte between `provider.AmountPlaceholder` and
  `llm.amountPlaceholder` rather than imported, because `internal/provider`'s
  own test file (`fallback_test.go`) imports `internal/llm`, so `llm`
  importing `provider` back is a genuine cycle — found by the compiler on
  the first attempt, not reasoned out in advance.
  **A real gap was found by live verification, not by any test**:
  `intervention_attempt.message_source` already existed in the schema
  (`docs/ARCHITECTURE.md` section 5b requires the audit trail to
  distinguish a generated message from a templated one) but nothing wrote
  it, because `ExecuteRequest` had no field to carry it — invisible to
  every unit test, since none of them checked a column nothing was
  populating. Found only by seeding a real batch against the real running
  seven-service stack and reading the row back. Fixed properly, not
  deferred: a second proto-only PR (#64, `ExecuteRequest.message_source`)
  merged before threading it through
  `clients.composeNudge` → `clients.execute` → `attempt.Store.Claim`.
  **The Decision Engine's scheduler is the only Execute call site, and now
  needs the Classifier too**: `NewScheduler`'s own doc comment previously
  said it "only needs the Executor client, not the Classifier" — true for
  re-classification (still true: a resumed record never re-classifies),
  false for composing a nudge's wording, which this unit needed. Composing
  the message happens inside the SAME bounded retry loop
  `executeWithRetry` already used for Execute failures, rather than a
  second failure-handling story: a ComposeNudge blip retries with the same
  backoff, three failures dead-letter the record the same way three
  Execute failures always have.
  **Adversarially verified three times** (inverted `isNudge` in the
  scheduler; forced `message_source` to UNSPECIFIED at the Decision
  Engine's forwarding call; forced it to UNSPECIFIED again at the
  Executor's persistence call), each caught by a dedicated test and
  reverted. **Verified live twice**, before and after the `message_source`
  fix, against the real seven-service stack: the composed, substituted
  Hinglish text and its source both landed correctly in Postgres, and
  `/metrics` showed `nudge_fallback_total` and `ComposeNudge`'s
  `requests_total` both move.
  **Built test-first throughout**, per explicit instruction this session:
  every new behaviour had its test written and confirmed failing against
  not-yet-written code before the implementation that made it pass.
- 2026-08-30: **`audit.events`'s wire schema lives in a new
  `internal/platform/auditevent` package, as plain JSON, not a proto
  message.** Starting Phase 5 Unit F's streaming half found the topic had
  no publisher anywhere in the codebase despite being fully architected
  since Phase 0 (docs/INCIDENTS.md, same date, has the full story). Fixing
  the producer side first raised a real design choice: the payload two
  services (Decision Engine, Reporting) must agree on needed a home.
  Considered reusing `reportingv1.BatchUpdate` directly, since its shape
  (`record_id`, `from_state`, `to_state`, `recovered_delta_paise`) is
  almost exactly right and would give producer and consumer the same type
  by construction. Rejected: proto/gen exists for gRPC contracts between
  services that call each other; Decision Engine and Reporting never do
  (they relate only through this one Kafka topic), and importing a proto
  package purely to reuse a struct shape stretches that convention past
  what it is for. `internal/platform` is the actual answer AGENTS.md
  already gives for "two services need to agree on something and neither
  owns it": a new small package there, one const (the topic name) and one
  struct (`Event`, plus `batch_id`, which `BatchUpdate` does not carry
  since a stream is already scoped to one batch -- the Kafka topic is not).
  **One notification per committed transaction, not one per internal
  audit-entry row.** `scheduleNew` can write New -> Scoring -> X in one
  transaction; the published event carries the transaction's start and end
  state only, matching `reporting.v1.BatchUpdate`'s own doc comment
  ("deliberately small: the dashboard refetches... rather than having
  aggregates recomputed per event") and the section 5 sequence diagram,
  which shows exactly one `publish audit.events` per decision, not one per
  row in `AUDIT_ENTRY`.
  **Publish failures are logged, never propagated**, mirroring
  `deadLetterPublisher`'s existing pattern exactly: the owning transaction
  has already committed by the time `audit.events` is published, so a
  Kafka outage must cost a stale cache or a missed live-dashboard tick
  (docs/ARCHITECTURE.md section 10a's own contract), never a failed
  Execute/Classify/ResumeNudge call that already succeeded where it
  actually matters, Postgres.
- 2026-08-30: **Reporting's live fan-out (`Hub`, Phase 5 Unit F's
  `StreamBatchUpdates`) drops rather than blocks when a subscriber falls
  behind, and has no replay buffer.** Two related choices, both trading
  perfect delivery for the property that actually matters here: one slow
  or stuck dashboard connection must never stall the Kafka consumer loop
  for every other subscriber, on this batch or any other. A publish to a
  full subscriber channel is silently dropped for that one subscriber
  (`select` with a `default` case) rather than blocking; a client that
  subscribes after a transition already published simply never sees it,
  the same way a live feed works and a log does not. Both are the same
  tolerance `docs/ARCHITECTURE.md` section 10a already states for
  `audit.events` itself ("losing a message costs a stale cache, never a
  wrong number"), carried one hop further down the pipeline rather than
  re-argued from scratch.
  **`Hub` is exported, its methods are not.** `cmd/main.go` constructs one
  `*Hub` and hands the same instance to both the Kafka consumer
  (`AuditConsumer`) and the gRPC server (`Server`), so the type itself
  has to be visible outside the `server` package; `subscribe`/`publish`
  stay lowercase because only those two package-internal types ever call
  them, and there is no reason for `cmd/main.go` to be able to.
  **Tested in three tiers on purpose, not one integration test standing
  in for all of it**: the fan-out logic (`hub_test.go`) and the Kafka
  message translation (`consume_test.go`) are both pure Go, no build tag,
  fast, and each proves one thing precisely; only the gRPC-facing half
  (`stream_test.go`) needs real Postgres, for the same `batch_id`
  validation `GetBatchReport`/`ListBatchRecords` already do. A minimal
  fake `grpc.ServerStreamingServer` (only `Send`/`Context` overridden,
  everything else delegates to a nil-embedded `grpc.ServerStream` the
  handler never calls) drives one assembly test through the real
  `StreamBatchUpdates` method end to end, proving the three pieces
  actually connect, not just that each is correct in isolation.
  **Reporting is deliberately not added to `test/e2e/harness_test.go` in
  this PR.** Unit G's Gateway WebSocket relay needs Reporting running in
  the harness anyway to dial `StreamBatchUpdates` through, and a
  submit-batch-and-watch-the-WebSocket e2e test is the more complete
  version of the live verification this unit already did by hand.
  Recorded here so the omission reads as a sequencing choice, not an
  oversight the next unit has to rediscover.
- 2026-08-31: **`DEMO_TIME_SCALE` scales waits we schedule, never a window
  compared against elapsed real time.** `RecoveryWindow` is now the one
  guardrail duration deliberately left unscaled, and the asymmetry is recorded
  here because it looks like an oversight and a future agent will otherwise
  "fix" it back.
  The distinction is the whole content of the decision. `RetryDelay`,
  `NudgeDelay`, `ContactCooldown` and `RetryMandateLeadTime` are futures we
  choose and then wait out; compressing them is the entire purpose of the
  knob. `RecoveryWindow` is compared against `now - record.created_at`, and no
  scale factor compresses the wall clock that age is measured on. Scaling it
  therefore never sped anything up, it only made the window shorter than the
  pipeline's own processing latency, and compression amplified that latency
  into logical time: at 300000, ten real seconds of classify-price-schedule
  "spends" 34 logical days of a 7 day window. A 100-record batch escalated 73
  records for "recovery window closed" before the economics scorer priced any
  of them (`docs/INCIDENTS.md` 2026-08-31).
  **Measured effect of the fix**, same seed, same sealed ground truth, only
  this changed: net recovered went from Rs 171,477 to Rs 536,405 against an
  unchanged baseline of Rs 487,769, i.e. from losing to a blind
  retry-everything policy by 2.8x to beating it, while spending Rs 44 against
  the baseline's Rs 79 and separately declining to chase 32 records worth
  Rs 344,385. Recovery rate 18% to 51%. The economics layer was never the
  problem; it had not been allowed to run.
  Consequence stated so it is not rediscovered as a bug: unscaled, this
  guardrail never fires in a short demo. That is correct (no receivable is
  stale after 60 seconds) but it does mean the window is not demonstrable on
  stage without a real logical clock, which is parked in `docs/BACKLOG.md`
  rather than solved.
- 2026-08-31: **Config profiles are applied with make's `PROFILE` variable,
  never by sourcing.** `make demo-up PROFILE=demo`, or
  `make run-<service> PROFILE=demo`. Implemented as a second `include` of
  `configs/$(PROFILE).env` after `include .env`, since a later assignment wins
  in GNU Make; anything the profile does not mention still falls through to
  `.env`, and a command-line variable still outranks both.
  This reverses the usage documented when the profiles were added
  (2026-08-28), which said to `set -a && source configs/demo.env`. That never
  worked: a makefile assignment takes precedence over the environment, so
  `include .env` silently discarded every sourced value on every target. The
  profile had therefore never once taken effect, and every demo run before
  today used real-time waits and no LLM chain. It is the reason the
  recovery-window bug above went unnoticed and the reason an earlier run
  appeared to show the baseline winning.
  The lesson worth keeping, and the reason this is a decision rather than only
  an incident: **a config mechanism nobody has executed end to end is not a
  config mechanism.** This one was reasoned about carefully, written into
  three documents, and never run once. The check is two lines and is now in
  both the README and `configs/demo.env`:
  `make --eval='__show:; @echo $(DEMO_TIME_SCALE)' __show PROFILE=demo`.
  Also corrected while here: `configs/demo.env` set
  `LLM_PROVIDER_CHAIN=groq,gemini,rules`, contradicting this log's own
  2026-08-28 entry, which measured Gemini at p50 3.01s and concluded the
  default must be `groq,rules`. The live run confirmed that measurement
  independently (`groq:circuit_open,gemini:timeout,rules:ok` six times in the
  audit trail, i.e. Gemini cost a full timeout and answered nothing). The
  profile now says `groq,rules`.
- 2026-08-31: **Added `make demo-up` / `make demo-down`.** Running the product
  meant starting nine services in nine shells, each needing identical
  configuration, where one terminal with the wrong profile silently poisons
  the whole run and the result reads as a modelling problem rather than a
  setup problem. That is not hypothetical, it is exactly what happened. It is
  also the first thing anyone cloning the repo hits.
  `demo-up` brings up infra, applies migrations, and starts all nine in
  dependency order, logging each to a gitignored `.demo-logs/`. `demo-down`
  kills **by listening port**, not by process name: `go run` execs the built
  binary from a temp path, so `pgrep` on the source path matches nothing,
  which defeated the first attempt at stopping the stack by hand. The
  individual `run-<service>` targets stay, for foreground development.
- 2026-09-01: **Unit AA, absent `due_at` on the wire is an empty string, not
  a null or an omitted key.** `record_state.due_at` is nullable in Postgres
  and set only while a record is `RECORD_STATE_RETRY_SCHEDULED` or
  `RECORD_STATE_NUDGE_SCHEDULED`; every other state, including
  `RECORD_STATE_NUDGED`, has none. `docs/API_GATEWAY.md`'s own wire
  conventions rule out both other options: convention 6 forbids `omitempty`
  on a documented field, and no field on this contract is ever rendered as
  JSON `null`. An empty string is also not a new convention, it is the one
  already used for `rationale` and `message_text` on the audit trail
  ("empty strings, never omitted, when not applicable"), so `due_at` follows
  existing precedent rather than adding a fourth representation of absence
  to the contract.
  A real bug turned up while wiring the Gateway side of this: the existing
  `formatTimestamp(ts interface{ AsTime() time.Time })` helper cannot detect
  a nil `*timestamppb.Timestamp` passed through that interface parameter (a
  nil concrete pointer boxed into an interface is not `== nil`), and
  `Timestamp.AsTime()`'s own getters are nil-safe, so the result silently
  came out as `"1970-01-01T00:00:00Z"` instead of `""`. `formatTimestamp` had
  never actually been exercised with a genuinely absent timestamp before
  (`GeneratedAt` and `CreatedAt` are always populated in practice). Fixed
  with a second helper, `formatOptionalTimestamp`, that takes the concrete
  `*timestamppb.Timestamp` so the nil check runs before the interface
  boundary. Caught by the failing test written first
  (`TestListBatchRecordsRendersDueAtOrEmptyStringWhenAbsent`), not by
  inspection, which is the point of writing it first.
- 2026-09-01: **Unit W, all four demo control RPCs land on
  `WorldSimulatorService`, not a new proto/service.** The spec calls out
  batch seeding specifically as needing World Simulator's
  `GROUND_TRUTH`-write permission; `ListScenarios`, `GetWorldState` and
  `InjectPoison` don't strictly need it (`ListScenarios` is pure static
  data, `GetWorldState` only reads Redis, `InjectPoison` only needs a Kafka
  producer). They went on the same service anyway rather than a fifth demo
  component, because the spec frames the whole surface as "proxying to a
  demo-only backend" (singular), World Simulator is that backend, and a
  second demo service would exist solely to hold a Kafka producer for one
  RPC. World Simulator gained a `*kafkax.Producer` for this reason; it had
  none before.
  Scenario presets (`normal`/`bank-outage`/`salary-day`/`dead-cards`) are an
  override layer on top of `syntheticgen.GenerateRecord`, not a second
  generation model: each preset calls `GenerateRecord` for the base record
  shape (amount, instrument sharing) and then, for a configured fraction of
  records, forces `type`/`failure_code`/bucket and re-derives the recovery
  numbers via a new export, `syntheticgen.ProfileForBucket`, so a forced
  record uses the exact same hidden-profile model as an unforced one. The
  one honest gap: `bank-outage`'s "concentrated on one issuer" has no
  `issuer`/`bank` field to concentrate on yet (`RECORD` carries no such
  column; it arrives with Unit Z's real Razorpay payload shape), so it
  concentrates on a single real failure code (`BANK_NOT_AVAILABLE`) instead,
  noted in the scenario's own description string rather than left implicit.
  The Gateway gates `/v1/demo/*` by whether `EnableDemoControls` was called
  before `Routes()`, not by checking a flag inside each handler: `New()`'s
  signature is unchanged (no existing test needed touching), and an
  unregistered route 404s by construction rather than needing every handler
  to remember to check a flag and return 404 itself.
- 2026-09-02: **Unit AC, `message_text` written onto `audit_entry` rather
  than joined from `intervention_attempt`.** The composed Hinglish nudge
  text was landing on `intervention_attempt.message_text` (the Executor's
  own table) but never on `audit_entry.message_text`, the column Audit's
  `GetRecordAudit` actually selects, so 134 real composed messages sat in
  the database unreachable through the API. Between the two fixes
  `docs/DEMO_READINESS.md` named, chose the write: `recordOutcome`
  (`services/decision-engine/internal/engine/store.go`) now takes the
  composed message and writes it in the same transaction as the outcome,
  the same pattern `attempt_number` and `cost_paise` already use on that
  row. The join was rejected on correctness grounds, not just style: a
  claimed nudge-scheduled record's "scheduler claimed due record" audit
  entry and its later outcome entry share the same `attempt_number` (both
  belong to the same claim), so a naive `LEFT JOIN
  intervention_attempt ON (record_id, attempt_number)` would attach the
  message to both rows, implying it was sent twice. Ruling that out needs
  either a `reason`/`to_state` filter baked into the join or a second join
  key Audit does not otherwise need, at which point the join is not
  actually less code than the write. Scope: only `recordOutcome`, the
  transition the drawer's `Nudged` entry renders. A nudge that fails to
  send synchronously (`NOTIFICATION_NOT_SENT`) goes through
  `recordRescore` instead, which still has no `message_text`; left alone
  since the measured defect and the demo beat are both about the sent
  message, not the failed one.
- 2026-09-02: **Unit AD, World Simulator's outcome rolls seeded per-record,
  not via a shared sequential stream.** `scripts/batchgen` and SeedBatch
  were already seeded for record generation; the unseeded part was
  `SimulateOutcome`'s Bernoulli roll (`rand.go`'s `randSource`,
  auto-seeded `math/rand/v2` globals, one shared draw sequence across every
  concurrent gRPC call). A mutex-guarded `*rand.Rand` would still be
  nondeterministic in the sense that matters: which record consumes which
  draw from the stream depends on goroutine scheduling order, not on the
  request. Instead, each roll derives from `hash(seed, record_id,
  attempt_number)` (`seededRand` in `rand.go`), so a record's own roll is
  reproducible independent of concurrency, ordering, or what else was in
  flight. The seed itself lives on `Server` as an `atomic.Int64`
  (`server.go`'s `randFor`), set once by `SeedBatch` (`seed.go`) from the
  same seed already threaded through `POST /v1/demo/batches`, so one seed
  reproduces both the generated batch and its outcome rolls. Before any
  batch is seeded through that RPC, `s.seed` is zero and every roll falls
  through to the old unseeded `realRand{}`, so `scripts/batchgen` and
  anything writing straight to Postgres are unaffected, matching
  `docs/DEMO_READINESS.md`'s "keep unseeded as the default."
  Known gap, accepted rather than fixed: `s.seed` is server-wide, not
  per-batch, so seeding a second batch while an earlier one is still
  in-flight reseeds its rolls too. Closing that needs a seed column
  alongside `GROUND_TRUTH`, not attempted here because the intended demo
  workflow seeds one batch and lets it play out before seeding the next.

- 2026-09-02: **Unit AD take two, the roll key supersedes the record_id
  keying recorded above.** The entry above describes #99, which keyed
  `SimulateOutcome`'s draw off `hash(seed, record_id, attempt_number)`. That
  is superseded by #104 and should not be treated as current. The reasoning
  in it about per-record derivation beating a mutex-guarded shared stream
  still holds and is why #104 kept the shape; what was wrong was the key.
  `record_id` is `uuid.NewString()` at generation time in both `SeedBatch`
  and `scripts/batchgen`, fresh every run regardless of seed, so the
  deterministic function was never fed the same inputs twice and two
  same-seed runs still diverged by Rs 153,632. #104 adds
  `GROUND_TRUTH.roll_key` (migration 00007), set from
  `(seed, ordinal index in batch)` by both writers and used by the roll
  instead. Record ids deliberately stay random: deriving them from the seed
  would mint identical ids for two batches seeded alike and collide on that
  table's primary key. Rejected alternative: keying off `(batch_id, ordinal)`
  computed at read time, which needs a second query on the hot path, where
  `roll_key` rides along on the profile row already being fetched.

- 2026-09-02: **A seed is not promised to reproduce a run end to end.**
  Following the measurement in `docs/INCIDENTS.md` 2026-09-02, the honest
  scope of "seeded" here is: inputs, the sealed answer key and the economics
  reproduce exactly; the run's final totals do not. Two causes, both
  deliberate earlier choices now in tension. `schedule.go` enforces TRAI
  TCCCPR 2018 contact hours against the real clock, and
  `DEMO_TIME_SCALE=300000` makes one real second about 3.5 simulated days,
  so sub-second jitter flips whether a nudge is permitted. And
  `LLM_SAMPLE_RATE=0.15` routes a subset to a live model. Closing either
  gap is a real change (a virtual clock, or a deterministic-only demo mode)
  and is not being done under hackathon time, so the claim is being narrowed
  rather than the system changed. Do not put a reproducible rupee total on a
  slide.

- 2026-09-02: **Unit AE's forbidden-vocabulary list is derived from the
  generated proto enum maps, with a small hand-written second tier.** Tier
  one reads `RootCauseBucket_name`, `ActionType_name` and `RecordState_name`,
  so a new enum value is covered as soon as protoc regenerates and nothing
  goes stale by hand. Tier two is four literal meta words (`bucket`,
  `root cause`, `action type`, `record state`), which is what actually leaked
  and cannot be derived mechanically: splitting the constants on `_` also
  yields `retry`, `action`, `risk` and `new`, all legitimate customer
  vocabulary that this repo's own fixtures use. The validator, not the
  prompt, is the control, consistent with 2026-08-28.

- 2026-09-03: **The live socket branches on the WebSocket close code rather
  than treating every close as failure.** The Gateway closes with
  `StatusNormalClosure` when the upstream report stream ends, meaning the
  batch finished, and with `StatusInternalError` otherwise. So 1000 reports
  `complete` and deliberately does not reconnect, because nothing further can
  arrive; anything else reconnects with backoff capped at 30s, showing amber
  and degrading to red only after three consecutive failures while still
  retrying. Rejected alternative: reconnecting on every close, which would
  reopen a socket forever against a finished batch.

- 2026-09-03: **The web CI job is pinned to node 24 rather than loosening
  `npm ci` to `npm install`.** `web/package-lock.json` is npm 11's; node 20
  ships npm 10, which resolves esbuild's optional platform packages
  differently and rejects the lockfile outright. Keeping the lockfile
  authoritative is the point of running it in CI, and an install that
  silently accepts a drifting dependency tree would not have caught the
  mismatch at all.

- 2026-09-02: **`decision_trace` is a typed proto message, `DecisionTrace`
  with a nested `Candidate`, not a passthrough of the raw JSON string
  already sitting in the `audit_entry.decision_trace` column (Unit S).** The
  column's own comment already calls the JSON shape informal, self-describing
  storage, not a contract; a typed message makes the wire contract explicit
  in the proto file instead of leaving a client to infer it from an example,
  and it removes an entire class of bug (a client parsing the JSON string
  itself, or worse, the raw text landing on screen when a caller forgets to
  parse it, both of which have happened elsewhere in this project with
  informally-shaped columns). Field types follow the existing Go types
  exactly rather than reinventing them: `ev_paise` is a `double`, matching
  `economics.Score.EVPaise`, because it is a probability-weighted
  expectation, not money anyone holds, and can be negative or fractional;
  `cost_paise` stays an `int64`, real money. `blocked` stays a
  `map<string, string>` keyed by the action's full enum name rather than
  `common.v1.ActionType`, because proto3 map keys must be an integral type
  or string, never an enum; the map value only ever needs to be looked up by
  that string in practice (the frontend renders the key as a label and the
  value as prose), so no information is lost by keeping it a string.

- 2026-09-02: **`GET /v1/records/{record_id}/audit` gained a `decision_trace`
  field on each entry, editing the frozen `docs/API_GATEWAY.md` contract
  (Unit S).** The freeze document says plainly that finding it wrong or
  ambiguous after the freeze date is a bug in the document, to be fixed
  there first; `decision_trace` was simply never in it, since nothing read
  the column when the document was written. The field is present only on
  the one audit entry that actually left `RECORD_STATE_SCORING`, using the
  same "missing key means no answer" convention the document's own
  `BatchReport.accuracy` and `baseline_comparison` fields already
  established, rather than a null value or an empty object; there is no
  zero value for "no comparison happened" that would not be confused with
  "one candidate was compared and it happened to be empty". Within the
  object, `candidates` and `blocked` are each independently omitted when
  empty for the same reason, one level down. `ev_paise` is documented as
  the one field on the whole contract that breaks Wire convention 1 (money
  as integer paise): it is copied from `economics.Score.EVPaise`, which is
  deliberately a float because it is an expectation, not a stored payment.
  Which candidate "won" is deliberately not a separate wire field: a client
  derives it with the same rule `economics.BestOf` uses server side
  (highest `ev_paise` above zero, ties broken by position in the
  `candidates` array as given), documented explicitly so a client does not
  try to parse the free-text `reason` field to find the winner instead.

- 2026-09-02: **The decision-trace panel's "which action won" logic lives in
  a small pure function, `pickWinningCandidate`
  (`web/src/lib/decisionTrace.ts`), not in the component and not by reading
  `entry.reason`.** `reason` on the winning entry happens to name the action
  in prose today, but `docs/API_GATEWAY.md` says outright that it is not a
  machine contract, so a frontend that parsed it would be one backend
  wording change away from silently marking the wrong row as chosen.
  Re-deriving the winner from `candidates` with the exact same rule
  `economics.BestOf` applies (highest positive EV, first-in-array on a tie)
  keeps the two implementations provably in agreement without sharing code
  across the Go/TypeScript boundary, and is unit-tested against a case
  where sorting by EV for display would pick a different tie-break than the
  correct first-in-list rule, to keep the two concerns (ranking for display,
  choosing the winner) from silently collapsing into one.

- 2026-09-02: **`RecordSummary` gained `first_action_at` and `last_action_at`
  rather than a per-record array of every audit timestamp (Unit AH).**
  `due_at` was the only time field on this message and it is always in the
  future or absent, so the timeline goes blank the instant a run finishes.
  A first/last pair is enough to draw the historical view actually built: a
  short connector from when a record was first classified to the most
  recent thing that happened to it, plus a marker at the end colored by
  outcome. `first_action_at` is `MIN(audit_entry.ts)` for the record, one
  correlated subquery per page using the existing `audit_entry_record_idx
  (record_id, ts)`, not a second endpoint and not N+1 gRPC calls.
  `last_action_at` is a zero-cost addition: it mirrors
  `record_state.last_action_at`, a column the Decision Engine already
  writes on every transition, just never surfaced before. A rejected
  alternative was shipping the full audit-entry timestamp array per record:
  more bytes per record for a chart that only ever reads the two extremes,
  and Audit's per-record trail route already exists for anyone who needs
  the whole sequence for one record.

- 2026-09-02: **`docs/API_GATEWAY.md` documents `first_action_at` and
  `last_action_at` on `GET /v1/batches/{batch_id}/records` before the Unit
  AH implementation that reads them, following the precedent Unit S set for
  `decision_trace`.** Both follow the exact convention `due_at` already
  established on the same message: empty string when absent, never
  omitted, never null (Wire convention 6), not the "missing key" convention
  `decision_trace`/`accuracy`/`baseline_comparison` use, because these two
  are scalar timestamp fields in the same category `due_at` is already in,
  not optional nested objects.

- 2026-09-02: **The historical timeline shows real wall-clock time and the
  simulated equivalent it represents, and the multiplication direction was
  verified against `docs/ARCHITECTURE.md` section 17 and `configs/demo.env`
  before writing any code, not assumed (Unit AH).** `DEMO_TIME_SCALE`
  compresses waits the system schedules (retry delay, contact cooldown,
  mandate lead time); a real elapsed duration this run actually took,
  multiplied by that constant, recovers what that duration represents, the
  same relationship `CLAUDE.md` already states in prose ("one real second
  is about 3.5 simulated days"). `RecoveryWindow` is deliberately excluded:
  `docs/INCIDENTS.md` 2026-08-31 already established it is compared against
  a record's real elapsed age directly, never scaled, so this module never
  divides a duration to schedule one, only multiplies one to describe one,
  and the two directions are not treated as interchangeable anywhere in
  `web/src/lib/demoTime.ts`. The constant (`300000`) is hardcoded in the
  frontend rather than fetched, the same choice already made for
  `LLM_SAMPLE_RATE` and `RECOVERY_WINDOW`: `docs/API_GATEWAY.md` is the
  frozen wire contract for record and batch data, not operational config.
  The dual-time axis is shown only on the History view, not Live: Live's
  ticks are already a relative countdown to a live "now" marker, a
  different, still-useful idiom that the absolute real/simulated pairing
  would clutter rather than clarify.

- 2026-09-02: **The Live/History toggle picks its own default once, from
  whatever records the component first mounts with, rather than always
  opening on Live (Unit AH).** If nothing is pending but the batch already
  has history, it opens on History instead of showing the "nothing pending
  right now" empty state on a run that has already finished, which is the
  exact symptom this unit exists to fix. `App.tsx` keys `TimelineView` on
  the active batch id so switching batches remounts it and re-evaluates the
  default, rather than a manual toggle choice from a previous batch
  silently carrying over to an unrelated one.

- 2026-09-02: **Unit AH's reporting-layer test
  (`TestListBatchRecordsIncludesFirstAndLastActionAt`,
  `services/reporting/internal/server/reporting_test.go`) is written and
  proven to compile under the `integration` build tag
  (`go build -tags integration ./...`, `go vet -tags integration ./...`)
  but was not executed**, honoring the working agreement not to run `make
  test-integration` against the live demo stack (`CLAUDE.md`). It follows
  the exact fixture pattern `TestListBatchRecordsIncludesDueAt` already
  uses (real Postgres via `testPool`, seeded rows, `t.Cleanup`), so it runs
  the same way that test already does whenever the tagged suite next runs.
  The Gateway-layer test for the same fields
  (`services/api-gateway/internal/httpapi/report_test.go`) is a real unit
  test against a fake gRPC client and was run both red (before the mapping
  code existed) and green (after), the same TDD proof available for every
  other layer in this unit.

- 2026-09-02: **The record drawer widened from `max-w-lg` (512px) to
  `max-w-3xl` (768px) rather than a narrower estimate, judged against the
  content rather than picked in advance (Unit AN).** 512px was sized before
  the drawer carried Unit S's three-column decision panel, a composed
  Hinglish message quote, and an audit trail that can run to a dozen
  entries; at that width the decision panel's numeric columns cramped and
  its blocked-action labels truncated (`Nudge (Update ...)`). 768px was
  checked against the actual widest realistic content, `Nudge (Update
  Method)` in the blocked-actions list, rather than assumed sufficient:
  `DecisionTracePanel`'s blocked-row label column moved from
  `minmax(0,7rem)` to `minmax(0,11rem)` to use the extra room, the one
  change made inside that component. Its approved internals (ranked
  candidates by EV, winner marked by value not row position, blocked
  actions in their own section) were left untouched, as scoped.

- 2026-09-02: **The record drawer's per-entry time presentation composes
  three figures rather than adding a fourth line of the timeline's own
  absolute-position phrasing (Unit AN).** Every entry already showed the
  real timestamp (`formatTime`, unchanged). Added: what that instant
  represents in the 7-day recovery window, using `formatSimulatedElapsed`
  unmodified, the exact function and phrasing `HistoryTimeline`'s axis
  already uses, so a judge reading both surfaces never sees them disagree;
  and, for every entry after the trail's first, the elapsed gap since the
  previous entry in both real and simulated terms
  (`+2.3s real, +8 days simulated`), which the unit brief called "the
  genuinely useful number when reading a trail" and which
  `formatSimulatedElapsed`'s own window-relative framing does not answer
  (`day 3.5 of the 7-day recovery window` says where an instant sits, not
  how far apart two instants are). A new function,
  `formatSimulatedGap` in `web/src/lib/demoTime.ts`, covers the second
  case: same day/hour/minute rounding as `formatSimulatedElapsed` so the
  two never disagree about which side of a boundary a value falls on, but
  phrased as a plain duration ("8 days") rather than a window position,
  since a gap between two entries is not itself a point in the window.
  Built test-first against hand-checkable values keyed to
  `DEMO_TIME_SCALE=300000`, the same discipline `formatSimulatedElapsed`'s
  own tests already used, before any drawer code read it. It reuses
  `simulatedElapsedMs`/`DEMO_TIME_SCALE` from the existing module rather
  than a second hardcoded scale constant. The trail's own first entry has
  no previous entry to gap against, so it shows the position line alone;
  a zero-entry trail renders an explicit empty state instead of an empty
  spine.

- 2026-09-02: **Repetitive audit-entry metadata is demoted by contrast, not
  removed, and only two `Source` values are promoted to a badge (Unit
  AN).** `actor` (almost always `"system"`) and `source` on most entries
  (`SOURCE_UNSPECIFIED`, since most audit entries are plain state
  transitions with no composed message at all) render as small, low-contrast
  text, same information, lower visual weight. `SOURCE_LLM` and
  `SOURCE_RULES_FALLBACK`, the two values that actually vary entry to entry
  and answer "which rung answered", get a small coloured badge instead,
  reusing colours already in the app's vocabulary (amber, already used for
  the LLM rationale block's `Cpu` icon; blue, already used for the
  retry/deterministic states) rather than introducing a new hue.

- 2026-09-02: **`web/src/types.ts`'s `Source` type gained a fourth value,
  `SOURCE_UNSPECIFIED`, a frontend-only correction (Unit AN).**
  `common.v1.Source`'s zero value was missing from the type even though
  `api-gateway`'s `audit.go` renders it (`e.GetSource().String()`) on every
  audit entry that never set a source, which is most entries in a trail.
  `docs/API_GATEWAY.md` documents only the 3 values a composed message
  actually carries and was left untouched, since it is the frozen wire
  contract for shape, not an enumeration of which existing enum values are
  a "real" answer versus a default; the type fix is additive, matches
  `common.proto`'s actual 4-value enum, and needed no backend or wire
  change. See `docs/INCIDENTS.md` 2026-09-02.

- 2026-09-02: **HistoryTimeline moved from one shared row per bucket to one
  sub-row per record, a small-multiples/Gantt layout, to fix overplotting
  rather than adding opacity tricks on top of the shared-row layout (Unit
  AO).** With up to 28 Abandonment and 16 Hard Decline records sharing a row,
  every connector line drew on top of the others regardless of opacity;
  jittering the shared row (the original layout's own mitigation) only
  scattered markers vertically, it never separated the lines connecting them.
  A dedicated row per record removes the overlap by construction instead of
  making it harder to see. Row height (13px, `TIMELINE_SUB_ROW_HEIGHT` in
  `web/src/lib/timelineGeometry.ts`) and `amountRadius`'s bounds (retuned to
  2.5-5.5px from the single-row layout's 3.5-9px) were chosen together so the
  largest marker at 100-record density still clears its row's edges by at
  least a pixel; `amountRadius` has no other caller, so retuning it was safe
  without a parallel variant. Records within a bucket sort by
  `first_action_at` then `record_id` (a stable tie-break) rather than
  render-order, so the row order reads as "who acted first" consistently
  across renders; one pre-existing test that had incidentally relied on
  array-declaration order for two same-bucket records was updated to look up
  each record by a new `data-record-id` attribute instead of by position,
  since declaration order was never the property being tested (marker size
  vs. amount was).

- 2026-09-02: **Isolating a bucket collapses its siblings to a one-line
  summary rather than removing them, and a bucket filter and an outcome
  filter compose rather than being mutually exclusive (Unit AO).** The
  alternative, hiding non-selected buckets entirely, would need an extra
  "show all buckets" step before switching which bucket is isolated;
  collapsing to a single compact row keeps every bucket clickable while
  isolated, so switching focus stays one click, and it caps the isolated
  view's height as a side effect (a collapsed bucket costs one row instead
  of up to 28). The outcome legend is deliberately built from the whole
  run's records, never re-scoped to the current bucket filter, so it does
  not shift under the reader as they filter and so a state absent from the
  isolated bucket is still one click away; the direct consequence is that a
  bucket-plus-outcome combination can legitimately match nothing, which is
  why a filtered-empty combination is reachable through ordinary use rather
  than only by deliberately constructed props, and needs its own honest
  `EmptyState` rather than a blank chart. The time axis's domain and
  `amountRadius`'s `maxAmountPaise` scale both stay computed from the whole
  unfiltered run regardless of the active filters, so filtering narrows
  which records draw without ever rescaling the axis or changing what
  "biggest amount" means mid-filter.

- 2026-09-02: **The record area is capped at 480px
  (`TIMELINE_MAX_BODY_HEIGHT`) with internal scroll, and the axis lives
  outside that scrolling container rather than inside it (Unit AO).** A
  per-record row layout makes total height proportional to record count
  (roughly `recordCount * 13px` plus small inter-bucket gaps) rather than a
  fixed 7-row height, so an unfiltered 80-record run is genuinely tall
  (over 1000px) if nothing bounds it. 480px was chosen against the real
  shape of a run rather than picked arbitrarily: an isolated single bucket
  at typical demo density (up to ~28 records) fits without a scrollbar,
  while the unfiltered all-buckets view scrolls, which is intentional
  since isolating a bucket is the same click that both focuses the view
  and, as a side effect of the collapse behaviour above, brings it under
  the cap. The axis renders in its own fixed row below the scrolling
  record area, computed from the same stable domain either way, so the
  time reference never scrolls out of view while the record area above it
  does.

- 2026-09-02: **The connector line's colour is a fixed neutral slate
  (`#cbd5e1`), not derived from the bucket or any per-record value, and it
  stays neutral on hover too, only its opacity and width change (Unit
  AO).** The bug being fixed was two encodings (bucket colour on the line,
  state colour on the marker) competing for the same visual channel; giving
  the connector a third colour on hover (e.g. recolouring it toward the
  hovered record's state) would have reintroduced exactly that competition
  for the one interaction state where it matters most. Bucket identity is
  carried by the row grouping and its own label/legend entry, which was
  always the primary channel for it, so the connector loses nothing by
  going neutral.

- 2026-09-02: **`RecordDrawer` suppresses a rationale box only when it is
  identical to the immediately previous entry's, not via a running set of
  rationales seen so far in the trail (Unit AO).** The defect was a specific
  adjacency (Scoring, then the entry right after it, both writing the same
  sentence because the Decision Engine composes it once at scoring rather
  than per hop); a broader "hide any repeat anywhere earlier in the trail"
  rule would also hide a genuinely coincidental repeat of an unrelated
  rationale much later in a long trail, which is not the defect this unit
  was asked to fix and would silently drop real information. Compared
  `entry.rationale` against `detail.entries[i - 1].rationale` only.

- 2026-09-02: **`DecisionTracePanel`'s EV column gets `whitespace-nowrap`
  and `flex-shrink-0` rather than only a wider fixed width (Unit AO).** The
  reported defect, `+₹1837.99` wrapping after the `+`, happens inside a
  flex row (`CandidateRow`'s `<li className="flex items-center gap-2">`)
  where a fixed-width child can still be compressed below its own content
  width unless told not to shrink; widening `w-16` alone would have raised
  the threshold at which the same wrap recurs for a larger EV rather than
  removing the failure mode.

- 2026-09-03: **Unit AI routes a record to a live model by the deterministic
  rules engine's own confidence, not by a random sample of `record_id`, and
  keeps `LLM_SAMPLE_RATE` only as a ceiling on total live calls.** Before
  this, `sampledForLLM` (Phase 3 Unit H) hashed `record_id` against
  `LLM_SAMPLE_RATE` and called it "sampling": a random 15% got a live model
  call regardless of whether the record needed one. `ARCHITECTURE.md`
  section 17 already named the intended design ("route by ambiguity, call
  the model when the deterministic table is not confident"), and the rules
  engine already returns a `Confidence` on every response
  (`rules/actions.go`'s `bucketToAction` table, 0.00 for the unknown-code
  path up to 1.00 for `RISK_HOLD`), so the fix was to actually read it.
  `clients.classify` (`services/decision-engine/internal/engine/clients.go`)
  now always places a rules-only "peek" call first
  (`force_rules_only=true`; the rules rung does no I/O and cannot fail, so
  this costs nothing but one gRPC round trip) and only places a second,
  full-chain call when that answer's `Confidence` is below the new
  `LLM_ROUTE_CONFIDENCE_THRESHOLD` (`CLASSIFY_CONFIDENCE_THRESHOLD`, the
  pre-existing threshold in `engine.go`, is a different knob entirely: it
  decides whether to escalate AFTER classification, not whether to spend a
  call before one). Threshold set to 0.80 in `configs/demo.env`, chosen by
  reading the actual confidence table rather than picking a round number:
  `TRANSIENT_BANK` (0.90), `HARD_DECLINE` (0.85), `INSUFFICIENT_FUNDS` and
  `ABANDONMENT` (0.80 each) and `RISK_HOLD` (1.00) stay rules-only, since
  the rail told us plainly what happened; `USER_ACTION_NEEDED` (0.70, the
  broadest bucket by the table's own comment), `OVERDUE` (0.75, no
  technical failure to point at) and the unknown-code path (0.00) are the
  genuinely ambiguous cases sent to a live model, budget permitting. The
  code default is 0.0, so an unconfigured deployment routes nothing to a
  live model, matching `LLM_SAMPLE_RATE`'s own zero-value default: a
  threshold of 0.0 can never be satisfied because confidence is never
  negative.

  **The streaming tradeoff.** `LLM_SAMPLE_RATE` had to keep meaning
  something, because Groq's free tier is 30 RPM and a seeded batch is
  consumed in seconds; the brief's own framing was "keep it as a ceiling,
  not a selector." The Decision Engine consumes `raw.events` as a stream,
  one record at a time, and never sees the whole batch before deciding, so
  there is no list to rank by ambiguity and take the top N% of without
  buffering the batch first, which would give up streaming to get it.
  Considered and rejected: buffer a window of records and rank it before
  classifying, which trades the one property a streaming pipeline exists
  for (bounded per-record latency, no batch boundary to wait on) for a
  ranking quality this demo does not need. Chosen instead: `llmBudget`
  (`llm_budget.go`), a running ratio under one mutex. Every `classify` call
  reports itself exactly once, via `consider(eligible)`, whether or not
  confidence judged it ambiguous, because the ceiling bounds a fraction of
  every record processed, not only the ambiguous ones; a call is granted
  only when doing so would keep `llmCalls/total` at or under the rate. This
  is greedy (it spends the moment it can afford to) and provably never
  exceeds the ceiling (`TestLLMBudgetNeverExceedsRateAtAnyPoint`,
  `services/decision-engine/internal/engine/llm_budget_test.go`, checked as
  a running invariant across 10,000 records, not only at the end). The
  honest cost of this simplicity: ordering bias. A batch that is mostly
  ambiguous spends its whole ceiling on the earliest such records and later
  ones fall back to rules even though nothing about them is less ambiguous;
  a full-batch rank would not have this bias, at the cost of not streaming
  at all. For a demo-scale batch this is an acceptable, stated tradeoff, not
  a silent one. A second, smaller cost: a transient RPC failure that
  triggers `classifyWithRetry`'s retry re-runs the whole `classify` call,
  including the confidence peek, so a retried record can call
  `llmBudget.consider` more than once for what is really one record. This
  does not let the ceiling be exceeded (`consider` never grants more than
  the rate allows regardless of how many times it is called) and only
  matters on the rare transient-failure path, so it was accepted rather
  than threaded through as extra state across retries.

  **Decision ordering is unchanged.** Routing only changes which records the
  model gets to see; `engine.go`'s `decide` still reads only `Bucket` off
  whichever response won (rules-only or live), never `RecommendedAction`,
  and the economics scorer still prices from the prior table keyed on that
  bucket. A live model call was never allowed to choose an action or a
  budget, and this change does not touch that boundary.

- 2026-09-03: **Quota exhaustion (budget-denied or provider-denied) is
  surfaced as one count, `BatchReport.llm_quota_exhausted_count`, computed
  from the same `provider_hops` audit trail column that already recorded
  `rate_limited` and `circuit_open`, rather than a new column or a new
  event type (Unit AI).** The one new fact this change introduces, that the
  Decision Engine's own sampling ceiling denied a call, is recorded the
  same way: `clients.go` appends a `sample_budget:exhausted` hop to the
  rules-only response it already has, so exhaustion from three different
  causes (Groq's free-tier throttling, the classifier's own breaker already
  open, or the Decision Engine's ceiling already spent) lands in one place
  Reporting already reads, instead of three. The counting rule itself,
  `llmQuotaExhausted` (`services/reporting/internal/server/exhaustion.go`),
  is a small pure function over decoded hops, unit-tested without a
  database; the SQL and `hopcodec.Decode` plumbing around it
  (`llmQuotaExhaustedCount`, `store.go`) follows this project's existing
  "do not mock what you own" convention for anything that does need
  Postgres, filtering on `from_state = RECORD_STATE_NEW` because
  `provider_hops` is only ever written on a record's first classification,
  never on a re-score (`recordRescore` leaves it `NULL` on purpose,
  `store.go`'s own comment). `docs/API_GATEWAY.md` documents the field
  before this implementation, per its own frozen-document rule and the
  precedent Units S (#107) and AH (#109) already set for editing it
  honestly rather than silently diverging: always present, defaulting to 0,
  not the "missing key means no answer" convention `accuracy` and
  `baseline_comparison` use, because nothing about this field depends on
  `GROUND_TRUTH` existing. The dashboard renders it as `LlmQuotaBanner`, a
  quiet slate note that renders nothing at all at count 0, deliberately not
  the amber `AlertTriangle` styling `RecordsTruncatedBanner` uses: a
  free-tier quota being spent is a normal operating condition on this
  project's stated cost posture, not a fault to flag, and dressing it as one
  would misstate what actually happened.
- 2026-09-03: **Unit AJ's live traffic generator became `scripts/loadgen`,
  not a new script.** `docs/PLAN.md` Phase 6 and `docs/ARCHITECTURE.md`
  section 14 had already named `scripts/loadgen` (throughput testing against
  the public HTTP API, distinct from `scripts/batchgen` which writes
  straight to Postgres because only it may seed `GROUND_TRUTH`) but no such
  directory existed yet: it was a planned slot, not a built tool, checked
  before writing anything per this unit's own instruction to read what is
  already there first. Building Unit AJ's CLI there rather than under a
  third name closes that gap instead of creating a second, differently
  named tool with an overlapping job description, and lets Phase 6's own
  "scripts/loadgen built" checkbox become literally true. Scope is
  deliberately narrower than section 14's full description (no ramping
  load profile, no p50/p95/p99 latency histogram): this unit needed steady
  webhook traffic for the Live Event Stream panel, not a performance test
  harness, and the remaining Phase 6 items (ramp/peak profile, latency
  measurement, worker pool calibration) stay open, unclaimed by this
  change.
- 2026-09-03: **`scripts/loadgen`'s rate limiter schedules every event at a
  fixed offset from the run's start time (`NextSendTime(n) = start + n *
  interval`), never accumulated from the previous send.** The alternative,
  sleeping `interval` after each send, drifts under load (a slow HTTP round
  trip delays every subsequent send by the same amount, compounding) and
  invites exactly the burst this unit's own requirement rules out ("rate
  limiting should be steady, not bursty") the moment something tries to
  "catch up". Pinning every event to an absolute offset means a slow send
  can only ever fall behind its own schedule, never accelerate to
  compensate, and it made the steadiness claim a pure, table-testable
  function (`TestNextSendTimeIsEvenlySpaced`,
  `scripts/loadgen/ratelimiter_test.go`) with no live server, no goroutine
  timing, and no flakiness, exactly what the unit's TDD instruction asked
  for. `clock.Clock` is injected (`docs/ENGINEERING.md` section 2)
  specifically so `RateLimiter.Wait`'s context-cancellation branch
  (SIGINT/graceful shutdown) is also synchronously testable: an
  already-cancelled context returns immediately without needing the fake
  clock to advance at all.
- 2026-09-03: **No API contract addition for the "no ground truth" dashboard
  explanation (Unit AJ part 2).** `batch.source` turned out to already be on
  the wire: `GET /v1/batches` (`docs/API_GATEWAY.md`) has returned
  `BatchSummary.source` since Unit G, and `App.tsx` already holds the batch
  list that response came from in state. So the active batch's source is
  looked up from state already on hand (`batches.find(b => b.batch_id ===
  activeBatchId)?.source`) rather than added to `BatchReport`, which has no
  `source` field and would have been a real, frozen-doc-first contract
  change if it had been needed. The copy itself
  (`web/src/lib/groundTruth.ts`, `noGroundTruthReason`) is one function
  shared by the Classification Accuracy panel and `BaselineComparisonCard`
  so the two panels can never drift into saying slightly different things
  about the same fact, and it branches only on the literal `"webhook"`
  source (`services/ingestion/internal/server/store.go`'s
  `rollingBatchSource`, what `scripts/loadgen` posts to) for the specific
  "this is live production traffic" wording; any other ground-truth-less
  source (a `count`-submitted batch, anything unrecognised) still gets an
  honest, generic reason rather than a wrong guess about which case it is.
  Kept deliberately quiet in tone (no exclamation marks, tested for it) per
  the unit's own instruction that this is "a statement about method, not a
  warning".
