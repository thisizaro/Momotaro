# Demo video: script and shot plan

Temporary working file. Delete after the hackathon.

Every number was measured on 2026-09-04 against the running stack, using the
two batches this script films: `COUNT=50 SEED=4` and `COUNT=100 SEED=4`.

**Runtime, measured rather than hoped for: about 9:15.** Narration is 1,167
words, which is 7:56 of speech, plus roughly 80 seconds of pauses and tab
switching. Batch fill sits outside that since you are fast forwarding it.

You asked for 7 minutes and then added the full architecture walkthrough, the
systems depth, and code on screen. Those three are worth about 3 minutes
between them. Rather than pretend it compresses, here is the honest number and
a specific way down.

To land near 7:00, cut these three and nothing else:

- The two sequence diagrams at 0:58. Keep the container view and the state
  machine. Saves about 40 seconds.
- The live run's second half at 4:20. Seed the 50, then cut straight to the
  settled 100-record numbers. Saves about 20 seconds.
- Fold "when it declines to act" at 6:22 into the end of the decision panel
  beat. Saves about 25 seconds.

Do not cut the systems beat at 2:42. That is the one that shows what kind of
engineer built this, and it is the reason the rest is believable.

The framing throughout is event-driven distributed systems, with the model as
one bounded component inside it. That is what the code is, and it is what you
want to be asked about.

---

## Part 1: before you record

### The diagram viewer

Open `diagrams/index.html` in a browser. It holds all 12 diagrams in
GitHub's own Mermaid theme, with mouse-wheel zoom anchored at the cursor and
drag to pan. Scroll to zoom, drag to move, `[` and `]` to step between
diagrams, `Fit` to reset.

The files came out of the repo's own Mermaid source, so the viewer and the
Markdown never disagree. Each one is also a standalone `.svg` and a 2x `.png`
in the same folder if you would rather open one directly.

| File | What it is | Use it at |
|---|---|---|
| `01-overview` | the record's journey, the README landing diagram | 0:28 |
| `03-container-view` | all 9 services, every protocol on every edge | 0:58 |
| `04-architecture-full` | the same plus production and demo boundaries | if asked to go deeper |
| `05-sequence-immediate` | one record, start to finish | 1:35 |
| `06-sequence-delayed` | the nudge callback path | 1:55 |
| `08-state-machine` | nine states, three terminal | 2:20 |
| `10-kafka-topics` | the three topics and who reads them | on request |
| `11-er-diagram` | the seven tables | on request |

`diagrams/` is untracked, so it stays out of the repo judges browse. Say the
word and I will commit it.

### Browser tabs

Razorpay, for the platform beats:

1. Error codes: https://razorpay.com/docs/errors/payments/list/
2. Webhook payloads: https://razorpay.com/docs/webhooks/payloads/payments/
3. Webhook signature validation: https://razorpay.com/docs/webhooks/validate-test/
4. Pricing, for the retry fee in the cost model: https://razorpay.com/pricing/

Regulators, for the compliance beat:

5. TRAI TCCCPR regulation: https://www.trai.gov.in/sites/default/files/2025-02/Regulation_12022025.pdf
6. RBI e-mandate master directions: https://www.rbi.org.in/Scripts/BS_ViewMasDirections.aspx?id=13374
7. NPCI NACH charges circular: https://www.npci.org.in/PDF/nach/circular/2018-19/circular%20no.032%20on%20NACH%20charges%20for%20transaction%20and%20mandates.pdf

Every one of those URLs is cited in `configs/intervention_costs.yaml` or
`configs/recovery_priors.yaml`, so the tab and the file agree when you click
through.

Local: the dashboard on `localhost:5173`, `localhost:8090/v1/help`, the
diagram viewer, and your editor.

### Editor tabs, opened in advance

You will show seven files. Open them all before recording, in this order, so
you switch rather than navigate.

| # | File | Line | Shows |
|---|---|---|---|
| 1 | `internal/platform/kafkax/keyed.go` | 81 | `workerFor`, hash dispatch by record id |
| 2 | `internal/platform/kafkax/keyed.go` | 19 | `commitTracker`, highest contiguous offset |
| 3 | `services/decision-engine/internal/engine/store.go` | 184 | `FOR UPDATE OF rs SKIP LOCKED` |
| 4 | `services/executor/internal/attempt/store.go` | 27 | `uniqueViolation = "23505"` |
| 5 | `services/classifier/internal/provider/chain.go` | 81 | `validateChainOrder`, rules must be last |
| 6 | `services/decision-engine/internal/economics/score.go` | 59 | the EV line |
| 7 | `services/classifier/internal/rules/buckets.go` | 110 | `PAYMENT_TIMED_OUT` mapped away from retry |

