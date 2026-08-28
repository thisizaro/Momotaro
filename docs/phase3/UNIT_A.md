# Unit A: Chain hardening (classifier provider chain)

**Branch**: `svc/classifier/chain-hardening`
**Worktree**: `../Momotaro-unit-a`
**Depends on**: nothing. Start immediately.
**Docker stack needed**: **no.** Everything here is a pure unit test.
**Estimated shape**: one package, three defects, roughly 200 lines of code and
more than that in tests.

---

## 1. Read before you write code

In this order.

1. `AGENTS.md` (repo root) and `services/classifier/AGENTS.md`: ownership
   boundaries.
2. `docs/PHASE3_IMPLEMENTATION.md`, **Unit A** for the LLD, and **Flaws 2, 3
   and 4** for why each of the three defects matters. That document is the
   spec; this file is the task brief. Where they appear to differ, the
   implementation doc wins.
3. `docs/ENGINEERING.md` sections 1 (TDD), 3 (deadlines), 5 (fail-fast
   config), 11 (Definition of Done), 14 (one job per file).
4. `services/classifier/SPEC.md` section 4.7. It describes the chain you are
   hardening and explicitly anticipates this work ("structure the loop so a
   rung can later be wrapped").
5. The code: `services/classifier/internal/provider/chain.go`,
   `provider.go`, `validate.go`, `chain_test.go`.

## 2. What to build

Three defects in one package. They are one unit because they all live in
`chain.go` and splitting them would mean three branches fighting over one
file.

**A1. Terminal-rung invariant.** `NewChain` rejects unknown names but does not
require a rung that cannot fail. Add four construction-time checks: the last
name must be `RulesName`; `RulesName` must appear exactly once; no name may
contain `:` or `,` (Unit E encodes hops into a delimited column and provider
names come from config, so rejecting the delimiters here is cheaper than
escaping them there).

**A2. Deadline budget.** No rung has a timeout today: `chain.Classify` passes
the inbound `ctx` straight through, and `LLM_TIMEOUT` is loaded in
`cmd/main.go` and never used. Add `budget.go` with a `rungCtx` that gives each
rung `min(perRung, remaining - reserve)`, records `deadline_exhausted` and
skips a rung it cannot afford, and holds `CHAIN_RESERVE` (new, default 150ms)
back so the terminal rules rung and the response marshal always fit inside the
caller's deadline.

**A3. Hop result vocabulary.** Replace the three bare string literals in
`chain.go` with named constants in `provider.go`, and classify the failure:
`errors.Is(err, context.DeadlineExceeded)` is a timeout, anything else is an
error. See the implementation doc for the full constant list.

## 3. The trap that will cost you an hour if you miss it

`rungCtx` must do its arithmetic against `ctx.Deadline()` and `time.Until`,
**not** against an injected `clock.Clock`. This looks like a violation of
`ENGINEERING.md` section 2 ("never call `time.Now()` in business logic") and
is not: context deadlines are wall-clock by construction. A `clock.Fake`
driving the budget while a real `context.Context` drives the cancellation
gives you a rung that either never times out or always does, depending on
which one the test advanced. Unit D's breaker cooldown *does* take the
injected clock, because it measures an interval of its own. These are
different and the difference is the point.

## 4. Explicitly out of scope

Do not build these. Each is another unit and building it here makes an
unreviewable PR that blocks somebody.

| Thing | Where it belongs |
|---|---|
| Any real provider (Groq, Gemini) | Unit B |
| The circuit breaker | Unit D |
| `HopCircuitOpen` / `HopRateLimited` producers | Unit D (define `HopCircuitOpen` as a constant, leave it unused) |
| Editing `proto/common/v1/common.proto` to extend the result vocabulary comment | Unit E's proto PR. A comment-only proto change still trips CI's "generated code is up to date" job and would turn this into a proto PR for no benefit. |
| `ComposeNudge` | Phase 5 |

## 5. Files

**You own**: `services/classifier/internal/provider/*.go` (including a new
`budget.go`), `services/classifier/cmd/main.go`, `.env.example`.

**Do not touch**: anything under `services/` other than `classifier/`,
`proto/`, `migrations/`, `internal/platform/`, `test/`. If you believe you
need a change in one of those, **stop and say so** rather than making it
(`services/classifier/AGENTS.md`).

`.env.example` is **not** union-merged and Units B and H also add keys to it.
Keep your diff to the lines you actually need.

## 6. Definition of done

`docs/ENGINEERING.md` section 11, plus the Unit A list in
`docs/PHASE3_IMPLEMENTATION.md`. The ones most often skipped:

- Five construction tests: empty list, unknown name, rules absent, rules not
  last, rules listed twice. All must fail at `NewChain`, none at request time.
- A rung that blocks on `<-ctx.Done()` is cut off at `LLM_TIMEOUT`, recorded
  as a timeout hop, and the next rung still runs.
- **The Flaw 3 test**: a chain whose per-rung budgets sum to more than the
  inbound deadline still returns a valid rules answer, inside the deadline,
  with a `deadline_exhausted` hop for the skipped rung. This is the test that
  closes the DLQ path and is the reason the unit exists. Do not ship without
  it.
- `LLM_TIMEOUT` and `CHAIN_RESERVE` justified in the PR with an explicit
  arithmetic budget against `CALL_TIMEOUT` (5s) and `PRD.md` section 10's 3s
  p95 target.
- Metric export is deliberately deferred to Phase 4 (`ARCHITECTURE.md`
  section 13: shared interceptor, not per-service wiring). Say so in the PR
  rather than quietly ticking the box.

## 7. Prove your test can fail

Non-negotiable, and it is item one on the list of things that have actually
gone wrong in this repo (`docs/INCIDENTS.md` has five entries about green
that was not evidence).

Set `reserve` to 0, confirm the Flaw 3 test goes red with the answer arriving
after the deadline, revert, and **paste the real output into the PR**.

## 8. Verify

```bash
gofmt -l . && go vet ./...
go test -race -count=1 ./services/classifier/...
go test -count=1 -race -tags='integration e2e' ./...   # needs the stack, see below
```

The last one needs `make up` and `make migrate-up`. **Read the shared-stack
rules in `docs/PHASE3_IMPLEMENTATION.md` "Shared hazards" before running it**:
there is one docker stack on this machine and every worktree shares it. Never
run `make down-clean`. Coordinate before running the tagged tiers, because
another agent may be mid-run.

All six `test/e2e/` tests must be **unchanged and green**. The default chain
is still `rules`, so single-rung behaviour must be byte-identical. If an e2e
test needed editing, you have changed behaviour you were not asked to change.

## 9. Commit and PR

- Branch `svc/classifier/chain-hardening`, already checked out in your worktree.
- Small commits, conventional prefixes (`feat(classifier):`, `test(classifier):`).
- **No AI attribution** in commit messages or PR descriptions. No
  `Co-Authored-By`, no "Generated with". Check before pushing:
  `git log -1 --format=%B | grep -i "claude\|codex\|copilot\|co-authored\|generated with"`
  should exit 1.
- Update `docs/PHASE3_IMPLEMENTATION.md`'s Unit A status and add a
  "What actually shipped" section if reality diverged from the LLD. It
  usually does, and writing down where is the most useful thing you leave
  behind.
- Log anything that cost you real time to `docs/INCIDENTS.md` (union-merged,
  append freely).
