# Phase 3 Implementation Plan: The Reasoning Layer

This is the working breakdown of `docs/PLAN.md` Phase 3. `PLAN.md` stays the
one-line checklist; this file explains what each item actually is, what the
checklist gets wrong, and an order it can be built in.

Same contract as `docs/PHASE2_IMPLEMENTATION.md`, which is the template for
this file. Every unit below is independently completable, names its
dependencies, the exact files it owns, its branch, and how to prove it works.
Read the **Collision notes** before running two units at once.

**Phase goal in one sentence**: put a real model in the diagnosis path, and
make its failures cost nothing.

Phase 1 proved a record can flow. Phase 2 made it flow only when worth it and
correctly when things break. Phase 3 is the first phase that adds an
*external dependency that will fail*, so most of the work here is not "call
the model", it is "the model going down changes nothing measurable".

**Where this sits**: Phases 0, 1 and 2 are complete and merged (13 of 13 Phase
2 units). Phases 4 through 8 are untouched. Nothing in Phase 3 is blocked by
anything outside it.

---

## Provider decision: resolved

`AGENTS.md` "Locked decisions", `ARCHITECTURE.md` section 5 and `PRD.md`
section 13 all recorded the LLM provider as deliberately undecided. It is
decided now. Full reasoning and the sourced numbers are in `docs/DECISIONS.md`
2026-08-28. The operative summary:

**`LLM_PROVIDER_CHAIN=groq,gemini,rules`**, with Groq running
`openai/gpt-oss-20b` at `reasoning_effort: low`.

| Rung | Role | Why this one |
|---|---|---|
| `groq` | primary | The only option evaluated that gives **guaranteed constrained decoding** (`json_schema` with `strict: true`, supported on `gpt-oss-20b`, `gpt-oss-120b` and `Qwen 3.8 27B` only, best-effort on everything else). Fastest output measured, roughly 1000 tok/s, which matters because Unit A must fit every rung inside a 5s `CALL_TIMEOUT`. OpenAI-compatible wire format, so `llm/client.go` is a standard chat-completions client. |
| `gemini` | automatic failover | Free tier, native `responseSchema` with `enum`. Buys quality continuity when Groq rate-limits or goes down. It is **not** what makes the system safe: `rules` is already a rung that cannot fail. |
| `rules` | terminal rung | Cannot fail, so the chain always terminates in a valid answer. Unit A makes that structural rather than conventional. |

Three findings from the evaluation are binding on the units below.

1. **Do not use Gemini's OpenAI-compatibility endpoint.** It exists, but its
   tool calling does not follow the OpenAI schema, so the one thing the
   fallback rung must do reliably is the one thing that layer does badly. Use
   native `generateContent` with `responseSchema`.
2. **Gemini does not guarantee schema compliance**, and its own documentation
   says to validate in the application. That is fine and already handled:
   `provider/validate.go` is exactly that validator and `SPEC.md` section 4.7
   wrote it for exactly this case. A non-compliant Gemini answer becomes a
   `schema_invalid` hop and falls through to rules.
3. **`LLM_TIMEOUT=2s` is a placeholder and must become a measured number.**
   GPT-OSS models are reasoning models. Artificial Analysis measures
   `gpt-oss-20b` (high reasoning) at **3.05s time to first token on Groq**,
   and Groq is the fastest provider for that model. That one number exceeds
   both the current `LLM_TIMEOUT` and `PRD.md` section 10's 3s p95 target
   before any network time is added. `reasoning_effort: low` is the fix, and
   is the right setting for a seven-way classification regardless, but the
   timeout has to be set from measurement rather than from the value sitting
   in `.env.example` today. Units A and B both carry this in their definition
   of done.

### Free-tier limits, and what they do to the demo

| Provider and model | RPM | Requests/day |
|---|---|---|
| Groq `gpt-oss-20b` / `gpt-oss-120b` | 30 | 1,000 |
| Groq `llama-3.1-8b-instant` (best-effort schema only) | 30 | 14,400 |
| Gemini 3 Flash | 10 | 1,500 |
| Gemini 2.5 Flash-Lite | 15 | 1,000 |

Groq's limits are **organization-level, not per-key**, so extra keys do not
multiply quota.

`PRD.md` section 12 calls for a demo batch of 50 to 100 records. At 30 RPM a
100-record batch cannot be classified live: the first thirty or so succeed and
the rest are rate limited, so the demo self-degrades to rules in an
uncontrolled way and the record a judge drills into may carry no model
rationale at all.

That is not something to engineer around, it is a knob to set. Unit H adds
`LLM_SAMPLE_RATE` so a demo runs at full record volume with a bounded number
of live calls. See Unit H for the arithmetic, and for how the same knob stages
the failover beat deliberately instead of by accident.

**Operational rule that follows from 1,000 requests per day**: the default
`LLM_PROVIDER_CHAIN` in `.env.example` and in `test/e2e`'s harness stays
`rules`. The live chain is opt-in through `configs/demo.env` (Unit H). One
accidental e2e loop against a live chain burns the day's quota, and it will
happen at the worst possible moment.

---

## Flaws in `PLAN.md` Phase 3, found before starting

The checklist is four lines. Checked against the code, one of the four is
already done, one is described in a way that cannot be tested without
flakiness, and the two most important pieces of work are not in the list at
all. Each flaw below names the evidence.

### Flaw 1: "Rationale stored and retrievable from the audit trail" is already done, and the thing that is actually missing is `hops`

`PLAN.md` Phase 3's fourth item asks for the rationale to be stored and
retrievable. It already is, and has been since Phase 1:

- `services/decision-engine/internal/engine/store.go:67` inserts `rationale`
  into `audit_entry` on every step it writes.
- `services/audit/internal/server/store.go:72` selects it back, and
  `GetRecordAudit` returns it on `AuditEntry.rationale`.
- `services/audit/internal/server/get_record_audit_test.go:80` and the e2e
  smoke test both assert on it.

What is genuinely missing is `ClassifyResponse.hops`. The chain computes it
(`provider/chain.go:74`), the proto carries it, and then it is thrown away:
grepping `services/` for `Hops` outside `services/classifier/` returns
nothing. There is no column for it, `audit.proto`'s `AuditEntry` has no field
for it, and the Decision Engine never reads it off the response.

