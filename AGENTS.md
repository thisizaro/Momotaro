# AGENTS.md

Notes for any AI agent (or human) working in this folder. If you are new to
this project: read this file first, then `docs/PRD.md`, then
`docs/ARCHITECTURE.md`. This file is the quick reference for *what* was
decided; those two have the full reasoning, and `docs/DECISIONS.md` has the
chronology of *why*.

> **Before you write a single line of code, read `docs/ENGINEERING.md`.**
> It is mandatory, not advisory. It covers TDD expectations, clock
> injection (required, our core logic is time-based and untestable
> without it), context deadlines, graceful shutdown, money handling, and
> the Definition of Done that gates every `docs/PLAN.md` checkbox.

## Project

**Momotaro**, a Payment Failure & Mandate Recovery Agent, built for Track 03
(AI Revenue Recovery). Detects revenue-at-risk records (failed payments,
mandates, abandoned checkouts), diagnoses root cause via a hybrid rules+LLM
classifier, executes a bounded, auditable recovery playbook, and reports
measured recovered revenue against a hidden ground truth.

## Locked decisions (do not relitigate without updating this file)

- **Language**: Go, every service.
- **Inter-service communication**: outside the cluster (client, load
  generator, dashboard <-> API Gateway), HTTP/WebSocket, whatever fits.
  Inside the cluster, every direct service-to-service call is **gRPC**, no
  internal HTTP. Full rule: `docs/ARCHITECTURE.md` section 2.
- **Kafka**: pub/sub only, three topics (`raw.events`, `audit.events`,
  `raw.events.dlq`). Not used for anything that is naturally
  request/response, those are gRPC calls. Full rule:
  `docs/ARCHITECTURE.md` sections 2, 8, 8b.
- **Postgres is the sole source of truth for history.** State changes and
  their `AUDIT_ENTRY` rows are written in one transaction by the service
  that owns the change. `audit.events` on Kafka is a notification stream
  for cache invalidation and live updates, never a system of record. Table
  ownership is tabulated in `docs/ARCHITECTURE.md` §10a, and no service
  writes a table it does not own.
- **Decision Engine runs a scheduler worker** (Postgres `due_at` +
  `FOR UPDATE SKIP LOCKED`) that advances all time-based work: salary-window
  retries, cooldown expiry. Without it, scheduled retries never fire. Not a
  separate service. `docs/ARCHITECTURE.md` §7a.
- **Decision Engine consumes Kafka with a keyed worker pool**, not one
  record at a time, because a blocking 2s LLM call otherwise caps throughput
  ~50x below the NFR. Offsets commit only the highest contiguous completed
  prefix. `docs/ARCHITECTURE.md` §8a.
- **Every external dependency sits behind a circuit breaker**, not just a
  timeout. Breaker state is per-pod and in-memory by design.
  `docs/ARCHITECTURE.md` §5.
- **Idempotency for money actions is durable, not Redis-only.** The real
  guarantee is `UNIQUE (record_id, attempt_number)` on
  `INTERVENTION_ATTEMPT`, insert-before-execute. Redis is a fast-path
  optimisation whose TTL cannot be relied on. `docs/ARCHITECTURE.md` §11.
- **Schema migrations**: one root `migrations/` directory, one tool, and a
  migration is always its own PR merged before anything depending on it.
  Additive only during the build. `docs/ARCHITECTURE.md` §12a.
- **Diagnosis**: hybrid, deterministic guardrails (retry/contact caps,
  cooldowns, escalation) plus a bounded LLM call for root-cause + action
  recommendation, with a rules-based fallback on LLM failure. Full model:
  `docs/PRD.md` section 2a, `docs/ARCHITECTURE.md` section 5.
- **LLM provider(s)**: deliberately not decided yet (cost/rate-limits still
  being evaluated). The Classifier calls a priority-ordered provider chain
  behind a swappable interface, ending in the rules fallback, so this can be
  decided later without redesigning anything. Load/throughput testing
  defaults to a synthetic mode with no real API calls, to avoid burning
  budget. Do not hard-wire a single provider when building the Classifier,
  build to the chain/interface described in `docs/ARCHITECTURE.md` section 5.
- **World simulator**: a dedicated service stands in for real bank/customer
  responses, seeded with a hidden ground truth that only it and the
  Reporting Service's accuracy scorer can read. Lives in `demo/`, not
  `services/`, see next bullet. `docs/ARCHITECTURE.md` section 6.
- **Main app vs. demo-only split**: the 7 real product services
  (api-gateway, ingestion, decision-engine, classifier, executor, audit,
  reporting) live in `services/`. World Simulator and Notification
  Simulator are hackathon-only stand-ins for real integrations and live in
  `demo/`, wired in through the same port/interface a real integration
  would use, so this is a config swap later, not a redesign.
  `docs/ARCHITECTURE.md` section 3b.
- **API Gateway is a real Go service** (`services/api-gateway`), not config.
  Its full external contract is `docs/API_GATEWAY.md`, that is the only
  document a UI agent needs. **Dashboard/any external client only ever
  talks to the API Gateway**, never to an internal service directly, this
  is a security boundary, not a suggestion. `docs/ARCHITECTURE.md` §2, §3a.
