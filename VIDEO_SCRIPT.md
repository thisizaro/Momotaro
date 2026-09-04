# Demo video: full script and shot plan

Temporary working doc, written 2026-09-04. Not committed, not linked, delete
after the hackathon. Target: the ~5 minute submission video. Every number,
file path and line number below was verified live against the running stack
on 2026-09-04, not quoted from memory.

**Length, measured rather than guessed.** The full nine-beat script is 720
words of narration, which is about 4:54 of actual speech at a clear technical
pace (147 wpm). With realistic pauses for screen action that lands at
**roughly 5:45 total**. Pick your version before you record:

| Your limit | What to do | Result |
|---|---|---|
| 6:00 or more | Record all nine beats as written | ~5:45, comfortable |
| Hard 5:00 | Drop the `WHAT IT IS` beat, fold its stack sentence into the hook | ~5:00, workable |
| 3:00 | See "Cut-down variants" at the bottom | keeps the whole argument |
| 90 seconds | See "Cut-down variants" | still a complete argument |

I do not know your actual submission limit, so I have written it at full
length and marked exactly what to drop. **Confirm the real limit before
recording**, because trimming after the fact is much worse than planning for
it.

---

## The strategy, in one paragraph

Most hackathon videos are a feature tour. This one should not be. The track
asks for one specific thing, "measured money recovered across a batch, with
compliant escalation, stopping rules, and an audit trail", and it is judged
on two named criteria beyond that: **AI Judgment** (was AI used
appropriately, or forced in) and **Failure Recovery** (did you find real
runtime failures and engineer graceful fallbacks). The shortlist then goes
to a technical panel, so the video's job is not to explain everything. Its
job is to prove the headline number is real, show the one design decision
that separates this from a retry script, and leave the panel wanting to ask
about the depth. Everything in the script below earns one of those.

The single strongest idea you have, and the spine of the video: **the agent
is allowed to do nothing, and it can tell you what it decided not to do and
why.** A retry script cannot do that. Lead toward it, land it, close on it.

---

## Pre-flight checklist, do this before you hit record

Run in order. Budget 10 minutes.

```bash
make demo-down                      # clean slate
make demo-up PROFILE=demo           # all 9 services, demo profile
cd web && npm run dev               # dashboard on :5173
```

Then **seed the batch you will film from the Demo Control Panel in the UI**
(scenario `normal`, count 100, seed 7), not from a terminal. That path writes
real ground truth and gives you the accuracy score and baseline, and seeding
on camera is better video than cutting to a shell. `make batchgen COUNT=100
SEED=7` produces an equivalent batch if you would rather not film the seeding
step.

Then verify, do not assume:

1. **Confirm the profile actually applied.** If it did not, every number is
   wrong and the run looks like the agent barely acted:
   ```bash
   make --eval='__show:; @echo $(DEMO_TIME_SCALE)' __show PROFILE=demo
   ```
   Must print `300000`. This exact failure cost a full misdiagnosis on
   2026-08-31 (`docs/INCIDENTS.md`).
2. **Confirm the dashboard is live, not mocked.** `VITE_API_BASE_URL` must be
   set to `http://localhost:8090`, and there must be no mock banner in the UI.
   Otherwise you are filming `web/src/lib/mockEngine.ts` inventing numbers.
3. **Confirm the deterministic anchor.** The naive baseline net must read
   **Rs 487,769**. Verified live on 2026-09-04 and it matched exactly. If that
   number moved, something real changed and you should not film yet.
4. **Pick your drill-down record in advance.** Do not hunt live. You want one
   record that has an LLM rationale and a full three-action EV table, and one
   that closed uneconomic on a contact cap. Find them, note the record ids,
   have them ready.
5. **Close every other tab.** Nothing kills a technical demo faster than a
   Slack notification over the audit trail.

**Two real runs measured on 2026-09-04, both SEED=7, count 100.** Run A was
seeded with `make batchgen`, run B through the Demo Control Panel. Both are
healthy. The spread between them is what normal variance looks like:

| | Run A (batchgen) | Run B (demo panel) | Naive baseline |
|---|---|---|---|
| At risk | Rs 11,05,166 | Rs 11,05,166 | same batch |
| Net recovered | Rs 6,15,359 | Rs 6,02,499 | **Rs 4,87,769** |
| Intervention spend | Rs 43.82 | Rs 47.45 | Rs 78.98 |
| Recovery rate | 51% | 43% | n/a |
| Classification accuracy | 92% | 93% | n/a |
| Deliberately not chased | 36 | 41 | 0, it chases everything |
| Escalated | 13 | 16 | n/a |
| Processing failures | 0 | 0 | n/a |

**What is deterministic and must not move**: the at-risk total, and the
baseline's Rs 4,87,769. Both were identical across the two runs and across
seeding paths. If either moves, stop and investigate before filming.

**What legitimately varies run to run**: net recovered, recovery rate,
escalations, uneconomic count. The World Simulator rolls real dice against
the sealed profile. A 51% run and a 43% run are both healthy, and `README.md`
documents exactly this (it records a second run at 43%).

**The headline, safe to say either way:** you recover **over a lakh more than
the naive policy while spending roughly 40% less**, at 92 to 93% accuracy.
Read your own screen for the exact figure rather than quoting this table.

---

## The script

Format: **[time] BEAT NAME (MUST/CUT)** then what is on screen, then the
words. Narration is written to be spoken, not read.

Timings below are each beat's measured speech length plus realistic pause,
running to 5:45 total. They are not idealised slots. Do not rush to hit them:
going ten seconds long on the decision panel is much better than clipping it,
and that beat is the one the whole video is built around.

If you are cutting to a hard 5:00, drop `WHAT IT IS` and shift everything
after it 22 seconds earlier.

---

### [0:00 - 0:25] HOOK: the problem (MUST)

**On screen:** Not the dashboard yet. Either a plain title card, or the
Razorpay checkout/failure framing. Keep it still and quiet.

> "A merchant on Razorpay loses revenue in ways that never show up as one
> clean incident. A card times out at the bank. An autopay mandate fails and
> nobody retries it before the next cycle. Today the only answers are a
> generic retry, usually wrong for the actual failure, or a person doing it by
> hand, which does not scale."

*Why this beat: it establishes that the problem is diagnosis, not retry
volume. Everything after this depends on the viewer accepting that.*

---

### [0:25 - 0:47] WHAT IT IS (MUST)

**On screen:** The architecture diagram from `docs/ARCHITECTURE.md` section 3
(the mermaid flowchart), or the dashboard header with the nine services
implied. Do not linger on the diagram, five seconds maximum.

> "Momotaro sits downstream of those failure events. For each one it works out
> why it actually failed, prices every action it is allowed to take, does the
> one worth doing, and stops when nothing is. Nine Go services: Postgres as
> the source of truth, Kafka for events, gRPC in between."

*Why this beat: names the stack once, quickly, then never talks about
plumbing again. Judges assume you can wire services. They are looking for
whether you can make a decision.*

---

### [0:47 - 1:37] THE LIVE RUN (MUST)

**On screen:** The **Demo Control Panel**, in the dashboard. Open the scenario
dropdown so the four presets are visible on camera, pick `normal`, count 100,
seed 7, and hit seed. Stay on the dashboard and let it visibly fill. Do not
speed it up, the live fill is the proof. No terminal needed.

> "I'll seed a hundred failed payments from the dashboard itself. Each preset
> concentrates a different root cause: bank outage, salary day, dead cards.
> I'll take the normal mix.
>
> Every record carries a hidden answer key, and the agent provably cannot read
> that table. A test enforces it."
>
> *(pause, let the dashboard fill for several seconds)*
>
> "That feed is a real WebSocket push, not polling. And this is what the track
> asks for: measured money recovered across a batch. **[your figure]**
> recovered out of eleven lakh at risk, for under fifty rupees of spend."

**Show alongside, briefly:** `test/integrity/ground_truth_isolation_test.go`
in the editor for two seconds while saying "there is a test that enforces
it." Do not read the test out loud, just let it exist.

*Why this beat: "measured" is the rubric's word. The sealed answer key is
what makes it a measurement instead of a claim, and saying so before anyone
asks is what makes it credible.*

---

### [1:37 - 2:45] THE DECISION, THE CENTREPIECE (MUST, never cut)

**On screen:** Open the record drawer on your pre-picked record. The "Why
this action" panel with the ranked EV table. Slow down here. This is the
single most important minute in the video.

