# Panel brief: how to explain Momotaro

Study material for the demo video and the technical panel. Everything here is
verified against the code, not written from memory. Where a number is quoted,
the file it came from is named so you can pull it up live if challenged.

**Track 03 asks for**: an agent that detects revenue at risk, determines the
right intervention, and executes a bounded recovery workflow, demonstrating
measured money recovered across a batch, with compliant escalation, stopping
rules, and an audit trail. Every section below maps to a phrase in that
sentence.

---

## 1. The one-paragraph answer

A payment fails. Momotaro classifies why it failed, prices every action it is
allowed to take, picks the one with the highest positive expected value, does
it, watches what happens, and re-prices. When no permitted action is worth
more than it costs, it stops and says so. Every step is written to an audit
trail that records not just what it did but what else it considered and why
those lost.

The part worth emphasising: **it is allowed to do nothing.** Roughly a third
of records in a typical run end `CLOSED_UNECONOMIC`. That is the product
working, not failing.

---

## 2. The formula, exactly

From `configs/recovery_priors.yaml` and `configs/intervention_costs.yaml`,
both of which state it identically:

```
EV(action) = P(recovery | action, root_cause, attempt_no) * amount_at_risk
             - direct_cost(action)
             - indirect_cost(action)
```

In integer basis points, which is how the config is authored:

```
ev_paise = (p_bps * amount_paise) / 10000 - direct - indirect
```

In code, `services/decision-engine/internal/economics/score.go`:

```go
p := float64(lift) / bpsPerUnit
EVPaise: p*float64(amountPaise) - float64(total)
```

### Three definitions that are load bearing

The priors file warns about these directly, saying getting any one wrong
"makes every EV in the system wrong in a way that will not look wrong."

**1. Units are integer basis points.** 10000 bps = 1.0 = certainty. 100 bps =
1%. Chosen so the money path never touches a float. Overflow headroom is
ample: a one crore record is 1e9 paise, times 10000 is 1e13, against an int64
ceiling near 9.2e18.

**2. The priors are INCREMENTAL LIFT, not absolute recovery rates.** This is
the subtle one and it is the question a sharp panelist asks. Each value is
the *increase* in probability that the record ends up paid, caused by taking
that action, over doing nothing. Two consequences fall out of it:

- `EV(do nothing) = 0` by construction, so it is a real baseline rather than
  a special case.
- `CLOSED_UNECONOMIC` is reachable at all. If priors were absolute recovery
  rates, almost every action would look worth taking and the agent would
  never stop.

**3. Truncating division, deliberately.** The sub-paise loss is deterministic
and rounds *against* us, which is the safe direction for authorising spend.

### One honest exception

`EVPaise` is a `float64` while every other money value in the codebase is
integer paise. That is deliberate and documented in `score.go`: an expected
value is not money anyone holds, it is a probability-weighted estimate, and
`INTERVENTION_ATTEMPT` stores it as `DOUBLE PRECISION` for the same reason.
`CostPaise` beside it is real money and stays an integer.

If asked "why is your money a float", that is the answer, and the
distinction between an estimate and a balance is worth making explicitly.

---

## 3. The decision flow, and why the order matters

```
   model proposes  ->  guardrails constrain  ->  economics decides
   (may be an LLM)     (can only REMOVE)         (deterministic)
```

This ordering is fixed and it is the most defensible design choice in the
project. Say it plainly:

**The LLM never authorises spending money.** It classifies a failure into a
root cause bucket and it drafts customer-facing wording. It cannot choose an
action, cannot raise a budget, and cannot remove a guardrail. Guardrails can
only ever *subtract* options from the permitted set. Whatever survives is
priced by deterministic arithmetic, and the highest positive EV wins.

That is the answer to "what if the model hallucinates". A hallucinated
classification produces a wrong *price*, which produces a wrong but still
bounded and still auditable action. It cannot produce an unbounded one.

