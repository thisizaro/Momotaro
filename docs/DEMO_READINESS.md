# Demo readiness: prioritised work (2026-09-02)

Found by running the product end to end and reading the dashboard with a real
browser, not by planning. Everything here is either a confirmed defect or a
capability that already exists in the backend and cannot be seen.

Ordered by what decides whether this wins. **P0 first, top to bottom.**

Detail per unit below the table. Units continue the Phase 5.5 letter sequence
(AC onward) so nothing collides with U to AB.

| # | Unit | What | Est |
|---|---|---|---|
| **P0** | | **Demo-breaking or embarrassing** | **~10h** |
| 1 | AC | Nudge messages are written to a column nothing reads | 2h |
| 2 | AD | Seed the World Simulator | 2h |
| 3 | AE | LLM messages leak internal vocabulary | 2h |
| 4 | AF | Empty states: infinite skeletons when no batch | 3h |
| 5 | AG | "Disconnected" badge on a healthy system | 1h |
| **P1** | | **Built already, invisible** | **~16h** |
| 6 | S | Surface `decision_trace` (the "why not" table) | 4h |
| 7 | AH | Historical timeline + real vs relative time | 6h |
| 8 | AI | Confidence-based LLM routing + quota banner | 4h |
| 9 | AJ | Live production stream (CLI + honest no-baseline) | 2h |
| **P2** | | **Differentiators** | **~11h** |
| 10 | Y | Razorpay payment-downtime webhooks | 5h |
| 11 | Z | Real webhook payload, signature, error taxonomy | 6h |
| **P3** | | **Polish** | **~6h** |
| 12 | AK | `/help` page from the frozen contract | 3h |
| 13 | AL | Misleading labels and the confusion matrix | 2h |
| 14 | AM | Read-only config panel | 1h |
| **Last** | | **Phase 8 rehearsal, non-negotiable** | **~4h** |

**Explicitly skipped**: Phase 6 load testing, Phase 7 Kubernetes, Unit T
modelling re-measure, OpenTelemetry tracing. Reasons in `docs/BACKLOG.md` and
`docs/PHASE5_IMPLEMENTATION.md`.

---

## P0

### Unit AC: nudge messages are written to a column nothing reads

**Confirmed defect, measured.**

```
intervention_attempt.message_text populated: 134
audit_entry.message_text populated:            0
```

The Executor writes the composed Hinglish message to
`intervention_attempt.message_text`. The Audit service's `GetRecordAudit`
selects `audit_entry.message_text`. **Nothing writes the column the drawer
reads**, so 134 real composed messages sit in the database unreachable through
the API. Verified by opening a record drawer in a browser and reading every
entry: no message text anywhere.

This was flagged in the very first codebase audit as one of three
never-written columns and is still true. It kills `PRD.md` section 12 demo
beat 4, which promises "the actual Hinglish message text that was composed and
sent".

**Fix**: either write `message_text` onto the audit entry in the same
transaction that records the outcome, or have Audit join
`intervention_attempt` on `(record_id, attempt_number)`. The join is less
duplication; the write is closer to how every other field on the trail works.
Whoever picks it up should say which and why.

**Where it renders**: the drawer's `Nudged` entry, beside the existing
`Attempt #2 / Cost Rs 0.25` line.

### Unit AD: seed the World Simulator

`scripts/batchgen` is seeded, so records, amounts, failure codes and the sealed
answer key reproduce byte for byte. **The World Simulator's outcome rolls are
not seeded**, so the agent's own results do not.

Measured across runs on identical input: gross recovered swung from Rs 349k to
Rs 594k, and **one run showed the agent losing to the naive baseline**. Amounts
span Rs 50 to Rs 75,000 and the top 5 records hold 29% of batch value, so
whether a few large records land dominates the total while recovery *rate*
barely moves.

That means a reproducible demo is currently impossible and an unlucky draw puts
"blind policy wins" on the headline slide.

**Fix**: accept a seed in the World Simulator, thread it from
`POST /v1/demo/batches` alongside the generator seed, so one seed reproduces
the whole run end to end. Keep unseeded as the default so nothing else changes.

### Unit AE: LLM messages leak internal vocabulary

Both LLM-composed nudges in the measured batch leaked an internal enum name
into customer-facing Hinglish:

