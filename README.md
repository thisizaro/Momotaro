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
cp .env.example .env     # fine for tests; see "Running the demo" before running the product
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

## Running the demo (the whole product, with the dashboard)

The commands above prove the system works. They do not *show* it. To watch a
batch of failing payments get diagnosed, priced, acted on and recovered, with
the dashboard live, you need the nine services running and a seeded batch.

### Use `PROFILE=demo`, and do not `source` the profile

`.env.example` ships with `DEMO_TIME_SCALE=1`, meaning **every wall-clock wait
is real**. A nudge takes up to 24 real hours to come back. An
insufficient-funds retry waits for the actual next salary window, days away.
So a batch run that way never finishes: records sit in `NUDGED` or
`RETRY_SCHEDULED` indefinitely, recovered totals stay near zero, and the
baseline comparison appears to beat the agent purely because the agent was
never allowed to finish.

`configs/demo.env` fixes that, and it is applied through make's `PROFILE`
variable:

```bash
make demo-up PROFILE=demo
```

**Sourcing the file into your shell does nothing**, even though it looks like
it works. The Makefile's own `include .env` outranks the environment, so a
sourced value is silently discarded on every target. That cost a full
misdiagnosis on 2026-08-31; see `docs/INCIDENTS.md`. `PROFILE` layers the
profile on top of `.env` with a second include, which wins. Verify any time
with:

```bash
make --eval='__show:; @echo $(DEMO_TIME_SCALE)' __show PROFILE=demo
```

The demo profile sets `DEMO_TIME_SCALE=300000` (compressing a 31-day salary
window to under 9 seconds), turns the live `groq,rules` chain on, and samples
15% of records for a real model call.

### Bring it up

One command. It starts infrastructure, applies migrations, and runs all nine
services in dependency order, logging each to `.demo-logs/`:

```bash
make demo-up PROFILE=demo
make demo-down                 # stops the services, leaves infra up
```

The dashboard, in its own shell:

```bash
cd web
npm install
cp .env.example .env.local     # then uncomment VITE_API_BASE_URL
npm run dev                    # http://localhost:5173
```

The individual `make run-<service> PROFILE=demo` targets still exist for
development when you want one service in the foreground with its logs.

**`VITE_API_BASE_URL=http://localhost:8090` must be set**, or the dashboard
runs on `src/lib/mockEngine.ts`, its built-in fake backend, and shows
convincing numbers that never touched any of the services above. The UI shows
a banner when it is in mock mode; if you see it, that is why.

### Seed a batch and watch

```bash
make batchgen                       # 100 records, hidden ground truth
make batchgen COUNT=50 SEED=7       # reproducible
```

This writes records straight into Postgres along with a sealed `ground_truth`
row per record, then publishes each to `raw.events` so the pipeline picks them
up exactly as if real webhooks had arrived. It bypasses the HTTP API on
purpose: only this tool may write the answer key, so it can never be reachable
through a public endpoint.

Then watch the dashboard fill in. Records move through diagnosis, pricing and
execution; nudges resolve after a (compressed) delay; the report shows gross
and net recovered, cost per rupee, what was deliberately not chased, and
classification accuracy scored against the sealed answer key.

**The dashboard's "generate batch" button is not the same thing.** It submits
through the public API, which never writes ground truth, so a batch made that
way has no accuracy score and no baseline comparison. For a demo, always seed
with `make batchgen` and select that batch.

### What a healthy run looks like

`make batchgen COUNT=100 SEED=7` on a fresh stack, measured 2026-08-31. Use it
to tell a working run from a misconfigured one:

```
               gross         spend           net
OURS       Rs 536,449        Rs 44     Rs 536,405
BASELINE   Rs 487,848        Rs 79     Rs 487,769

recovery rate 51.0%     classification accuracy 91.0%
final states: 51 recovered, 32 closed-uneconomic, 17 escalated
```

The agent recovers more than a blind retry-everything policy while spending
roughly half as much, and separately declines to chase 32 records worth
Rs 344,385 that no intervention could economically recover. Both figures are
evaluated against the same sealed ground truth, and both are modelled: the
claim is that this policy beats a blind one *in our simulated world*, not that
it recovers real money.

Two symptoms of a misconfigured run, both seen for real: a recovery rate near
18% with most records `ESCALATED` means the profile did not apply (check
`DEMO_TIME_SCALE`), and an accuracy score that is absent entirely means the
batch has no ground truth, so it came from the dashboard button rather than
`make batchgen`.

### Optional: metrics

```bash
make up-observability          # Prometheus, Alertmanager, Grafana
```

On Docker Desktop with WSL2 in NAT mode, `host.docker.internal` will not reach
your services. Pass your distro's IP instead:
`make up-observability HOST_IP=$(hostname -I | awk '{print $1}')`.

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
  (state machine, scheduler worker, dead-letter path), Classifier, Executor
  (durable idempotency, the two ports), Audit (trail plus a continuous
  invariant verifier).
- **Phase 2, durability, safety and economics**: done. Retry budgets, contact
  caps, cooldowns, the checked-in cost model, and the expected-value scorer
  that closes a record as `ClosedUneconomic` when chasing it is not worth it.
  Proven by crash-safety, re-run-safety and idempotency tests.
- **Phase 3, reasoning layer**: done. Groq wired as the primary rung with
  guaranteed constrained decoding, Gemini built and tested but held out of the
  default chain on measured latency, per-provider circuit breakers, and every
  rung attempted recorded in the audit trail.
- **Phase 4, observability**: mostly done. Prometheus metrics, Alertmanager
  rules and Grafana dashboards. OpenTelemetry tracing is deliberately deferred,
  see [`docs/BACKLOG.md`](docs/BACKLOG.md).
- **Phase 5, demo realism**: nearly done. World Simulator, Reporting, the
  Gateway's read routes and WebSocket relay, Hinglish nudge composition,
  Razorpay's real error codes, TRAI/RBI compliance guardrails, the baseline
  comparison, and the dashboard wired to the real Gateway.
- **Phases 6 to 8**: load testing, Kubernetes, and demo rehearsal. Not started.

Two things worth being explicit about, since they change how you read the
numbers:

- **The LLM is sampled, not universal.** `LLM_SAMPLE_RATE` decides per record
  whether to spend a live model call, because free-tier rate limits do not fit
  a 100-record batch. The sample is a deterministic hash of `record_id`, not
  random, so re-running a batch gives identical results. Records that were not
  sampled are honestly labelled `SOURCE_RULES_FALLBACK` in the audit trail,
  and the provider hop list shows exactly which rungs were tried.
- **Outcomes are simulated, deliberately, and that is the point.** There is no
  real bank and no real customer here. `demo/world-simulator` plays both,
  rolling each outcome against a per-record probability that
  `scripts/batchgen` wrote in advance and sealed. The decision path provably
  cannot read it (`test/integrity/ground_truth_isolation_test.go`). That is
  what makes classification accuracy a *measurement* rather than a claim: we
  can say how often the agent was right, because something wrote down the
  right answer first. A real bank integration would look more impressive and
  tell us nothing about whether the agent was correct.

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
