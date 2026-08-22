# API Gateway contract (Momotaro)

This is the one document a UI-focused agent needs to build `web/` against.
Everything else (gRPC, Kafka, internal services, Postgres/Redis) is
invisible from outside the cluster, by design, see `ARCHITECTURE.md` section
2. This is also the exact contract `services/api-gateway` must implement, so
it is the shared source of truth for both sides.

**Rule, stated plainly**: the dashboard (and any other external client)
talks to the API Gateway only. It never connects to Reporting, Audit, or any
other internal service directly, no exceptions.

## Auth

Hackathon simplification, not real user auth: every request carries a
static shared API key in an `X-API-Key` header. There is no login, no
sessions, no per-user identity, this is deliberate, see `ARCHITECTURE.md`
section "Real-world vs. hackathon values" for what production would use
instead. The key is documented in `.env.example` and in the demo README so a
judge can copy-paste it and try the API themselves without setup friction.

## Endpoints

### `POST /v1/webhooks/payment-failed`

The production entry point: one failure event, as it happens. This is how
records actually arrive in a real deployment (Razorpay emits a webhook when
a payment or mandate fails, see `ARCHITECTURE.md` section 0a). The dashboard
never calls this, it is listed here because it is part of the Gateway's
contract, not because `web/` needs it.

Response: `202 Accepted` with `{ "record_id": "<uuid>" }`. Accepting fast
and processing asynchronously is deliberate, a webhook sender must never be
made to wait on our whole recovery pipeline.

### `POST /v1/batches`

Submit a batch for the agent to process. Used by the demo, and in production
for backfill/replay. Converges onto the same pipeline as the webhook above.

Request body:
```json
{
  "source": "synthetic-demo",
  "records": [
    {
      "type": "PAYMENT",
      "amount_paise": 50000,
      "currency": "INR",
      "failure_code": "BANK_TIMEOUT",
      "instrument_ref": "card_ref_1"
    }
  ]
}
```
`type` is one of `PAYMENT`, `MANDATE`, `CHECKOUT`, `INVOICE`. `currency`
defaults to `INR` if omitted.

Response:
```json
{ "batch_id": "<uuid>", "accepted_count": 1, "rejected": {} }
```
`rejected` maps the string index of any record that failed validation (e.g.
`amount_paise` not positive) to a human-readable reason; a partial accept is
reported, never silently dropped. Every subsequent call scopes to
`batch_id`.

### `GET /v1/batches/{batch_id}/report`

Response:
```json
{
  "batch_id": "...",
  "total_records": 100,
  "in_flight_count": 12,
  "at_risk_amount": 500000,
  "recovered_amount": 341000,
  "recovery_rate": 0.68,
  "escalated_count": 9,
  "by_root_cause_bucket": { "transient": {...}, "hard_decline": {...} },
  "by_intervention_type": { "retry": {...}, "nudge": {...} },
  "classification_accuracy_vs_ground_truth": 0.84
}
```

### `GET /v1/batches/{batch_id}/records`

List of record summaries: `id`, `type`, `amount`, `current_state`,
`root_cause_bucket`. Powers the dashboard's record table.

### `GET /v1/records/{record_id}/audit`

Full, ordered audit trail for one record: every state transition, the
action taken, the outcome, and (when applicable) the LLM's stored rationale
and which provider/fallback produced it. Powers the "drill into one record"
demo beat.

### `WS /v1/batches/{batch_id}/live`

Streams incremental updates as records change state, so the dashboard can
fill in live instead of polling. Internally, the Gateway relays this from
Reporting Service's server-streaming gRPC method (`StreamBatchUpdates`, see
`ARCHITECTURE.md` "Live updates"), the browser never knows gRPC or Kafka
exist. Message shape:
```json
{ "record_id": "...", "from_state": "...", "to_state": "...", "ts": "..." }
```

## Errors

Standard shape on any non-2xx response:
```json
{ "error": { "code": "RETRY_BUDGET_EXHAUSTED", "message": "human-readable" } }
```

## What this document deliberately does not cover

Internal service boundaries, gRPC method signatures, Kafka topics, database
schema, the LLM provider chain, none of it is reachable or relevant from
outside the Gateway. If building `web/` ever seems to need one of those, that
is a sign the Gateway's contract above is missing something, add it here and
to the Gateway's implementation together, don't reach around it.
