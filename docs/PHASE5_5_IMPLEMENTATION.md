# Phase 5.5 Implementation Plan: demo control surface and Razorpay depth

Working breakdown of `docs/PLAN.md` Phase 5.5. Same contract as the other
`PHASEn_IMPLEMENTATION.md` files: every unit is independently completable and
names its dependencies.

**Why this phase exists.** Phase 5 made the system work end to end and proved
it beats a naive baseline on measured numbers (`docs/PHASE5_IMPLEMENTATION.md`
"Demo-readiness remediation"). What it did not do is make any of that
*visible*, or make the project's Razorpay grounding evident to someone who
works there. Both are presentation problems rather than engineering ones, and
for a competition judged first on a short demo and then at a technical panel,
presentation is not polish.

**Phase goal in one sentence**: make the system's intelligence operable and
legible without a terminal, and ground it in Razorpay's real published
integration surface rather than a plausible imitation of it.

| Unit | What | Status | Depends on |
|---|---|---|---|
| U | Dead-letter unprocessable records instead of crashing | not started | nothing |
| V | Extract batchgen's generation logic into a package | not started | nothing |
| W | `/v1/demo/*` control API, flag-gated | not started | V |
| X | **[FRONTEND]** Demo control panel | not started | W |
| Y | Payment-downtime webhooks and outage-aware retry | not started | Z (for the payload shape) |
| Z | Real Razorpay webhook payload, signature, error taxonomy | not started | nothing |

**Ordering.** U first, it is a correctness defect and the stack does not stay
up without it. Then V, W, X as one track (the control surface) and Z, Y as
another (Razorpay depth); the two tracks share no files. X and Y converge on
the single best demo beat this project can produce, described under Unit Y.

---

## Unit U: dead-letter unprocessable records instead of crashing

**Status**: not started. **Confidence**: confirmed, reproduced twice.
**Size**: small. **Blocks**: keeping a demo stack alive.

**Problem.** One Kafka message referencing a record that no longer exists
kills the entire decision-engine, permanently:

```
fatal: consume raw.events: handle raw.events[8]@6:
       load attempt history for 10187b3b-...: no rows in result set
```

Restarting consumes the next poisoned offset and dies again. That is a crash
loop with no way out but clearing offsets. Full account in
`docs/INCIDENTS.md` 2026-08-31.

**Root cause, precisely.** Not the consumer's contract, which is correct. A
missing record is a **permanent, data-shaped** condition: retrying cannot help,
because the row is never coming back. `loadAttemptHistory` reports it the same
way it would report a dropped connection, and `kafkax.ConsumeKeyed` documents
that a non-nil handler error means infrastructure failure and stops the loop.
The error's *classification* is wrong, not its handling.

This violates `docs/PRD.md` section 10 directly: "No poison record stalls the
pipeline; DLQ, never silently dropped, never counted as a business outcome."

**Proposed solution.** Distinguish the two at the store boundary. `pgx.ErrNoRows`
from a record lookup in the handler path is a poison-message signal and must
route to `raw.events.dlq` with a reason, commit the offset, and continue.
Anything else stays fatal. Audit the whole handler path for other
permanent-but-reported-as-fatal conditions while in there, since nothing
guarantees this is the only one.

**Regression test.** Publish a `raw.events` message for a record id that does
not exist; assert the consumer dead-letters it, commits, and goes on to process
the next message successfully. Prove it goes red by reverting the
classification.

**Also in this unit**, both small and both from the same incident: a `README.md`
note that running `make test-integration` against a live demo stack poisons its
`raw.events` (the tests share the topic and their cleanup deletes the rows), and
a `make demo-reset` target that clears the consumer group so a wedged stack can
be recovered without a full `down-clean`.

## Unit V: extract batchgen's generation logic into a package

**Status**: not started. **Size**: small. **Blocks**: W.

**Problem.** `scripts/batchgen/profile.go` holds the whole synthetic-record
model: the per-bucket ground-truth profiles, the record-type code pools, the
log-uniform amount distribution, the shared instrument-ref pool, and the
deliberate divergence between the visible failure code and the hidden true
bucket. All of it is `package main`, so nothing can import it. Unit O already
flagged this when the `count` form was specced.

**Proposed solution.** Move the pure generation logic to an importable package
under `internal/` (it is shared infrastructure, not one service's business
logic, the same precedent as `kafkax` and `hopcodec`). `scripts/batchgen` keeps
its CLI and its Postgres writes and becomes a thin caller. **No behaviour
change**, and the existing `profile_test.go` assertions move with it unchanged,
which is what proves that.

**Watch out for**: only the World Simulator may write `GROUND_TRUTH`
(`docs/ARCHITECTURE.md` section 6). Extracting the *generator* does not extend
that permission, and the package must not acquire a database handle.