### API keys

Put the fresh `GROQ_API_KEY` in `.env` before you start anything. Services
read config once at boot, so a key swapped after `make demo-up` does nothing
until you restart.

Do not open `.env` on camera. Nothing else in the demo shows a key.

### Start clean

```bash
make demo-down
make down-clean                 # wipes Postgres and Kafka volumes
make demo-up PROFILE=demo       # infra, migrations, all nine services
cd web && npm run dev           # dashboard on :5173
```

Optional, if you want Grafana on screen:

```bash
make up-observability HOST_IP=$(hostname -I | awk '{print $1}')
```

Run that before `make demo-up`, not alongside it. Both bring up the base
stack.

### Seed both batches

```bash
make batchgen COUNT=100 SEED=4    # settles while you set up
make batchgen COUNT=50 SEED=4     # or seed this one live on camera
```

Film it with the 100 already settled so its numbers are on screen
immediately, and seed the 50 live from the Demo Controls page so viewers
watch it fill.

### Four checks before you hit record

```bash
make --eval='__show:; @echo $(DEMO_TIME_SCALE)' __show PROFILE=demo
```

1. That prints `300000`. If it prints `1` the profile did not apply and the
   batch will never finish.
2. No amber mock banner on the dashboard. If it is there, `VITE_API_BASE_URL`
   is unset and the numbers are invented.
3. The 100-record batch reads `Rs 7,10,036` at risk with a `Rs 1,75,512`
   baseline. Both are deterministic. If either moved, stop and find out why.
4. Pick your drill-down record and note the id. One with a full three-action
   EV table and an LLM rationale, one closed uneconomic on a contact cap.

Close Slack and hide everything that is not the demo.

### Numbers you should see

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
46% less spend. The 50-record gap is wider, 83% more on half the spend.

At risk and the baseline reproduce exactly. Net recovered, recovery rate and
accuracy move a few points per run, because the simulator rolls real dice and
a share of records get a live model call. With new API keys your accuracy may
land a point either side. Read your own screen.

---

## Part 2: the script

### 0:00 to 0:28, the problem

On screen: title card, or the Razorpay error code list.

> "A merchant on Razorpay loses revenue in ways that never show up as one
> clean incident. A card times out at the bank. An autopay mandate fails and
> nobody retries it before the next cycle. The only answers today are a
> generic retry, which is usually wrong for the actual failure, or a person
> working a spreadsheet, which does not scale."

### 0:28 to 0:58, what it is

On screen: diagram viewer, `01-overview`.

> "Momotaro sits downstream of those failures. For each one it works out why
> it failed, prices every action it is allowed to take, does the one worth
> doing, and stops when nothing is.
>
> Underneath, this is an event-driven pipeline. Nine Go services, Kafka
> carrying the events, gRPC for the synchronous calls, Postgres holding the
> truth. The model is one component inside it, and a replaceable one."

### 0:58 to 2:42, architecture

Diagram viewer. Zoom as you talk. This beat is yours, take the time.

`03-container-view`:

> "Nine services. One gateway is the only door in, everything behind it
> speaks gRPC. Kafka does two jobs: it decouples ingestion from processing,
> and it broadcasts state changes. Every request-response call is direct gRPC
> with a deadline, because pushing an RPC through a topic and back buys
> latency and nothing else.
>
> Three boundaries here are deliberate. The Classifier has no database, so it
> scales horizontally without coordination. Audit and Reporting only read,
> which is what lets Audit check the system's promises without being able to
> alter them. And Redis has one user, the simulator's delayed-outcome queue."

`05-sequence-immediate`, then `06-sequence-delayed`:

> "One record's path. Watch the middle: the state change and its audit row go
> into Postgres in one transaction. An earlier design wrote state, published
> to Kafka, and let a consumer write the trail. That is a dual write, and a
> pod dying between the two loses the audit entry permanently.
>
> The second diagram is the asynchronous half. A bank answers a retry in
> seconds. A customer answering a nudge does not, so that outcome parks in a
> Redis sorted set and arrives later through a callback into the same state
> machine. The state machine cannot tell the two apart."

`08-state-machine`:

> "Nine states, three terminal: recovered, escalated, and closed as
> uneconomic. Keeping the last two separate is the difference between an
> agent that decides to stop and one that runs out of retries."