```
SOURCE_LLM: "Aapka Rs 75.48 ka payment fail ho gaya kyunki aapka
             bucket overdue hai."
```

"because your **bucket** is overdue". 2 of 2 LLM messages. Template fallbacks
are clean, so this is a prompt problem, not a pipeline one.

**Fix**: strengthen the `ComposeNudge` prompt to forbid internal vocabulary,
and add a validation rule alongside the existing length cap that rejects a
composed message containing any `RootCauseBucket`, `ActionType` or
`RecordState` token, falling back to the template. `validate_nudge.go` is
already the right place. A prompt instruction is not a guarantee, which is the
lesson from `DECISIONS.md` 2026-08-28; the validator is the actual control.

### Unit AF: empty states

With no batch selected, `report` is null and every panel renders
`animate-pulse` skeletons **forever**: seven metric tiles, the donut, the
recovery bar, the timeline. Nothing distinguishes "loading" from "there is
nothing to load", and nothing points at Demo Controls.

**Fix**: a real first-run empty state with a call to action. Also applies to
the Live Event Stream and World Simulator state cards, which read "Waiting for
events" and "Nothing pending" in the same undifferentiated way.

### Unit AG: the "Disconnected" badge

A red failure indicator sits in the header on both tabs of a working system.
Either connect the WebSocket properly or do not render red when the honest
state is "no batch selected yet".

---

## P1

### Unit S: surface `decision_trace`

Carried over from Phase 5, still the highest-value single item.

The EV of every candidate action and the per-action guardrail refusal reasons
are computed, persisted to `audit_entry.decision_trace` (migration 00006), and
**read by nothing**: no proto field, no route, no component. Today the drawer
shows only the winner, as a raw string:

```
best expected value: ACTION_TYPE_NUDGE_REMINDER worth 8169 paise
(p=0.1200, cost=35 paise, at risk=68363 paise)
```

That is one number, not a comparison. The panel it should become:

```
retry            EV +Rs 340   <- chosen
nudge whatsapp   EV +Rs 120
nudge sms        EV  -Rs 15
retry #4         blocked: retry budget 3 of 3 used
```

"Every money action explainable" is the house standard (`PRD.md` section 0,
borrowed from Track 01's wording because Track 03 implies it without saying it
as crisply). This is the artifact that proves it, and the expensive half is
already done.

### Unit AH: historical timeline, and real vs relative time

**Why this is P1 and not P0, since it feels like P0.** The symptom is
P0-grade: the analysis tool goes blank exactly when someone wants to analyse.
It was in an earlier draft of this list as a 2 hour P0 item. It moved on
evidence, not on judgement: there is **no cheap version**, because the data to
draw a history is not in any endpoint the dashboard can call. `RecordSummary`
carries one time field and it is always in the future, and the per-record audit
trail cannot be fetched for a whole batch without N+1 requests. So the honest
fix needs a proto change plus backend plus frontend, which is 6 hours, not 2.

Everything in P0 costs 3 hours or less and fixes something that looks
**broken**. This costs 6 and fixes something that looks **limited**. That is
the only reason it sits here, and it should be the first thing started once P0
lands.

**Two problems, one unit.**

First, the timeline goes blank the moment a run finishes, which is exactly when
someone wants to analyse it. The filter is `records.filter(r => r.due_at !==
'')`, and `due_at` is only set while something is scheduled.

The deeper cause: **the API carries no historical timing at all.**
`RecordSummary` has exactly one time field and it is always in the future:

```
record_id, type, amount_paise, current_state, bucket,
attempt_count, spend_paise, due_at
```

**Fix**: add historical timestamps to `RecordSummary` (`first_action_at`,
`last_action_at`, or a compact attempts array), then give the timeline a
Live / History toggle. Live shows what is scheduled; History shows what
actually happened across the run.

Second, **show real and relative time together**. `audit_entry.ts` is real
wall-clock and `DEMO_TIME_SCALE` is a known constant, so scaled time is just
elapsed x scale. Render `11:50:04` on the axis with `day 12 of the 7-day
recovery window` underneath. That makes the time compression visible rather
than something a judge takes on trust, and it is the honest version of the
"we compress a month into seconds" claim.

