# API Gateway contract (Momotaro)

**Status: FROZEN as of 2026-08-29 (Phase 5 Unit O).** This is the one document
a UI-focused agent needs to build `web/` against, and it is also the exact
contract `services/api-gateway` must implement, so it is the shared source of
truth for both sides. Everything else (gRPC, Kafka, internal services,
Postgres/Redis) is invisible from outside the cluster, by design, see
`ARCHITECTURE.md` section 2.

**Rule, stated plainly**: the dashboard (and any other external client)
talks to the API Gateway only. It never connects to Reporting, Audit, or any
other internal service directly, no exceptions.

**What "frozen" means here**: every field name, enum spelling and endpoint
shape below is fixed. A frontend agent can build the entire UI against this
document with no further clarification needed, and a backend agent can
implement every route to this exact shape. Where a route is not yet backed
by a real gRPC method (marked **not yet backed**), the JSON shape is still
frozen, only the Gateway-side wiring remains, and adding it must not change
this document. If either side finds this document ambiguous or wrong after
today, that is a bug in this document, fix it here first and tell the other
side, don't silently diverge.

## Wire conventions

Stated once, applies to every endpoint below.

1. **Money is always an `int64` of paise, and the JSON field name always
   ends in `_paise`**. Never a float, never a bare `amount`. This matches
   `docs/ENGINEERING.md` section 8's rule for the internal system and closes
   gap 2 from the pre-freeze audit (the old draft mixed `at_risk_amount` with
   `amount_paise` in different endpoints).
2. **Enums are serialized as their full proto constant name**, e.g.
   `"RECORD_STATE_RETRY_SCHEDULED"`, `"ROOT_CAUSE_BUCKET_HARD_DECLINE"`,
   `"ACTION_TYPE_RETRY"`, `"OUTCOME_SUCCESS"`, `"SOURCE_LLM"`. Never a
   shortened frontend-style spelling (`"RetryScheduled"`). This closes gap 3:
   picking the literal proto name means the wire value can be grepped
   straight back to `proto/common/v1/common.proto`, no translation table to
   keep in sync, no second place to introduce a typo. The full closed
   vocabulary for every enum is listed once, below, rather than scattered
   per-endpoint.
3. **`failure_code` is an open string, not a closed enum.** It is whatever
   the upstream rail returned (today Razorpay's own published error codes,
   `docs/PHASE5_IMPLEMENTATION.md` Unit I), and the set can grow without a
   contract change. A frontend lookup table over it must have a fallback for
   an unrecognised code, never an exhaustive `Record<FailureCode, ...>` type
   that renders blank on a miss.
4. **Timestamps are RFC3339 strings** (`google.protobuf.Timestamp` marshaled
   the standard way), e.g. `"2026-08-29T14:03:11Z"`.