### 2:42 to 4:20, the parts that were actually hard

Switch to the editor. Three files, roughly fifteen seconds each. This is the
beat that shows what kind of engineer built it.

Editor tab 1 and 2, `keyed.go`:

> "Kafka gives at-least-once delivery and ordering inside a partition.
> Consuming one message at a time and then blocking on the classifier caps
> you around half a record per second per partition.
>
> So each pod runs a worker pool and dispatches on a hash of the record id.
> Same record, same worker, still ordered. Different records run
> concurrently.
>
> That creates the real trap: offset commits. Records now finish out of
> order, and committing whatever finished last silently drops everything
> still in flight behind it when the pod dies. This tracker commits the
> highest contiguous completed offset, so an unfinished record at offset N
> pins the commit at N minus one however many later ones are done."

Editor tab 3, `store.go:184`:

> "Scheduled work uses `FOR UPDATE SKIP LOCKED`, the Postgres job queue
> pattern. Every pod polls concurrently, each claims a disjoint set of rows,
> no leader election and no distributed lock."

Editor tab 4, `attempt/store.go:27`:

> "Idempotency is one durable layer. The executor inserts the attempt row
> before it acts, against a unique constraint on record and attempt number. A
> duplicate hits Postgres error 23505 and returns the recorded outcome
> instead of acting twice. I skipped the Redis fast path deliberately: a
> cache with a TTL cannot be a correctness guarantee, and two layers that can
> disagree are worse than one that cannot."

### 4:20 to 5:22, the live run

Dashboard, Demo Controls. Open the scenario dropdown so the presets show,
pick normal, count 50, seed 4, seed it. Then switch to the settled
100-record batch.

> "I will seed fifty failed payments from the dashboard. These presets each
> concentrate one root cause: a bank outage, salary day, dead cards.
>
> Every record carries a hidden answer key, what is really wrong with it and
> whether it is really recoverable. The decision path provably cannot read
> that table and a test enforces it, which is what makes the accuracy number
> a measurement rather than a claim.
>
> The live feed is a real WebSocket push from the gateway, relayed off a
> server-streaming gRPC call.
>
> Here is the settled hundred-record run. Seven lakh ten thousand at risk,
> two lakh seventy six thousand recovered net, for forty six rupees of spend."

### 5:22 to 6:22, the decision panel

Open your chosen record. The Why this action panel. Slow down.

> "Open any record and you see what the agent considered, not only what it
> did. Three actions, each priced. The method-update nudge wins on expected
> value. The retry is negative, because the failure code says the customer
> has to act, so retrying the same card costs money and recovers nothing.
>
> Below that is the message it sent, in Hinglish, generated for this customer
> and stored on the record.
>
> The ordering is fixed and it is the decision I would defend hardest. The
> model proposes, guardrails constrain and can only ever remove options, then
> deterministic arithmetic picks the winner. A wrong classification produces
> a wrongly priced action that is still inside every guardrail and still
> fully audited. It cannot produce an unbounded one."

Editor tab 6 for three seconds while you say priced.

### 6:22 to 6:50, when it declines to act

The Closed Uneconomic tile, then a record blocked by guardrails.

> "Forty seven of the hundred were deliberately not chased. The agent priced
> them and decided chasing cost more than it would recover.
>
> That is reported separately from escalations, because a finance lead needs
> to tell two things apart: what a human should look at, and what the agent
> decided was not worth the money."

### 6:50 to 7:30, stopping rules and compliance

Guardrail reason strings, then tab 5 (TRAI), then tab 6 (RBI), then the
System Invariants panel.

> "Retry cap, contact cap, cooldown, recovery window. Two of them come from
> real Indian regulation rather than from me. TRAI's contact-hour window, ten
> in the morning to nine at night. And RBI's twenty four hour pre-debit
> notice for e-mandates, which means a mandate retry can never be scheduled
> sooner than a day out, whatever the economics say.
>
> Both are enforced in the scheduler, in code. And an invariant checker runs
> continuously: zero stopping-rule violations, zero incomplete audit trails,
> across every batch."

### 7:30 to 8:12, failure recovery

A record's audit trail with the provider hop chips. Then editor tab 5.

