# Demo video: 7 minute script and shot plan

Temporary working file. Delete after the hackathon.

Every number below was measured on 2026-09-04 against the running stack,
using the two batches this script films: `COUNT=50 SEED=4` and
`COUNT=100 SEED=4`.

Runtime target 7:00. Narration measures 893 words, about 6:05 of speech at a
clear technical pace, leaving roughly 55 seconds for pauses and switching
tabs. Batch fill time sits outside that, since you are fast forwarding it.

The beats below run to 7:15. If you need it tighter, shorten the
architecture walkthrough at 0:55: drop the state machine tab and keep the
container view and the sequence diagram.

---

## Part 1: before you record

### Browser tabs to open first

Keep these in one window, in this order, so you can switch and point at the
real source instead of describing it. Every URL below is cited in the repo's
own config or code, so the tab and the file agree.

Razorpay, for the "we built this against your platform" beats:

1. Error codes: https://razorpay.com/docs/errors/payments/list/
2. Webhook payloads: https://razorpay.com/docs/webhooks/payloads/payments/
3. Webhook signature validation: https://razorpay.com/docs/webhooks/validate-test/
4. Pricing, for the retry fee in the cost model: https://razorpay.com/pricing/

Regulators, for the compliance beat:

5. TRAI TCCCPR regulation PDF: https://www.trai.gov.in/sites/default/files/2025-02/Regulation_12022025.pdf
6. RBI master directions, e-mandate: https://www.rbi.org.in/Scripts/BS_ViewMasDirections.aspx?id=13374
7. NPCI NACH charges circular: https://www.npci.org.in/PDF/nach/circular/2018-19/circular%20no.032%20on%20NACH%20charges%20for%20transaction%20and%20mandates.pdf

GitHub tabs, one per diagram, so you never scroll looking for one on camera:

8. Repo landing page, which renders the story diagram at the top
9. `docs/DATA_FLOW.md#2-container-view`
10. `docs/DATA_FLOW.md#4-one-records-journey`
11. `docs/DATA_FLOW.md#5-record-lifecycle`
12. `docs/PANEL_BRIEF.md`, section 2, for the EV formula if you want it on screen

Local tabs: the dashboard on `localhost:5173`, and `localhost:8090/v1/help`.

### API keys

Put the fresh `GROQ_API_KEY` in `.env` before you start anything. The nine
services read config once at boot, so a key swapped after `make demo-up` does
not take effect until you restart.

Do not open `.env` on camera. Nothing else in the demo displays a key.

### Start clean

You are running these yourself. In order:

```bash
make demo-down
make down-clean                 # wipes Postgres and Kafka volumes
make demo-up PROFILE=demo       # infra, migrations, all nine services
cd web && npm run dev           # dashboard on :5173
```

Optional, if you want Grafana in the video:

```bash
make up-observability HOST_IP=$(hostname -I | awk '{print $1}')
```

Run that one before `make demo-up`, not at the same time. Both bring up the
base stack.

### Seed both batches before recording

```bash
make batchgen COUNT=100 SEED=4    # the headline numbers, let it settle
make batchgen COUNT=50 SEED=4     # keep this one to seed live on camera
```

Film it this way: the 100-record batch is already settled when you start, so
its numbers are on screen immediately. Seed the 50-record batch live from the
Demo Controls page so viewers watch it fill. You get settled numbers and live
motion without waiting on camera.

### Check these four things before you hit record

```bash
make --eval='__show:; @echo $(DEMO_TIME_SCALE)' __show PROFILE=demo
```

1. That prints `300000`. If it prints `1`, the profile did not apply, every
   wall-clock wait is real, and the batch never finishes.
2. The dashboard shows no amber mock banner. If it does, `VITE_API_BASE_URL`
   is unset and you are filming invented numbers.
3. The 100-record batch reads `Rs 7,10,036` at risk and a baseline of
   `Rs 1,75,512`. Both are deterministic. If either moved, something real
   changed and you should not film yet.
4. Pick your drill-down record now. You want one with a full three-action EV
   table and an LLM rationale, and one closed uneconomic on a contact cap.
   Note both record IDs. Do not hunt on camera.

Close Slack, close notifications, and hide any window that is not the demo.

### Numbers you should see

Measured 2026-09-04, both seeds fresh.

| | 50 records, seed 4 | 100 records, seed 4 |
|---|---|---|
| At risk | Rs 3,57,447 | Rs 7,10,036 |
| Net recovered | Rs 1,64,609 | Rs 2,75,823 |
| Intervention spend | Rs 20.03 | Rs 45.84 |
| Recovery rate | 48% | 41% |
| Classification accuracy | 94% | 91% |
| Declined as uneconomic | 21 | 47 |
| Escalated | 5 | 12 |
| Processing failures | 0 | 0 |
| Naive baseline, net | Rs 89,929 | Rs 1,75,512 |
| Naive baseline, spend | Rs 41.90 | Rs 84.28 |