5. IDs are UUID strings throughout.
6. **Responses are hand-written Go structs with explicit `json` tags, not
   `protojson`, and every documented field is always emitted, zero value
   included.** No field in this document ever carries `omitempty`. This is
   the rule an earlier draft of this document left unstated, and one live
   route already violated it (`submitBatchResponse.Rejected` was tagged
   `omitempty`, so an empty map vanished instead of showing `{}` as this
   doc's own example promised); fixed to match. Stated once here because it
   governs the "always present" claims made per-field below (`cost_paise:
   0`, `recovered_delta_paise: 0`, `rationale: ""` must all render, not
   silently disappear), and because `protojson`'s own default behaviour
   (drop every zero value) and its `EmitUnpopulated` option (never drop
   anything, including unset message fields as `null`) would each break a
   different one of those promises if picked instead.

## Auth

Hackathon simplification, not real user auth: every request carries a
static shared API key in an `X-API-Key` header. There is no login, no
sessions, no per-user identity, this is deliberate, see `ARCHITECTURE.md`
section "Real-world vs. hackathon values" for what production would use
instead. The key is documented in `.env.example` and in the demo README so a
judge can copy-paste it and try the API themselves without setup friction.

**The one exception is the WebSocket route**, see gap 5 below.

## Endpoints

### `POST /v1/webhooks/payment-failed`

The production entry point: one failure event, as it happens. This is how
records actually arrive in a real deployment (Razorpay emits a webhook when
a payment or mandate fails, see `ARCHITECTURE.md` section 0a). The dashboard
never calls this, it is listed here because it is part of the Gateway's
contract, not because `web/` needs it. **Already implemented** exactly as
below (`services/api-gateway/internal/httpapi/handler.go`).

Request body:
```json
{
  "type": "PAYMENT",
  "amount_paise": 50000,
  "currency": "INR",
  "failure_code": "bank_not_available",
  "instrument_ref": "card_ref_1",
  "occurred_at": "2026-08-29T14:00:00Z",
  "idempotency_key": "provider-event-id-123"
}
```
`type` is one of `PAYMENT`, `MANDATE`, `CHECKOUT`, `INVOICE` (the Gateway's
own short spelling for this one field, not the proto enum name, since this
route mirrors what a real webhook payload looks like). `currency` defaults
to `INR` if omitted. `occurred_at` defaults to receipt time if omitted.
`idempotency_key` is optional; two submissions with the same key are the
same event, no duplicate record is created.

Response: `202 Accepted`
```json
{ "record_id": "<uuid>", "batch_id": "<uuid>", "deduplicated": false }
```
Accepting fast and processing asynchronously is deliberate, a webhook sender
must never be made to wait on our whole recovery pipeline.

### `POST /v1/batches`

Submit a batch for the agent to process. Used by the demo, and in production
for backfill/replay. Converges onto the same pipeline as the webhook above.
**Partially implemented**: the `records` form below is live today; the
`count` form closes gap 6 and is **not yet backed**, and it is not a small
addition. `scripts/batchgen/profile.go` and `main.go` are both `package
main`, so `generateRecord`, `bucketProfiles` and the amount distribution are
not importable by Ingestion or anything else as they stand. Whoever picks
this up extracts the pure generation logic (`profile.go`, already has no
I/O) into a real internal package first, imported by both `batchgen` and
Ingestion, rather than duplicating it and letting the two drift.

Request body, **either** an explicit record list:
```json
{
  "source": "synthetic-demo",
  "records": [
    {
      "type": "PAYMENT",
      "amount_paise": 50000,
      "currency": "INR",
      "failure_code": "bank_not_available",
      "instrument_ref": "card_ref_1"
    }
  ]
}
```
**or** a count, for the dashboard's "generate a demo batch" button:
```json
{ "source": "dashboard-generated", "count": 80 }
```
`type` is one of `PAYMENT`, `MANDATE`, `CHECKOUT`, `INVOICE`. `currency`
defaults to `INR` if omitted. Exactly one of `records` or `count` must be
present; both or neither is a 400.

**The ground-truth boundary applies here and must be stated on the tile,
not just in this doc.** A batch created with `count` gets records generated
with the same realistic distribution `scripts/batchgen` uses, submitted
through Ingestion's normal path, but it does **not** get a `GROUND_TRUTH`
row. Only `scripts/batchgen`, run directly against Postgres, is permitted to
write `GROUND_TRUTH` (`ARCHITECTURE.md` section 6, `scripts/batchgen/main.go`'s
own header comment). So a `count`-submitted batch reports like real
production traffic: `BatchReport.accuracy` and the baseline-comparison block
are simply absent for it, same condition as a real webhook batch. If the
demo narrative needs the accuracy story (Unit K), seed that specific batch
with `scripts/batchgen` ahead of time and select it via `GET /v1/batches`
rather than pressing generate.

**Warning, not a footnote: the dashboard's main call-to-action is this exact
trap today.** `web/src/lib/api.ts`'s `submitBatch(80)` is the `count` form,
which is what a judge gets from pressing the most obvious button on the
screen, and it produces a batch with neither an accuracy score nor a
baseline comparison, the two headline differentiators this whole phase
exists to show. Whoever builds Unit H/F1 should either relabel that button
to something like "generate sample data" and make the real demo batch a
pre-seeded, selected one, or point it at a pre-seeded batch by default.
`docs/PRD.md` section 12's demo script should say this explicitly rather
than leaving it implied here.

Response:
```json
{ "batch_id": "<uuid>", "accepted_count": 1, "rejected": {} }
```
`rejected` maps the string index of any record that failed validation (e.g.
`amount_paise` not positive) to a human-readable reason; a partial accept is
reported, never silently dropped. Every subsequent call scopes to
`batch_id`.

### `GET /v1/batches`

**Closes gap 1. Backed** (Phase 5 Unit G): `ListBatches` lives on
**Ingestion, not Reporting**, since Ingestion already owns the `batch` table,
already writes every row in it (`services/ingestion/internal/server/store.go`),
and already keeps `total_records` accurate on every write, so no second
aggregate query is needed. The proto landed in its own PR (#68) before this
route's implementation (#69), per this repo's proto-first convention.

This route backs the *other* flow, distinct from the primary demo flow:
`POST /v1/batches` already returns the new batch's `batch_id` directly, so
generating and watching one batch never depended on listing batches at all.
This one is for picking a specific, already-seeded batch (typically one made
with `scripts/batchgen`, so it carries ground truth) out of several.

Lists batches newest first, so a "pick the most recent one" default has
something real to select.

Query params: `limit` (optional, default 20).

Response:
```json
{
  "batches": [
    { "batch_id": "<uuid>", "created_at": "2026-08-29T14:00:00Z", "total_records": 80, "source": "synthetic-demo" }
  ]
}
```

### `GET /v1/batches/{batch_id}/report`

The headline numbers. Mirrors `reporting.v1.BatchReport` field for field
(`proto/reporting/v1/reporting.proto`); every field here already exists on
that message except `baseline_comparison`, which is specced now (Unit K
below) so it is written once rather than appended to this doc twice.

Response:
```json
{
  "batch_id": "<uuid>",
  "total_records": 100,
  "in_flight_count": 12,
  "at_risk_paise": 5000000,
  "recovered_paise": 3410000,
  "intervention_spend_paise": 84500,
  "net_recovered_paise": 3325500,
  "cost_per_rupee_recovered": 0.0248,
  "recovery_rate": 0.68,
  "escalated_count": 9,
  "closed_uneconomic_count": 4,
  "closed_uneconomic_paise": 210000,
  "processing_failure_count": 0,
  "by_root_cause": {
    "ROOT_CAUSE_BUCKET_TRANSIENT_BANK": { "record_count": 40, "at_risk_paise": 2000000, "recovered_paise": 1800000, "recovery_rate": 0.9 }
  },
  "by_intervention": {
    "ACTION_TYPE_RETRY": { "attempt_count": 120, "success_count": 95, "spend_paise": 3000, "recovered_paise": 3000000, "success_rate": 0.79 }
  },
  "accuracy": {
    "scored_records": 100,
    "overall_accuracy": 0.84,
    "by_bucket": { "ROOT_CAUSE_BUCKET_TRANSIENT_BANK": 0.92 },
    "confusion": {
      "ROOT_CAUSE_BUCKET_TRANSIENT_BANK": { "true_bucket_counts": { "ROOT_CAUSE_BUCKET_TRANSIENT_BANK": 37, "ROOT_CAUSE_BUCKET_HARD_DECLINE": 3 } }
    }
  },
  "baseline_comparison": {
    "policy_name": "naive_retry3_nudge1",
    "gross_recovered_paise": 3200000,
    "intervention_spend_paise": 410000,
    "net_recovered_paise": 2790000,
    "note": "Evaluated analytically against the same sealed ground truth using a fixed naive policy (retry every record up to 3x, nudge every record once, no economics). Measures this policy against our modelled world, not real money."
  },
  "generated_at": "2026-08-29T14:05:00Z"
}
```
`by_root_cause` and `by_intervention` are keyed by the full enum string (see
Wire conventions 2). `accuracy` and `baseline_comparison` are **both absent**
when the batch has no `GROUND_TRUTH` (see the ground-truth boundary above),
never present with null or zeroed-out values, a missing key means no answer
key exists, distinct from a real zero.

### `GET /v1/batches/{batch_id}/records`

Mirrors `reporting.v1.ListBatchRecordsResponse`. Powers the dashboard's
record table. Paginated, since a batch can be large.

Query params: `page_size` (optional), `page_token` (optional, opaque),
`state` (optional, one of the `RecordState` values), `bucket` (optional, one
of the `RootCauseBucket` values).

Response:
```json
{
  "records": [
    {
      "record_id": "<uuid>",
      "type": "RECORD_TYPE_PAYMENT",
      "amount_paise": 50000,
      "current_state": "RECORD_STATE_NUDGED",
      "bucket": "ROOT_CAUSE_BUCKET_TRANSIENT_BANK",
      "attempt_count": 2,
      "spend_paise": 50,
      "due_at": ""
    }
  ],
  "next_page_token": "",
  "total_count": 100
}
```
An empty `next_page_token` means this is the last page.

`due_at` mirrors `record_state.due_at` (`migrations/00001_initial_schema.sql`), which
is when the Decision Engine's scheduler worker will next act on this record
(`docs/ARCHITECTURE.md` section 7a). Set only while `current_state` is
`RECORD_STATE_RETRY_SCHEDULED` or `RECORD_STATE_NUDGE_SCHEDULED`.

**Absent `due_at` is an empty string, never omitted, never null.** Wire
convention 6 rules out `omitempty` for a documented field, and convention 4
already reserves timestamp fields for RFC3339 strings, so an empty string is
the representation that stays inside both rules rather than adding a third
(a nullable field, or a boolean sibling flag). This is also the existing
precedent on this same response shape: `rationale` and `message_text` on the
audit trail are empty strings, never omitted, when not applicable. The
example above is a `RECORD_STATE_NUDGED` record on purpose: that state
deliberately has no `due_at`, since it is parked waiting for
`ReportDelayedOutcome` from the customer and nothing polls it, distinct from
a record in a terminal state, which also has no `due_at` because there is
nothing left to schedule. The dashboard tells these apart by
`current_state`, not by `due_at` alone: `RECORD_STATE_NUDGED` with an empty
`due_at` renders "awaiting customer", any other state with an empty
`due_at` renders "not scheduled".

### `GET /v1/records/{record_id}/audit`

Full, ordered audit trail for one record. Mirrors `audit.v1.GetRecordAuditResponse`
and `audit.v1.AuditEntry` field for field; every field below already exists
on those messages and is already implemented and tested. Powers the "drill
into one record" demo beat, and per `docs/PHASE5_IMPLEMENTATION.md` Unit L
this is the cheapest genuinely-live route in the whole phase, since it needs
only the Audit service, nothing else.

Response:
```json
{
  "record": {
    "id": "<uuid>",
    "batch_id": "<uuid>",
    "type": "RECORD_TYPE_PAYMENT",
    "amount_paise": 50000,
    "currency": "INR",
    "failure_code": "bank_not_available",
    "created_at": "2026-08-29T14:00:00Z",
    "instrument_ref": "card_ref_1"
  },
  "current_state": "RECORD_STATE_NUDGED",
  "trail_complete": true,
  "entries": [
    {
      "ts": "2026-08-29T14:00:05Z",
      "from_state": "RECORD_STATE_NEW",
      "to_state": "RECORD_STATE_SCORING",
      "reason": "classified",
      "rationale": "Transient bank-side timeout, no evidence of a hard decline; retry is likely to succeed on resubmission.",
      "source": "SOURCE_LLM",
      "actor": "system",
      "attempt_number": 0,
      "cost_paise": 0,
      "message_text": "",
      "hops": [
        { "provider": "groq", "result": "ok" }
      ]
    }
  ]
}
```
**`from_state` is never absent, including on a record's first entry.**
`audit_entry.from_state` is `NOT NULL` (`migrations/00001_initial_schema.sql`),
the state machine has no code path that writes an unspecified from-state
(`docs/INCIDENTS.md` 2026-08-23 has a fixture bug that once fabricated one
and the note "nothing in the system writes that"), and a record's first
transition is always `RECORD_STATE_NEW -> RECORD_STATE_SCORING`. So a
frontend "first entry has no `from_state`" branch is dead code, don't write
one. `hops` is empty on a transition that followed no classification.
`rationale`, `message_text` are empty strings, never omitted, when not
applicable, so the frontend can render them unconditionally.

**Not yet on this response, needed before Unit L's provenance UI is
complete**: `ev_score_at_decision` and `p_recovery_at_decision` per entry.
These are already persisted on `INTERVENTION_ATTEMPT`
(`ARCHITECTURE.md` section 10) but `AuditEntry` does not yet carry them.
Adding them is a small, additive proto change, do it in the same proto PR
that adds `ListBatches` and `count`, not here.

### `GET /v1/batches/{batch_id}/invariants` and `GET /v1/invariants`

**Closes part of Unit L. Backed** (Phase 5 Unit G): a thin Gateway route
calling `Audit.VerifyInvariants`, no proto change needed since the RPC
already existed. The batch-scoped route passes `batch_id`; the unscoped one
checks the whole system, mirroring the RPC's own "empty `batch_id` means
everything" rule.

Response, mirrors `audit.v1.VerifyInvariantsResponse` exactly:
```json
{
  "stopping_rule_violations": 0,
  "incomplete_audit_trails": 0,
  "impossible_transitions": 0,
  "records_checked": 100,
  "examples": {}
}
```
Every count must be zero. A non-zero count is a bug being surfaced, not a
business outcome; render it as an alarm, never as a neutral metric tile.

### `WS /v1/batches/{batch_id}/live`

Streams incremental updates as records change state, so the dashboard can
fill in live instead of polling. Internally, the Gateway relays this from
Reporting Service's server-streaming gRPC method (`StreamBatchUpdates`, see
`ARCHITECTURE.md` section 6a), the browser never knows gRPC or Kafka exist.
**Backed** (Phase 5 Unit G, built last as originally scheduled, once
Reporting's `StreamBatchUpdates` existed from Unit F). The dashboard's own
2-second report refetch already drives every aggregate on screen, so this
feeds only the scrolling live-event log (`docs/PRD.md` section 12a). Uses
`github.com/coder/websocket`, a minimal RFC-6455 library, chosen since it
was already resolved in the module graph and needs no extra footprint.

**Auth, closes gap 5**: a browser's WebSocket handshake cannot set a custom
header, so `X-API-Key` does not apply here. The key is sent as a
**WebSocket subprotocol**: `new WebSocket(wsUrl, [apiKey])`. Chosen over a
`?api_key=` query parameter because a query parameter lands in server access
logs and browser history by default, a subprotocol does not. The Gateway
must check the negotiated subprotocol (`Sec-WebSocket-Protocol`), not the
`X-API-Key` header, on this one route.

Message shape, mirrors `reporting.v1.BatchUpdate`:
```json
{
  "record_id": "<uuid>",
  "from_state": "RECORD_STATE_NUDGED",
  "to_state": "RECORD_STATE_RECOVERED",
  "ts": "2026-08-29T14:12:00Z",
  "recovered_delta_paise": 50000
}
```
`recovered_delta_paise` is `0` (present, not omitted) on a transition that
recovered nothing, so the dashboard can add it to a running total
unconditionally without a null check.

## Demo controls: `/v1/demo/*`

**Phase 5.5 Unit W.** Everything below exists only when the Gateway is
started with `DEMO_CONTROLS_ENABLED=true` (`.env.example`, default
`false`). When it is not set, none of these routes are registered at all:
a request to any of them returns a plain `404`, the same as any other
unknown path, never a `401`/`403`. That is deliberate: demo controls are a
surface that does not exist in a production deployment, not a feature that
exists but is locked.

Every route here is a thin proxy onto World Simulator's own gRPC surface
(`proto/worldsim/v1/worldsim.proto`), the same way every other route in
this document proxies onto Ingestion, Reporting or Audit. The Gateway
never gains a database handle for these: batch seeding, in particular, is
implemented on World Simulator because only a `demo/` component may ever
write `GROUND_TRUTH` (`ARCHITECTURE.md` section 6).

### `POST /v1/demo/batches`

Seeds a batch of synthetic records with hidden ground truth, exactly like
`scripts/batchgen`, and publishes each one to `raw.events` so the real
pipeline processes it. Distinct from `POST /v1/batches`'s `count` form:
that one deliberately carries no ground truth (the ground-truth boundary
above); this one is the seeded, scored path.

Request body:
```json
{ "scenario": "dead-cards", "count": 80, "seed": 42 }
```
`scenario` is one of `GET /v1/demo/scenarios`' names; empty defaults to
`"normal"`. `count` is required, between 1 and 1000. `seed` is optional;
`0` (or omitted) picks one, which is always echoed back on the response so
the exact batch can be reproduced later.

Response:
```json
{ "batch_id": "<uuid>", "generated_count": 80, "seed": 42 }
```

### `GET /v1/demo/scenarios`

Lists the scenario presets, with a human-readable description of what each
one makes visible.

Response:
```json
{
  "scenarios": [
    { "name": "normal", "description": "The current default mix: a realistic spread across every root-cause bucket, no concentration." },
    { "name": "bank-outage", "description": "Concentrated on one bank being unavailable (BANK_NOT_AVAILABLE), all seeded in the same short window, so per-bucket reporting shows a systemic spike instead of 80 unrelated customer problems." },
    { "name": "salary-day", "description": "Heavy INSUFFICIENT_FUNDS, so the salary-window retry timing (wait for the 1st to 7th, not tomorrow) becomes the visible story." },
    { "name": "dead-cards", "description": "Heavy CARD_EXPIRED and DEBIT_INSTRUMENT_BLOCKED, so the nudge-versus-retry distinction and the uneconomic close are visible: a retry cannot fix a dead instrument, only a method update can." }
  ]
}
```
Every `failure_code` a scenario forces is one of Razorpay's real, published
codes (`services/classifier/internal/rules/buckets.go`), never an invented
one.

### `GET /v1/demo/world`

The World Simulator's live state: every entry still sitting in its Redis
delayed-outcome queue (`ARCHITECTURE.md` section 6) and when it is due.
Read-only: viewing this never drains or delivers anything early. This is
the first route anywhere that makes this component's state visible outside
its own logs.

Response:
```json
{
  "pending": [
    { "record_id": "<uuid>", "attempt_number": 1, "outcome": "OUTCOME_SUCCESS", "due_at": "2026-09-01T14:12:00Z" }
  ]
}
```
`outcome` is the already-rolled answer waiting to be delivered at `due_at`
(Wire conventions 2: full enum constant name). An empty pipeline returns
`"pending": []`, not an absent key.

### `POST /v1/demo/inject-poison`

Publishes one `raw.events` message for a record id that was never inserted
anywhere, to demonstrate the dead-letter path live
(`docs/PHASE5_5_IMPLEMENTATION.md` Unit U) without a shell. Takes no
request body.

Response:
```json
{ "record_id": "<uuid>", "batch_id": "<uuid>" }
```
Both ids are freshly generated and deliberately never written to Postgres;
`record_id` is what the Decision Engine's consumer dead-letters within a
few seconds of the call.

## Errors

Standard shape on any non-2xx response:
```json
{ "error": { "code": "RETRY_BUDGET_EXHAUSTED", "message": "human-readable" } }
```

## Closed vocabularies (closes gap 4)

Stated once here, the single source of truth for every lookup table
`web/src/lib/format.ts` builds. If `common.proto` ever adds a member, add it
here in the same PR, and every `Record<>` type in `web/` must be exhaustive
over the updated list, or fall back gracefully rather than render blank.

**`RecordType`** (4): `RECORD_TYPE_PAYMENT`, `RECORD_TYPE_MANDATE`,
`RECORD_TYPE_CHECKOUT`, `RECORD_TYPE_INVOICE`.

**`RootCauseBucket`** (7): `ROOT_CAUSE_BUCKET_TRANSIENT_BANK`,
`ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS`, `ROOT_CAUSE_BUCKET_HARD_DECLINE`,
`ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED`, `ROOT_CAUSE_BUCKET_RISK_HOLD`,
`ROOT_CAUSE_BUCKET_ABANDONMENT`, `ROOT_CAUSE_BUCKET_OVERDUE`.

**`ActionType`** (5): `ACTION_TYPE_RETRY`, `ACTION_TYPE_NUDGE_METHOD_UPDATE`,
`ACTION_TYPE_NUDGE_REMINDER`, `ACTION_TYPE_ESCALATE`, `ACTION_TYPE_NONE`.

**`RecordState`** (9): `RECORD_STATE_NEW`, `RECORD_STATE_SCORING`,
`RECORD_STATE_RETRY_SCHEDULED`, `RECORD_STATE_RETRYING`,
`RECORD_STATE_NUDGE_SCHEDULED`, `RECORD_STATE_NUDGED`,
`RECORD_STATE_RECOVERED`, `RECORD_STATE_ESCALATED`,
`RECORD_STATE_CLOSED_UNECONOMIC`. The last three are terminal.

**`Outcome`** (3): `OUTCOME_SUCCESS`, `OUTCOME_FAILURE`, `OUTCOME_PENDING`.

**`Source`** (3): `SOURCE_LLM`, `SOURCE_RULES_FALLBACK`,
`SOURCE_TEMPLATE_FALLBACK`.

**`ProviderHop.result`** (7, not an enum, a closed set of strings): `"ok"`,
`"error"`, `"timeout"`, `"rate_limited"`, `"schema_invalid"`,
`"circuit_open"`, `"deadline_exhausted"`.

Every `_UNSPECIFIED = 0` member is never sent on the wire by a correctly
functioning service; if the frontend ever sees one, render it as a visible
error state, not a blank cell, since it means something upstream is broken.

## What this document deliberately does not cover

Internal service boundaries, gRPC method signatures, Kafka topics, database
schema, the LLM provider chain, none of it is reachable or relevant from
outside the Gateway. If building `web/` ever seems to need one of those, that
is a sign this contract is missing something, add it here and to the
Gateway's implementation together, don't reach around it.