> "This is the part I actually care about. Open any record and you do not just
> see what the agent did. You see what it considered and rejected.
>
> Three actions, each priced. A method-update nudge wins at plus two hundred
> forty seven rupees of expected value. A retry comes out at minus six
> twenty five, because the failure code says the customer has to act, so
> retrying the same card costs money and recovers nothing. The agent priced
> that before deciding.
>
> The ordering is fixed, and it is the most deliberate decision in the
> project. The model proposes. Guardrails constrain, and they can only remove
> options, never add one. Then deterministic arithmetic decides. The LLM never
> authorises spending money. It classifies a failure and drafts the customer's
> message. That is the whole of its job."

**Show alongside:** `services/decision-engine/internal/economics/score.go:59`
for the actual EV line, on screen for three seconds while you say
"deterministic arithmetic decides".

*Why this beat: this is the AI Judgment criterion, answered by demonstration
rather than assertion. "What if the model hallucinates" is the obvious
challenge, and this beat pre-answers it: a hallucinated bucket produces a
wrongly priced but still bounded and still audited action. It cannot produce
an unbounded one.*

---

### [2:45 - 3:20] IT IS ALLOWED TO DO NOTHING (MUST, this is your differentiator)

**On screen:** The "Closed (Uneconomic)" tile, then click into one such
record showing "Blocked by guardrails" with the contact-cap reason string.

> "**[read the tile: 36 in run A, 41 in run B]** of these hundred were
> deliberately not chased. Close to three lakh rupees at risk that the agent
> decided was not worth spending money on.
>
> That is shown separately from escalations on purpose, because 'we decided
> this is not worth it' and 'a human should look at this' are different facts,
> and a finance team needs to tell them apart. Knowing when not to act is the
> product working."

*Why this beat: this is the thing a retry script structurally cannot do, and
almost no other submission will have it. It is also the beat most likely to
be quoted back to you at the panel. Land it cleanly.*

---

### [3:20 - 4:00] STOPPING RULES, COMPLIANCE, AUDIT TRAIL (MUST)

**On screen:** `services/decision-engine/internal/engine/guardrails.go`
around lines 128 to 141 (the reason strings), then
`services/decision-engine/internal/engine/schedule.go:96` (the TRAI window
constants), then cut to the System Invariants panel on the dashboard.

> "The stopping rules are real. Retry cap, contact cap, cooldown, recovery
> window, plus two actual Indian regulations: TRAI's contact-hour window, ten
> in the morning to nine at night, and RBI's twenty four hour pre-debit notice
> for e-mandates. Enforced in code, not described in a design document.
>
> Every state change and its audit row are written in one Postgres
> transaction, so there is never a window where a record changed and nothing
> recorded why. And an invariant checker runs continuously: zero violations,
> zero incomplete trails, across the whole batch."

*Why this beat: "compliant escalation, stopping rules, and an audit trail" is
a direct quote from the track brief. This beat is you handing the judge those
exact three things, with named regulations and a live zero-violation counter
instead of a claim.*

---

### [4:00 - 4:45] FAILURE RECOVERY, HONESTLY (MUST)

**On screen:** A record's audit trail showing the provider hop chips, ideally
one reading `groq: rate_limited` then `rules: ok`. If you cannot find one
live, the `llm_quota_exhausted_count` of 9 on the report is your evidence.

> "Failure recovery is an explicit judging criterion, so here it is honestly.
> The classifier is a chain: Groq first, then a deterministic rules engine
> that cannot fail, because it does no I/O at all.
>
> When Groq hits its real rate limit, and on a hundred-record batch it does,
> you see it here in the trail: Groq rate limited, rules ok. Nine records hit
> that in this run and every one still got a real answer. A last rung that
> cannot fail is what turns a provider outage into slightly less accurate
> answers, instead of a stalled pipeline."

**Optional, only if you have the time and nerve:** the live kill. SIGKILL the
decision engine mid-batch, show no record lost and no audit gap. It is backed
by `test/e2e/crash_safety_test.go`. Rehearse it three times before filming or
do not do it at all.

*Why this beat: the criterion says "identified system failures at runtime".
The rate limit is a real runtime failure, visible in a real artifact, not a
simulated one. That is stronger than any staged outage.*

---

### [4:45 - 5:25] RAZORPAY DEPTH AND THE FINTECH INSTINCT (MUST)