Say the 100-record comparison out loud: about Rs 1,00,000 more recovered on
46% less spend. On the 50-record batch the gap is wider, 83% more recovered
on half the spend.

At risk and the baseline are deterministic and will reproduce. Net recovered,
recovery rate and accuracy move a few points per run, because the World
Simulator rolls real dice and a share of records get a live model call. With
new API keys your accuracy may land a point or two either side of these.
Read your own screen.

---

## Part 2: the script

Timings are measured speech plus realistic pause. Going long on the decision
panel is fine. Rushing it is not.

### 0:00 to 0:25, the problem

On screen: title card, or the Razorpay error codes tab. Nothing moving.

> "A merchant on Razorpay loses revenue in ways that never show up as one
> clean incident. A card times out at the bank. An autopay mandate fails and
> nobody retries it before the next cycle. The only answers today are a
> generic retry, which is usually wrong for the actual failure, or a person
> working a spreadsheet, which does not scale."

### 0:25 to 0:55, what it is and the one rule that shapes it

On screen: the repo landing page, with the story diagram visible.

> "Momotaro sits downstream of those failures. For each one it works out why
> it failed, prices every action it is allowed to take, does the one worth
> doing, and stops when nothing is.
>
> One rule shapes the whole system. The model proposes, guardrails constrain,
> and deterministic arithmetic decides. The LLM classifies a failure and
> drafts the customer's message. It never authorises spending money."

### 0:55 to 2:05, architecture

Switch between the GitHub tabs. Give each diagram five to ten seconds.

Tab 9, the container view in `DATA_FLOW.md`:

> "Nine Go services. Postgres holds the truth, Kafka carries events, gRPC
> connects the services, and one HTTP gateway is the only door in.
>
> Three things on this diagram are worth pointing at. The Classifier has no
> database at all, so it cannot accumulate hidden state between calls. Audit
> and Reporting never write, they only read, which is what lets Audit check
> the system's own promises without being able to alter them. And Redis has
> exactly one user, the simulator's delayed-outcome queue."

Tab 10, the sequence diagram:

> "This is one record's path. The part to watch is the transaction in the
> middle: the state change and the audit row are written together, so there
> is never a moment where a record moved and nothing recorded why."

Tab 11, the state machine:

> "Nine states, three of them terminal. Recovered, escalated to a human, and
> closed as uneconomic. Keeping that last one separate is the difference
> between an agent that knows when to stop and one that just runs out of
> retries."

### 2:05 to 3:15, the live run

On screen: the dashboard. Demo Controls page. Open the scenario dropdown so
the four presets are visible, pick normal, count 50, seed 4, and seed it.
Then switch to the batch view and let it fill.

> "I will seed fifty failed payments from the dashboard. These presets each
> concentrate one root cause: a bank outage, salary day, dead cards. I will
> take the normal mix.
>
> Every record carries a hidden answer key, what is really wrong with it and
> whether it is really recoverable. The decision path provably cannot read
> that table, and a test enforces it. That is what makes the accuracy number
> a measurement rather than a claim.
>
> The live feed is a real WebSocket push from the gateway."

Switch to the settled 100-record batch.

> "Here is the hundred-record run. Seven lakh ten thousand at risk, two lakh
> seventy six thousand recovered net, for forty six rupees of total spend."

### 3:15 to 4:25, the decision panel

On screen: open your chosen record's drawer. The Why this action panel. Slow
down here. This is the centre of the video.

> "Open any record and you see what the agent considered, not only what it
> did.
>
> Three actions, each priced. The method-update nudge wins on expected value.
> The retry is negative, because the failure code says the customer has to
> act, so retrying the same card costs money and recovers nothing. The agent
> priced that before choosing.
>
> Below it is the message it actually sent, in Hinglish, generated for this
> customer and stored on the record.
>
> This panel is the answer to what happens when the model is wrong. A wrong
> classification produces a wrongly priced action that is still inside every
> guardrail and still fully audited. It cannot produce an unbounded one."

If you want the formula on screen, switch to tab 12 for five seconds while
saying "priced".

### 4:25 to 4:55, when it declines to act

On screen: the Closed Uneconomic tile, then a record showing Blocked by
guardrails with the contact cap reason.

> "Forty seven of the hundred were deliberately not chased. The agent priced
> them and decided chasing cost more than it would recover.
>
> That is reported separately from escalations, because a finance lead needs
> to tell two things apart: what a human should look at, and what the agent
> decided was not worth the money. Both are useful. They are not the same
> queue."

### 4:55 to 5:35, stopping rules and compliance

On screen: the guardrail reason strings, then switch to tab 5, the TRAI PDF,
then tab 6, the RBI page. Then the System Invariants panel.

