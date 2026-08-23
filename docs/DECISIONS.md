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
