# Phase 4 Implementation Plan: Observability

This is the working breakdown of `docs/PLAN.md` Phase 4. `PLAN.md` stays the
one-line checklist; this file explains what each item actually is and an
order it can be built in.

Same contract as `docs/PHASE2_IMPLEMENTATION.md`/`docs/PHASE3_IMPLEMENTATION.md`.
Every unit below is independently completable and names its dependencies.

**Phase goal in one sentence**: make it possible to see what the running
system is doing, without changing what it does.

**Where this sits**: Phases 0 through 3 are complete and merged. Units below
are ordered easiest/most self-contained first; tracing (Unit F) is
deliberately last because Kafka does not propagate trace context on its own,
so every hop through a topic needs manual header injection/extraction. That
unit is being held pending a decision on whether it is needed for the demo at
all, rather than built speculatively.

| Unit | What | Status | Depends on |
|---|---|---|---|
| A | Prometheus metrics: shared gRPC interceptor + per-service `/metrics` | merged | nothing |
| B | Kafka consumer-lag exporter (decision-engine) | merged | nothing |
| C | docker-compose Prometheus wiring + scrape config | merged | A, B |
| D | Alertmanager rules | not started | C |
| E | Grafana dashboards | not started | C |
| F | OpenTelemetry tracing across gRPC + Kafka hops | on hold | pending a go/no-go decision |

---

## Unit A: Prometheus metrics interceptor

**Status**: merged.
**Depends on**: nothing.

**What it is**: the two metrics `internal/platform/interceptors/doc.go` has
named since Phase 0 (`request_duration_seconds`, `requests_total`), recorded
by one gRPC unary server interceptor so no handler can be missed, plus a
`/metrics` HTTP endpoint on `cfg.MetricsPort` (already a config field on
every service, previously unused) for Prometheus to scrape.

**Files**:
- `internal/platform/metrics/metrics.go` (new): `Metrics` struct wrapping a
  private `prometheus.Registry`, the two vectors, plus Go/process default
  collectors so a service's memory/GC/fd behaviour is visible from the same
  endpoint. `New()` builds one, `Handler()` serves it, `Registry()` is
  exposed so Unit B can register the consumer-lag gauge into the same
  registry without this package needing to know about it.
- `internal/platform/interceptors/metrics.go` (new): `UnaryServerMetrics`,
  added to every gRPC server's existing `ChainUnaryInterceptor` call next to
  `UnaryServerRecovery`/`UnaryServerRequireDeadline`.
- `internal/platform/interceptors/metrics_test.go` (new): unit tests
  (untagged tier, no I/O) proving a success is recorded under its method with
  code `OK`, a `*status.Status` error under its real code, and a plain
  (non-status) error under `Unknown` rather than being dropped.
- Every service's `cmd/main.go` (audit, classifier, executor, ingestion,
  api-gateway, decision-engine): starts a second `http.Server` on
  `cfg.MetricsPort` serving `m.Handler()`, shut down gracefully via the same
  `shutdown.Close(...)` call each service already uses for its primary
  listener. audit/classifier/executor/ingestion also add
  `interceptors.UnaryServerMetrics(m)` to their gRPC interceptor chain.
  api-gateway and decision-engine have no inbound gRPC server (an HTTP edge
  and a Kafka consumer, respectively, both with only outbound gRPC clients),
  so they expose Go/process metrics only for now.

**Deliberately not done here**: `reporting`'s `cmd/main.go` is an
unimplemented stub (`docs/PLAN.md` Phase 5) with nothing to wire yet.
Client-side gRPC call metrics (decision-engine → classifier/executor,
api-gateway → ingestion) are also out of scope: `doc.go`'s framing
("added as an interceptor specifically so no handler can be missed") is
about server-side coverage, and every one of those calls already lands on an
instrumented server on the other end.