> "The classifier is a chain. Groq first, then a deterministic rules engine
> that cannot fail, because it makes no network call at all.
>
> When Groq hits its rate limit, and on a hundred-record batch it does, the
> trail shows it: groq rate limited, rules ok. Every one of those records
> still got a real answer.
>
> The chain refuses to start unless the terminal rung is the rules engine.
> That check runs at boot, so a bad configuration is a failed deploy rather
> than a pipeline that stalls the first time a provider goes down."

### 8:12 to 8:58, Razorpay depth and where the instinct comes from

Tab 1, the error code list. Editor tab 7. Then tab 2, webhook payloads.

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
> consumes Razorpay's payment.downtime webhooks and holds retries back during
> a known outage, and verifies webhook signatures with HMAC-SHA256 in
> constant time."

### 8:58 to 9:15, close

The comparison, agent against naive baseline.

> "Against a naive retry-everything policy on the same sealed answer key:
> about a lakh more recovered on forty six percent less spend, at ninety one
> percent classification accuracy. Every number came through the pipeline."

---

## Part 3: the scaling question, and how to answer it honestly

You did not run a load test and you did not deploy to Kubernetes. Do not
imply otherwise. The strong answer is that you can say exactly where the
system saturates and why, which is a design answer rather than a measurement.

If asked how this scales:

> "Three different ceilings, and they are not the same one. The Classifier
> and Executor are stateless gRPC servers, so they scale horizontally with no
> coordination. The Decision Engine is capped by partition count, because a
> Kafka consumer group gives a partition to at most one consumer, so past
> replicas equal to partitions the extra pods sit idle. `raw.events` has
> twelve partitions for that reason. Inside a pod, the lever is the worker
> pool rather than more pods, because the bottleneck is waiting on an LLM
> rather than CPU. So an autoscaler belongs on Classifier and Executor, and
> putting one on the Decision Engine would show pods appearing while
> throughput stays flat."

If asked why you did not measure it:

> "I chose to spend the time on the decision layer. The numbers I would have
> got from a load test on one laptop would not have told you much about
> production anyway, and I would rather show you a correct offset-commit
> implementation than a throughput chart from a machine that is also running
> the database."

---

## Part 4: traps

Do not say mock and World Simulator in the same sentence. The simulator holds
a sealed answer key and stays permanently. The dashboard's mock mode is
development scaffolding that is off in the demo.

Do not claim load testing or Kubernetes.

Do not promise a reproducible rupee total. At risk and the baseline
reproduce. Net recovered does not.

Do not read code aloud line by line. Show the file, make the point, move on.

---

## Part 5: what a panel will ask next

Answers are in `docs/PANEL_BRIEF.md`.

Where is the AI, really? Two places, both bounded: classification and message
composition. Not action selection, not budgets.

What if the model is wrong? It is, about 9% of the time, measured against
sealed ground truth.

Why is expected value a float when every other money value is an integer?
Because an expected value is an estimate, not a balance. `CostPaise` beside
it is real money and stays an integer.

Is the demo reproducible? Inputs, the answer key and the economics reproduce
exactly. The final total does not: the TRAI guardrail is checked against a
real clock while time runs 300000 times compressed, and a share of records
get a live model call.

Two incidents worth having ready, both in `docs/INCIDENTS.md`:

The live WebSocket had never worked and three rounds of green tests said
otherwise. A polling loop kept every number moving while the socket was dead.
The tests drove a fake socket and were correct; the condition that failed in
production, a cross-origin handshake returning 403, was the one an httptest
server structurally cannot reproduce.

`DEMO_TIME_SCALE` compressed the recovery window to two seconds and escalated
73 of 100 records before economics ever priced them. The dashboard showed the
naive baseline winning by 2.8x. Total spend was Rs 11, which reads as
efficiency and actually meant the agent had barely acted. A duration compared
against elapsed wall-clock time is not the same kind of value as a duration
you wait out.

---

## What changed from the previous version

Added the diagram viewer at `diagrams/index.html`, with all 12 diagrams
exported from the repo's own Mermaid source in GitHub's theme, plus
standalone SVG and PNG. Wheel zoom, drag pan, keyboard switching.

Added a systems beat at 2:10 with three code files on screen: the keyed
worker pool and its offset-commit tracker, the `SKIP LOCKED` scheduler claim,
and insert-before-execute idempotency. Reframed the architecture beat at 0:50
around event-driven design rather than a service inventory, and moved the LLM
to one bounded component inside it.

Added Part 3, the scaling question, so the missing load test and Kubernetes
work become a design answer instead of a gap.

Added an editor tab table with seven file and line references, opened in
advance.

Runtime moved from 7:15 to about 7:35.
