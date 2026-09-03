# Demo readiness: prioritised work (2026-09-02)

Found by running the product end to end and reading the dashboard with a real
browser, not by planning. Everything here is either a confirmed defect or a
capability that already exists in the backend and cannot be seen.

Ordered by what decides whether this wins. **P0 first, top to bottom.**

> **Status, 2026-09-02: all five P0 units are merged** (#98, #99, #100, #101).
> Each was built in its own worktree, reviewed against its central claim
> rather than accepted on report, and merged only on green CI. Per-unit
> resolutions are recorded under each heading below. **P1 is next**, and Unit
> S remains the highest-value single item in the list.
>
> **Unit AN added 2026-09-02, done.** Not in the original list: the drawer
> outgrew its own container carrying Unit S's decision panel plus a long
> audit trail, and the trail had no sense of the 300000x time compression at
> all. Detail under P1 below.
>
> **Unit AO added 2026-09-02, done.** Not in the original list either: a
> review of the working Unit AH timeline found dense buckets overplotting
> into a solid band, two competing colour encodings, a near-invisible legend
> caption, and no click-to-filter. Detail under P1 below, after Unit AN.
>
> **P1 closed 2026-09-03.** Unit AJ was the last item: `scripts/loadgen`
> now posts steady, varied traffic at the public webhook API, and the
> dashboard explains rather than blanks the accuracy/baseline panels for a
> batch with no ground truth. S, AH, AI and AJ are all done. Detail under
> P1 below.
>
> **Unit AP added 2026-09-03, done.** Not in the original list either: the
> user reviewed Unit AO's shipped per-record Gantt directly and rejected it
> as the default ("too much scrolling and so gapped"). The compact,
> one-row-per-bucket view is the default again; Unit AO's per-record layout
> is now an opt-in "Per-record" toggle, and search was added. Detail after
> Unit AO below.
>
> **Unit Y done, scoped down, 2026-09-03.** Payment-downtime webhooks now
> hold a retry back from a known Razorpay-published outage and let it
> through again once resolved. Signature verification (Unit Z) is not part
> of this PR. Detail under P2 below and in `docs/DECISIONS.md`.
>
> **Unit Z done, 2026-09-03. P2 is now closed, the last planned unit.**
> `X-Razorpay-Signature` is verified (HMAC-SHA256 over the raw body,
> constant-time, fail-closed on a missing header, a wrong signature, or an
> unset secret) on both `payment-failed` and `payment-downtime`, one shared
> middleware rather than duplicated. The four-field error taxonomy
> (`error_code`, `error_description`, `error_source`, `error_step`,
> `error_reason`) is accepted alongside `failure_code` and used by the
> rules engine as a fallback signal when the failure code alone is
> unrecognised. Detail under P2 below and in `docs/DECISIONS.md`.
>
> **Unit AM done, 2026-09-03.** The Decision Engine now answers a new
> `GetAgentConfig` RPC with the guardrail and LLM-routing values it loaded
> and validated at startup; the Gateway proxies it as `GET /v1/demo/config`,
> gated on `DEMO_CONTROLS_ENABLED` like every other `/v1/demo/*` route. The
> Demo Controls page shows it grouped into time compression, retry/contact
> limits and LLM routing, clearly marked fixed at startup. `LLM_PROVIDER_CHAIN`
> is left out (Classifier-owned, out of scope for a contained change, see
> `docs/DECISIONS.md`). The **56** figure below was stale: `.env.example`
> was recounted fresh and has **60** entries today.

Detail per unit below the table. Units continue the Phase 5.5 letter sequence
(AC onward) so nothing collides with U to AB.

| # | Unit | What | Est | Status |
|---|---|---|---|---|
| **P0** | | **Demo-breaking or embarrassing** | **~10h** | |
| 1 | AC | Nudge messages are written to a column nothing reads | 2h | **done** #100 |
| 2 | AD | Seed the World Simulator | 2h | **partial** #99, #104 |
| 3 | AE | LLM messages leak internal vocabulary | 2h | **done** #98 |
| 4 | AF | Empty states: infinite skeletons when no batch | 3h | **done** #101 |
| 5 | AG | "Disconnected" badge on a healthy system | 1h | **done** #101, #106 |
| **P1** | | **Built already, invisible** | **~16h** | |
| 6 | S | Surface `decision_trace` (the "why not" table) | 4h | **done** #107 |
| 7 | AH | Historical timeline + real vs relative time | 6h | **done** #109 |
| 8 | AI | Confidence-based LLM routing + quota banner | 4h | **done** #113 |
| 9 | AJ | Live production stream (CLI + honest no-baseline) | 2h | **done** |
| **P2** | | **Differentiators** | **~11h** | |
| 10 | Y | Razorpay payment-downtime webhooks | 5h | **done, scoped down** |
| 11 | Z | Webhook signature verification, four-field error taxonomy | 6h | **done** |
| **P3** | | **Polish** | **~6h** | |
| 12 | AK | `/help` page from the frozen contract | 3h | **done** |
| 13 | AL | Misleading labels and the confusion matrix | 2h | **done** |
| 14 | AM | Read-only config panel | 1h | **done** |
| 15 | AN | Redesign the record drawer, and show real time against simulated time | 3h | **done** #110 |
| 16 | AO | Timeline overplotting, filtering and interactivity | 4h | **done** #112 |
| 17 | AP | Restore the compact timeline as the default, add search | 3h | **done** |
| **Last** | | **Phase 8 rehearsal, non-negotiable** | **~4h** | |

**Explicitly skipped**: Phase 6 load testing, Phase 7 Kubernetes, Unit T
modelling re-measure, OpenTelemetry tracing. Reasons in `docs/BACKLOG.md` and
`docs/PHASE5_IMPLEMENTATION.md`.

---

## P0

### Unit AC: nudge messages are written to a column nothing reads

**Resolved 2026-09-02 (#100).** Fixed by the write, not the join. The join was
rejected on correctness rather than style: a claimed nudge-scheduled record
produces two audit rows sharing one `attempt_number`, so joining on
`(record_id, attempt_number)` would attach the message to both and imply it
was sent twice. `recordOutcome` now writes `message_text` in the same
transaction as the outcome, matching how `attempt_number` and `cost_paise`
already work on that row. The proto, gateway mapping and drawer rendering all
turned out to be wired already, so only the database write was missing.

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

**Partially resolved 2026-09-02 (#99, then #104). Read this before
claiming the demo is reproducible.**

What IS reproducible now, measured on a live stack by seeding the same seed
twice and comparing all 100 records by ordinal position:

| Layer | Diffs across two same-seed runs |
|---|---|
| Amounts, failure codes | 0 |
| Hidden ground truth (recovery probability, delay, true bucket) | 0 |
| EV score and p at decision | 0 |
| Classification (root cause bucket) | 3 |
| Final record state | 9 |

Generation, the sealed answer key and the economics are exactly reproducible.
Two sources of variance remain, and **neither is the seed**:

1. **Wall-clock guardrails under time compression (8 of the 9 state diffs).**
   `schedule.go` enforces TRAI TCCCPR 2018's contact-hour window,
   `[10:00, 21:00)` IST, against the real clock. At `DEMO_TIME_SCALE=300000`
   one real second is about 3.5 simulated days, so a few hundred
   milliseconds of ordinary scheduler jitter moves simulated time across
   the window boundary and flips whether a nudge is a permitted action at
   all. Captured directly in the audit trail: two runs produced a
   byte-identical first decision (`nudge EV 369793 paise, p=0.0900`), both
   nudged, both failed, and then one re-scored to
   `no permitted action has positive expected value` while the other
   re-scored to `nudge EV 143787 paise, p=0.0350` and went on to recover.
   Same maths, different permitted set.
2. **Live LLM sampling (the 3 bucket diffs).** `LLM_SAMPLE_RATE=0.15` sends
   a subset of records to Groq, whose answers and rate-limit behaviour vary
   run to run.

Neither is a defect introduced by #99 or #104; both are consequences of
design choices made earlier and on purpose. What is now false is any claim
that one seed reproduces a run end to end. See `docs/INCIDENTS.md`
2026-09-02 for how this was missed twice.

**What #99 and #104 did fix.** Every roll now derives from
`hash(seed, record_id, attempt_number)` rather than a shared sequential
stream. A mutex-guarded shared generator was rejected because it stays
nondeterministic where it matters: which record consumes which draw depends
on goroutine scheduling, so two runs with one seed would roll the same
outcomes and hand them to different records. Per-record derivation is the
only form of seeding that survives concurrency, and there is a test that
rolls the same records from ten goroutines twice and asserts identical
per-record results. Unseeded remains the default.

#99 alone did not work: it keyed the draw off `record_id`, which is a fresh
`uuid.NewString()` every run, so the deterministic function was never fed the
same inputs twice. #104 replaced that key with a `roll_key` stored on
`GROUND_TRUTH` and derived from `(seed, ordinal index)`, which is the part
that actually repeats. Record ids stay random, because two batches seeded
alike would otherwise collide on that table's primary key.

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

**Resolved 2026-09-02 (#98).** Both layers built, and the validator is the
actual control. Its forbidden-token list is read off the generated proto enum
maps rather than hand-copied, so a new enum value is covered the moment
protoc regenerates. A second, deliberately hand-written tier covers the
taxonomy's own meta-vocabulary (`bucket`, `root cause`, `action type`,
`record state`), which is what actually leaked and cannot be derived
mechanically: splitting enum constants on `_` also yields `retry`, `action`,
`risk` and `new`, all legitimate customer vocabulary. All eight static
templates are covered by a test that walks every `RootCauseBucket` through
the template-only chain, proven non-vacuous by temporarily poisoning a
template and confirming it failed.

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

**Resolved 2026-09-02 (#101).** The seven sites now distinguish three states
rather than two, backed by a shared `EmptyState` component. Verified in a
real browser: zero `animate-pulse` elements with no batch selected, down from
seven, and a designed first-run card with a working call to action.

With no batch selected, `report` is null and every panel renders
`animate-pulse` skeletons **forever**: seven metric tiles, the donut, the
recovery bar, the timeline. Nothing distinguishes "loading" from "there is
nothing to load", and nothing points at Demo Controls.

**Fix**: a real first-run empty state with a call to action. Also applies to
the Live Event Stream and World Simulator state cards, which read "Waiting for
events" and "Nothing pending" in the same undifferentiated way.

### Unit AG: the "Disconnected" badge

**Resolved 2026-09-02 (#101 and #106), and it was FOUR defects, not a label
bug.** The fourth is the one that was actually causing the red badge, and it
was found only by driving a real browser at the real Gateway after #101 had
already been reviewed and merged: every handshake was rejected with HTTP 403,
because `websocket.Accept` was called without `OriginPatterns` and
`coder/websocket` refuses cross-origin by default. The dashboard on :5173 and
the Gateway on :8090 are different origins, so **the live stream had never
worked in any dev or demo run**. #106 made the allowed origins config-driven
(`WS_ALLOWED_ORIGINS`, defaulting to same-origin so production is not
loosened) and set it in `configs/demo.env`. See `docs/INCIDENTS.md`
2026-09-02 for why three rounds of green tests did not catch it.

The first three, all real and all worth fixing on their own:
See `docs/INCIDENTS.md` 2026-09-02. The socket had no reconnect logic at all,
a clean close was reported as failure, and teardown raced. The fix reads the
Gateway's actual close code: `StatusNormalClosure` (1000) means the batch
finished, so it reports `complete` in green and correctly stops; anything
else reconnects with backoff from 1s to a 30s cap, amber while retrying and
red only after three consecutive failures, still retrying underneath.

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

**Done, 2026-09-02.** Turned out to be five pieces, in dependency order: the
frozen `docs/API_GATEWAY.md` contract documented first (its own rule requires
fixing the document before implementing against it), a typed
`audit.v1.DecisionTrace` proto message (a `Candidate` repeated field plus a
`blocked` map, not a passthrough of the raw JSONB string), the Audit store
decoding the nullable column, the Gateway mapping it field for field, and an
always-visible panel under each decision entry: candidates ranked by EV with
the winner marked (derived from the value, the same rule
`economics.BestOf` uses server side, not by parsing the free-text `reason`),
guardrail-blocked actions kept in their own separate section rather than
mixed into the ranking. Full reasoning in `docs/DECISIONS.md` 2026-09-02.

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

**Resolved 2026-09-02.** Built as scoped, with the field shape decided rather
than guessed: `RecordSummary` gained `first_action_at`
(`MIN(audit_entry.ts)`, one correlated subquery, not N+1) and
`last_action_at` (mirrors `record_state.last_action_at`, already written by
the Decision Engine on every transition, so this half was free), not a
per-record attempts array, since a first/last pair is enough for the chart
actually built and Audit's own per-record trail route already exists for
anyone who needs the full sequence. `docs/API_GATEWAY.md` documents both
before the implementation that reads them, following Unit S's precedent.

The timeline is now a Live/History toggle
(`web/src/components/TimelineView.tsx`, delegating to `LiveTimeline` and
`HistoryTimeline`). Live is unchanged in substance (what's scheduled, via
`due_at`). History plots every record the agent has acted on from
`first_action_at` to `last_action_at`, bucket-row layout matching Live so
the two read as one visual language, marker size encoding amount and color
encoding current state/outcome (the same `STATE_FILL` palette
`StateDistribution` already uses), with a legend for whichever states are
actually present. The toggle's default is chosen once from the records it
first mounts with: if nothing is pending but history exists, it opens on
History rather than reproducing the exact "nothing pending right now" bug
this unit exists to fix; `App.tsx` keys the component on the active batch
id so switching batches re-evaluates that default.

The real/simulated dual-time axis landed on History only, formatted as
"HH:MM:SS" real time above "day N of the 7-day recovery window" simulated
time beneath, computed in `web/src/lib/demoTime.ts` and unit-tested against
hand-checkable values. The multiplication direction was verified against
`docs/ARCHITECTURE.md` section 17 and `configs/demo.env` before writing any
code (a real elapsed duration times `DEMO_TIME_SCALE` recovers what it
represents; `RecoveryWindow` is excluded, per `docs/INCIDENTS.md`
2026-08-31, since it is compared against real elapsed age, never scaled).
Both charts are click-to-drawer and hover-for-detail, wired straight to
`App.tsx`'s existing `handleSelectRecord` rather than a second drawer-opening
path. `docs/DECISIONS.md` has the full reasoning for each choice.

### Unit AI: confidence-based LLM routing, and a quota banner

**Done, 2026-09-03.** Routing now asks the deterministic rules engine first
(free, cannot fail) and only spends a live model call when its `Confidence`
is below the new `LLM_ROUTE_CONFIDENCE_THRESHOLD` (`configs/demo.env`:
0.80, chosen from `rules/actions.go`'s actual confidence table, not a round
number: `USER_ACTION_NEEDED` 0.70, `OVERDUE` 0.75 and the unknown-code path
0.00 are the genuinely ambiguous cases). `LLM_SAMPLE_RATE` is reinterpreted
as a running-ratio ceiling (`services/decision-engine/internal/engine/llm_budget.go`)
rather than the old hash-of-`record_id` selector, because the Decision
Engine consumes `raw.events` as a stream and never sees a whole batch to
rank before deciding; a confidence threshold plus a budget counter turned
out simpler and more honest than trying to rank a batch not yet fully seen.
An ambiguous record the ceiling denies still gets the rules answer, tagged
with a `sample_budget:exhausted` hop so it is indistinguishable from a
`rate_limited`/`circuit_open` fallback only in cause, not in effect: both
count toward `BatchReport.llm_quota_exhausted_count`
(`services/reporting/internal/server/exhaustion.go`), documented in
`docs/API_GATEWAY.md` first per its own frozen-doc rule, and shown as a
quiet slate `LlmQuotaBanner` that renders nothing at all when the count is
zero. Full reasoning in `docs/DECISIONS.md` 2026-09-03.

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

**Resolved 2026-09-03.** The CLI landed as `scripts/loadgen`: that name had
been reserved since Phase 6 (`docs/PLAN.md`, `docs/ARCHITECTURE.md` section
14) for exactly this job, throughput traffic through the public HTTP API,
but no such tool existed yet, so this closes that gap rather than adding a
third generator alongside `scripts/batchgen`. It POSTs to
`/v1/webhooks/payment-failed` at a steady, `-rate`-controlled pace (every
event's send time pinned to a fixed offset from the run's start, so a slow
request falls behind rather than compounding into a burst), drawing type,
amount and failure code from `internal/platform/syntheticgen`, the same
generator `scripts/batchgen` and the World Simulator already use, so live
traffic looks like the same vocabulary rather than an invented one. It
never carries the hidden ground-truth fields `syntheticgen.GeneratedRecord`
also has: the webhook route's wire shape has no field for them, by design.
SIGINT stops it cleanly and it always prints one summary line
(`sent=N accepted=N failed=N`); a dead gateway is reported per failure and,
after a few consecutive failures, with an explicit "gateway appears
unreachable" line, never a silent hang. Rate limiting, event variety, and
the summary counters are all unit-tested with no live server
(`scripts/loadgen/*_test.go`), per this unit's own TDD instruction.

For the dashboard half, `batch.source` turned out to already be on the wire
(`BatchSummary.source`, `GET /v1/batches`, since Unit G), so no
`docs/API_GATEWAY.md` change was needed: `App.tsx` already holds the batch
list that response came from, so the active batch's source is looked up
from state already on hand. `web/src/lib/groundTruth.ts`'s
`noGroundTruthReason` is one function shared by the Classification Accuracy
panel and `BaselineComparisonCard`, so the two can never drift into saying
slightly different things about the same fact; it names the `"webhook"`
source specifically (the always-on rolling batch every webhook event
attaches to, `services/ingestion/internal/server/store.go`, what
`scripts/loadgen` posts to) and falls back to an honest generic reason for
any other ground-truth-less source. Quiet in tone by design, tested for it:
no exclamation marks, no em dash, a statement about method rather than a
warning. Full reasoning in `docs/DECISIONS.md` 2026-09-03.

### Unit AN: redesign the record drawer, and show real time against simulated time

**Done, 2026-09-02.** Added after the rest of this list was written: a judge
opens one record and reads it, and the drawer carrying that read had grown
past the container it was given before it carried anything.

**Congestion had a concrete cause, not a vague one.** The drawer was
`max-w-lg` (512px), sized before it carried Unit S's three-column decision
panel, composed Hinglish message quotes, and an audit trail that can run to
a dozen entries. Widened to `max-w-3xl` (768px), which is roomy enough that
the decision panel's blocked-action labels (`Nudge (Update Method)`) no
longer need to truncate; the panel's own label column moved from
`minmax(0,7rem)` to `minmax(0,11rem)` to use the room, the only change made
inside `DecisionTracePanel`, which otherwise keeps its approved internals:
ranked candidates, winner marked by value, blocked actions in their own
section. The header (record id, amount, current state) is now sticky
(`flex flex-col` outer panel, only the trail body scrolls) so scrolling a
long trail never loses what record is on screen. The trail itself gained a
real spine: one continuous line down the left edge with a node per entry
coloured by `to_state` via the same `STATE_FILL` palette `DonutChart` and
the historical timeline already use, and each entry is its own bounded
`.card`, so a dozen entries read as one journey through states rather than
a stack of look-alike blocks. Repetitive metadata (`actor`, almost always
"system"; `source`, `SOURCE_UNSPECIFIED` on most entries since most are
plain state transitions with no composed message) is now quiet, small,
muted text, while the two sources that actually vary, `SOURCE_LLM` and
`SOURCE_RULES_FALLBACK`, get a small coloured badge, so which rung answered
is visible rather than buried in a dozen identical lines. Along the way,
`web/src/types.ts`'s `Source` type turned out to be missing
`SOURCE_UNSPECIFIED`, the enum's real zero value and the one most entries
actually carry on the wire (`docs/INCIDENTS.md` 2026-09-02); fixed as a
frontend-only type correction, no backend or wire change.

**Time compression is now shown per entry, not left implicit.** Each audit
entry already carried a real timestamp; it now also shows what that instant
represents in the 7-day recovery window (`formatSimulatedElapsed`, the same
function and phrasing Unit AH's timeline axis already uses, so the two
surfaces agree) and, for every entry after the first, the elapsed gap since
the previous one in both real and simulated terms
(`+2.3s real, +8 days simulated`), which is the genuinely useful number when
reading a trail compressed 300000x. The gap math is new
(`formatSimulatedGap` in `web/src/lib/demoTime.ts`), test-driven with
hand-checkable values against `DEMO_TIME_SCALE=300000` before it was
implemented, and reuses `simulatedElapsedMs`/`DEMO_TIME_SCALE` rather than
a second hardcoded constant. Full reasoning in `docs/DECISIONS.md`
2026-09-02.

No backend change was needed: `audit_entry.ts` already carried real
timestamps and the drawer already received them.

### Unit AO: refine and make the timeline interactive

**Added 2026-09-02, after Unit AH shipped and was verified working.** Unit
AH's Live/History timeline was called "a genuinely great job" in review, and
this unit is refinement, not a redesign: the bucket rows, the shared colour
palette with `DonutChart` and `StateDistribution`, the Live/History toggle,
and the dual-axis tick labels all stay exactly as built.

**Two problems with the visual encoding, one with contrast, one missing
capability.**

First, overplotting: `HistoryTimeline` put every record in a bucket on the
same row, so a dense bucket's connector lines drew directly on top of each
other. With up to 28 Abandonment records and 16 Hard Decline records in one
row, the connectors merged into a solid pastel band and no individual
record's journey could be followed, the entire point of a timeline.

Second, two colour encodings competed for the same space: the connector was
bucket-coloured, the end marker was state-coloured, so the eye had no single
thread to follow.

Third, the `circle size = amount at risk` caption rendered `text-slate-300`
on white, explaining the chart's most important encoding at unreadable
contrast.

Fourth, the chart had no interactivity beyond click-to-drawer: no way to
isolate a bucket or an outcome, which is what "I can choose to see a
specific group of records or a specific record" actually asks for.

**Resolved 2026-09-02.** `HistoryTimeline` now gives every record its own
thin sub-row within its bucket band, a small-multiples/Gantt layout instead
of one shared row per bucket, so a dense bucket's connectors read as
individual lines rather than a band. The connector is now a fixed neutral
slate (`#cbd5e1`) rather than `BUCKET_COLORS[bucket]`, so the state colour
on the marker is the only meaningful hue in the plot; bucket identity is
still carried by the row grouping and its label, which was always the
primary channel for it. The caption's contrast was fixed
(`text-slate-300` to `text-slate-500`).

**Row layout and height bounding.** A record's sub-row height is 13px
(`TIMELINE_SUB_ROW_HEIGHT`, `web/src/lib/timelineGeometry.ts`), tuned
against `amountRadius`'s bounds (also retuned, 2.5-5.5px instead of the
single-row layout's 3.5-9px, so the largest marker in a batch still clears
its row's edges by a pixel). Records within a bucket sort by
`first_action_at` then `record_id` for a stable chronological read. A
bucket band's height is `max(recordCount, 1) * 13px` when that bucket is
"expanded" (every bucket, when no bucket filter is active; only the
isolated bucket, when one is), and a single collapsed row otherwise, so an
isolated bucket's siblings stay visible and one click away rather than
disappearing. The whole record area sits in a container capped at 480px
(`TIMELINE_MAX_BODY_HEIGHT`) with internal `overflow-y: auto` rather than
letting the card grow unboundedly; the axis is pinned below that container,
outside the scroll, so the time reference never scrolls out of view.
Judged against the real shape of a run (up to ~80 acted-on records across 7
buckets, some buckets much denser than others): the unfiltered view scrolls
past two or three buckets before the cap kicks in, and isolating a single
bucket at typical demo density (up to ~28 records) usually fits without
scrolling at all, which makes isolate a practical way to both focus and
shrink the card at once.

**Interactivity, layered on top of the unchanged default view.** Clicking a
bucket row (which already doubled as that bucket's legend entry, per Unit
AH) isolates it; clicking again restores every bucket. Clicking a state in
the outcome legend filters to it; clicking again clears it. Both filters
compose: an isolated bucket and an active outcome filter apply together, and
the legend always lists every state present in the whole run regardless of
the current bucket filter, so a state absent from the isolated bucket stays
one click away, which is also what makes a filtered-empty result reachable
through normal use rather than only by construction. Hovering a record
highlights its connector and marker (full opacity, a darker slate stroke on
the line, a dark outline on the marker) and dims every other visible record
to a low opacity, without touching the existing `<title>` tooltip. Clicking
a marker still opens the drawer via the same `onSelect` path Unit AH wired,
unchanged. Active filters render as dismissible chips above the chart
(`Clear filters, show everything` always present alongside them) so a
filtered view is never silently different from the default one, and a
combination that matches nothing renders the shared `EmptyState` instead of
a blank panel, with the chips and clear control still visible above it so
there is always an obvious way back. Filter state
(`bucketFilter`/`stateFilter`/`hoveredId`) is local `useState` inside
`HistoryTimeline` and needs no explicit reset wiring: `TimelineView` already
remounts on a batch switch (keyed on the batch id, Unit AH) and remounts
`HistoryTimeline` for free every time the Live/History toggle swaps which
component renders, since they are different component types.

**Two small fixes from the same review, done in this unit.**
`DecisionTracePanel`'s EV value (`w-16` column) could wrap after the `+`
sign in the winning row, breaking the column's alignment; fixed with
`whitespace-nowrap` (plus `flex-shrink-0`, since the column sits inside a
flex row that could otherwise shrink it below its content width) rather
than only widening the column, which would not have guaranteed no future
wrap. `RecordDrawer` rendered the amber rationale box twice in a row when
the Scoring entry and the entry right after it (often Nudge Scheduled)
carried the same `rationale` verbatim, since the Decision Engine writes it
once at scoring rather than re-deriving it per hop; suppressed by comparing
each entry's rationale only to the immediately previous entry's, so an
exact consecutive repeat is hidden but a rationale that differs, or repeats
a non-adjacent earlier entry, still renders.

No backend change was needed: `RecordSummary` already carried everything
this unit reads (`first_action_at`, `last_action_at`, `current_state`,
`bucket`, `amount_paise`).

---

### Unit AP: restore information density to the timeline

**Added 2026-09-03, after the user reviewed Unit AO's shipped per-record
Gantt directly and rejected it as the default.** Their words, close to
verbatim: "about the current timeline you did a genuinely great job!! but
now what we need is a bit of refinement and interactiveness... i dont like
the current one... it is too much scrolling and so gapped... and I think
even if congested the initial view of the last one was better it gave a
better idea in one view... we could have added selection options or
switches to select a specific category or search a specific entry... or
clicking on a specific entry will open the record drawer and there will be
an option to see the gantt chart something like that could have been
better... but not a big portion with all those gapped lines doesnt look
good and dont give enough data to the user at one glance".

The tradeoff Unit AO made was the wrong one: per-record legibility nobody
asked for, purchased with the whole-batch read at a glance that
`HistoryTimeline` exists to give. A dense bucket's connectors merging into a
band (the problem Unit AO fixed) is a real cost, but it is a smaller cost
than needing to scroll past most of a run before seeing all of it.

**Resolved 2026-09-03, not by reverting Unit AO but by changing what sits on
top of its fixes.** The compact, one-row-per-bucket layout is `HistoryTimeline`'s
default again, recovered from Unit AH's original implementation in git
history (`git show <AH commit>:web/src/components/HistoryTimeline.tsx`)
rather than reinvented from the current file: one fixed-height
(`TIMELINE_ROW_HEIGHT`, 34px) row per bucket, every acted-on record drawn in
it and jittered vertically (`jitter()`, `web/src/lib/timelineGeometry.ts`,
shared with `LiveTimeline`) so a dense bucket reads as a cloud of dots
rather than one dot hiding the rest. Because a bucket's row height no longer
depends on how many records are in it, the chart's total height is fixed
(`TIMELINE_BUCKETS.length * TIMELINE_ROW_HEIGHT`, about 240px, plus the
axis) regardless of batch size, which is what makes "no scrolling at
typical batch density" a structural guarantee rather than a hope: a 40 or
90-record fixture asserts the same rendered SVG height as a 3-record one in
`HistoryTimeline.test.tsx`.

**What Unit AO built was not thrown away.** Its actual fixes, as opposed to
its layout, were correct and stay: the connector is still a fixed neutral
slate (`#cbd5e1`) rather than the bucket colour, so the marker's state
colour remains the only meaningful hue; the `circle size = amount at risk`
caption still renders at readable contrast (`text-slate-500`); clicking a
bucket row still isolates it and clicking again restores every bucket;
clicking an outcome in the legend still filters to it and composes with an
active bucket filter; hovering a record still highlights its connector and
marker while dimming the rest; a filtered-empty combination still renders
the shared `EmptyState` with the filter chips and a "Clear filters, show
everything" control staying visible above it. `amountRadius`
(`web/src/lib/timelineGeometry.ts`) gained optional `minR`/`maxR`
parameters (defaulting to its existing bounds, so every prior call site is
unaffected) plus a new `amountRadiusCompact` wrapper that restores Unit
AH's original 3.5-9px bounds, tuned to the taller 34px compact row rather
than the 13px sub-row Unit AO's markers were bounded to.

**The per-record Gantt view is not gone, it is opt-in.** A small "Compact /
Per-record" toggle sits next to the search input, above the chart; clicking
"Per-record" switches `HistoryTimeline` into exactly Unit AO's original
layout (one sub-row per record, a bucket's non-isolated siblings collapsed
to a single row, the record area capped at `TIMELINE_MAX_BODY_HEIGHT` with
internal scrolling), sharing the same bucket/outcome/search filter state so
switching views never loses a reader's place. This is the opt-in the user
suggested directly: "clicking on a specific entry will open the record
drawer and there will be an option to see the gantt chart, something like
that could have been better." It defaults to Compact and is never what
someone sees first.

**Search, the other thing the user asked for directly** ("search a specific
entry"), matches a record's id (substring, so a short prefix like the
records table's `f43f0a35` works without typing the whole id) or its amount
in rupees (substring on the digits, so typing "1235" finds a ₹1,235 record;
matching in rupees rather than paise because rupees is what a reader sees on
the chart and in the drawer, not an internal storage unit). A match narrows
the view to it through the same isolate mechanism the bucket and outcome
filters already use, composes with them, and renders as a dismissible chip
alongside the other active filters. A query that matches nothing renders the
shared `EmptyState` with a description naming the query verbatim
(`No acted-on record's id or amount matches "<query>". Clear the search to
see everything again.`), rather than a panel that is silently empty for a
reason the reader has to guess at.

**Overplotting in the compact view is accepted, not solved by spreading
records out again**, per the same feedback ("even if congested... it gave a
better idea in one view"). Jitter and the marker's white stroke keep
individually dense buckets legible as a cloud rather than a single blob
without growing the row; a reader who needs to separate two overlapping
points reaches for the Per-record toggle or search rather than the default
view trying to do both jobs at once.

No backend change was needed: `RecordSummary` already carried everything
this unit reads, the same fields Unit AO already read.

---

## P2

### Unit Y: Razorpay payment-downtime webhooks
Unchanged from `docs/PHASE5_5_IMPLEMENTATION.md`. Still the strongest
differentiator found in any research pass, because it is Razorpay's own
published signal rather than an invented one.

**Done, deliberately scoped down.** `POST /v1/webhooks/payment-downtime`
receives Razorpay's real `payment.downtime.started`/`.updated`/`.resolved`
payload (not a Gateway-invented flat body); a new `payment_downtime` table
holds one row per downtime id; `guardrails.go` holds RETRY back with reason
`bank downtime active: <method> <instrument>, severity <severity>`, leaving
nudges untouched; a genuinely worthwhile retry is DEFERRED rather than
escalated, so resuming on `.resolved` needs no separate "wake up" mechanism.
Signature verification is Unit Z's job, not done here. Full writeup,
including what was cut for time, in `docs/DECISIONS.md` and `docs/PLAN.md`.

### Unit Z: webhook signature verification and the four-field error taxonomy

**Done, 2026-09-03. This closes P2, the last planned unit.** `X-Razorpay-
Signature` (HMAC-SHA256 over the raw body, verified before decoding,
constant-time comparison) is now required on both `payment-failed` and
`payment-downtime`, one shared middleware rather than duplicated per route.
`WEBHOOK_SECRET` is required at startup, the same requiredness as `API_KEY`:
an unset secret fails the Gateway's startup rather than silently letting
every webhook through or quietly rejecting them forever. `scripts/loadgen`
signs its own traffic, so `make loadgen` keeps working with no bypass on the
Gateway side.

Razorpay's four additional error fields (`error_code`, `error_description`,
`error_source`, `error_step`, `error_reason`) are accepted alongside the
existing `failure_code` and stored on `record` (migration 00009). The
classifier's rules engine uses `error_source`/`error_step` as a fallback
signal, exactly the worked example this unit was scoped around: an
unrecognised, vague code like Razorpay's own `payment_failed` paired with
`error_step=payment_authorization` and a systemic `error_source` (bank,
gateway, network) resolves to a retry; paired with
`error_step=payment_authentication` and `error_source=customer` it does
not. Both fields stay open string vocabularies (no closed enum), since
Razorpay's own docs say the possible values vary by payment method.

**Scoped out**: switching `payment-failed` to Razorpay's real nested
payload shape (kept the flat wire body, additive fields only); surfacing
the new fields in `web/`, Reporting or Audit; any change to the LLM prompt
or provider chain. Full reasoning in `docs/DECISIONS.md`.

---

## P3

### Unit AK: `/help` page

**Resolved 2026-09-03, in two passes.** First pass shipped `GET /v1/help`
as JSON only; the user asked for what they actually meant by a help page,
"like a help doc for someone trying to connect to the real system", closer
to FastAPI's `/docs` than a raw endpoint. Second pass made the route content
negotiated: a browser's `Accept: text/html` gets a rendered page grouped
under this document's own section headings, click a route to expand its
auth requirement and description, built with a native
`<details>`/`<summary>` accordion so it needs no JavaScript at all.
`curl`'s default `Accept: */*` and everything else still gets the original
JSON. Both representations are built from the one `helpRoutes` slice, so
they cannot drift apart. Six tests total: the original three plus one
proving the JSON default is unchanged, one proving `Accept: text/html`
renders the page with every route and the auth vocabulary present, one
proving `Accept: */*` does not accidentally trip the HTML branch.

### Unit AL: misleading labels

**Resolved 2026-09-03, two of three.** Verified live on a real batch before
touching anything, and again after, not assumed from a static screenshot.

- **"In flight / lost (X%)"** on the recovery bar read as active work even
  on a fully-settled batch (`in_flight_count: 0`), directly contradicting
  the IN FLIGHT tile elsewhere on the same screen. That value is escalated
  plus closed-uneconomic money, not money still being worked. Renamed to
  **"Not recovered"**, true in every batch state rather than only while
  records are moving (`RecoveryBar.tsx`).
- **"Escalations 0"** sat directly under a tile reading **"ESCALATED N"**.
  Traced to the source rather than assumed: `by_intervention.ACTION_TYPE_
  ESCALATE?.attempt_count` is structurally always 0, not merely usually 0.
  `decideForAction` (`state.go`) hands the Executor `ACTION_TYPE_
  UNSPECIFIED` alongside `RECORD_STATE_ESCALATED`, never `ACTION_TYPE_
  ESCALATE`; escalation is a direct state transition the Decision Engine
  writes itself, never a pending action dispatched for execution. No
  rename could make a permanently-zero stat honest, so the row was removed
  from `App.tsx` rather than relabelled.
- **"Confusion matrix shows 3 of 7 buckets"?** Checked and this one is not
  real. The container is `max-h-[160px] overflow-y-auto`
  (`ConfusionMatrix.tsx`): genuinely scrollable, and scrolling it reveals
  all 7 buckets with their real counts. A first pass at this investigation
  claimed data was hidden with no indication, based on one static
  screenshot; that was wrong, caught by actually scrolling the container
  (`scrollHeight: 364` vs `clientHeight: 160`, confirmed programmatically)
  before writing anything down. Left as-is: a thin scrollbar with no
  "scroll for more" hint is a minor discoverability nit at most, not a
  data problem, and not worth a code change on its own.

### Unit AM: read-only config panel
Show what the agent is bounded by, on the Demo Controls page, clearly marked
startup-only: time scale, max retries, max contacts, recovery window, contact
cooldown, LLM chain and sample rate. Doubles as a flex and as an honest
statement of the guardrails. A small `GET /v1/demo/config` route backs it.

Context for why this is worth showing: there are **56 environment variables**
and **none of them are adjustable at runtime**. The UI currently exposes four
actions and zero configuration.

**Done, 2026-09-03.** The 56 figure above was stale: `.env.example` was
recounted fresh with `grep -cE '^[A-Z_][A-Z0-9_]*=' .env.example` rather
than trusted, and has **60** entries today, still none adjustable at
runtime. `GetAgentConfig`, a new RPC on the Decision Engine's own gRPC
service, returns the values it already loaded and validated at startup:
`DEMO_TIME_SCALE`, `MAX_RETRIES`, `MAX_CONTACTS`, `CONTACT_COOLDOWN`,
`RECOVERY_WINDOW`, `LLM_SAMPLE_RATE`, `LLM_ROUTE_CONFIDENCE_THRESHOLD`,
`CLASSIFY_CONFIDENCE_THRESHOLD`, `NUDGE_MAX_CHARS`, and the
`downtimeMaxUnresolvedHold` Go constant (no env var backs it, but it is a
real bound). `GET /v1/demo/config` proxies that RPC, the same thin-proxy
pattern every other `/v1/demo/*` route already uses, gated on
`DEMO_CONTROLS_ENABLED` identically. The Demo Controls page shows it as a
new, clearly-labelled "Agent configuration" section, grouped into time
compression, retry/contact limits and LLM routing, not a flat list.

`LLM_PROVIDER_CHAIN` is deliberately **not** shown: it is owned by the
Classifier, not the Decision Engine, and surfacing it here would have
meant either a second cross-service call or a duplicated copy of the
Classifier's own config parsing sitting in the Decision Engine, exactly
the kind of drift this unit exists to avoid causing elsewhere. Left out
rather than read independently. `docs/DECISIONS.md` 2026-09-03.

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