## Unit W: `/v1/demo/*` control API, flag-gated

**Status**: not started. **Size**: medium. **Depends on**: V.

**Problem.** Everything that drives a demo is a terminal command. A judge
cannot seed a batch, choose a scenario, or trigger a failure without being
handed a shell. Worse, the two components that make this project's measurement
claims possible, the World Simulator and batchgen, have no representation
anywhere a person can see.

**Proposed solution.** A `/v1/demo/*` namespace on the API Gateway, **disabled
by default** behind `DEMO_CONTROLS_ENABLED`, proxying to a demo-only backend.

Routes, minimum set:
- `POST /v1/demo/batches` seed a batch with ground truth (count, seed, scenario)
- `GET /v1/demo/scenarios` the available presets
- `POST /v1/demo/downtime` raise or resolve a payment downtime (Unit Y)
- `POST /v1/demo/inject-poison` publish an unprocessable record, to demonstrate
  the DLQ path Unit U fixes
- `GET /v1/demo/world` the World Simulator's live state: pending delayed
  outcomes in the Redis queue, and their due times

**Two architectural rules this must not break**, and the reason the design
looks like this rather than something simpler:

1. **The dashboard talks only to the Gateway** (`web/AGENTS.md`, since Phase
   0). The panel is not an exception; it goes through the Gateway like
   everything else.
2. **Only `demo/` components may write `GROUND_TRUTH`**
   (`docs/ARCHITECTURE.md` section 6). So batch seeding is implemented in the
   World Simulator, which already holds that permission, and the Gateway only
   proxies. The Gateway never gains a database handle.

The flag is what makes this defensible rather than a hole: demo controls are a
surface that does not exist in a production deployment, and saying so is a
better answer than pretending the endpoints are production features.

**Scenario presets** are the interesting part, not the plumbing. Each is a
distribution over the synthetic generator, and each exists to make one of the
agent's behaviours visible in a way a static batch cannot:
- `normal` the current default mix
- `bank-outage` a slice concentrated on one issuer and one failure code inside
  a short window, so the per-bucket reporting shows a systemic spike rather
  than 80 unrelated customer problems
- `salary-day` heavy `insufficient_funds`, so the salary-window retry timing
  becomes the visible story
- `dead-cards` heavy `card_expired` / `debit_instrument_blocked`, so the
  nudge-vs-retry distinction and the uneconomic close are the visible story

## Unit X: the demo control panel

**Status**: not started. **Size**: medium. **Depends on**: W.
**[FRONTEND]**, lives entirely in `web/**`.

**Problem.** The "Generate Sample Data" button is Phase 0 leftover. It builds
80 records in the browser from five hardcoded failure codes, **two of which no
longer exist** in the classifier's vocabulary since Unit I adopted Razorpay's
real codes, so roughly 40% of what it produces escalates as unrecognised. It
uses seven fixed amounts against the generator's log-uniform range, and a
random `instrument_ref` per record, so `instrument_history` is always empty.
And because it goes through the public API it can never carry ground truth,
which means **a batch made with it has no accuracy score and no baseline
comparison**: both headline differentiators, absent, on the most obvious button
on the screen.

**Proposed solution.** Replace it with a link to a control panel view. Do not
patch it; it should not survive.

The panel, driven entirely by Unit W's routes:
- **Seed a batch**: scenario preset, count, optional seed. Seeded batches carry
  ground truth, so accuracy and the baseline comparison are present.
- **World state**: what the World Simulator is holding, i.e. pending delayed
  outcomes and when they come due. This is the first time that component is
  visible at all.
- **Failure injection**: raise a bank downtime (Unit Y), resolve it, inject a
  poison record. Each has an observable consequence elsewhere on the dashboard.
- **A plain-English explanation of what the World Simulator is**, on the page.
  A judge seeing "simulator" needs the distinction between substituting for a
  bank we cannot have (legitimate, permanent, and what makes measurement
  possible) and faking our own backend (which we do not do). `docs/PRD.md`
  section 12a has the wording.

**Deliberately not included**: a "kill a service" button. Killing a process by
hand on stage is more convincing than clicking something that claims to, and
`test/e2e/crash_safety_test.go` already proves the property.

## Unit Y: payment-downtime webhooks and outage-aware retry

**Status**: not started. **Size**: medium. **Depends on**: Z for the payload
shape. **This is the highest-value unit in the phase.**

**What it is.** Razorpay publishes **`payment.downtime.started`**,
**`payment.downtime.updated`** and **`payment.downtime.resolved`** webhooks,
backed by a real Payment Downtime API. The downtime entity carries `severity`
(high when an issuer, bank or network is fully down; medium for elevated
declines or low success rates; low for unknown cause with minimal impact),
`method`, `scheduled`, and `begin`/`end` timestamps.