**Verification**: `go build ./...`, `go vet ./...`, `gofmt -l .` all clean;
`go test ./...` green. Adversarially broke the interceptor (hardcoded the
label to `codes.OK` regardless of the handler's actual error) and confirmed
both error-path tests failed with the exact expected values, then reverted.
Also ran the built `classifier` binary standalone (`POSTGRES_DSN=...
GRPC_PORT=19090 METRICS_PORT=19091 ./classifier`) and confirmed
`curl localhost:19091/metrics` serves real Prometheus text output and that
`SIGTERM` shuts both listeners down cleanly.

## Unit B: Kafka consumer-lag exporter

**Status**: merged.
**Depends on**: nothing (Unit A's `Metrics.Registry()` gives it somewhere to
register into, but the exporter itself needs no code from A).

**What it is**: `internal/platform/kafkax/lag.go`'s `LagExporter`, next to
the existing `Producer`/`Consumer` types. Uses `franz-go`'s `kadm.Client.Lag`
(already a dependency via `EnsureTopic`'s admin client) to compare
`decision-engine`'s consumer-group committed offsets against each
partition's high-water mark on a timer, publishing a `kafka_consumer_lag`
gauge labelled by topic and partition. Only decision-engine runs it: it is
the only Kafka consumer group in the system worth watching.

**Files**:
- `internal/platform/kafkax/lag.go` (new): `NewLagExporter` dials its own
  admin client (kept separate from the consumer's own client so admin
  metadata calls do not compete with the fetch loop over one connection),
  registers the gauge into the `*prometheus.Registry` passed in. `Run`
  polls on a ticker until its context is done. `poll` (the network call)
  and `record` (the pure mapping from `kadm.DescribedGroupLags` to gauge
  updates) are split apart deliberately, the same way `engine`'s
  `state.go`/`engine.go` split pure decision logic from I/O, so the mapping
  logic is unit-testable without a broker.
- `internal/platform/kafkax/lag_test.go` (new, untagged): exercises `record`
  directly against hand-built `kadm.DescribedGroupLags` values. Runs with no
  I/O: `kgo.NewClient` connects lazily on first request, so building a real
  `LagExporter` in this tier never dials anything.
- `internal/platform/kafkax/lag_integration_test.go` (new, `integration`
  tagged): publishes 3 messages, commits exactly 1 via a real consumer
  group, polls, and asserts the gauge reports lag 2 against the real
  docker-compose Kafka.
- `services/decision-engine/cmd/main.go`: constructs the exporter after
  `metrics.New()`, runs it as a goroutine bound to the same `runCtx` the
  consumer and scheduler goroutines already share, closes it via the
  existing `shutdown.Close(...)` call. New `KAFKA_LAG_POLL_INTERVAL` config
  field (default 30s), deliberately **not** scaled by `DEMO_TIME_SCALE`: it
  is an operator-facing refresh cadence for a real wall clock, like a
  Prometheus scrape interval, not a wait the demo needs compressed.

**Verification**: `go build ./...`, `go vet ./...`, `gofmt -l .` clean;
`go test ./...` and `go test -tags integration ./internal/platform/kafkax/...`
both green. Adversarially broke `record` three ways (disabled the
per-partition error skip, disabled the group-level error check, hardcoded
the recorded value to 0 instead of the real lag) and confirmed the exact
test each was meant to catch failed with the expected wrong value each
time, then reverted and re-confirmed green. Also built and ran the
`decision-engine` binary standalone against the live docker-compose stack
(`KAFKA_LAG_POLL_INTERVAL=1s`, fake unreachable classifier/executor
addresses since gRPC dialing is lazy) and confirmed
`curl localhost:METRICS_PORT/metrics` shows `kafka_consumer_lag` for all 12
`raw.events` partitions, and that `SIGTERM` shuts everything down cleanly.

## Unit C: docker-compose Prometheus wiring

**Status**: merged.
**Depends on**: A and B, so there is something real to scrape.

**What it is**: Prometheus, scraping every service's `/metrics` endpoint.
Alertmanager and Grafana are their own units (D, E): a container with no
rules or dashboards yet is a placeholder, not a deliverable.

Getting Prometheus real targets surfaced a real gap: `.env.example`'s
`GRPC_PORT`/`METRICS_PORT` are documented as "defaults for running one at a
time", so there was no fixed, collision-free port per service to scrape once
more than one runs together. Resolved (docs/DECISIONS.md 2026-08-29) with a
fixed port table via new `make run-<service>` targets, and a **separate**
`docker-compose.observability.yml` rather than adding to the base
`docker-compose.yml`, so `make up`/`make test-integration` (and CI's
`integration` job) stay exactly as fast as before: no test asserts anything
about metrics, so there is no reason for every integration run to also pull
and start Prometheus.

**Files**:
- `deploy/observability/prometheus.yml` (new): scrape config, one job per
  service, targeting `host.docker.internal:<fixed metrics port>`. App
  services are still not containers on the docker-compose network (that
  file's own header comment, unchanged), so this is the only way in.
- `docker-compose.observability.yml` (new): the `prometheus` container,
  `extra_hosts: host-gateway` for portability across Docker Desktop and
  native Linux Engine 20.10+, host port 9900 (not 9090: that is ingestion's
  `GRPC_PORT` on the same host).
- `Makefile`: seven new `run-<service>` targets, each exporting a fixed,
  permanent `GRPC_PORT`/`METRICS_PORT` pair (and the cross-service address
  env vars that need to agree with them) so all seven can run
  simultaneously without colliding; a new `up-observability` target
  layering both compose files; `down`/`down-clean` updated to reference
  both files so either is safe to tear down regardless of which was
  started.
- `.env.example`: a note pointing at `make run-<service>` for anyone still
  reading the single `GRPC_PORT`/`METRICS_PORT` pair as the only option.

**Verification**: `docker compose -f docker-compose.yml -f
docker-compose.observability.yml config -q` accepts both files together.
Ran all six real services (ingestion, classifier, executor, audit,
decision-engine, api-gateway) simultaneously via their new `make run-*`
targets and confirmed zero port collisions and every `/metrics` endpoint
live (`curl` 200 from the host on each fixed port). Ran `make
up-observability` and confirmed Prometheus starts cleanly and its own
target list (`/api/v1/targets`) shows exactly the seven expected jobs at
the right addresses.
**Gap, stated plainly**: the actual `host.docker.internal` scrape hop
itself could not be confirmed inside this session's sandboxed dev
environment — its Bash tool's network namespace has no route back from
Docker's containers to ports those same commands bind (tested
`host-gateway`, plain `host.docker.internal`, and the raw docker0 bridge
address, all connection-refused, despite `ss -ltn` showing every service
listening on `0.0.0.0`). This is the standard Docker-documented mechanism
and should work on a real machine or in CI (native Linux Engine, no extra
sandbox layer); confirm the scrape actually succeeds there before trusting
this note alone. See docs/DECISIONS.md 2026-08-29 for the full account.

## Unit D: Alertmanager rules

**Status**: not started. **Depends on**: C.

Consumer lag, LLM fallback rate, stopping-rule violation, per
`docs/PLAN.md` Phase 4's own bullet list.

## Unit E: Grafana dashboards

**Status**: not started. **Depends on**: C.

Per-service and business-metrics dashboards, provisioned (not clicked
together by hand) so they survive a container restart.

## Unit F: OpenTelemetry tracing

**Status**: on hold pending a go/no-go decision, not built speculatively.

Context propagation on every gRPC call, `record_id` forced as the trace id
rather than a randomly generated one, so one payment's journey across seven
services is a single trace. The hard part: Kafka does not propagate trace
context automatically, so every hop through `raw.events`/`raw.events.dlq`
needs manual header injection on publish and extraction on consume.
