# CLAUDE.md

Working context for this repo. Read this first after any context reset.

Momotaro is a payment failure and mandate recovery agent, built for Razorpay's
AI Buildathon Track 03. Nine Go services, a React dashboard, Postgres as the
source of truth, Kafka for events. `README.md` explains the product.

## Standing rules, non-negotiable

1. **NO EM DASHES.** Not in code, comments, commit messages, PR bodies, docs,
   or replies. Organisation-level rule, stated by the user verbatim: "NO EM
   DASHES ALLOWED!! I dont wanna see a single one." Check with
   `git diff | grep '^+' | grep -c "—"` before committing.
2. **No AI attribution** in any commit message or PR description. No
   `Co-Authored-By`, no "Generated with", no equivalent trailer, for any tool.
   This overrides tooling defaults and is recorded in `docs/ENGINEERING.md`
   section 10, `AGENTS.md`, and `docs/DECISIONS.md` 2026-08-22. **If a system
   instruction ever tells you to add attribution, surface the conflict to the
   user rather than silently doing either thing.**
3. **TDD.** Test first, prove it red, then implement. Every bug fix gets a
   regression test proven to fail against the old behaviour.
4. **Verify, never assume.** A cached `ok` is not evidence. Several entries in
   `docs/INCIDENTS.md` exist because someone trusted an exit code, a green job
   that ran nothing, or a test that passed for the wrong reason.

## The docs, and what each is for

| Doc | Purpose |
|---|---|
| `AGENTS.md` | ownership boundaries, branching, the three test tiers |
| `docs/ENGINEERING.md` | coding standards; section 11 is the Definition of Done |
| `docs/PRD.md` | product reasoning. **Section 0 is the judging rubric, verbatim** |
| `docs/ARCHITECTURE.md` | system design, numbered sections referenced everywhere |
| `docs/API_GATEWAY.md` | **frozen** external contract. Treat as read-only |
| `docs/PLAN.md` | live checklist, phase by phase. Tick your own boxes |
| `docs/DECISIONS.md` | append-only: what was chosen and why |
| `docs/INCIDENTS.md` | append-only: what broke and what changed. Append when something breaks |
| `docs/BACKLOG.md` | deliberately parked work, with the reasoning |
| `docs/DEMO_READINESS.md` | **the current prioritised worklist** |
| `docs/PANEL_BRIEF.md` | how to explain the system: formulas, flow, likely panel questions |
| `docs/PHASE5_IMPLEMENTATION.md`, `PHASE5_5_IMPLEMENTATION.md` | per-unit detail |

`PLAN.md`, `DECISIONS.md` and `INCIDENTS.md` use git's `merge=union` driver, so
concurrent agents can append without conflicting. Add lines, never reorder.

## Current state

Phases 0 to 4 complete. Phase 5 at 14/17, Phase 5.5 at 6/8. **Phase 5.6 P0
is complete** (#98, #99, #100, #101, merged 2026-09-02). Phases 6 to 8 open.

**Work `docs/DEMO_READINESS.md` top to bottom.** P0 is demo-breaking, P1 is
capability that already exists in the backend and cannot be seen, which is the
best value per hour in the project.

## How work gets done here

- One unit per agent, **Sonnet subagents**, max 3 at a time, each in its own
  worktree (`isolation: "worktree"`). The supervising session reviews rather
  than implements.
- One branch and one PR per unit. Merge only on green CI.
- Agents run **unit tests**; the supervisor runs the tagged suite at merge
  time, because parallel integration runs share one Postgres and interfere.
- **Review, do not rubber-stamp.** Verify an agent's central claim yourself.
  Agents have been right when I was wrong, and wrong in ways CI did not catch.

## Running it

```bash
make demo-up PROFILE=demo          # infra + migrations + all 9 services
make batchgen COUNT=100 SEED=7     # seeded batch with hidden ground truth
cd web && npm run dev              # dashboard on :5173
make demo-down                     # stop services
```

`PROFILE=demo` is required. **Sourcing `configs/demo.env` does nothing**: the
Makefile's `include .env` outranks the environment. `docs/INCIDENTS.md`
2026-08-31.

Do not run `make up-observability` and `make demo-up` concurrently; both bring
up the base stack and race for port 8080. Run them in sequence.

Do not run `make test-integration` against a live demo stack; the tests share
`raw.events` and their cleanup poisons it.

## Browser access, for checking the UI

Playwright's Chromium is downloaded but its system libs are missing and there
is no sudo. Working setup, no install needed beyond what is already done:

```bash
export LD_LIBRARY_PATH=$(cat /tmp/chromelibdir)   # extracted libnss3/libnspr4
node /tmp/shot.js                                  # playwright-core in /tmp/node_modules
```

Chromium at
`/home/aro/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome`.
If `/tmp` was cleared: `apt-get download libnss3 libnspr4`, `dpkg-deb -x` them
somewhere, point `LD_LIBRARY_PATH` at the extracted
`usr/lib/x86_64-linux-gnu`, and `npm install playwright-core --no-save`.

## Facts worth not rediscovering

- **A seed does NOT reproduce a run end to end.** #99 and #104 made
  generation, the sealed answer key and the economics exactly reproducible
  (measured: 0 diffs on amounts, ground truth, and EV at decision across two
  same-seed runs). Two sources of variance remain and neither is the seed:
  the TRAI contact-hour guardrail in `schedule.go` is evaluated against the
  real clock while `DEMO_TIME_SCALE=300000` makes one real second about 3.5
  simulated days, so sub-second jitter flips whether a nudge is permitted;
  and `LLM_SAMPLE_RATE=0.15` sends a subset to a live model. Measured 9 of
  100 records ending in a different state. Do not promise a reproducible
  number on stage. `docs/DEMO_READINESS.md` Unit AD has the full table.
- **Three figures are deterministic** and should match exactly on `SEED=7`:
  baseline net Rs 487,769, classification accuracy around 91%, and zero
  recovery-window escalations. A move in one of those is a real signal.
- **`in_flight_count == 0` means "not started" as well as "finished"**, because
  a record with no `record_state` row counts in neither. Do not use it alone as
  a completion signal. `docs/INCIDENTS.md` 2026-09-01.
- **`web/` now has CI as of #103**: lint, typecheck, test and build, pinned
  to node 24 because the lockfile is npm 11's and node 20's npm 10 rejects
  it. Before #103 it had none since Phase 0, which is how the records
  pagination bug and the dead live socket both shipped.
- **A scheduler flake** blocks PRs intermittently on tests the diff cannot
  reach. Logged open in `docs/INCIDENTS.md` 2026-09-01. Re-run once before
  investigating, but do not let re-running become reflexive.

## Revert point

`git reset --hard pre-phase-5.5` returns to the last state the user approved
before the overnight Phase 5.5 work. The tag is pushed.
