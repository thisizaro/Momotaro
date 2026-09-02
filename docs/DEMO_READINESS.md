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

Detail per unit below the table. Units continue the Phase 5.5 letter sequence
(AC onward) so nothing collides with U to AB.

| # | Unit | What | Est | Status |
|---|---|---|---|---|
| **P0** | | **Demo-breaking or embarrassing** | **~10h** | |
| 1 | AC | Nudge messages are written to a column nothing reads | 2h | **done** #100 |
| 2 | AD | Seed the World Simulator | 2h | **partial** #99, #104 |
| 3 | AE | LLM messages leak internal vocabulary | 2h | **done** #98 |
| 4 | AF | Empty states: infinite skeletons when no batch | 3h | **done** #101 |
| 5 | AG | "Disconnected" badge on a healthy system | 1h | **done** #101 |
| **P1** | | **Built already, invisible** | **~16h** | |
| 6 | S | Surface `decision_trace` (the "why not" table) | 4h | **done** |
| 7 | AH | Historical timeline + real vs relative time | 6h | **done** |
| 8 | AI | Confidence-based LLM routing + quota banner | 4h | |
| 9 | AJ | Live production stream (CLI + honest no-baseline) | 2h | |
| **P2** | | **Differentiators** | **~11h** | |
| 10 | Y | Razorpay payment-downtime webhooks | 5h | |
| 11 | Z | Real webhook payload, signature, error taxonomy | 6h | |
| **P3** | | **Polish** | **~6h** | |
| 12 | AK | `/help` page from the frozen contract | 3h | |
| 13 | AL | Misleading labels and the confusion matrix | 2h | |
| 14 | AM | Read-only config panel | 1h | |
| 15 | AN | Redesign the record drawer, and show real time against simulated time | 3h | **done** |
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

**Resolved 2026-09-02 (#101), and it was three defects, not a label bug.**
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