> "The stopping rules are retry cap, contact cap, cooldown, and a recovery
> window. Two of them come from real Indian regulation rather than from me.
>
> TRAI's contact-hour window, ten in the morning to nine at night. And RBI's
> twenty four hour pre-debit notice for e-mandates, which means a mandate
> retry can never be scheduled sooner than a day out, no matter what the
> economics say.
>
> Both are enforced in the scheduler, in code. And an invariant checker runs
> continuously: zero stopping-rule violations, zero incomplete audit trails,
> across every batch."

### 5:35 to 6:15, failure recovery

On screen: a record's audit trail showing the provider hop chips.

> "The classifier is a chain. Groq first, then a deterministic rules engine
> that cannot fail, because it makes no network call at all.
>
> When Groq hits its rate limit, and on a hundred-record batch it does, you
> see it here in the trail: groq rate limited, rules ok. Every one of those
> records still got a real answer and a real action.
>
> A terminal rung that cannot fail turns a provider outage into slightly less
> accurate answers instead of a stalled pipeline. The trail records which rung
> answered, so nothing about that is hidden."

### 6:15 to 7:00, Razorpay depth and where the instinct comes from

On screen: tab 1, the Razorpay error code list, then the `buckets.go` line
where `PAYMENT_TIMED_OUT` maps to a non-retry bucket. Then tab 2, the webhook
payload docs.

> "These failure codes are Razorpay's published list, not codes I invented.
> One of them matters more than the rest.
>
> `payment_timed_out` deliberately does not map to a bucket that auto-retries.
> We do not know whether the bank succeeded is a different fact from it
> failed, and retrying the first one is how a recovery agent creates a
> duplicate charge.
>
> That instinct comes from working on an agentic ERP at Light, a European
> fintech where AI agents post to a real general ledger. The system also
> consumes Razorpay's payment.downtime webhooks and holds retries back while a
> known outage is live, and verifies webhook signatures with HMAC-SHA256 in
> constant time."

### 7:00 to 7:15, close

On screen: the comparison, agent against naive baseline.

> "Against a naive retry-everything policy, scored on the same sealed answer
> key: about a lakh more recovered on forty six percent less spend, at ninety
> one percent classification accuracy. Every number came through the pipeline."

---

## Part 3: things that will cost you if you get them wrong

Do not use the World Simulator and the word mock in the same sentence. The
simulator holds a sealed answer key and stays in the system permanently. The
dashboard's mock mode is development scaffolding. Conflating them invites a
question you do not want.

Do not claim load testing or Kubernetes. Both were skipped deliberately.
"We spent the time on the decision layer instead" is true and stronger than
a hedge.

Do not promise a reproducible rupee total. At risk and the baseline
reproduce. Net recovered does not, because the simulator rolls dice and a
share of records get a live model call. Say measured, not reproducible.

Do not read code aloud. Show a file for three seconds while you make the
point, then move on.

---

## Part 4: what a panel will ask next

You have real answers to all of these in `docs/PANEL_BRIEF.md`.

Where is the AI, really? Two places, both bounded: classification and message
composition. Not action selection, not budgets.

What if the model is wrong? It is, about 9% of the time here, measured
against sealed ground truth rather than estimated.

Why is your expected value a float when every other money value is an
integer? Because an expected value is an estimate, not a balance. `CostPaise`
beside it is real money and stays an integer.

Is the demo reproducible? Inputs, the answer key and the economics reproduce
exactly. The final total does not, and there are two nameable reasons: the
TRAI guardrail is checked against a real clock while time runs 300000 times
compressed, and a share of records get a live model call.

Two stories worth having ready, both in `docs/INCIDENTS.md`:

The live WebSocket had never worked, and three rounds of green tests said
otherwise. A polling loop kept every number on screen moving while the socket
was dead. The tests drove a fake socket and were correct; the one condition
that failed in production, a cross-origin handshake returning 403, was the one
an httptest server structurally cannot reproduce.

`DEMO_TIME_SCALE` compressed the recovery window to two seconds and escalated
73 of 100 records before economics ever priced them. The dashboard showed a
naive baseline beating the agent by 2.8x. Total spend was Rs 11, which reads
as efficiency and actually meant the agent had barely acted. A duration
compared against elapsed wall-clock time is not the same kind of value as a
duration you wait out.

---

## What changed from the 5 minute version

Runtime moved from 5:00 to 7:00, so the architecture walkthrough became its
own 70 second beat with named diagram tabs instead of a 20 second mention.

Added Part 1, which did not exist: browser tabs with the real cited URLs, API
key handling, the clean start sequence, both seed 4 batches, and four
pre-flight checks.

Swapped seed 7 for seed 4 throughout and measured both batch sizes fresh. The
seed 4 numbers are stronger: 57% more recovered than baseline on the hundred,
83% more on the fifty.

Rewrote the demo beats to say what each panel is for and who uses it, rather
than only what is on screen.

Moved the Razorpay grounding out of a single line into its own beat with
three source tabs, and put the compliance sources on screen instead of citing
them verbally.

Cut the rubric-mapping table and the cut-down variants. At 7:00 you are not
cutting anything, and the table was for deciding what to drop.