`ScoreAll` returns every permitted candidate and `Best` is built on top of it
rather than being a second loop, specifically so the ranking shown in the UI
and the winner actually chosen can never drift apart.

Selection rule, `economics.BestOf`: skip any candidate with `EVPaise <= 0`,
then take the maximum. If nothing survives, the record closes uneconomic.

---

## 4. The guardrails, which are the stopping rules

From `services/decision-engine/internal/engine/guardrails.go`. These are what
Track 03 means by "bounded" and "compliant". Each produces a human-readable
reason string that ends up on screen:

| Guardrail | Reason string it emits |
|---|---|
| Retry budget | `retry budget exhausted: 3 of 3 attempts used` |
| Contact cap | `contact cap reached: 3 of 3 contacts used` |
| Contact cooldown | `contact cooldown active: last contact N ago, cooldown is N` |
| Recovery window | window closed, record escalates rather than being chased forever |
| TRAI contact hours | `schedule.go`, TCCCPR 2018 promotional window `[10:00, 21:00)` IST |

The TRAI one is worth calling out unprompted. It is a real Indian regulation,
not an invented constraint, and it is enforced in the scheduler rather than
being a comment in a design doc.

RBI's 24 hour pre-debit notification for e-mandates is handled the same way.

---

## 5. How to read the "Why this action" panel

This is the centrepiece. It is the thing to slow down on.

A real record, ₹9,902.36 at risk:

```
Nudge Scheduled     Why this action              at risk ₹9902.36
                  ✓ Nudge (Update Method)  +₹247.27  p 3%  cost ₹0.29
                    Nudge (Reminder)        +₹59.06  p 1%  cost ₹0.35
                    Retry                    -₹6.25  p 0%  cost ₹6.25

                    "Aapke Rs 9902.36 ke payment ko poora karne ke liye
                     thoda action chahiye..."

Closed (Uneconomic) Why this action              at risk ₹9902.36
                    Retry                    -₹6.25  p 0%  cost ₹6.25
                    Blocked by guardrails
                    Nudge (Update...)  contact cap reached: 3 of 3 contacts used
                    Nudge (Reminder)   contact cap reached: 3 of 3 contacts used
```

**The narration**, in order:

1. Three options priced. Method-update nudge wins on expected value, not on a
   rule someone wrote.
2. Retry is negative here. The failure code says the customer must act, so
   retrying the same card costs ₹6.25 and gains nothing. The agent knows that.
3. It sends a real Hinglish message, and that exact text is on the trail.
4. Three attempts later it has hit its own contact cap. Both nudges are now
   blocked, and the reason is stated in the customer's terms, not a code.
5. The only action left is negative EV. It stops, on ₹9,902 at risk, rather
   than spending money it will not get back.

**Point at the EV decay across attempts**: `+₹247.27`, then `+₹197.70`, then
`+₹59.06`. The agent is losing confidence as attempts fail. That is the prior
table's per-attempt depth doing its job, and it is the difference between an
agent and a retry script.

`attemptNo` is per action, not per record: a record on its third retry may be
on its first nudge, because the prior table asks about each action's own
depth separately.

---

## 6. How we prove it works, not just that it ran

**The answer key is sealed.** Every generated record has a hidden
`GROUND_TRUTH` row: its true root cause, its true recovery probability, its
response delay. The decision path provably cannot read that table, and there
is a test that enforces it, `test/integrity/ground_truth_isolation_test.go`.

Say this before anyone asks, because "how do we know your accuracy number is
not circular" is the obvious challenge and the answer is already built.

**We measure against a naive baseline**, `naive_retry3_nudge1`: retry every
record up to three times, nudge every record once, no economics. Evaluated
analytically against the same sealed ground truth.

A representative seeded run:

| | Momotaro | Naive baseline |
|---|---|---|
| Net recovered | ₹6.0L | ₹4.0L |
| Spend | ₹38 | ₹65 |
| Classification accuracy | ~91% vs ground truth | n/a |

