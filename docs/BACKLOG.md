# Backlog (Momotaro)

Work that is deliberately **not now**: sized up, reasoned about, and
parked on purpose rather than forgotten. Distinct from `docs/PLAN.md`
(what we're building, phase by phase) and `docs/DECISIONS.md` (what we
chose and why). This file is "we will do this, later, and here is why not
now."

Append-only in spirit, same as `DECISIONS.md`/`INCIDENTS.md`. When an item
here gets picked up, move its status to **in progress**, and once merged,
delete it from this file (its story lives in `PLAN.md`/the relevant
`PHASEn_IMPLEMENTATION.md` from then on) rather than leaving a stale
"done" marker here.

---

## OpenTelemetry tracing (Phase 4 Unit F)

**Status**: deferred, not started.
**Decided**: 2026-08-29.
**Where to pick this up**: `docs/PHASE4_IMPLEMENTATION.md` Unit F already
has the shape of it (gRPC interceptor propagates trace context on every
call, `record_id` forced as the trace id, Kafka producers/consumers
inject/extract trace context from message headers since Kafka does not do
this on its own). `internal/platform/interceptors/doc.go` also already
names it as belonging there.

**Why deferred rather than built now**: it is the single hardest piece of
Phase 4 — every hop through `raw.events`/`raw.events.dlq`/`audit.events`
needs manual header injection/extraction, on top of a new trace backend
(Jaeger or Tempo) added to the observability stack and a new interceptor
wired into every service. That is a bigger lift than any other Phase 4
unit. And its payoff overlaps a lot with something that already exists:
`GetRecordAudit` plus the `ProviderHop` list already gives a complete,
ordered account of everything that happened to one record across every
service (Phase 2/3 work), which covers most of what a demo would use
tracing to show. What tracing adds on top is flame-graph-style
cross-service timing, genuinely nice, not currently blocking anything.

**Decision**: build Phase 5 (demo realism, the actual thing standing
between this project and a working demo) first, then come back.

## Production-grade hardening pass

**Status**: not started, not yet scoped in detail.
**Decided**: 2026-08-29 (the user's own framing: "after \[Phase 5's
important stuff\] are done we will wire things up to make proper
production grade").

This project's `docs/PRD.md`/`docs/ARCHITECTURE.md` are already written
for a real system, not a toy, but a hackathon build still takes shortcuts
a genuine production deployment would not accept. Once Phase 5 (demo
realism) and whatever else turns out to matter for the demo are done,
this is the pass to come back and close that gap deliberately rather than
by accident. Not scoped item-by-item yet; when this gets picked up, the
first step is an audit against `docs/ARCHITECTURE.md`/`docs/ENGINEERING.md`
for exactly this kind of gap: static demo `API_KEY` instead of real auth
(`.env.example`), no TLS between services, no real Alertmanager
notification channel (`docs/PHASE4_IMPLEMENTATION.md` Unit D), Postgres/
Redis/Kafka running as single instances with no HA, secrets in a `.env`
file rather than a secrets manager, and whatever else that audit turns up
that isn't already tracked here.