**Also make it interactive**: click a dot to open that record's drawer, hover
for bucket, amount, action and due time. Cheap once the data is there.

### Unit AI: confidence-based LLM routing, and a quota banner

**Two knobs that are currently one, and should not be.**

- **Routing**: which records deserve a model call. Today it is a deterministic
  hash of `record_id` against `LLM_SAMPLE_RATE`, i.e. a random 15%. That is a
  cost hack, not a design, and "we sample at random" is a weak answer at a
  panel.
- **Budget**: how many live calls are affordable. Still needed regardless of
  routing, because Groq's free tier is 30 RPM.

`ARCHITECTURE.md` section 17 already names the production design: *"route by
ambiguity, call the model when the deterministic table is not confident."* The
rules engine already returns a confidence, so the plumbing is short.

**Fix**: route by confidence, keep `LLM_SAMPLE_RATE` as a **ceiling** rather
than a selector.

**And surface exhaustion as a first-class state.** When the budget or the rate
limit is hit, say so: *"LLM quota exhausted, 12 records fell back to
deterministic rules."* That converts an apparent weakness into a statement
about resourcing rather than capability, and the data already exists as
`rate_limited` and `circuit_open` hops in the audit trail. It is strictly
better than a silently worse result.

### Unit AJ: live production stream

**Fixes the dead Live Event Stream panel and strengthens the story at once.**

`POST /v1/webhooks/payment-failed` is the production entry point and already
works. Records arriving that way go through the same pipeline but carry **no
ground truth**, so `accuracy` and `baseline_comparison` are correctly absent.

**Fix**: a small CLI that POSTs webhook events at a chosen rate, so the event
stream fills continuously with traffic that arrived the way production traffic
does. It talks only to the public API, so it needs no new backend surface and
no new permissions.

Then make the absence explanatory rather than empty: for a production-sourced
batch the report says plainly that live traffic has no answer key, which is
precisely why the seeded batch exists alongside it. Two modes that explain each
other.

---

## P2

### Unit Y: Razorpay payment-downtime webhooks
Unchanged from `docs/PHASE5_5_IMPLEMENTATION.md`. Still the strongest
differentiator found in any research pass, because it is Razorpay's own
published signal rather than an invented one.

### Unit Z: real webhook payload, signature, error taxonomy
Unchanged from `docs/PHASE5_5_IMPLEMENTATION.md`.

---

## P3

### Unit AK: `/help` page
`docs/API_GATEWAY.md` is already a complete frozen contract with every
endpoint, shape and closed enum vocabulary. This is assembly, not authorship.

### Unit AL: misleading labels
- **"In flight / lost (40.2%)"** on the recovery bar while the In Flight tile
  reads 0. Those records are settled-but-not-recovered, not in flight.
- **"Escalations 0"** sitting directly under a tile reading **"ESCALATED 13"**.
  The first counts executed interventions (escalation is not one), the second
  counts records in that state. Both correct, reads as a contradiction.
- **Confusion matrix shows 3 of 7 buckets** with no indication whether the rest
  were perfect or simply never predicted.

### Unit AM: read-only config panel
Show what the agent is bounded by, on the Demo Controls page, clearly marked
startup-only: time scale, max retries, max contacts, recovery window, contact
cooldown, LLM chain and sample rate. Doubles as a flex and as an honest
statement of the guardrails. A small `GET /v1/demo/config` route backs it.

Context for why this is worth showing: there are **56 environment variables**
and **none of them are adjustable at runtime**. The UI currently exposes four
actions and zero configuration.

---

## Still outstanding, independent of this list

- **`gh auth refresh -h github.com -s workflow`** is needed before the frontend
  CI job can be pushed. `web/` has had **no CI since Phase 0**: no typecheck,
  no build, no tests, ever. That is part of how the pagination bug survived.
  The commit is ready on branch `ci/frontend-checks`.
- **The scheduler flake** (`docs/INCIDENTS.md` 2026-09-01) has now blocked two
  PRs on tests their diffs could not reach. Re-running works, but it trains
  everyone to dismiss a red integration job, which is how a real regression
  eventually ships. Recommended fix is to scope the assertions to each test's
  own seeded `record_id`, the same correction already applied for this class on
  2026-08-23.