**On screen:** `services/classifier/internal/rules/buckets.go:110`, the line
where `PAYMENT_TIMED_OUT` maps to `RISK_HOLD` rather than to a retry bucket.
Then the payment-downtime guardrail.

> "Two things here come from working on an agentic ERP at Light, a European
> fintech where AI agents post to a real general ledger.
>
> First, these are Razorpay's own published failure codes, and one matters
> more than the rest. `payment_timed_out` deliberately does not auto-retry.
> 'We do not know whether the bank succeeded' is not the same fact as 'it
> failed', and retrying that is how a recovery agent creates a duplicate
> charge.
>
> Second, it consumes Razorpay's `payment.downtime` webhooks and holds retries
> back while a known outage is live."

*Why this beat: this is the credibility beat, and the indeterminate-versus-
failed distinction is the single sharpest domain point in the whole project.
It is the kind of thing you only say if you have thought about what happens
when an agent touches money. Say it plainly and move on. Do not oversell the
Light connection, one clause is enough, the technical point does the work.*

---

### [5:25 - 5:45] CLOSE (MUST)

**On screen:** The comparison, side by side. Momotaro versus naive baseline.

> "Against a naive retry-everything policy scored on the same sealed ground
> truth: over a lakh more recovered, on roughly forty percent less spend, at
> ninety two percent classification accuracy. Every number you just saw came
> through the pipeline."

**Read the exact figures off your own run.** Run A gave "one lakh twenty seven
thousand more, forty four percent less spend"; run B gave "one lakh fifteen
thousand more, forty percent less". Both true, both good. The phrasing above
is safe for either.

*Why this beat: ends on the measured number, which is what the track asked
for in its first sentence. "None of it is hardcoded" pre-empts the single
most common suspicion about a polished hackathon dashboard.*

---

## Rubric mapping: what each beat is buying

| Beat | Rubric clause it answers |
|---|---|
| 0:00 hook | "detects revenue at risk" (frames why detection is hard) |
| 0:20 what it is | context only, buys nothing directly, keep it short |
| 0:45 live run | **"measured money recovered across a batch"**, the headline |
| 1:35 decision panel | **AI Judgment**, "appropriately, instead of forcing unnecessary tech stacks" |
| 2:35 do nothing | "determines the right intervention", and the differentiator |
| 3:05 stopping rules | **"compliant escalation, stopping rules, and an audit trail"** verbatim |
| 3:40 failure recovery | **Failure Recovery**, "identified system failures at runtime and engineered graceful fallbacks" |
| 4:20 Razorpay depth | domain credibility, "bounded recovery workflow" done properly |
| 4:50 close | back to the headline number |

Every clause in the track brief is covered. Check this table again after you
cut anything.

---

## Where the fintech instinct lands, and how to say it

You said you want the Light experience visible without it sounding like a
claim. The rule: **never assert the credential, demonstrate the instinct and
attribute it in one clause.** Four places it fits naturally, in priority
order:

1. **Indeterminate is not failed** (beat 4:20). The strongest one. An agent
   that retries a `payment_timed_out` creates a duplicate charge. This is a
   ledger-correctness instinct and it is genuinely uncommon in hackathon work.
2. **Money is integer paise everywhere, and the one exception is deliberate.**
   `EVPaise` is a float precisely because an expected value is an estimate,
   not a balance, and `CostPaise` next to it stays an integer because it is
   real money. If anyone asks "why is your money a float", that is the answer,
   and it is a very good one. Keep it for the panel, not the video.
3. **The audit row and the state change are one transaction.** This is the
   dual-write problem, and it is the same discipline as never letting a ledger
   and its journal diverge. One sentence in beat 3:05 already carries it.
4. **Guardrails are a policy layer that can only subtract.** "Policy-driven
   workflow" is exactly the vocabulary of the multi-entity accounting world.
   Already carried by beat 1:35.

**Suggested phrasing for the attribution**, use once, not twice: *"from
working on an agentic ERP at Light, a European fintech where AI agents post
to a real general ledger"*. It is specific, verifiable, and explains why you
would have the instinct, all in one clause.

---

## Cut-down variants

**If you have 3:00:** drop beat 0:20 (what it is, fold one sentence into the
hook) and beat 4:20 (Razorpay depth, move it to the panel). Tighten the live
run to 35 seconds. Keep 1:35, 2:35, 3:05 and 3:40 intact. You lose
credibility framing but keep the entire argument.