That matters more once a real provider exists, because
`sourceFor` (`provider/chain.go:95`) only distinguishes rules from LLM. When
groq times out and gemini answers, `source` is `SOURCE_LLM`, exactly the
same value as when groq answers on the first try. So after Phase 3 the audit
trail will be structurally unable to show that a fallback happened, which is
the one thing `PRD.md` section 12 promises to demonstrate ("one record where
the LLM call failed and fell back to rules").

**Effect on the plan**: item 4 is replaced by Unit E, persisting hops, which
needs a migration and a proto change and is therefore the unit with the most
serialised PR overhead. It should start first, not last.

### Flaw 2: nothing enforces that the chain ends in a rung that cannot fail

The whole resilience claim rests on the rules engine being last.
`provider/chain.go:26`'s `NewChain` requires at least one name and rejects
unknown names. It does not require `rules` to be present, let alone last.

So `LLM_PROVIDER_CHAIN=groq` starts the pod cleanly, and then every
classification that fails returns `no rung produced a valid response`
(`chain.go:80`). The Decision Engine retries `Classify` three times
(`engine.go:32`, `maxClassifyAttempts = 3`) and dead-letters the record. A
perfectly classifiable record goes to the DLQ because of a config typo. That
is the exact outcome `services/classifier/SPEC.md` section 5 spends a page
warning against.

`force_rules_only` makes it worse: `onlyRulesRung` (`chain.go:83`) filters for
a rung that may not exist, returns an empty slice, and the loop falls straight
to the same error. The load generator's cost-safety switch would dead-letter
every record it touched.

Today this is invisible because the default chain is `rules` and there is no
other rung to misconfigure. Phase 3 is the moment it becomes reachable.

### Flaw 3: no rung has a timeout, and the per-rung budget is never reconciled with the caller's deadline

`chain.Classify` passes the inbound `ctx` straight to every rung
(`chain.go:53`). `LLM_TIMEOUT` is loaded in `classifier/cmd/main.go:55` and
**never used by anything**. So a hanging provider hangs until the caller's
deadline expires, and the rules rung never runs at all.

Even with a per-rung timeout added naively, the arithmetic does not work.
`CALL_TIMEOUT` defaults to 5s (`decision-engine/cmd/main.go:94`) and is what
bounds the inbound `Classify` call. `LLM_TIMEOUT` defaults to 2s. A
`groq,gemini,rules` chain that fully times out burns 4s of a 5s budget
before the rung that always answers gets a turn, and the response then has to
marshal and travel back inside the remaining 1s. Add a third LLM rung, or
tighten `CALL_TIMEOUT`, and the classifier produces a correct answer that
arrives after its caller has given up: `DeadlineExceeded`, three retries, DLQ.
The fallback chain, whose entire purpose is to stop a provider failure from
losing a record, becomes the mechanism that loses it.

Separately, `PRD.md` section 10 sets p95 under 3s on the LLM path. A 2s + 2s
fallback already exceeds that before any real latency is added.

**Effect on the plan**: the chain needs deadline *budgeting*, not a per-rung
timeout constant. Unit A.

### Flaw 4: `ProviderHop.result` has a documented vocabulary the code cannot produce

`common.proto:128-131` documents the result field as
`"ok"`, `"timeout"`, `"schema_invalid"`, `"circuit_open"`. `chain.go` emits
three bare string literals: `"ok"`, `"error"`, `"schema_invalid"`. So:

- a timeout is currently indistinguishable from a 500 in the trail, and
  `PLAN.md`'s item 2 explicitly asks to "simulate timeout/error per provider";
- `"circuit_open"` has no producer at all, and item 3 is the unit that needs
  it;
- the vocabulary lives as literals at three call sites rather than as a closed
  set, so nothing stops a fourth spelling appearing.

### Flaw 5: an LLM rung would receive exactly the same two inputs as the lookup table it is supposed to improve on

This is the most consequential flaw, because it makes the headline item
decorative rather than wrong.

`clients.classify` (`decision-engine/internal/engine/clients.go:23`) builds
the request as `&classifierv1.ClassifyRequest{Record: record}` and nothing
else. `history` and `instrument_history` are always empty, which
`services/classifier/SPEC.md` section 3 already documented in Phase 1 and
raised as a cross-service item in its section 10. Nobody picked it up.

So a model rung would see `failure_code` and `type`, which is precisely what
`rules.Provider.Classify` (`rules/rules.go:34`) reads. Same inputs, same
closed output vocabulary of seven buckets and four actions. A model given
identical inputs to a lookup table cannot add information the table does not
already encode. It can only add latency, cost and variance, and then a judge
asks what the model is for and there is no answer.

The reasoning layer earns its place only when it sees something the table
cannot: what has already been tried on this record, and what has happened on
this same instrument across other records. Both fields exist in
`classifier.proto`'s `ClassifyRequest` and are documented for exactly this
("so the model can reason about what has already been tried rather than
starting cold"; "signal for distinguishing this rail is flaky right now from
this card is dead").

**Effect on the plan**: populating those two fields becomes its own unit
(Unit F), it is not optional, and it should land before or with the provider
rung.

### Flaw 6: item 3's test as worded is a wall-clock assertion, which is this repo's most reliable source of flakes

`PLAN.md` asks for "a test proving that a sustained provider outage does not
make every record pay the full timeout". The literal reading is: time N
records with the provider down and assert the total is small. That is a
latency assertion, and `docs/INCIDENTS.md` has entries on 2026-08-23
(1-in-4 flaky test), 2026-08-25 (unresolved kafkax flake) and 2026-08-26
(Unit L catching nothing until the racer count went to 25) about exactly this
class of test.

The provable version asserts **call counts** on a fake provider plus the hop
result, with a `clock.Fake` driving the cooldown: after the breaker opens the
wrapped provider is not invoked at all, and every skipped request records
`circuit_open`. Wall-clock numbers get reported in the PR for the demo
narrative, never asserted.

### Flaw 7: the plan never says how any of this is tested without a live API key, and CI has neither a key nor guaranteed egress

`.github/workflows/ci.yml`'s `build-test` job runs `go test -race ./...` with
no build tags, no secrets and no services. Every unit here has to be fully
testable in that job. That is a design constraint on Unit B, not an
afterthought:

- the provider rung's HTTP base URL must come from config, so every test can
  point at an `httptest.Server` and still exercise the real HTTP client, real
  JSON decoding and real context cancellation;
- the default `LLM_PROVIDER_CHAIN` must stay `rules`, so all six existing e2e
  tests keep passing untouched and at zero cost;
- a real-key run is a manual, documented step with output pasted into the PR,
  never a test.

Use `httptest.NewServer` rather than the harness's `freePort()` helper for
anything new that needs a port. `freePort()` has a documented reuse race
(`INCIDENTS.md` 2026-08-23, and again 2026-08-25 when two services were handed
the same port); `httptest` holds its listener, so the race does not exist.

### Flaw 8: two files already contain comments describing a confidence threshold that no code implements

`classifier.proto` documents `ClassifyResponse.confidence` as "Below the
configured threshold the record is escalated rather than acted on".
`ARCHITECTURE.md` section 5 puts that decision in the Decision Engine. And the
Decision Engine says so twice, in comments:

- `engine/state.go:128-131`, on `directPath`: "a risk hold or a low-confidence
  classification is a safety decision, and pricing it would imply it were
  negotiable."
- `engine/engine.go:166-169`, in `decide`: the same sentence again.

No code reads `resp.GetConfidence()` anywhere in `services/decision-engine/`.
Today that is harmless, because confidence is a constant from a table
(`rules/actions.go`), so the only low-confidence records are the ones the
rules engine already recommends escalating. The moment a model produces the
number it becomes a live safety signal with nothing acting on it, and two
comments in the repo become false.

`services/classifier/SPEC.md` section 10 item 2 raised this during Phase 1 and
recommended it as Decision Engine work. It belongs in Phase 3 because it is
only meaningful once a model, not a lookup table, produces the number.

### Flaw 9: the four items cannot be built in the order they are listed

Items 2 and 3 both need item 1's real rung to exist. Item 4's real content
(Flaw 1) is a migration plus a proto change plus two services, is completely
independent of any provider, and carries three serialised PRs. Listing it last
serialises the phase for no reason. Corrected order is in the dependency graph
below.

### Flaw 10: prompt injection and cost exposure are not mentioned at all

Two new risks arrive with the first real provider call, and neither appears in
the checklist:

- **Injection.** `failure_code` and `instrument_ref` are strings from an
  upstream rail, and Phase 3 interpolates them into a prompt. The *decision*
  fields are safe: they are enum-validated twice (by the vendor's structured
  output and then by `provider/validate.go`, which rejects any bucket, action
  or confidence outside range). The residual surface is `rationale`, the only
  freeform field, which is stored verbatim in `audit_entry.rationale` and will
  be rendered on the Phase 5 dashboard. It needs a length cap and control
  character stripping, and the dashboard must render it as text.
- **Cost.** `force_rules_only` exists and is tested, and nothing sets it
  (`SPEC.md` section 3: it is always false). So the only thing standing between
  an accidental load run and a real bill is `LLM_PROVIDER_CHAIN` defaulting to
  `rules`. That default must be preserved deliberately, and a non-rules chain
  must announce itself loudly at startup.

### Not a flaw, but a deferral to state out loud

`ENGINEERING.md` section 11 item 3 asks for "the relevant metric exported".
Every phase so far has deferred that half to Phase 4, and `ARCHITECTURE.md`
section 13 is explicit that metrics arrive via a shared gRPC interceptor
rather than per-service hand-wiring. Phase 3's deferral costs more than the
previous ones, because `ARCHITECTURE.md` section 5 names `llm_circuit_state`
by hand and the circuit breaker's entire value proposition is
observability-shaped ("a barely visible dip").

**Recommendation**: keep the deferral, for consistency and because
hand-rolling one exporter in the classifier is the thing section 13 forbids.
Compensate with structured logs good enough to demo from: a `Warn` on every
breaker state change naming the provider, the old state, the new state and the
consecutive failure count. Say so in each PR rather than quietly ticking the
box.

---

## Status at a glance

| Unit | What | Status | Blocks |
|---|---|---|---|
| A | Chain hardening: terminal rung, deadline budget, hop vocabulary | **merged** | B, C, D |
| B | The real provider rung | **merged** | C, D, G |
| C | Fallback path proven per failure mode | not started | nothing |
| D | Circuit breaker per provider | not started | nothing |
| E | Provider hops persisted and retrievable | not started | nothing |
| F | Populate `history` and `instrument_history` | not started | nothing (but B is decorative without it) |
| G | Confidence threshold enforced in the Decision Engine | not started | nothing |
| H | `LLM_SAMPLE_RATE` and the config profiles | not started | nothing |

**2 of 8 merged.**

Mapping back to `PLAN.md`: A and B are the "providers decided and wired"
checkbox. C is "fallback path deliberately tested". D is "circuit breaker per
provider". E replaces "rationale stored and retrievable" with the part that is
actually missing (Flaw 1). F, G and H are additions the checklist does not
contain: F and G are justified by Flaws 5 and 8, and H is what makes a
50-to-100-record demo possible against a 30 RPM free tier.

---

## Dependency graph

```
  E (3 stacked PRs: migration -> proto -> code)  independent, START FIRST
  A (chain hardening)                            independent, start now
  F (classify history)                           independent, start now
  H (sample rate + profiles)                     independent of every unit,
                                                 but collides with F on files
                                                       |
                    A ──> B (real provider rung) <─────┘  soft: B works without F,
                              │                             but is decorative
                              ├──> C (fallback proven)
                              ├──> D (circuit breaker)
                              └──> G (confidence threshold)
```

**The critical path is A -> B.** Nothing else sits on it.

H blocks nothing in code but blocks the *demo*: without it a 100-record batch
cannot be shown against a 30 RPM free tier. Treat it as required for Phase 3
to be demonstrable, not as an optional extra.

E should start first despite blocking nothing, because it is three PRs that
must merge in sequence (a migration is its own PR per `ARCHITECTURE.md`
section 12a; a proto change is its own PR merged before dependent code per
section 9), so it has the longest wall-clock floor of any unit here.

---

## Unit A: Chain hardening

**Status**: merged.
**Depends on**: nothing.
**Branch**: `svc/classifier/chain-hardening`.
**Files owned**: `services/classifier/internal/provider/chain.go`,
`provider.go`, a new `budget.go`, `chain_test.go`;
`services/classifier/cmd/main.go`; `.env.example`.

### What it is

Three defects in one file, so one unit rather than three that would all
collide on `chain.go`. Fixes Flaws 2, 3 and 4.

### Why it matters

All three are currently unreachable, because the chain has exactly one rung
and it cannot fail. Unit B makes all three reachable in the same commit. Doing
this first means B is a feature addition rather than a feature addition plus
three latent DLQ paths.

### LLD

**A1, the terminal-rung invariant.** `NewChain` gains four checks, all at
construction time so a config mistake stops the pod rather than degrading
every classification (`ENGINEERING.md` section 5):

- the last name must be `RulesName`;
- `RulesName` must appear exactly once;
- no name may contain `:` or `,`. This looks arbitrary and is not: Unit E
  encodes hops into a single delimited column, and provider names come from
  config. Rejecting the delimiters here is cheaper than escaping them there.
  If A merges before E is written, E picks this up as a one-line follow-up.

`onlyRulesRung` then cannot return an empty slice, but keep a defensive check
that returns a named error rather than falling through to the generic
"no rung produced a valid response".

**A2, the deadline budget.** New `budget.go`:

```go
// rungCtx returns the context one rung gets, or ok=false when there is not
// enough of the caller's deadline left to be worth spending on it.
func rungCtx(ctx context.Context, perRung, reserve time.Duration) (context.Context, context.CancelFunc, bool)
```

- `perRung` is `LLM_TIMEOUT`, which currently exists and is never used.
- `reserve` is a new `CHAIN_RESERVE` (default 150ms) held back from every
  non-terminal rung so the rules rung and the response marshal always fit
  inside the caller's deadline. This is the single change that closes the
  Flaw 3 DLQ path.
- `ok=false` means: record a hop with result `deadline_exhausted` and move on
  without calling the rung. Do not call a provider you cannot afford to wait
  for.
- The rules rung is exempt from `perRung` (it does no I/O) but still receives
  whatever remains.

**One trap, and it will cost an hour if it is not written down.** `rungCtx`
must do its arithmetic against `ctx.Deadline()` and `time.Until`, **not**
against an injected `clock.Clock`. `ENGINEERING.md` section 2 forbids
`time.Now()` in business logic, and this looks like a violation. It is not:
context deadlines are wall-clock by construction, and a `clock.Fake` driving
the budget while a real `context.Context` drives the cancellation gives a rung
that either never times out or always does, depending on which one the test
advanced. Unit D's breaker cooldown *does* need the injected clock. These two
are different, and the difference is that one is comparing against a real
context and the other is measuring an interval of its own.

**A3, the hop vocabulary.** Named constants in `provider.go`, replacing the
bare literals in `chain.go`:

```go
HopOK, HopTimeout, HopError, HopSchemaInvalid, HopCircuitOpen, HopDeadlineExhausted
```

and classify the failure instead of flattening it: `errors.Is(err,
context.DeadlineExceeded)` gives `HopTimeout`, anything else gives
`HopError`. `HopCircuitOpen` gets its producer in Unit D.

Do **not** touch `common.proto` to add `deadline_exhausted` to its comment
list. A comment-only proto change still trips CI's "generated code is up to
date" check and would make this a proto PR for no benefit. Unit E already
opens a proto PR; let it carry the comment update.

### Definition of done

- Five construction tests: empty list, unknown name, rules absent, rules not
  last, rules listed twice. All fail at `NewChain`, none at request time.
- A rung that blocks on `<-ctx.Done()` is cut off at `LLM_TIMEOUT`, recorded
  as `HopTimeout`, and the next rung still runs.
- **The Flaw 3 test**: a chain whose per-rung budgets sum to more than the
  inbound deadline still returns a valid rules answer, inside the deadline,
  with a `deadline_exhausted` hop for the rung that was skipped. This is the
  test that proves the DLQ path is closed, and it is the reason this unit
  exists.
- Provider names containing `:` or `,` are rejected at construction.
- **`LLM_TIMEOUT` and `CHAIN_RESERVE` are justified in the PR with an
  arithmetic budget**, not left at the placeholder values. State the sum of
  the worst-case chain against `CALL_TIMEOUT` and against `PRD.md` section
  10's 3s p95 target, and show it fits. Unit B replaces the arithmetic with a
  measurement once a real rung exists; A's job is to make the budget explicit
  instead of implicit.
- All six `test/e2e/` tests unchanged and green. Single-rung behaviour must be
  byte-identical, since the default chain is still `rules`.
- Full suite: `go test -count=1 -race -tags='integration e2e' ./...`, twice.

### Prove the test can fail

Set `reserve` to 0 and confirm the Flaw 3 test goes red with the classifier's
answer arriving after the deadline. Revert. Paste the real output.

### What actually shipped

All three defects as scoped, plus two things the LLD did not anticipate.

**`NewChain` gained a `provider.Config`** (`RungTimeout`, `Reserve`) rather
than two loose duration arguments, and validates both: a non-positive rung
timeout or a negative reserve fails at construction alongside the chain-shape
checks. That changed the signature, which touched **eight** call sites, not
the seven in `chain_test.go` that a package-scoped run compiles.
`services/classifier/internal/server/server_test.go` also builds a real chain,
and only `go vet ./...` across the whole repo caught it. Worth noting for the
next unit that changes a shared constructor: `go test ./services/classifier/...`
was green while the repo did not compile.

**The terminal rung is exempt from both the cap and the reserve, and always
runs**, including when the caller's deadline has already expired. `rungCtx`
returns the parent context unchanged for it. The LLD implied it would be
budgeted like any other rung; that is wrong, because the rules engine does no
I/O and cannot fail, so capping it only creates a path where the chain
produces no answer at all, and refusing to run it past the deadline guarantees
the failure that running it might still avoid.

`budget.go` carries a long comment on why its arithmetic uses `ctx.Deadline()`
and `time.Until` rather than an injected `clock.Clock`, since that reads as an
`ENGINEERING.md` section 2 violation and is not one. Unit D's breaker
cooldown is the contrasting case and the comment names it.

Verified with two clean full-suite runs (`go test -count=1 -race
-tags='integration e2e' ./...`) and two adversarial checks, both reverted:

1. **Reserve set to 0** (the pre-Unit-A behaviour) makes the Flaw 3 test go
   red with `chain answered after the caller's deadline expired (context
   deadline exceeded): the Decision Engine would dead-letter this record`. The
   first dead rung consumes the whole 600ms deadline and the rules answer
   arrives too late, which is exactly the DLQ path this unit closes.
2. **The terminal-rung invariant deleted** makes three of the five
   construction cases go red (`rules not last`, `rules listed twice`, `rules
   absent`). The other two, empty chain and unknown name, stay green because
   the pre-existing checks already covered them, which is the correct result
   rather than a gap.

Deliberately deferred, and said out loud rather than quietly skipped:
`ENGINEERING.md` section 11 item 3's exported metric goes to Phase 4 with the
rest of observability (`ARCHITECTURE.md` section 13: shared interceptor, not
per-service wiring). The chain logs a `Warn` per failed and per skipped rung,
and `cmd/main.go` logs the computed worst-case budget at startup so it does
not have to be reconstructed from two env vars during an incident.

### Collision notes

Owns `chain.go` outright. B, C and D all touch `cmd/main.go`'s registry, so A
must merge before any of them start.

---

## Unit B: The real provider rung

**Status**: merged.
**Depends on**: A merged. The provider decision is resolved (top of this file).
**Branch**: `svc/classifier/llm-provider`.
**Files owned**: a new `services/classifier/internal/llm/` package;
`services/classifier/cmd/main.go`; `.env.example`;
`services/classifier/SPEC.md` (its section 2 table lists this as out of
scope, which stops being true here).

### What it is

The first real model call in the system. A rung implementing
`provider.Provider`, registered in `cmd/main.go`'s registry, selected by
`LLM_PROVIDER_CHAIN`.

### Why it matters

It is the answer to "where is the AI", and it is also the first thing in this
system that can fail for reasons nobody controls. Everything else in Phase 3
exists because of this unit.

### LLD

```
services/classifier/internal/llm/
  provider.go   the Provider implementation: Name(), Classify()
  client.go     shared HTTP transport: base URL, key, timeout, status handling
  groq.go       Groq request/response shape (OpenAI chat-completions)
  gemini.go     Gemini request/response shape (native generateContent)
  schema.go     the JSON schema both vendors are handed, built from the enums
  prompt.go     prompt construction, with the record data in a delimited block
  parse.go      response -> ClassifyResponse, enum-strict
  *_test.go     all against httptest, no key, no network
```

Two rungs, one package, because they differ only in request shape and
response envelope. Both are registered in `cmd/main.go`; which ones actually
run is `LLM_PROVIDER_CHAIN`'s business.

**The one decision that makes or breaks testability**: the base URL comes from
config (`GROQ_BASE_URL`, `GEMINI_BASE_URL`, each defaulting to the real
endpoint). Every test then points at `httptest.NewServer` and exercises the
real HTTP client, real JSON, real context cancellation, real status code
handling, with no key and no egress. A rung that hardcodes its endpoint is a
rung that can only be tested by mocking the layer under test.

**Structured output, two gates, and they are not equally strong.** Build the
JSON schema in `schema.go` from the proto enums themselves, so a new
`RootCauseBucket` cannot be added without the schema following.

- **Groq**: `response_format: {type: "json_schema", json_schema: {..., strict:
  true}}` on `openai/gpt-oss-20b`. Strict mode is token-level constrained
  decoding, so the model *cannot* emit an out-of-vocabulary bucket. Strict
  mode requires every field `required` and `additionalProperties: false` on
  every object; a schema that omits either is rejected by the API, not
  silently downgraded. Set `reasoning_effort: low`.
- **Gemini**: native `generateContent` with `responseSchema` and `enum`, not
  the OpenAI-compatibility endpoint (see the provider decision section).
  Compliance here is best-effort, and Google says so.

Then `provider/validate.go` rejects it again on the way out. Two gates on
purpose: Groq's guarantee is real but vendor-controlled, Gemini's is not a
guarantee at all, and `validate.go` is the one gate this repo owns. Do not
skip it because strict mode is on.

**`rationale` is the only freeform field.** Cap it (500 chars is generous for
two sentences), strip control characters and newlines, and never feed it back
into anything. It lands verbatim in `audit_entry.rationale` and will be
rendered on the Phase 5 dashboard. This is the entire mitigation for Flaw 10's
injection surface, and it is sufficient *because* the decision fields are
enum-locked: the worst a malicious `failure_code` achieves is odd text in one
audit column.

**Prompt shape.** Record data goes in a clearly delimited block, and the
system prompt states that the block is data. Do not rely on that: the closed
output vocabulary is the defence, the delimiter is hygiene.

**What the prompt must not ask for.** Not how many times to retry, not whether
a cap is hit, not whether to contact the customer. `ARCHITECTURE.md` section
5's last bullet: guardrails never move downstream of the model's judgment. The
model says what went wrong. Unit F gives it history so it can say that better,
not so it can count.

**Missing key is a startup failure.** If `LLM_PROVIDER_CHAIN` names a provider
whose key is empty, fail in `loadConfig` (`ENGINEERING.md` section 5). Do not
register a rung that will fail every call. The default chain is `rules`, so
this never fires for anyone who has not opted in.

**Measure the timeout, do not inherit the placeholder.** Binding finding 3
from the provider decision section: `LLM_TIMEOUT=2s` is a guess, and
`gpt-oss-20b` at high reasoning effort is measured at 3.05s time to first
token. Run the manual live check below at `reasoning_effort: low`, record p50
and worst-of-ten TTFT in the PR, and set `LLM_TIMEOUT` from that with headroom.
If the measured value will not fit two live rungs inside `CALL_TIMEOUT`, say
so and drop the default chain to `groq,rules` rather than shipping a budget
that only works on paper.

**Announce a live chain.** When the resolved chain contains any non-rules
rung, log at `Warn` at startup naming the chain, so a run that costs money is
never accidental (Flaw 10).

**Still no database, still no clock, still no ground truth.** `SPEC.md`
section 8 holds. A provider call needs a context deadline, which Unit A
supplies, not a clock.

**`ComposeNudge` stays `Unimplemented`.** Phase 5. The chain is shaped so a
second method can be added without restructuring; do not add it.

### Definition of done

- Every test uses `httptest`. `go test -race ./services/classifier/...`
  passes with no key set and no network reachable.
- One test per failure mode, not one test for "it fails": 200 with a valid
  answer; 200 with an out-of-vocabulary bucket; 200 with a body that is not
  valid JSON; 429; 500; a server that hangs; a truncated body; a rationale
  over the cap; a rationale containing control characters.
- **Zero-cost default proven**: a test that builds the chain from the default
  `LLM_PROVIDER_CHAIN` and asserts no non-rules rung is ever reached, plus all
  six e2e tests still asserting `SOURCE_RULES_FALLBACK` and still green.
- One manual live run against a real key, with the request, the response and
  the resulting hops pasted into the PR. Not automated, not in CI.
- `SPEC.md` section 2's out-of-scope table updated, and `AGENTS.md`'s locked
  decision on providers updated to record the choice.
- Full suite twice.

### What actually shipped

Both rungs, in one package, sharing everything except wire shape:
`schema.go` (output schema derived from the proto enums, in two dialects),
`prompt.go`, `parse.go`, `client.go` (shared HTTP, typed errors), `groq.go`,
`gemini.go`, `provider.go`. Plus `livecheck_test.go` under a **`manual` build
tag**, which no automated tier runs, for the two jobs a fake server cannot do:
confirming the request shapes are what the vendors actually accept, and
measuring real latency.

Four findings from the live check, all of which changed something.

**1. The measurement moved the default chain to `groq,rules`.** Groq
`gpt-oss-20b` at `reasoning_effort: low`, 16 calls: min 237ms, p50 ~570ms,
max 688ms, comfortably inside every budget. Gemini `gemini-2.5-flash`, 6
calls: p50 3.01s, max 6.19s, roughly five times slower. No single
`LLM_TIMEOUT` serves both: near Groq's profile Gemini always times out and the
rung is decorative, above Gemini's max one rung alone exceeds the whole 5s
`CALL_TIMEOUT`. Even Groq-rate-limited (instant 429) plus Gemini overruns it.
This unit's own definition of done specified this outcome in advance, and
taking that exit is why the clause existed. The Gemini rung is implemented,
unit-tested and confirmed working live; it is one config value from returning,
and doing so honestly needs per-rung timeouts rather than one chain-wide
`LLM_TIMEOUT`. Full reasoning in `docs/DECISIONS.md` 2026-08-28.

**2. `LLM_TIMEOUT=2s` is now measured rather than guessed, and the value did
not change.** Worth stating plainly: the placeholder was right, and the
published 3.05s time-to-first-token figure for this model said it was wrong.
That figure is the *high* reasoning variant. Only the measurement settled it,
which is the argument for having demanded one.

**3. The model fabricated a confident diagnosis, and the prompt fixed it.**
Groq initially answered a deliberately undiagnosable failure code
(`ERR_7734_XQ`) with `HARD_DECLINE` at confidence **0.90**, despite the prompt
already instructing it to answer `UNSPECIFIED` and escalate. Naming the
failure mode and its consequence, rather than only the desired behaviour,
moved it to `UNSPECIFIED` + `ESCALATE` at confidence **0.30**. Two things
follow, both recorded in `DECISIONS.md`: a prompt instruction is not a
guarantee, so the enum gates remain the actual control; and Unit G's
confidence threshold would have caught nothing before this fix and catches
exactly the right record after it, so **Unit G's value depends on prompt
quality rather than being independent of it**. Unit G should say so.

**4. The rungs disagree on `EXPIRED_INSTRUMENT`** (Groq says
`USER_ACTION_NEEDED`, Gemini and the rules table say `HARD_DECLINE`), and that
is deliberately left standing. All three recommend the same action so no
spending decision changes, but the bucket keys the priors and Phase 5's
accuracy scoring. Recorded rather than tuned away.

One implementation note for whoever writes Unit D: the 429 detection already
exists here. `client.go` returns a typed `*RateLimitedError` carrying
`RetryAfter` parsed from the header in both RFC 9110 forms. Detecting it
belongs in the only code that sees an HTTP status; acting on it is the
breaker's job. **Gemini's live rate limit sent no `Retry-After` header at
all**, so the fallback-to-configured-cooldown branch is not hypothetical.

### Prove the test can fail

Removing the enum gate in `parse.go` (accepting whatever bucket the model
names) turns four tests red, across both vendors:
`TestParseAnswerRejectsAnythingItCannotVerify/bucket_outside_the_enum`,
`/lowercase_bucket`, and
`TestProviderRejectsEveryUntrustworthyResponse/bucket_outside_the_enum` for
groq and gemini. Reverted, then re-run five times to confirm the package is
not flaky.

---

## Unit C: Fallback path proven per failure mode

**Status**: not started.
**Depends on**: A and B merged.
**Branch**: `test/classifier-fallback-path`.
**Files owned**: a new `services/classifier/internal/provider/fallback_test.go`;
a new file in `test/e2e/`; `test/e2e/harness_test.go` (additive, see collision
notes).

### What it is

`PLAN.md`'s "simulate timeout/error per provider, confirm the chain falls
through correctly and every hop tried is recorded", at two tiers.

### Why it matters

`PRD.md` section 12's demo script promises "one record where the LLM call
failed and fell back to rules". Right now that is a claim. This unit is what
makes it a fact, and the e2e half is what makes it demonstrable rather than
asserted in a Go test nobody watches.

### LLD

**Unit tier**, against fakes, in `internal/provider`: for each of timeout,
transport error, HTTP 5xx, HTTP 429, invalid JSON, and valid JSON with an
out-of-vocabulary enum, assert the rung is recorded with the *specific* result
string Unit A introduced, and the next rung runs. Then a two-LLM-rung chain
where the first fails and the second answers: hops must be
`[{groq,timeout},{gemini,ok}]`, in that order, and `source` must be
`SOURCE_LLM`.

**e2e tier**, against the real classifier binary: the test runs its own
`httptest.NewServer` that always returns 500, starts the stack with
`LLM_PROVIDER_CHAIN=groq,rules` and `GROQ_BASE_URL` pointed at it, and
submits a record. The record must still reach `RECOVERED`, and `source` on its
audit trail must still be `SOURCE_RULES_FALLBACK`.

This needs a harness change: `startStack` currently builds the classifier's
environment with a bare `commonEnv(classifierPort, classifierMetrics)` and no
extras. Make it additive, the same shape the Decision Engine's env already
has, so none of the seven existing call sites change.

**Do not block C's e2e half on Unit E.** Asserting the *hops* end to end
requires E's persistence. Until E lands, assert the failed hop by calling
`Classify` directly over gRPC from the test (the harness already exposes
service addresses) and assert the record's `source` through the audit trail.
Note in the PR that the audit-trail hop assertion belongs to E.

### Definition of done

- Six unit-tier failure modes, one test each, each asserting the exact hop
  result string rather than merely that a fallback occurred.
- Two-rung chain: hops in attempt order, correct `source`.
- e2e: real binary, real HTTP failure, record still reaches `RECOVERED` with
  `SOURCE_RULES_FALLBACK`, and a direct `Classify` call shows the failed hop.
- No existing `startStack` call site modified (there are seven, across six files).
- Full suite twice.

### Prove the test can fail

Make the fake server return a valid classification and confirm the "fell back
to rules" assertion goes red. This one matters: a test that asserts a fallback
happened, against a stack where the primary was never going to answer anyway,
proves nothing.

### Collision notes

Touches `test/e2e/harness_test.go`, which is shared with every e2e test. Phase
2's shared hazard applies: adding a new file to `test/e2e/` is safe, changing
the harness is not. Nothing else in Phase 3 changes the harness, so C owns it,
but the change must stay additive.

---

## Unit D: Circuit breaker per provider

**Status**: not started.
**Depends on**: B merged. Independent of C, E, F, G.
**Branch**: `svc/classifier/circuit-breaker`.
**Files owned**: a new `services/classifier/internal/provider/breaker.go` and
`breaker_test.go`; `services/classifier/cmd/main.go`; `.env.example`.

### What it is

A wrapper that stops calling a provider that is down, so a sustained outage
costs one timeout rather than one timeout per record.

### Why it matters

`PRD.md` section 10's resilience NFR says a *sustained* provider outage must
not degrade throughput, "which is what the circuit breakers are for, not just
the timeouts". At the section 10 throughput target of 50 records/sec, a 2s
timeout per record with no breaker means the pipeline is spending 100
seconds of wall time per second of traffic waiting on a dead endpoint. That is
a full stall, not a dip.

### LLD

**The breaker wraps a rung and is itself a `provider.Provider`.** That is the
shape Unit A's loop was already built for, and `SPEC.md` section 4.7 says so
("structure the loop so a rung can later be wrapped by a circuit breaker
without rewriting the loop"). Do not put breaker state in `chain.go`. The
temptation to is strong and it would couple every future wrapper to the walk.

**Per-pod, in-memory, and that is deliberate.** `ARCHITECTURE.md` section 5:
"Breaker state is deliberately per-pod and in-memory, not shared: it is a
local health observation, and a shared breaker would itself become a
coordination point and a shared failure mode. Do not build distributed breaker
state." Repeat this in the file's doc comment so nobody helpfully adds Redis.

Three states, closed / open / half-open. Config:
`LLM_BREAKER_THRESHOLD` (consecutive failures, default 5),
`LLM_BREAKER_COOLDOWN` (default 30s), one trial request in half-open.

**A 429 is not the same failure as an outage, and this is the case that
actually matters here.** Both free tiers this project runs on are rate
limited (30 RPM on Groq, 10 to 15 RPM on Gemini), so rate limiting is the
failure mode most likely to fire in a demo, not a provider going down. Plain
consecutive-failure counting makes the pipeline pay five failed calls before
the breaker opens, which on stage is a visible five to fifteen second stall
while records grind through rejections that were never going to succeed.

So the breaker treats HTTP 429 as its own case:

- **open immediately** on the first 429, without waiting for the threshold;
- **cooldown from the provider's `Retry-After` header** when it sends one,
  falling back to `LLM_BREAKER_COOLDOWN` when it does not. Confirm whether
  Groq and Gemini actually send it as the first task in this unit rather than
  assuming, and record the answer in the PR either way;
- record the hop as `HopRateLimited` (a seventh vocabulary constant, added
  here rather than in Unit A because this is the unit that produces it) so the
  audit trail can tell "we were throttled" apart from "the provider was
  broken". Those call for completely different operator responses.

This is what makes the failover in `PRD.md` section 12 step 5 look
deliberate rather than stuttering: Groq rejects instantly at zero cost, Gemini
answers, and the record never notices.

It is also what makes `groq,gemini,rules` viable at all. The three-rung
latency worry in Flaw 3 assumes each rung *times out*. A rate-limited rung
costs approximately nothing because no call is made, so the throttled path is
groq (instant reject) plus gemini plus rules, which fits the budget
comfortably.

**This is where the injected clock belongs.** The cooldown is an interval the
breaker measures itself, so it takes a `clock.Clock` and a test drives it with
`clock.Fake`. Contrast Unit A's `rungCtx`, which must not. `SPEC.md` section 8
says the classifier needs no clock; that stops being true here, so update it.

**Decide explicitly: does `schema_invalid` count as a failure?** Recommendation
is yes, and record it in `DECISIONS.md`. A provider returning well-formed
garbage is as useless as one that is down, and the breaker exists to stop
paying for useless calls. Left undecided, this is the sort of thing two agents
implement differently.

**When open, return `ErrCircuitOpen` immediately and make no call.** The chain
records `HopCircuitOpen` and moves to the next rung.

**Concurrency.** The breaker is shared across concurrent gRPC requests against
one chain built at startup. Mutex-guarded counters, `-race`, and the half-open
trial must admit **exactly one** request. That is the same
"exactly once under concurrency" property Phase 2's Unit L spent two
iterations failing to prove; read `INCIDENTS.md` 2026-08-26 before writing the
test. Many goroutines, assert the wrapped provider's call count is exactly 1.

### Definition of done

Per Flaw 6, all four assertions are on **call counts and hop results**, never
on wall-clock time:

1. After `LLM_BREAKER_THRESHOLD` consecutive failures, the wrapped provider's
   call count stops increasing across the next 50 requests, and all 50 still
   receive a valid rules answer.
1a. **A single 429 opens the breaker on its own**, with no second call made,
   and the hop records `rate_limited` rather than `error`. A `Retry-After`
   header sets the cooldown; its absence falls back to the configured one.
   Both branches tested.
2. Each of those 50 records a `circuit_open` hop.
3. Advancing `clock.Fake` past the cooldown admits **exactly one** trial call
   to the provider, proven under concurrency.
4. A successful trial closes the breaker; a failed trial reopens it and resets
   the cooldown.

Plus: a `Warn` log on every state change naming provider, old state, new
state, and consecutive failure count (this is the compensating control for the
deferred `llm_circuit_state` metric, see the deferral note above). A measured
wall-clock number reported in the PR for the demo narrative, explicitly not
asserted. `SPEC.md` section 8 updated. Full suite twice.

### Prove the test can fail

Set the threshold above 50 so the breaker never opens, and confirm assertion 1
goes red with the real call count in the message. Then set the half-open trial
limit to 2 and confirm assertion 3 goes red.

---

## Unit E: Provider hops persisted and retrievable

**Status**: not started.
**Depends on**: nothing. **Start this first** (see the dependency graph).
**Branches**, three, merged in this order:

1. `infra/audit-entry-provider-hops`, migration
   `00005_audit_entry_provider_hops.sql`
2. `proto/audit-entry-hops`, `AuditEntry.hops` plus the `ProviderHop.result`
   comment vocabulary Unit A deferred here
3. `svc/audit/provider-hops`, the service code

**Files owned**: `migrations/00005_*.sql`; `proto/audit/v1/audit.proto`;
`proto/common/v1/common.proto` (comment only);
`services/decision-engine/internal/engine/store.go` and `engine.go`;
`services/audit/internal/server/store.go` and a new `hops.go`.

### What it is

Flaw 1's replacement for `PLAN.md`'s already-done item 4: carry
`ClassifyResponse.hops` from the classifier's answer into `audit_entry`, and
back out through `GetRecordAudit`.

### Why it matters

Without it, the audit trail after Phase 3 can say `SOURCE_LLM` but cannot say
*which* provider answered or what the ones before it did, because `sourceFor`
(`chain.go:95`) only distinguishes rules from LLM. `PRD.md` section 12's
"one record where the LLM call failed and fell back to rules" is a drill-down
into an audit trail, and the drill-down currently has nothing to show. This
unit is what makes that demo claim true rather than narrated.

### Why it is three PRs

`ARCHITECTURE.md` section 12a: a migration is always its own PR, merged before
anything depending on it. Section 9: a proto change is its own PR, merged
before any code depending on the new shape, and CI mechanically enforces it
(the `proto` job fails a PR that mixes proto and service changes). Both rules
apply, so this is three sequenced PRs. That serialised floor is exactly why it
starts first.

### LLD

**The column**: `audit_entry.provider_hops TEXT`.

TEXT rather than JSONB, matching the house style: every enum in this schema is
already stored as TEXT (`record_state.current_state`, `pending_action`,
`intervention_attempt.action_type`, `outcome`, `audit_entry.source`), and both
halves of a hop are closed vocabularies once Unit A lands. Encoding is
`provider:result` pairs joined by `,`, for example
`groq:timeout,gemini:ok`. Readable in `psql` without a JSON operator, which
is worth something for a project whose whole pitch is an auditable trail.

One `hops.go` holding `encodeHops`/`decodeHops` and nothing else, with a
round-trip test. NULL for "no classification happened", never the empty
string: those are different facts and the trail should not blur them.

**Where it gets written.** `store.scheduleNew` already writes `rationale` and
`source` on **every** step of `scoringPath`, not only the classification step.
Match that exactly and write `provider_hops` on every step too, so a caller
reading any single entry gets the same picture `rationale` already gives.
Note it in the migration comment, because the alternative looks tidier and
would make a single-entry read silently incomplete.

**What `recordRescore` writes: NULL.** A re-score does not re-classify, so it
made no provider call. Copying the original classification's hops forward onto
a later entry would misrepresent the trail as "we asked the model again",
which is precisely the kind of quiet overstatement this field exists to
prevent. Write NULL and say why in the code.

**Audit side**: select the column in `store.go`'s existing query, decode, and
populate `AuditEntry.hops`.

**Cross-unit dependency**: the delimited encoding requires provider names to
contain neither `:` nor `,`. Unit A's `NewChain` validation covers it. If A
has not merged, add the check here rather than assuming it.

### Definition of done

- Migration applies and rolls back cleanly, both directions run against the
  live stack.
- Round-trip test: `decodeHops(encodeHops(h))` is the identity over every
  `(provider, result)` pair in the closed vocabulary, plus the empty slice,
  plus nil. Table-driven over the vocabulary constants so a seventh result
  string cannot be added without a test covering it.
- Integration tier (`-tags integration ./services/audit/...`): an entry seeded
  with `groq:timeout,gemini:ok` comes back as two `ProviderHop` messages in
  order.
- e2e: a record classified through the default rules-only chain has exactly
  one hop, `rules:ok`, on its `NEW -> SCORING` entry, retrievable through a
  live `GetRecordAudit` call.
- A record that escalated without ever being classified has NULL, and comes
  back as an empty hop list rather than a one-element list of empty strings.
- Full suite twice. The migration means the integration and e2e tiers are not
  optional here.

### Prove the test can fail

Seed an entry with three hops and assert two; confirm red with the real count.
Then, separately, seed a provider name containing a `:` and confirm the
encoding either round-trips it or rejects it, whichever the implementation
chose. A delimiter-based encoding whose delimiter case is untested is a bug
waiting for the first provider named `groq:v2`.

### Collision notes

PR 3 touches `services/decision-engine/internal/engine/store.go`, which Unit F
also owns. **Do not run E's third PR and F in parallel.** E's first two PRs
(migration, proto) touch neither, so they can run alongside F freely.

---

## Unit F: Populate `history` and `instrument_history`

**Status**: not started.
**Depends on**: nothing structurally. Should land before or with B.
**Branch**: `svc/decision-engine/classify-history`.
**Files owned**: `services/decision-engine/internal/engine/store.go`,
`clients.go`, `engine.go`, and their tests.

### What it is

Fill the two `ClassifyRequest` fields that have been empty since Phase 1, so
the model has inputs the lookup table does not.

### Why it matters, and why it is not optional

This is Flaw 5. `clients.classify` (`clients.go:23`) sends
`&classifierv1.ClassifyRequest{Record: record}`. The rules engine reads
`failure_code` and `type` (`rules/rules.go:34`). So without F, a model rung
receives *exactly the inputs the lookup table receives*, and produces a value
from *exactly the same closed vocabulary*. It cannot add information the table
does not already encode. It can only add latency, cost and variance.

`classifier.proto` says what these fields are for, and it is precisely this:
`history` so "the model can reason about what has already been tried rather
than starting cold", `instrument_history` as "signal for distinguishing this
rail is flaky right now from this card is dead". Neither is derivable from
`failure_code`.

`services/classifier/SPEC.md` section 3 documented the gap in Phase 1, said
"do not fix this by populating history yourself", and named the Decision
Engine as the right owner. Section 10 item 1 raised it as a cross-service item
to pick up later. This is later.

### LLD

Two new queries in `store.go`. `loadAttemptHistory` (`store.go:303`) is **not**
reusable: it returns aggregate counts and a cooldown timestamp for the
guardrails, not rows.

```go
// oldest first, per the proto's contract
func (s *store) loadAttemptRows(ctx, recordID) ([]*commonv1.InterventionAttempt, error)

// attempts on OTHER records sharing this instrument, most recent first, capped
func (s *store) loadInstrumentHistory(ctx, instrumentRef, excludeRecordID string, limit int) ([]*commonv1.InterventionAttempt, error)
```

- `record_instrument_idx` already exists (`00001_initial_schema.sql:34`),
  partial on `instrument_ref IS NOT NULL`, so the instrument query is indexed.
  No migration needed.
- **Cap the instrument query.** A popular instrument accumulates rows
  indefinitely; an unbounded read here is a growing per-record query *and* a
  growing prompt. Ten most recent, and say ten in a comment.
- `instrument_ref` is nullable. Empty means skip the query entirely, not query
  for `''`.
- Exclude the record's own rows from `instrument_history`; they are already in
  `history` and duplicating them tells the model the same fact twice with more
  weight.

**Do not merge the two history paths.** The guardrails need counts and a last
contact timestamp; the classifier needs rows. One query serving both would
couple a compliance check to a prompt-shaping read, and the guardrail counters
are deliberately derived rather than cached (`DECISIONS.md` 2026-08-24).

**Do not add `failure_code` to the `InterventionAttempt` proto message.** The
column exists in `intervention_attempt` but the proto message has five fields
and none of them is it. Action type, outcome and timestamp are enough to
separate "this rail is flaky right now" from "this card is dead", and adding a
field would serialise this unit behind a proto PR for no gain. Stated here so
it does not get re-litigated mid-branch.

**The trust model does not move.** Giving the model history does not touch a
single guardrail. `ARCHITECTURE.md` section 5's last bullet still holds: caps,
cooldowns and escalation are enforced in the Decision Engine after the
classifier answers, and are never influenced by what it recommended. Unit B's
prompt must not ask the model to count attempts or judge whether a cap is hit.

**Cost.** Two extra indexed reads per record on the classify path. At
`PRD.md` section 10's 50 records/sec that is 100 extra reads per second.
Measure it and report the number; if it is material, the instrument query is
the one to cache, not the per-record one.

### Definition of done

- Integration test: a record with two prior attempts produces a
  `ClassifyRequest` carrying both, oldest first, with the right action type
  and outcome on each.
- A record whose instrument has attempts on two *other* records receives
  those, and its own rows do not appear in `instrument_history`.
- `instrument_ref` NULL sends an empty `instrument_history` and issues no
  second query.
- The instrument cap holds, proven by seeding more than the cap.
- All six e2e tests green and **unchanged**. The rules engine ignores both
  fields, so nothing observable changes yet, which is exactly why F is safe to
  merge before B.
- `services/classifier/SPEC.md` section 3 updated: it currently tells the next
  agent that both fields are always empty.
- Full suite twice.

### Prove the test can fail

Assert the attempt count, then seed a third attempt and confirm the test goes
red at the old count. Do the same for the instrument cap by seeding cap+1.

### Collision notes

Owns `services/decision-engine/internal/engine/store.go`, which Unit E's third
PR also touches. **Do not run F and E's third PR in parallel.**

---

## Unit G: Confidence threshold enforced in the Decision Engine

**Status**: not started.
**Depends on**: B merged. Meaningless while confidence is a table constant.
**Branch**: `svc/decision-engine/confidence-threshold`.
**Files owned**: `services/decision-engine/internal/engine/engine.go`,
`state.go`, `cmd/main.go`, `.env.example`, and their tests.

### What it is

Flaw 8: make the two comments that already describe this behaviour true.

### Why it matters

`classifier.proto` documents `confidence` as "Below the configured threshold
the record is escalated rather than acted on". `ARCHITECTURE.md` section 5
puts that decision in the Decision Engine. `state.go:128-131` and
`engine.go:166-169` both say a low-confidence classification is a safety call
that bypasses pricing. No code in `services/decision-engine/` reads
`resp.GetConfidence()`.

Harmless today, because confidence is a constant from `rules/actions.go` and
the only low-confidence records are the ones the rules engine already
recommends escalating. The moment a model produces the number, it becomes a
live safety signal with nothing acting on it, and two comments in the repo
become false.

### LLD

New config `CLASSIFY_CONFIDENCE_THRESHOLD`, **default 0.0**, so the behaviour
is off until deliberately enabled. Default-off is the right call and not
timidity: turning it on in the same PR that adds it would change the outcome
of six e2e tests for a reason unrelated to what any of them test.

In `Engine.decide` (`engine.go:164`), before the existing escalate check:

```
confidence < threshold  ->  directPath(ESCALATED, "classification confidence below threshold")
```

Same route as a risk hold, for the reason `directPath`'s own comment already
gives: it is a safety call, not a priced option, so it bypasses economics. The
`NEW -> ESCALATED` edge already exists in `services/audit/internal/server/statemachine.go`,
so there is no state-machine change and no Unit-H-style surprise.

**Do not implement this in the classifier.** A classifier that escalates on
its own confidence is the model deciding, the trust inversion `PRD.md` section
2a forbids. `SPEC.md` section 10 item 2 already says so.

**The re-entry path has no confidence to check.** `scheduler.go`'s
`handleFailedAttempt` calls `scoreAndRoute` without re-classifying, so there
is no fresh confidence value there. State it, so nobody adds a threshold check
against a zero value and escalates every retry.

**One interaction worth getting right.** The unknown-code path already returns
confidence 0.0 *and* recommends `ESCALATE`, so with any threshold above 0
those records satisfy both rules. The reason string on the audit entry must
distinguish which one fired, or the trail cannot tell "we do not recognise
this failure code" from "the model was unsure", and those call for different
human follow-up.

### Definition of done

- Unit tests on the boundary: just below the threshold escalates with the
  threshold reason; exactly at the threshold does not; just above does not.
  The comparison is `<`, and the at-threshold case is the one that proves it.
- Threshold 0.0 escalates nothing on confidence, proven, so the six e2e tests
  are provably unaffected.
- Integration test: the audit entry's `reason` names the threshold rule, and an
  unknown-code record's `reason` names the unknown-code rule instead.
- `services/classifier/SPEC.md` section 10 item 2 updated: it currently
  describes this as an open cross-service item.
- Full suite twice.

### Prove the test can fail

Change `<` to `<=` and confirm the at-threshold case goes red. Paste the
output.

---

## Unit H: `LLM_SAMPLE_RATE` and the config profiles

**Status**: not started.
**Depends on**: nothing. Collides with F, see Collision notes.
**Branch**: `svc/decision-engine/llm-sample-rate`.
**Files owned**: `services/decision-engine/internal/engine/clients.go`,
`engine.go`, `cmd/main.go`; new `configs/demo.env` and `configs/dev.env`;
`.env.example`; `docs/ARCHITECTURE.md` section 17 (one new row).

### What it is

A per-record decision about whether this record is worth a live model call,
plus the checked-in config profiles that set it and the other demo knobs.

### Why it matters

Without it there is no demo. `PRD.md` section 12 step 1 calls for 50 to 100
records; Groq's free tier is 30 RPM. Those two numbers do not fit, and the
failure is not graceful in the way that matters: the first thirty records get
a model, the rest get rate limited, the breaker opens, and which records ended
up with a real rationale is decided by scheduling luck. Step 4 asks a judge to
drill into a record's LLM reasoning, and there is no way to know in advance
that the record they pick has any.

`ClassifyRequest.force_rules_only` already exists as a **per-request** field
(`classifier.proto`, and `ARCHITECTURE.md` section 5 calls it the cost-safety
switch). `SPEC.md` section 3 confirms nothing has ever set it. This unit is
the thing that sets it.

### LLD

```go
// in clients.classify, before building the request
ForceRulesOnly: !sampledForLLM(record.GetId(), cfg.LLMSampleRate)
```

**Sample deterministically, by hash of `record_id`.** Not `rand`. Re-run
safety is a headline claim of this project and `test/e2e/rerun_safety_test.go`
asserts identical outcomes on replay. Random sampling would not break CI today
(the default chain is `rules`, so the sampled and unsampled paths produce the
same answer), but it makes the guarantee conditional on a config value, and
determinism costs one FNV hash. Use `hash(record_id) % 10000 < rate*10000` so
the same record always takes the same path, on every replay, forever.

`LLM_SAMPLE_RATE` is a float in `[0,1]`, **default 0.0**, validated at
startup. Default-off means every existing test and every default run is
provably free, and it means this unit changes no observable behaviour when it
merges.

What the rate buys at 100 records:

| `LLM_SAMPLE_RATE` | Live calls | What it is for |
|---|---|---|
| `0.0` | 0 | the default, and the setting the Phase 6 load run must use |
| `0.15` | ~15 | full record volume, real rationales, comfortably under 30 RPM, no failover |
| `0.5` | ~50 | **deliberately trips the real rate limit**: live failover to Gemini, then rules, with real `rate_limited` hops in the trail |

That last row is the point worth understanding. It stages `PRD.md` section 12
step 5's "graceful failure" beat using an *actual* provider rate limit and the
*actual* breaker, not a simulated outage. One number moves the demo between
smooth and dramatic, and both are honest.

**The profiles, and why not a `MODE` enum.** A single `MODE=demo|dev|prod`
switch silently changes many behaviours at once, which is how a demo becomes
unreproducible and how "does this work in production?" stops being answerable.
`ARCHITECTURE.md` section 17 has deliberately gone the other way: a table of
individually named knobs, each with its documented real-world counterpart.

So: checked-in `configs/demo.env` and `configs/dev.env` that set the existing
named knobs. The code never learns what "demo" means, it just reads variables.
Every behaviour stays individually visible and individually overridable, and
production is not a code path that only ever runs on the judge's laptop.

```
# configs/demo.env
DEMO_TIME_SCALE=300000
LLM_PROVIDER_CHAIN=groq,gemini,rules
LLM_SAMPLE_RATE=0.15
```

Note `DEMO_TIME_SCALE` is already built and already threaded through
`config.Common.Scale()`; the e2e harness runs at 300000 today. The "make it
run fast" half of the demo is solved. This unit adds the other half.

**Add one row to `ARCHITECTURE.md` section 17**: hackathon value
`LLM_SAMPLE_RATE=0.15`, real-world value "route by ambiguity rather than by
hash: call the model when the deterministic table is not confident, which is
the same cost posture for a different reason". That is the honest framing, and
it is a better production design than a fixed rate. Do not build it here; note
it as the real-world counterpart, which is what section 17 is for.

### Definition of done

- Deterministic: the same `record_id` produces the same sampling decision
  across process restarts, asserted directly rather than inferred.
- Distribution: over 10,000 synthetic ids, a rate of 0.15 selects within a
  sane band of 15%. Assert a band, not an exact count.
- `0.0` selects nothing and `1.0` selects everything, both asserted.
- Out-of-range values fail at startup, not at request time.
- `force_rules_only` actually arrives on the wire: an integration test
  asserting the field is set on the `ClassifyRequest` the classifier receives,
  not just that the helper returned false.
- All six e2e tests green and unchanged, at the default rate of 0.0.
- `configs/demo.env` and `configs/dev.env` checked in;
  `ARCHITECTURE.md` section 17 row added.
- Full suite twice.

### Prove the test can fail

Swap the hash for `rand.Float64()` and confirm the determinism test goes red.
Then set the rate to 0.15 and assert 100% selection, and confirm the
distribution test goes red with the real proportion in the message.

### Collision notes

Owns `services/decision-engine/internal/engine/clients.go`, which **Unit F
also owns**. Both add to `classify`. Do not run H and F in parallel; F first
is the better order, because F's two new request fields and H's one new
request field land in the same function and F is the larger change.

---

## Parallelization guide

Three units touch `services/decision-engine/internal/engine/`
(E's third PR, F, and H), so the parallelism ceiling is lower than eight units
suggests. The waves below are the maximum genuinely-safe fan-out.

### Wave 1: three agents, zero file overlap

| Agent | Unit | Owns | Needs the docker stack? |
|---|---|---|---|
| 1 | **A** | `services/classifier/internal/provider/`, classifier `cmd/main.go` | no, pure unit tests |
| 2 | **E, PRs 1 and 2 only** (migration, proto) | `migrations/`, `proto/` | migration only, briefly |
| 3 | **F** | `services/decision-engine/internal/engine/` | yes, integration tier |

These three share no file. Agent 2 deliberately stops after the proto PR: E's
third PR touches `store.go`, which agent 3 owns.

### Wave 2: after wave 1 merges

- **E, PR 3** (decision-engine `store.go`, audit service). Needs F merged.
- **H** (sample rate, profiles). Needs F merged; it shares `clients.go`.

These two do not collide with each other (E is `store.go`, H is
`clients.go`/`engine.go`), so they can run in parallel as two agents.

### Wave 3: after A merges

- **B**, the real provider rung. This is the judgment-heavy one: prompt
  design, the injection surface, two vendors' schema modes, and a measured
  timeout that changes the config if it comes out wrong. Not a good fan-out
  candidate.

### Wave 4: after B merges

- **C**, **D** and **G**, all three in parallel. They share no files: C is
  `test/e2e/` plus one additive harness change, D is a new file in
  `internal/provider/`, G is the decision-engine.

### The two tempting mistakes

Starting **B** first because it is the headline. Before A it lands three
latent DLQ paths (Flaws 2, 3 and 4) in the same commit as the feature; before
F it lands a model that cannot beat the lookup table it sits in front of
(Flaw 5).

Treating **H** as optional polish. It blocks no code, and it blocks the
entire demo.

## Shared hazards

**`test/e2e/harness_test.go` is touched only by Unit C in this phase.** Adding
a new file to `test/e2e/` is safe; changing the harness is not. C's change must
stay additive, leaving all seven existing `startStack` call sites unmodified.

**`cmd/main.go` for the classifier is touched by A, B and D.** They are
sequenced (A before B before D), so this is safe as written, but do not
parallelise them on the strength of "they are different features".

**`.env.example` is not union-merged.** A, B and D each add config keys to it.
Keep each diff to the lines actually needed; a concurrent edit conflicts.

**`docs/PLAN.md`, `docs/DECISIONS.md` and `docs/INCIDENTS.md` are safe to edit
concurrently** (git's `merge=union`, see `.gitattributes`). Append only, never
restructure. **This file is not union-merged**, so two units updating their own
status here will conflict, exactly as `PHASE2_IMPLEMENTATION.md` did during
Phase 2. Merge locally, not through GitHub's web UI, which does not apply the
driver.

**There is exactly one docker stack on this machine, and every worktree
shares it.** `docker-compose.yml` hardcodes the project name `momotaro` and
fixed host ports, so `make up` from a second worktree attaches to the same
Postgres, Kafka and Redis rather than starting its own. Consequences for
anyone running a unit in a worktree:

- **Never run `make down-clean`.** It destroys the shared volumes, including
  another agent's in-flight integration run. `make up` and `make migrate-up`
  are safe and idempotent.
- **Two agents running the integration or e2e tier at the same time can
  interfere.** The Decision Engine's scheduler polls `record_state`
  system-wide by design, so one worktree's scheduler can claim another's
  seeded rows. This has already cost this repo real time twice
  (`docs/INCIDENTS.md`, and the reason `test/e2e/harness_test.go` gives every
  stack its own Kafka topics and consumer group). Coordinate: unit-tier work
  is unrestricted, tagged-tier runs take turns.
- A migration applied from one worktree is applied for everybody. Unit E's
  migration PR lands first for exactly this reason.

**Nothing in this phase may make CI depend on a provider.** `ci.yml`'s
`build-test` job runs `go test -race ./...` with no tags, no secrets and no
services. If a unit's test needs a key or the network, it is written wrong. See
Flaw 7.

**Every unit must state, honestly, whether its tests can fail.** This is the
recurring failure in this repo and it has now cost real time five times
(`INCIDENTS.md` 2026-08-23 through 2026-08-27): a test that passed three runs
in four by chance, a hardcoded `true`, a fabricated fixture, a mutation
harness reporting errors as successes, and a readiness probe checking a port
nothing opens. The cheap defence is mechanical: break the code on purpose,
confirm the test goes red, revert, and paste the real output. Every unit above
has a **Prove the test can fail** section for this reason, and they are not
suggestions.

**Phase 3 is the first phase where a green local run can be green for the
wrong reason in a new way**: the default chain is `rules`, so a unit that
forgets to point its test at a non-rules chain will pass by never exercising
the code it added. Assert the hop list, not just the outcome.

## Definition of done, for every unit

`docs/ENGINEERING.md` section 11 is the gate. Two items need naming here:

- **Item 3, the exported metric.** Deferred to Phase 4 as in every prior
  phase, with structured logs as the compensating control. See the deferral
  note in the flaws section above: for Unit D specifically, say so in the PR
  rather than quietly ticking the box, because `ARCHITECTURE.md` section 5
  names `llm_circuit_state` by hand.
- **Item 4, `ctx` with a deadline on every outbound call.** Phase 3 is the
  first phase where this is not free. Unit A is the unit that makes it true,
  and every subsequent unit inherits it.

Plus, for this phase only: **the full tagged suite, twice.**
`go test -count=1 -race -tags='integration e2e' ./...` against `make up` and
`make migrate-up`. Phase 2's Units H, K and M each shipped a bug that the
untagged run did not see (`INCIDENTS.md` 2026-08-26 and 2026-08-27); the
untagged run is not evidence.

## What Phase 3 deliberately does not do

Named so nobody builds them early and nobody assumes they were forgotten.

| Thing | Where it belongs | Why not here |
|---|---|---|
| `ComposeNudge` | Phase 5 | No caller. The chain is shaped so a second method is additive; leave it `Unimplemented`. |
| Prometheus metrics (`llm_fallback_total`, `llm_call_duration_seconds`, `llm_circuit_state`) | Phase 4 | `ARCHITECTURE.md` section 13: shared gRPC interceptor, not per-service hand-wiring. |
| Tier 2 observed recovery rates blended over the priors | Phase 5 or later | `ARCHITECTURE.md` section 5a describes it; it needs volume this system has not generated, and it is economics, not reasoning. |
| The load generator's `live` mode | Phase 6 | `force_rules_only` already exists and is tested. The generator does not. |
| Distributed / shared circuit breaker state | nowhere | `ARCHITECTURE.md` section 5 forbids it explicitly. |
| Reading `GROUND_TRUTH` from anywhere in the decision path | nowhere | The integrity rule. `test/integrity/ground_truth_isolation_test.go` enforces it across all three decision-path services and will fail the build. |
