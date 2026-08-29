# `configs/`: the numbers the agent spends money against

This directory holds the two checked-in tables that the Decision Engine's
economics scorer reads, plus the environment profiles. It is referenced six
times from inside those YAML files and did not exist until 2026-08-29, so
this file is written to answer exactly the questions those references point
at, and nothing else.

| File | What it is |
|---|---|
| `intervention_costs.yaml` | What each action costs us, per `ActionType` and per channel |
| `recovery_priors.yaml` | How much each action *improves* the odds of recovery, per bucket and attempt ordinal |
| `demo.env`, `dev.env` | Environment profiles, individually named knobs, no `MODE` enum (`docs/DECISIONS.md` 2026-08-28) |

The formula both feed is `docs/ARCHITECTURE.md` §5a:

```
EV(action) = P(recovery | action, root_cause, attempt_no) * amount_at_risk
             - direct_cost(action)
             - indirect_cost(action)
```

Read `docs/PRD.md` §2b for why this exists at all. The short version: an
agent that recovers revenue at a loss is worse than no agent, so the decision
to spend is priced before it is taken, and a record with no positive-EV action
terminates as `ClosedUneconomic` rather than being chased.

## Why costs and priors are two files, not one

Referenced from the header of both YAML files.

They are two files because they are two different *kinds* of claim, with
different owners, different evidence, and different failure modes when wrong.

**Costs are prices.** They are externally verifiable, mostly published, and
have a right answer that exists independently of us: Meta's WhatsApp Utility
rate card, MSG91's SMS pricing, NPCI's NACH switching fee. When one is wrong
it is wrong the way a typo is wrong, and the fix is to go and read the rate
card. They also have a second consumer, `services/executor/internal/ports/
cost.go`, which logs what an attempt *actually* cost, and a drift test binds
the two together.

**Priors are beliefs.** No published source gives P(recovery | WhatsApp nudge,
HARD_DECLINE, second attempt) for an Indian merchant, because it depends on the
merchant, the customer base and the month. They are reasoned estimates with a
documented derivation, and when one is wrong it is wrong the way a hypothesis
is wrong. The honest fix is not to look it up, it is to run an experiment.

Collapsing them into one file would blur that, and the blur is not cosmetic: a
reader would have no way to tell which numbers they can check against a URL
and which ones are our judgement. Keeping them apart is what makes the
`[SOURCED]` / `[ASSUMPTION]` / `[UNVERIFIED]` tagging meaningful. It also keeps
the two review paths separate, since arguing about a price and arguing about a
probability are different conversations with different people.

A third reason, more practical: the priors file is the one that should
eventually be *replaced by measurement* rather than edited. Costs will stay
hand-maintained more or less forever.

## The MDR gap

Referenced from `intervention_costs.yaml`'s `informational_not_in_formula`
block. **This is the largest known inaccuracy in the headline metric and it is
better stated than discovered.**

`docs/PRD.md` §9 defines net recovered as gross recovered minus logged
intervention spend, and intervention spend today means messaging and switching
fees: tens of paise per action. But the single largest real cost of recovering
a payment is Razorpay's own success fee (MDR), roughly 2.36% for domestic
cards and 3.42% for recurring, charged *on the recovered amount*. On a ₹2,000
recovery that is around ₹47, against messaging costs measured in fractions of
a rupee.

So the reported "net recovered" **overstates true net recovery by roughly two
orders of magnitude relative to the messaging costs it does subtract.**

It is excluded rather than hidden, and the exclusion is structural rather than
lazy: the §5a formula subtracts costs that are *unconditional and flat*, while
MDR is *conditional on success* and *proportional to amount*. Adding it is a
formula change, not a constant, and it changes what EV means:

```
EV(action) = p * (amount * (1 - mdr_rate)) - direct - indirect
```

That is the recommended fix. It is not a large change and it makes every
positive-EV decision slightly more conservative, which is the correct
direction for a spend authorisation. It was not made because it landed
mid-phase and changing the EV formula invalidates the persisted
`ev_score_at_decision` snapshots that Phase 2 wrote and that Reporting will
read.