**If you have 90 seconds:** hook (15s), live run and headline number (30s),
the decision panel (30s), "it is allowed to do nothing" (15s). Nothing else.
That is still a complete argument.

**If you have 10 minutes:** keep all of the above at this pace and add: a
walk through `docs/DECISIONS.md` on the Gemini call (built, measured, and
deliberately kept out of the default chain on latency grounds), the
confusion matrix by bucket, and the live pod-kill crash-safety demo.

---

## Panel prep: what they will ask after this video

The video's job is the shortlist. These are the follow-ups it sets up, and
you have real answers to all of them already in `docs/PANEL_BRIEF.md`.

- **"Where is the AI, really?"** Two places, both bounded: classification and
  message composition. Not action selection, not budgets.
- **"What if the model is wrong?"** It is, about 8% of the time here, and it
  is measured against sealed ground truth rather than guessed. A wrong bucket
  produces a wrongly priced but still bounded and still audited action.
- **"Why is your money a float?"** See point 2 above. Have `score.go` open.
- **"Is the demo reproducible?"** Partly, and be precise rather than
  overclaiming. Inputs, answer key and economics reproduce exactly. The final
  rupee total does not, for two nameable reasons: the TRAI guardrail is
  checked against a real clock while time is compressed 300000x, and 15% of
  records get a live model call. Roughly nine records in a hundred end
  differently. This is written up in `docs/INCIDENTS.md` 2026-09-02.
- **"How do you know the audit trail is complete?"** An invariant checks it
  continuously and it is on the dashboard.

**Two war stories worth having ready**, both real, both in `INCIDENTS.md`,
both showing the kind of debugging judges actually respect:

1. **The live WebSocket had never worked and three rounds of green tests said
   otherwise** (2026-09-02). A polling loop masked it, so every number on
   screen kept moving while the stream was dead. The tests exercised the state
   machine against a fake socket and were correct; the one condition that
   failed in production, a cross-origin handshake returning 403, was the one
   condition an `httptest` server structurally could not reproduce. Lesson: a
   test double cannot fail the way the real dependency fails.
2. **`DEMO_TIME_SCALE` compressed the recovery window into nothing and
   escalated 73 of 100 records** (2026-08-31). The dashboard showed a naive
   baseline beating the agent 2.8x, and the first hypothesis was that the
   economics were broken. It was not. A duration compared against elapsed
   wall-clock time had been scaled like the durations we wait out. The
   giveaway was that total spend was Rs 11, which reads as impressive
   efficiency and actually meant the agent had barely acted at all. Lesson:
   when a headline metric looks wrong, group the audit `reason` column before
   theorising about the model.

That second story is particularly good for a panel, because it shows you can
be wrong about your own system, find it with a query rather than a hunch, and
say so.

---

## Traps, do not do these

- **Do use the Demo Control Panel to seed.** (Corrected 2026-09-04 after
  checking the code rather than the docs. `docs/PRD.md` section 12 and
  `README.md` both still warn you off "the dashboard's generate button", and
  both are stale: that was the old "Generate Sample Data" button, which Unit X
  removed. The panel that replaced it drives `POST /v1/demo/batches` into
  `WorldSimulator.SeedBatch`, which writes real `GROUND_TRUTH` "exactly like
  scripts/batchgen" in its own words, and `submitBatch` no longer exists in
  `web/src/lib/api.ts` at all. Verified live: a panel-seeded batch returned a
  93% accuracy score and the full baseline comparison.)
- **Do not claim load testing or Kubernetes.** Both were deliberately skipped.
  If asked, "we chose to spend the time on the decision layer" is the honest
  and stronger answer.
- **Do not promise a reproducible rupee total on camera.** Say "measured",
  not "reproducible", unless you are talking about the baseline, the accuracy
  or the zero recovery-window escalations, which are genuinely deterministic.
- **Do not describe the World Simulator as a mock.** It is a sealed-answer-key
  simulator, and that distinction is what makes the accuracy number a
  measurement. `docs/PRD.md` section 12a has the exact framing.
- **Do not read file contents aloud.** Show them for two or three seconds
  while saying the point. Reading code on camera is dead air.
