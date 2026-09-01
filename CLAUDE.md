# CLAUDE.md (temporary, delete when Phase 5.5 is done)

Working notes for the session running Phase 5.5 overnight on 2026-08-31.
The user is asleep and will review in the morning. This file exists so a
context compaction does not lose the rules.

## Standing rules, non-negotiable

1. **NO EM DASHES.** Not in code, comments, commit messages, PR bodies, docs,
   or replies. Organisation-level rule from the user, verbatim: "NO EM DASHES
   ALLOWED!! I dont wanna see a single one."
2. **No AI attribution anywhere.** No `Co-Authored-By: Claude`, no "Generated
   with", no equivalent trailer, in any commit message or PR description.
   Overrides tooling defaults. See `docs/ENGINEERING.md` section 10.
3. **TDD.** Test first, watch it fail, then implement. Every bug fix gets a
   regression test that is proven to go red against the old behaviour.
   `docs/ENGINEERING.md` section 1.
4. **Verify, do not assume.** A cached `ok` is not evidence. Run
   `make check` and, for anything touching the pipeline, the tagged suite.
   Several incidents in `docs/INCIDENTS.md` exist because someone trusted an
   exit code.

## Read these before working

| Doc | Why |
|---|---|
| `AGENTS.md` | ownership boundaries, branching, testing tiers |
| `docs/ENGINEERING.md` | mandatory coding standards, Definition of Done (section 11) |
| `docs/PLAN.md` | the live checklist; tick your own boxes |
| `docs/PHASE5_5_IMPLEMENTATION.md` | the units being built now |
| `docs/INCIDENTS.md` | what already broke; append when something breaks |
| `docs/DECISIONS.md` | append load-bearing decisions |

## Current task

Phase 5.5, in this order: **U, then V, then W.** Then **AA and AB** if time
allows. Full detail per unit in `docs/PHASE5_5_IMPLEMENTATION.md`.

- **U** dead-letter unprocessable records instead of crashing (correctness,
  blocks everything operationally)
- **V** extract batchgen generation logic into an importable package
- **W** `/v1/demo/*` control API, flag-gated (needs V)
- **AA** surface `due_at` through the stack with a live countdown
- **AB** timeline view (needs AA, frontend only)

Not tonight: X, Y, Z.

## How to work

- **Sonnet subagents only**, maximum 3 at a time, each in its own worktree
  (`isolation: "worktree"`). The supervising session is on Opus and should
  supervise rather than implement, to keep cost down.
- One unit per agent, one branch per unit, PR per unit, merge only when CI is
  green.
- Agents run **unit tests** in their worktree. The supervisor runs the tagged
  integration suite at merge time, because parallel integration runs share one
  Postgres and Kafka and interfere (see `docs/INCIDENTS.md` 2026-08-23).

## Running the stack

```bash
make demo-up PROFILE=demo      # infra + migrations + all 9 services
make batchgen COUNT=100 SEED=7 # seeded batch with hidden ground truth
make demo-down                 # stop services
```

**`PROFILE=demo` is required.** Sourcing `configs/demo.env` does nothing: the
Makefile's `include .env` outranks the environment. See `docs/INCIDENTS.md`
2026-08-31.

**Do not run `make test-integration` while a demo stack is live.** The tests
share `raw.events` and their cleanup deletes rows, which poisons the running
consumer. That is the bug Unit U fixes.

## Known broken right now

The decision engine crash-loops on orphaned Kafka messages left by an earlier
test run. Until Unit U lands, a wedged stack needs `make down-clean` and a
fresh start.

## Revert point

`git reset --hard pre-phase-5.5` returns to the state the user approved before
going to sleep. Tag is pushed to origin.

## Reference numbers, for checking a run is healthy

`make batchgen COUNT=100 SEED=7` on a fresh stack should give roughly:
net Rs 536,405 against a baseline of Rs 487,769, recovery rate 51%,
classification accuracy 91%, and zero recovery-window escalations. A recovery
rate near 18% with most records escalated means the profile did not apply.