More recovered, for less spend. The naive figure is deterministic and should
reproduce exactly; if it moves, something real changed.

**System invariants** run continuously and are on screen: zero stopping-rule
violations, zero incomplete audit trails, zero impossible transitions.

---

## 7. Questions to expect, and honest answers

**"Where is the AI, really?"**
Two places, both bounded. Classification of a failure into a root cause
bucket, and composition of the customer message. Not action selection, not
budgets. That is a deliberate design choice, not a limitation: money
decisions are deterministic and reproducible, and the model is used where
language and ambiguity actually live.

**"What if the LLM is wrong?"**
It is, about 9% of the time, and we measure that against sealed ground truth
rather than guessing. A wrong bucket produces a wrongly priced action that is
still inside every guardrail and still fully audited. There is also a
provider chain: Groq first, then a deterministic rules engine that cannot
fail. The audit trail records which rung answered, so `groq:rate_limited,
rules:ok` is visible rather than hidden.

**"What if the LLM writes something inappropriate to a customer?"**
A validator rejects any composed message containing internal vocabulary and
falls back to a static template. The forbidden list is derived from the
generated proto enums, so a new enum value is covered automatically. This
exists because it actually happened: two messages went out saying "because
your bucket is overdue". The prompt now forbids it, but the validator is the
control, because a prompt instruction is not a guarantee.

**"Is your demo reproducible?"**
Partly, and be precise here rather than overclaiming. Inputs, the sealed
answer key and the economics reproduce exactly from a seed: zero differences
across two same-seed runs. The final rupee total does not, for two reasons we
can name. The TRAI contact-hour guardrail is evaluated against a real clock
while the demo compresses time 300000x, so sub-second scheduler jitter moves
simulated time across the 21:00 boundary. And 15% of records get a live LLM
call whose answers vary. Nine records in a hundred end differently. It is
written up in `docs/INCIDENTS.md` 2026-09-02.

**"How do you know the audit trail is complete?"**
An invariant checks it continuously, and it is on the dashboard. Zero
incomplete trails across the batch.

**"Why Postgres as the source of truth and Kafka only for events?"**
Because money state needs transactions and a single writer, and events need
fan-out. Mixing them means reconstructing balances from a log.

---

## 8. Weak spots to know before someone finds them

Be ready to name these first. Volunteering a known limitation reads as
confidence; being caught by one does not.

- **Reproducibility is partial**, as above.
- **`web/` had no CI until 2026-09-02.** A pagination bug and a dead
  WebSocket both shipped because of it. Now it runs lint, typecheck, tests
  and build.
- **The live WebSocket never worked in dev until 2026-09-02**, rejected 403
  cross-origin, and a polling loop hid it. The lesson is in
  `docs/INCIDENTS.md`: redundancy masked the failure of the path it backed up.
- **Load testing and Kubernetes were deliberately skipped.** Say "we chose to
  spend the time on the decision layer" rather than pretending they exist.

---

## 9. Files to have open during the panel

| Question | File |
|---|---|
| The formula and the three definitions | `configs/recovery_priors.yaml` |
| The costs, each with a provenance tag | `configs/intervention_costs.yaml` |
| EV computation and the winner rule | `services/decision-engine/internal/economics/score.go` |
| Stopping rules and their reason strings | `services/decision-engine/internal/engine/guardrails.go` |
| TRAI contact hours | `services/decision-engine/internal/engine/schedule.go` |
| Proof the answer key is sealed | `test/integrity/ground_truth_isolation_test.go` |
| The external contract | `docs/API_GATEWAY.md` |
| What broke and what we learned | `docs/INCIDENTS.md` |

Every cost number in `intervention_costs.yaml` carries a provenance tag:
`[SOURCED]` with a URL, `[ASSUMPTION]` with its derivation shown, or
`[UNVERIFIED]`. If someone challenges a number, open the file. The reasoning
is already written next to it.