That is a real, published, first-party signal for exactly the thing this agent
has to decide: **whether now is a sensible moment to retry.**

**Why it matters more than it looks.** Two external reviews independently asked
for a "bank health intelligence layer", and both proposals were declined as
too large and as inventing a data source we do not have. This is the same
capability with neither problem: Razorpay already publishes it, so consuming it
is an integration rather than an invention, and it is the difference between
"we retry on a schedule" and "we know the issuer is down and we do not spend
attempts into an outage."

**Proposed solution.**
- Accept the three downtime events at the webhook route (Unit Z's shape).
- Hold open downtimes in a small store keyed by method and instrument, with
  their severity and window.
- A **guardrail**, not a scorer input: while a high-severity downtime covers a
  record's method, a `RETRY` is deferred rather than blocked, with a `due_at`
  past the downtime's `end` when it is a scheduled one, and a resolve event
  releasing it otherwise. It sits alongside the TRAI and RBI guardrails and
  obeys the same rule that a guardrail may only remove or delay an option,
  never add one, and it produces an audit reason naming the downtime.
- Nudges are unaffected. A customer can update a payment method while their
  bank's authorisation path is degraded.

**The demo beat this unlocks**, and the reason X and Y are the two units worth
building: the control panel raises a bank outage, retries for that method
visibly pause with the downtime named in the audit trail, the outage resolves,
and the parked retries fire. Real API shape, real intelligence, interactive,
and no competitor observed so far has anything comparable.

**Stated limit, so it is not oversold**: we consume the event, we do not have a
live Razorpay account emitting real ones. The panel raises them, exactly as the
World Simulator stands in for a bank. The payload shape is theirs.

## Unit Z: real Razorpay webhook payload, signature, and error taxonomy

**Status**: not started. **Size**: medium. **Depends on**: nothing.

Three related pieces of the same idea: what arrives at our webhook should be
what Razorpay actually sends.

**1. The real payload shape.** `POST /v1/webhooks/payment-failed` currently
takes a flat body we designed. Razorpay's actual `payment.failed` event:

```json
{"entity":"event","account_id":"acc_BFXXX","event":"payment.failed",
 "contains":["payment"],
 "payload":{"payment":{"entity":{
   "id":"pay_DEAU825sJlCbGa","entity":"payment","amount":50000,
   "currency":"INR","status":"failed","order_id":"order_DEATVTRRctwEGb",
   "method":"netbanking","bank":"HDFC","vpa":null,"international":false,
   "error_code":"BAD_REQUEST_ERROR","error_description":"Payment failed",
   "error_source":"bank","error_step":"payment_authorization",
   "error_reason":"payment_failed",
   "acquirer_data":{"bank_transaction_id":null},
   "created_at":1567610214}}},
 "created_at":1567610215}
```

Accept it. Keep the existing simple shape too, since the demo and tests use it,
but the production entry point should take the real thing. Note `amount` is
already integer paise, which matches our own money rule.

**2. Signature verification.** Razorpay signs every webhook with
`X-Razorpay-Signature`, an HMAC-SHA256 hex digest over the **raw request body**,
keyed with the webhook secret. Verify it before parsing, in constant time.
Cheap, genuinely correct, and the single clearest signal that we read the
integration docs rather than guessing. Gate on a configured secret so the demo
path still works without one.

**3. The four-field error taxonomy.** We classify on one field. Razorpay gives
four, and they carry genuinely different information:
- `error_code`: the class (`BAD_REQUEST_ERROR`, `GATEWAY_ERROR`, `SERVER_ERROR`)
- `error_source`: **who** failed (`bank`, `customer`, `business`, `gateway`,
  `razorpay`, `network`)
- `error_step`: **where** in the flow (`payment_initiation`,
  `payment_authentication`, `payment_authorization`, `payment_capture`)
- `error_reason`: the specific reason, which is the code we already use

`source` and `step` are the additions worth having. `source: bank` plus
`step: payment_authorization` says systemic-and-not-the-customer's-fault, which
is a retry; `source: customer` plus `step: payment_authentication` is a failed
OTP, which is not. That distinction currently has to be inferred from the
reason code alone, and for vague codes like `payment_failed` it cannot be.

Extend `ClassifyRequest` with the three new fields (additive, optional), let
the rules table and the LLM prompt use them, and have the generator emit them.
Where they are absent, behaviour is exactly as today.

**Definition of done addition**: a test that a real captured Razorpay
`payment.failed` body, verbatim from their docs, is accepted and classified
correctly, and that a body with a wrong signature is rejected with 401.
