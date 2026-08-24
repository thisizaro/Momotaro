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
