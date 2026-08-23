# Momotaro

**A payment failure and mandate recovery agent.** It watches payments,
mandates, checkouts and invoices as they degrade, diagnoses *why* each one is
failing, and runs the one bounded, auditable intervention that matches that
root cause, instead of a generic retry.

Built for Track 03, AI Revenue Recovery.

The point is not that it retries things. It is that a generic retry is the
wrong answer to most failures: retrying an expired card cannot succeed no
matter how many times you try, and retrying an insufficient-funds failure
tomorrow just burns an attempt and an SMS when the money arrives on payday.
So the agent classifies the failure, picks the intervention that fits, obeys
hard contact and retry limits, and logs every decision it made and what that
decision cost, so a finance team gets a number it can actually defend: how
much revenue came back, out of how much was at risk.

Full product reasoning in [`docs/PRD.md`](docs/PRD.md). System design in
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Running it

You need Go 1.26+ and Docker with Compose.

```bash
cp .env.example .env     # defaults work as-is for local dev
make up                  # postgres, redis, kafka, and a kafka UI on :8080
make migrate-up          # apply the schema
make test-integration    # build every service, run every test tier
```

That last command is the one that proves the thing works. It brings up the
stack if it is not already running, applies migrations, then runs the unit,
integration and end-to-end tiers: six real service binaries started as
subprocesses, a batch posted through the public HTTP API, records driven to
their terminal states, and the audit trail verified.

To watch it happen:

```bash
go test -count=1 -tags='integration e2e' -run TestSmokeBatch -v ./test/e2e/
```

That prints the live logs of every service as seven records flow through:
the classifier deciding root causes, the scheduler claiming records as they
come due, the executor recording what each intervention cost.

Other useful targets (`make help` lists them all):

| Command | What it does |
|---|---|
| `make test` | unit tests only, no infrastructure needed |
| `make check` | what CI checks: fmt, vet, proto lint, build, unit tests |
| `make down` / `make down-clean` | stop the stack, optionally deleting its data |
| `make migrate-status` | which migrations have been applied |

**One gotcha worth knowing**: a bare `go test ./...` runs neither the
integration nor the end-to-end tests, because both sit behind build tags. It
will pass while testing almost nothing about the pipeline. Use
`make test-integration` when you want the real answer. See
[`AGENTS.md`](AGENTS.md) for the three test tiers and what each is for.

## How a record moves

```
POST /v1/batches
  -> api-gateway      auth, rate limit
  -> ingestion        creates the batch, publishes to Kafka (raw.events)
  -> decision-engine  consumes, calls the classifier, owns the state machine
       -> classifier    root cause + one action from a closed menu + rationale
       -> executor      performs that action exactly once, records what it cost
  -> audit            serves the trail, and continuously verifies its own invariants
```

Postgres is the single source of truth for history. Every state change and its
audit entry are written in one transaction, so there is no window where a
record changed state without a record of why. Kafka carries events, never
truth.

## Layout

| Path | What it is |
|---|---|
| `services/` | the seven product services, each with `cmd/`, `internal/`, a Dockerfile and its own `AGENTS.md` |
| `demo/` | hackathon-only stand-ins: the world simulator (how reality responds) and the notification simulator |
| `internal/platform/` | shared plumbing: clock, config, logger, gRPC interceptors, Kafka and Postgres helpers |
| `proto/` | gRPC contracts, and the generated code, which is committed |
| `migrations/` | schema, applied with goose |
| `test/e2e/` | the end-to-end tests, which run the real binaries |
| `web/` | the dashboard (Vite + React + TS) |
| `docs/` | everything about why the system is shaped the way it is |

`demo/` is deliberately a separate top level from `services/`, so it is
obvious from the layout alone which components are the product and which exist
only to make a demo possible without real banks or real customers. Swapping a
simulator for a real provider is a config change, not a redesign.

## Status

Phases are tracked in [`docs/PLAN.md`](docs/PLAN.md), which is the live
checklist rather than a plan of record.

- **Phase 0, foundations**: done. Contracts, schema, shared packages, CI, and
  a walking skeleton proving one record end to end before any depth was built.
- **Phase 1, core pipeline**: done. API Gateway, Ingestion, Decision Engine
  (state machine, scheduler worker, dead-letter path), Classifier (rules
  engine with the LLM provider chain stubbed), Executor (durable idempotency,
  the two ports, scripted outcomes), Audit (trail plus a continuous invariant
  verifier). Two end-to-end tests, one narrow and one covering the branches.
- **Phase 2, durability, safety and economics**: next. Retry budgets, contact
  caps, cooldowns, the checked-in cost model, and the expected-value scorer
  that decides when chasing a record is not worth it.
- **Phases 3 to 8**: the reasoning layer (real LLM providers behind the
  existing chain), observability, demo realism (Reporting, the world
  simulator, the dashboard), load testing, Kubernetes, and rehearsal.

Two things are worth being explicit about, since a half-built system invites
wrong assumptions:

- **No LLM is wired up yet.** The Classifier's provider chain exists and its
  final rung, the deterministic rules engine, is what answers today. Every
  classification is honestly labelled `rules_fallback` in the audit trail.
  Real providers are Phase 3, deliberately, because provider choice depends
  on cost and rate limits still being evaluated.
- **The dashboard runs against its own mock**, not the backend. The endpoints
  it needs beyond batch submission are served by the Reporting service, which
  is Phase 5 and not yet built.

## Working on it

Read [`AGENTS.md`](AGENTS.md) first for ownership boundaries and conventions,
then [`docs/ENGINEERING.md`](docs/ENGINEERING.md) before writing code. The
short version: tests first, inject the clock, deadline every outbound call,
money is always integer paise, and one job per file.

Three documents are append-only logs rather than specifications, and they are
the fastest way to understand why the code looks the way it does:

- [`docs/DECISIONS.md`](docs/DECISIONS.md), what was chosen and why
- [`docs/INCIDENTS.md`](docs/INCIDENTS.md), what broke and what changed as a
  result, which is the honest version of the same story
- [`docs/PLAN.md`](docs/PLAN.md), what is done and what is next
