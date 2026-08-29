# Concepts (Momotaro): a study companion, not a spec

This file exists for one reason: so you can explain what this system does
and why, in your own words, without needing to re-read the code. It is
conceptual, not a code walkthrough — every other doc in `docs/` already
covers the how in detail (`ARCHITECTURE.md`, `ENGINEERING.md`, the
`PHASEn_IMPLEMENTATION.md` files). This one answers "what is this pattern,
why does it exist, and why did we use it here."

Grows as the project grows. Whenever a new pattern or technology lands,
add a section here for it, written for the same bar: could you explain
this to someone else without looking anything up.

---

## 1. The shape of the system

Momotaro watches payments fail, figures out *why*, and decides whether to
retry, nudge the customer, or give up — automatically, within hard safety
limits. It's built as seven small services instead of one big program.

**Why split it up at all?** Three real reasons, not "microservices are
trendy":
- **Different failure domains.** The service that calls an LLM
  (Classifier) needs to survive that LLM being slow, rate-limited, or
  down. The service that talks to Postgres (Audit) has a completely
  different failure mode. Keeping them as separate processes means one
  degrading doesn't take the other down with it.
- **Different scaling needs.** Under load, you might need many Classifier
  pods (LLM calls are slow) but far fewer Audit pods (a DB read is fast).
  Splitting services lets you scale each independently (this is also why
  Phase 7's Kubernetes HPA demo exists at all).
- **Ownership boundaries force honesty.** Each service owns specific
  database tables and nothing else (`ARCHITECTURE.md` §10a). No service
  reaches into another's tables directly. This is annoying on day one and
  saves you on day ninety: you can change how a table is structured
  without hunting down every other service that might have been reading
  it directly.

The seven services, one line each: **api-gateway** (the only door in,
HTTP/WebSocket), **ingestion** (accepts failure events, publishes to
Kafka), **decision-engine** (the brain: owns the state machine and the
economics), **classifier** (root-cause diagnosis, rules + LLM), **executor**
(actually performs a retry/nudge, exactly once), **audit** (the trail, plus
a continuous correctness checker), **reporting** (dashboards data, not yet
built).

## 2. How services talk to each other: gRPC and Protobuf

**Protobuf** is a language for describing a data shape and a set of
remote calls (a `.proto` file), independent of any programming language.
You write the contract once (`proto/classifier/v1/classifier.proto`, for
example), and a code generator produces matching Go types and client/
server stubs for every language involved.

**Why not just use JSON over HTTP?** You could. The reasons this project
uses gRPC + Protobuf instead:
- **The contract is enforced, not just documented.** A JSON API's shape
  lives in a doc that can drift from the code. A `.proto` file *is* the
  code the compiler checks against — if Decision Engine and Classifier
  disagree on a field's type, that's a compile error, not a 2am bug.
  This is why `proto/gen/` is checked into git (`ARCHITECTURE.md` §9):
  the contract itself is a build artifact everyone shares.
- **It's fast.** Protobuf is a compact binary format, and gRPC runs over
  HTTP/2, which reuses one connection for many concurrent calls instead
  of opening a new one per request.
- **Streaming is built in.** Reporting's live dashboard updates
  (`StreamBatchUpdates`, Phase 5) use gRPC server-streaming: one call,
  many responses over time, no polling.

The one place this project deliberately does *not* use gRPC: the outside
world talking to api-gateway. External callers get plain HTTP/WebSocket
(`docs/API_GATEWAY.md`), because a hackathon judge shouldn't need a gRPC
client to try the API. Internally, every service-to-service call is gRPC.

### Deadlines, not just timeouts

Every outbound gRPC call in this system carries a deadline
(`ENGINEERING.md` §3) — not "wait up to 5 seconds," but "this call must
finish by this exact wall-clock instant," and that deadline is *passed
along* to whatever that call itself calls. If api-gateway gives ingestion
5 seconds and ingestion is already 3 seconds into its own work when it
calls decision-engine, decision-engine only gets 2 seconds, not a fresh 5.
This is what stops one slow dependency from making an entire call chain
hang far longer than any caller actually intended. `interceptors.
UnaryClientDefaultDeadline` is the one place this gets enforced if a
caller forgets to set a deadline itself.

## 3. Kafka: why it's here, and why only in two places

Kafka is a distributed log: producers append messages to a **topic**,
consumers read them in order, and — unlike a queue — a message isn't
deleted once read, so multiple independent consumer groups can each read
the same stream at their own pace.

**This project uses exactly two Kafka topics** (`raw.events` and
`raw.events.dlq`, plus `audit.events`), not one topic per service hop.
Early design drafts considered Kafka as the transport for *every*
inter-service call (submit → classify → execute, each its own topic).
That was deliberately dropped (`docs/DECISIONS.md`): Kafka is for
**events that happened** (a payment failed) that multiple things might
independently care about, not for **RPCs** (please classify this now, and
tell me the answer). An RPC wants a direct answer with a deadline; forcing
that through a topic-then-topic-back round trip adds latency and
complexity for no benefit. So: Kafka publishes the *fact* that a failure
happened, and everything downstream of that (classify, decide, execute) is
direct gRPC calls, fast and deadline-bound.

### Partitions, keys, and ordering

A topic is split into **partitions** — independent, ordered logs. Kafka
guarantees order *within* a partition, never across partitions. Every
message here is published with `key = record_id`
(`kafkax.Producer.Publish`), and Kafka routes same-key messages to the
same partition deterministically. That's what guarantees "everything that
happens to one payment record is processed in the order it happened,"
without needing a global lock — you get per-record ordering for free from
how partitioning works, as long as you always key by the thing whose order
you care about.

`raw.events` has **12 partitions**, deliberately more than the expected
pod count (`ARCHITECTURE.md` §12): a Kafka consumer group assigns each
partition to at most one consumer at a time, so partition count is a hard
ceiling on how many pods can consume in parallel. Under-provision
partitions and adding more pods stops helping — this is exactly the trap
Phase 6's load testing exists to catch before it becomes a live-demo
surprise.

### At-least-once delivery, and why idempotency is load-bearing

Kafka's default delivery guarantee is **at-least-once**: if a consumer
crashes after processing a message but before committing that it did, the
message gets redelivered on restart. That means **every consumer must be
safe to receive the same message twice.** This project doesn't fight that
guarantee (exactly-once delivery is expensive and still leaky in
practice) — it designs for redelivery to be harmless. See idempotency,
next.

## 4. Idempotency: the two-layer guard

"Idempotent" means doing something twice has the same effect as doing it
once. Executor (the service that actually performs a retry or sends a
nudge) needs this property badly: a redelivered Kafka message, or a
retried gRPC call from a timed-out client, must never cause a customer to
be charged twice or nudged twice.

Two layers, doing different jobs:
- **Redis `SETNX idem:{record_id}:{attempt_number}`** — a fast, in-memory
  "has this exact attempt already started" check, with a TTL. This is the
  fast path: cheap, checked first, catches the common case immediately.
- **A Postgres `UNIQUE (record_id, attempt_number)` constraint, checked
  via insert-before-execute** — the durable guarantee. Redis is a cache;
  its TTL can expire, it can lose data on a restart if not configured
  carefully, and it is explicitly *not* trusted as the source of truth
  here. The database constraint is what actually can't be violated: if
  two requests race past the Redis check simultaneously, the second
  insert into Postgres fails on the constraint, and that failure is what
  Executor treats as "already handled," not the Redis check.

The general shape — a fast probabilistic check backed by a slow but
certain one — shows up constantly in real systems. The lesson worth
keeping: **never trust a cache for a correctness guarantee**, only for
speed.

## 5. Reliability patterns in the Classifier's provider chain

Classifier answers "why did this payment fail" using a **chain** of
providers tried in order: real LLM providers first (Groq, then Gemini as
automatic failover), a deterministic rules engine last. This is a stack of
three separate patterns working together.

### The fallback chain

Try the first thing; if it fails (error, timeout, invalid response), try
the next; keep going until something succeeds. The critical design rule
here: **the last rung must be something that cannot fail.** The rules
engine is a lookup table over failure codes — no network call, no
external dependency, so it always returns *something*. This is what turns
"the LLM is down" from an outage into "slightly less accurate answers for
a while." A chain without a can't-fail terminal rung just moves the outage
one level deeper instead of eliminating it.

### The circuit breaker

If a provider has failed N times recently, stop calling it for a cooldown
period and fail fast to the next rung instead of waiting out a timeout
every single time. This exists for a specific reason: a call that's going
to time out anyway still *burns your deadline budget* while you wait to
find that out. If Groq is down, discovering that instantly and falling
through to Gemini is strictly better than waiting 2 seconds to discover it
on every single record. The breaker is per-provider, per-pod, in memory —
simple on purpose, since the actual safety net is the chain's terminal
rung, not the breaker.

### Deterministic sampling, not `rand`

`LLM_SAMPLE_RATE` decides what fraction of records actually get a live
model call versus going straight to rules (a cost-safety knob — LLM calls
cost money and have rate limits). The sampling decision is computed from
an FNV hash of `record_id`, **not** `math/rand`. This matters for a
concrete reason: if a batch is ever re-run (a real operational need — a
demo re-run, a debugging session), a hash-based decision reproduces the
exact same sampling every time, while `rand` would sample a different
subset each run, making two runs of "the same" batch behave differently
for no visible reason.

## 6. The decision core: rules + economics, not just "an AI agent"

It's tempting to describe this system as "an AI agent that decides what
to do." That undersells what's actually happening, and the real design is
more interesting: classification (what's wrong) and the decision of what
to *do* about it are two separate steps, and only the first one touches
an LLM.

### Guardrails, then economics — in that order, always

Once a record is classified, `scoreAndRoute` (the one function both the
first-attempt path and every retry path go through) does two things, in a
fixed order:
1. **Guardrails**: hard limits — max retries, max contact attempts,
   cooldown windows, the recovery window closing. These are compliance
   rules, not preferences. If a guardrail says no, the answer is no,
   full stop, before economics is even consulted.
2. **Economics**: among whatever guardrails still permit, compute the
   expected value (probability of recovery × amount at risk, minus the
   cost of the action) for each candidate action, and take the best one —
   *if* it's actually positive. A guardrail-permitted action with negative
   expected value still doesn't happen (closed as "uneconomic" instead).

Why this order and not the reverse? Because pricing something the rules
already forbid would imply it's negotiable, and it isn't. A retry-cap
guardrail exists so no customer gets contacted forever regardless of how
good the math looks; letting the math run first would make the guardrail
decorative.

### The state machine

A record moves through a fixed set of states (New → Scoring →
RetryScheduled/NudgeScheduled/Escalated/ClosedUneconomic → ... →
Recovered), and every single transition is written to an append-only
audit log in the same database transaction as the state change itself.
This is what makes "show me exactly what happened to this record and
why" a query, not a reconstruction exercise — and it's also what Phase 4's
`stopping_rule_violation_total`/`incomplete_audit_trail_total` metrics
check continuously: a background watcher re-verifies that every state has
a matching audit entry and that no transition happened that the state
machine doesn't allow.

## 7. Testing in tiers, and adversarial verification

Tests here run in three tiers, controlled by Go build tags:
- **Untagged (default, `go test ./...`)**: no external dependencies, runs
  in milliseconds, runs on every push. Anything with real I/O (a
  database, Kafka) does not belong here.
- **`integration`**: needs the real docker-compose stack (Postgres,
  Kafka, Redis). Tests the real thing, not a mock of it — this project's
  stated principle is "do not mock what you own" (`ENGINEERING.md` §1):
  a mocked database can drift from what the real one actually does, and
  that drift is exactly the kind of bug a test suite exists to catch.
- **`e2e`**: builds and runs every service as a real binary and drives a
  record through the whole pipeline. The most expensive tier, and the one
  that catches integration bugs no single service's tests could ever see
  (a real example from this session: a test asserting "the fallback path
  fired" that never checked whether the primary path had actually been
  reachable at all — see `docs/INCIDENTS.md` 2026-08-28).

**Adversarial verification** is a discipline, not a tool: for every new
test, deliberately break the code it's supposed to protect, confirm the
test actually goes red with the failure you expected, then put the code
back and confirm green again. A test that has never been watched to fail
is a test whose value is unproven — it might be passing for a reason that
has nothing to do with the thing it claims to check.

## 8. Fail fast at startup, not silently at 2am

Every service loads its configuration once, at startup, validates all of
it, and refuses to start if anything is missing or nonsensical
(`ENGINEERING.md` §5). A guardrail config with a zero-value `MaxRetries`
would silently escalate every single record — so it's validated and
rejected at boot instead of discovered in production traffic. The general
principle: a configuration mistake should be a deploy that fails
immediately and loudly, never a service that starts fine and then behaves
wrong under some later condition nobody tested for.

## 9. Observability (Phase 4): metrics, alerts, dashboards

"Observability" here means: can you tell what a running system is doing
without reading its source code or attaching a debugger. Three
conventional pillars — metrics, logs, traces — this project has built two
of the three so far (tracing is deferred, see `docs/BACKLOG.md`).

### Prometheus and metric types

Prometheus is a **pull-based** metrics system: instead of services
pushing data somewhere, Prometheus itself periodically **scrapes** (HTTP
GETs) a `/metrics` endpoint every service exposes, and stores everything
it reads as a time series. Three metric types matter here, and picking
the wrong one is a real, easy-to-make bug:
- **Counter**: only ever goes up (or resets to zero on restart).
  `requests_total`, `llm_fallback_total` — you ask Prometheus for the
  *rate* of increase (`rate(...)`) to get something meaningful, since the
  raw cumulative number by itself isn't interesting.
- **Gauge**: can go up or down, a snapshot of "right now" (or "as of the
  last check"). `kafka_consumer_lag`, `stopping_rule_violation_total`
  (yes, despite the `_total` name — see `docs/DECISIONS.md` 2026-08-29
  for exactly why that naming mismatch was kept anyway: the audit
  verifier's violation count can fall as well as rise between scans,
  which is gauge semantics, but the name was already committed to in
  `ARCHITECTURE.md` before that detail was worked out, and renaming three
  other documents' cross-references wasn't worth the naming purity).
- **Histogram**: buckets observations (like request duration) so you can
  later ask "what's the p95 latency" via `histogram_quantile`, without
  having to have decided in advance exactly which percentile you'd want.

### The mistake worth remembering: a metric can lie about what it measures

Building the `llm_fallback_total` alert in this project, the first
version counted any classification answered by the rules engine as "a
fallback." That's wrong in a way that's easy to miss: the sampling gate
(`LLM_SAMPLE_RATE`) *also* routes most records straight to rules on
purpose, as a cost control, not a failure. A metric meant to alert on
degradation has to distinguish "we asked the LLM and it failed" from "we
never asked" — otherwise the alert fires constantly on completely healthy
days. (Full story: `docs/INCIDENTS.md` 2026-08-29.) The general lesson:
before wiring a metric into an alert, ask what specific event increments
it, and whether every code path that increments it actually represents
the thing you're claiming to alert on.

### Alertmanager: separate from Prometheus on purpose

Prometheus evaluates alerting *rules* (PromQL expressions that become
"firing" when true for a sustained period) but doesn't itself decide who
to notify or how to avoid spamming the same alert repeatedly — that's
Alertmanager's job: grouping related alerts, routing them to the right
receiver, and de-duplicating repeats. This project's Alertmanager has no
real receiver wired up yet (no Slack, no PagerDuty) — alerts are visible
in Alertmanager's own UI, which is enough to prove the rules actually
fire, without inventing a fake destination for a demo.

### Grafana: reads from Prometheus, doesn't store anything itself

Grafana is a dashboard/visualization layer, not a separate data store —
every panel is a PromQL query against the same Prometheus data the alerts
read. The dashboards here are **provisioned**: checked-in YAML/JSON that
Grafana loads automatically on startup, rather than someone clicking
panels together by hand in a UI that would be lost the moment the
container restarts.

## 10. Docker and docker-compose: dev infra, not the app

`docker-compose.yml` in this repo runs only infrastructure — Postgres,
Redis, Kafka — never the application services themselves (a deliberate
choice, stated in that file's own header comment). The actual services run
directly on the host (`go run`, or via `make run-<service>`) during
development, and as separately built container images later
(`services/*/Dockerfile`, one per service). Why keep the app services out
of the same compose file: it keeps the local dev loop fast (rebuild-and-
restart a Go binary is instant; rebuilding a Docker image is not), and it
keeps the "local dev infra" concern cleanly separate from "how this
actually deploys" (Phase 7's Kubernetes manifests).

`docker-compose.observability.yml` (Prometheus/Alertmanager/Grafana) is a
**second**, optional compose file, layered on top of the first only when
you actually want them (`make up-observability`). Kept separate from the
base file so `make up`/`make test-integration` — which CI runs on every
push — doesn't pay the cost of starting three extra containers nothing in
the test suite actually asserts against.

### Container networking, and today's real lesson

A container has its own private network namespace by default — it cannot
see "localhost" the way a process on your actual machine can; `localhost`
*inside* a container means the container itself, not your host. Two
mechanisms bridge that gap:
- **Container-to-container, same compose network**: Docker gives every
  service a DNS name equal to its service name (`prometheus` can reach
  `alertmanager:9093` just by that hostname) — this "just works" and
  needs no special configuration.
- **Container-to-host** (a container reaching a process running directly
  on your machine, outside any container): this needs `host.docker.
  internal`, a special DNS name Docker is supposed to resolve to "the
  host." On Docker Desktop + WSL2, this genuinely does not always mean
  what you'd expect: Docker Desktop runs its own internal VM, and
  depending on the networking mode, `host.docker.internal` can resolve to
  *that* VM rather than to the actual WSL2 distro your shell and your
  processes are running in — two different machines from a container's
  point of view, even though they feel like "the same computer" to you.
  This project hit exactly that (`docs/INCIDENTS.md` 2026-08-29): every
  Prometheus scrape target showed `down`, and the fix was making the
  scrape host an explicit, overridable setting (`HOST_IP`) instead of
  assuming the alias would always be correct.

The general lesson, worth keeping independent of Docker specifically:
**"this is the standard, documented way to do it" is not the same as
"I've verified it works here."** Two systems that are supposed to
transparently see each other across a virtualization boundary are exactly
where documented defaults are most likely to be environment-dependent.

## 11. What's next to add here

As Phase 5 (demo realism) and beyond land, this file should grow to
cover: the probabilistic outcome model (World Simulator), WebSocket
streaming for live dashboard updates, and whatever Phase 7's Kubernetes
work teaches about container orchestration, headless Services, and HPA —
another place a "documented default" is likely to need real verification,
the same way `host.docker.internal` just did.