**Whoever picks this up**: the rates are already in the YAML as
`domestic_card_success_fee_bps: 236` and `recurring_card_success_fee_bps: 342`.
Note that a *failed* retry incurs no MDR, since Razorpay charges on success
only, which is what makes the term belong inside the `p *` product rather than
alongside the flat costs.

## The escalation cost sensitivity

Referenced from `intervention_costs.yaml` around the `ESCALATE` action.

`ESCALATE` is priced at 1800 paise direct, modelled as human handling time.
That number is an `[ASSUMPTION]` and it is the single most load-bearing
assumption in the table, because escalation is the fallback for every path
that runs out of options. Price it too low and the scorer escalates records it
should have closed as uneconomic. Price it too high and escalation stops being
selectable at all, which matters because escalation is also the *safety*
outcome for `RISK_HOLD` and low-confidence classifications.

The mitigation already in the design is that **escalation deliberately bypasses
economics** (`docs/PLAN.md` Phase 2, `docs/DECISIONS.md`): a risk hold is a
safety call, and pricing it would imply it were negotiable. So the sensitivity
is bounded to the case where the scorer selects `ESCALATE` on EV grounds, not
to the safety paths.

Not resolved. Worth a sensitivity check (vary it 900 to 3600 and see whether
the batch's action mix moves) before anyone quotes escalation counts as
evidence of anything.

## The indirect-cost signature change

Referenced from `intervention_costs.yaml` around the retry indirect cost.

`indirect_cost(action)` takes only an action, so it cannot express that the
fourth re-presentment damages issuer standing far more than the first. Today
that escalation is absorbed by the priors decaying with attempt ordinal
instead. **Those are not equivalent**, and the file says so: decaying the
benefit and escalating the cost produce different EV curves, and only the
second one can make a late attempt *negative* rather than merely
unattractive.

Recommended change is to widen the signature to
`indirect_cost(action, attempt_no)`. Small, and deliberately not made
mid-phase.

## Calibration, and why the priors are not calibrated

Referenced from `recovery_priors.yaml`.

The priors are `[ASSUMPTION]` on level and, where sourced, `[SOURCED]` only on
*shape*. They have never been checked against outcomes, and the file is
explicit that they cannot be honestly calibrated as things stand.

The blocker is methodological, not effort: calibration needs a randomised
holdout arm where some eligible records deliberately get `NONE`, so the
observed recovery rate of the untreated group gives the passive baseline the
lift values are measured against. Nothing in the design does that today, and
without it, measuring the priors against outcomes generated by a simulator
seeded from a related set of assumptions would measure our own consistency
rather than our accuracy.

Note the deliberate firewall this protects: the World Simulator's ground-truth
probabilities are *not* copied from this table (`scripts/batchgen/profile.go`
sets its own), specifically so classification accuracy is a real measurement
and not the agent grading its own exam by proxy.

`docs/PHASE5_IMPLEMENTATION.md` Unit K takes the defensible part of this value
(comparing against a naive baseline policy over the same ground truth) without
claiming calibration it cannot support.

## House rules for editing anything here

1. **Every number carries a provenance tag**, `[SOURCED]` with a URL or
   citation inline, `[ASSUMPTION]` with the derivation shown, or
   `[UNVERIFIED]` meaning believed roughly right with no citation obtained.
   A number without one is a bug.
2. **Integer paise and integer basis points, never floats.**
   `docs/ENGINEERING.md` §8. The EV form is
   `(p_bps * amount_paise) / 10000 - direct - indirect`, truncating, which
   rounds against us. That is the safe direction for a spend authorisation.
3. **If you change a cost that `cost.go` also holds, the drift test will fail.**
   That is the test doing its job. Update both, in the same commit.
4. **Do not tune these to make a demo look better.** The whole argument for
   the economics layer is that the numbers are arguable input by input. A
   figure edited to improve a headline is worth less than no figure at all.
