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