- **Auth**: a static shared API key at the Gateway (`X-API-Key`), not real
  user/session auth, deliberately, so judges can try the system with zero
  setup. `docs/ARCHITECTURE.md` §17.
- **Live dashboard updates**: WebSocket, terminated at the Gateway only,
  relaying Reporting's server-streaming gRPC method. Not polling, not a
  direct browser connection to any internal service. `docs/ARCHITECTURE.md`
  §6a.
- **Every record belongs to a `batch_id`** (new `BATCH` table), so reports
  scope to one demo run instead of a lifetime cumulative total.
  `docs/ARCHITECTURE.md` §10.
- **Momotaro is a continuous background agent, not a batch tool.** Two
  entry points, both real and both permanent: a webhook (production, one
  event at a time, how records actually arrive) and a batch submit (demo,
  plus backfill/replay in production). Both converge on the same
  `raw.events` path immediately, so nothing downstream knows the
  difference. Do not build the pipeline as if batch is the only mode.
  `docs/ARCHITECTURE.md` §0a.
- **The user is a merchant** (a business using Razorpay to collect
  payments), not Razorpay staff and not the end customer.
  `docs/PRD.md` §1, §5.
- **Intervention economics is a core feature, not a nice-to-have.** Every
  action is expected-value scored before it runs; a record with no
  positive-EV action terminates as `ClosedUneconomic` rather than being
  chased at a loss. Retry timing is cause-aware (salary window for
  insufficient funds, short backoff for bank timeouts, no retry at all for
  hard declines). Headline metric is **net** recovered, after logged
  intervention spend. Order is fixed: classify, then guardrails filter,
  then economics decides. `docs/PRD.md` §2b, `docs/ARCHITECTURE.md` §5a.
- **Nudge messages are LLM-composed in Hinglish**, via a second RPC on the
  Classifier (`ComposeNudge`), so all LLM access stays behind one provider
  chain and one set of circuit breakers. Static Hinglish templates per
  bucket are the fallback. The model writes wording only, never whether or
  whom to send to, and never interpolates amounts or dates itself. Text
  only, no voice. `docs/ARCHITECTURE.md` §5b.
- **Integrity rule**: the Decision Engine must never hold a query path to
  `GROUND_TRUTH`. Recovery probabilities come from a checked-in prior table
  blended with the agent's own observed outcomes, never from the sealed
  answer key. There is a test for this. `docs/ARCHITECTURE.md` §5a, §6.
- **Storage**: Postgres (state, audit log, ground truth), Redis (idempotency
  locks, retry/cooldown counters, dashboard cache, World Simulator's
  delayed-outcome queue).
- **Deployment target**: Kubernetes, demoed via minikube, built and proven
  locally first, containerized/deployed last.
- **Proto contracts are written and merged before the service that
  implements them.** Generated Go code is committed, not generated per
  build. `buf` (pinned) is the toolchain, with `buf lint` + `buf breaking`
  in CI so the rule is mechanically enforced rather than remembered. A
  service PR whose diff touches `proto/gen/` is wrong and should be split.
  See `docs/ARCHITECTURE.md` section 9, this is the rule that makes
  concurrent multi-agent development on different services actually work
  without integration breakage.
- **Repo structure**: single `go.mod` at the root; each service
  self-contained under `services/<name>/` with its own `cmd/`, `internal/`,
  and `Dockerfile`; all shared code in `internal/platform/`. Service
  isolation is enforced by Go's `internal/` rule (a cross-service import is
  a **compile error**), not by module boundaries. Every service still
  builds its own image and deploys independently. Docker build context is
  the **repo root**, not the service directory. Full reasoning:
  `docs/ARCHITECTURE.md` §2a.

## Layout

Single Go module at the repo root. Full reasoning and the container build
rules are in `docs/ARCHITECTURE.md` §2a.

```
go.mod                      one module for the repo
proto/<service>/v1/*.proto  contracts, buf-managed
proto/gen/                  committed generated Go
migrations/                 shared, ordered, one tool
internal/platform/          the ONLY shared code
services/<name>/            cmd/ + internal/ + Dockerfile + AGENTS.md
demo/<name>/                same shape, hackathon-only
web/                        dashboard, not Go
scripts/  docs/
```

- `proto/`: all `.proto` contracts, one file per service at
  `proto/<service>/v1/`, plus `proto/gen/` for committed generated Go code.
  Managed with `buf` (pinned), with `buf lint` + `buf breaking` in CI.
  Written and merged **before** any service that implements them.
- `services/`: **the real product**, one subfolder per Go service:
  `api-gateway`, `ingestion`, `decision-engine`, `classifier`, `executor`,
  `audit`, `reporting`. Each is self-contained: its own `cmd/`, its own
  `internal/` (compiler-enforced private), its own `Dockerfile`, its own
  image, its own k8s Deployment, scaled independently.
- `internal/platform/`: the only shared code (clock, logger, config, gRPC
  interceptors, Kafka and Postgres helpers). Shared logic moves here in its
  own PR; it is never imported across service trees.
- `demo/`: **hackathon-only**, not part of the shipped product:
  `world-simulator`, `notification-simulator`. Implement the same ports a
  real integration would (`docs/ARCHITECTURE.md` §3b), swappable later
  without touching `services/`.
- `web/`: the dashboard, not Go. Only needs `docs/API_GATEWAY.md`, see
  `web/AGENTS.md`.
- `scripts/`: synthetic batch/ground-truth generator, `loadgen` (throughput
  and latency load testing), minikube/k8s deploy helpers.
- `migrations/`: all Postgres migrations, ordered, single tool, shared by
  every service. See `docs/ARCHITECTURE.md` §12a.
- `docs/`: `PRD.md` (product, requirements, NFR targets), `ARCHITECTURE.md`
  (system design, the how), `ENGINEERING.md` (**mandatory** coding
  standards and Definition of Done), `API_GATEWAY.md` (external contract,
  the only doc the `web/` agent needs), `PLAN.md` (living phase-by-phase
  build plan), `DECISIONS.md` (append-only decision log with reasoning),
  `DECISIONS.md` (append-only decision log), `INCIDENTS.md` (append-only
  log of what broke and what we did about it),
  `ORCHESTRATION.md` (for whoever is coordinating agents: sequencing,
  allocation, and prompt/service templates).

## Branching and CI conventions

Trunk-based, chosen for hackathon speed with multiple agents pushing
concurrently:

- `main` is always buildable. No direct pushes, every change lands via PR.
- Branch naming: `svc/<service-name>/<short-task>` for work inside one
  service (e.g. `svc/classifier/add-llm-fallback`), `infra/<short-task>` for
  proto/scripts/deploy changes that aren't scoped to one service.
- Every PR must pass CI before merge: build + unit tests for whichever
  service(s) it touches. Use GitHub Actions path filters keyed on
  `services/<name>/**` and `proto/**` so a PR touching one service doesn't
  wait on or rebuild every other service, this matters once several agents
  are merging small PRs concurrently.
- Keep PRs small and frequent (one service, one concern), this is what
  trunk-based is actually buying you, the alternative is a large conflicting
  merge near the deadline.
- **No AI attribution in commit messages or PR descriptions.** No
  `Co-Authored-By: Claude`, no "Generated with", no equivalent trailer for
  any tool. This overrides whatever your tooling does by default. Full rule:
  `docs/ENGINEERING.md` §10.
- A proto change (section 9 in Architecture) is always its own PR, merged
  before any service PR that depends on the new shape. Same for a
  migration (§12a).
- **`docs/PLAN.md`, `docs/DECISIONS.md` and `docs/INCIDENTS.md` are yours to
  update directly.** Tick your own checkboxes, append your own decisions,
  and log what broke. All three use git's `merge=union` driver (see
  `.gitattributes`), so concurrent edits merge instead of conflicting. Only
  tick a box when it meets the Definition of Done in
  `docs/ENGINEERING.md` §11. Do not restructure or reorder these files,
  union merge handles additions cleanly but not rewrites.
- **Stay inside your service.** Do not modify another service's directory,
  `proto/`, `migrations/`, or `internal/platform/` unless that is
  explicitly your assigned task. If you need a change in any of them, stop
  and propose it rather than making it, another agent is probably working
  there right now.

## Testing conventions

- Unit tests colocated with each service's code, standard Go table-driven
  tests.
- Each service also tests its own gRPC handlers directly against the
  generated stubs, no separate contract-testing framework needed at this
  scale, the committed generated code (section 9 in Architecture) already
  is the contract.
- One end-to-end integration test runs the whole `docker-compose` stack and
  asserts the correctness invariants from `docs/PRD.md` §9/§10 (zero
  stopping-rule violations, 100% audit trail completeness) over a small
  synthetic batch, this is the test that proves the system, not just its
  parts.
- CI runs unit tests on every PR (path-filtered). The end-to-end test is
  heavier (needs the full stack up) and runs on merge to `main`, not on
  every PR.

## Secrets and config

- `.env.example` is checked in with every variable name documented and
  placeholder values. The real `.env` is gitignored, never committed.
- No real API keys, DB passwords, or provider credentials in code, PRs, or
  docs, including this one.
- Local dev: `docker-compose` reads from `.env`. Kubernetes: the same
  variables become k8s Secrets (not ConfigMaps) for anything
  credential-shaped.
- If a secret is ever accidentally committed, treat it as compromised and
  rotate it, removing it in a later commit is not enough, git history keeps
  it.

## Decision log

Moved to `docs/DECISIONS.md`, which uses git's union merge driver so
concurrent agents can append to it without conflicting. Read it to
understand *why* things are the way they are; append to it when you settle
something load-bearing.

## Status

Idea, product description, and system design confirmed. Build plan (service
breakdown, task assignment across concurrent agents, branching/CI
conventions) not yet started, pending the user's plan. Once that starts,
this file's "Layout" and "Locked decisions" sections should be kept current
as the fastest way for a new agent to get oriented.
