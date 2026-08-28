# Phase 3 agent briefs

One file per unit that is ready to be handed to an agent in its own worktree.
`docs/PHASE3_IMPLEMENTATION.md` is the spec for all eight units; these are the
task briefs for the ones that can run **right now, in parallel, with zero file
overlap**.

## Ready now (wave 1)

| Brief | Unit | Branch | Worktree | Needs the docker stack? |
|---|---|---|---|---|
| [UNIT_A.md](UNIT_A.md) | A, chain hardening | `svc/classifier/chain-hardening` | `../Momotaro-unit-a` | no |
| [UNIT_E.md](UNIT_E.md) | E, PRs 1 and 2 only (migration, proto) | `infra/audit-entry-provider-hops` | `../Momotaro-unit-e` | briefly |
| [UNIT_F.md](UNIT_F.md) | F, classify history | `svc/decision-engine/classify-history` | `../Momotaro-unit-f` | yes |

These three share no file. That is not a coincidence, it is the constraint
that decided the split: three of the eight units touch
`services/decision-engine/internal/engine/`, so the safe fan-out is narrower
than eight suggests.

## Deliberately held back

| Unit | Why it is not here yet |
|---|---|
| E, PR 3 (service code) | touches `store.go`, which Unit F owns until it merges |
| H (`LLM_SAMPLE_RATE`, config profiles) | touches `clients.go`, which Unit F owns until it merges |
| B (the real provider rung) | needs A merged; also the judgment-heavy one (prompt design, injection surface, two vendors' schema modes, a timeout that must be measured), so it is not a good fan-out candidate |
| C, D, G | all need B merged |

When wave 1 lands, E-PR3 and H become two more parallel briefs.

## Rules that apply to every brief here

1. **There is one docker stack on this machine and every worktree shares it.**
   `docker-compose.yml` hardcodes the project name and host ports. **Never run
   `make down-clean`**: it destroys volumes another agent is using. `make up`
   and `make migrate-up` are safe and idempotent. Coordinate before running
   the `integration` or `e2e` tiers, because the Decision Engine's scheduler
   polls `record_state` system-wide and two simultaneous runs can claim each
   other's rows.
2. **Stay inside the files your brief says you own.** Every brief has a
   "do not touch" list. If you need a change outside it, stop and say so.
   That is `AGENTS.md`'s rule, not a formality: another agent is probably
   working there right now.
3. **Prove your test can fail.** Break the code on purpose, confirm red,
   revert, paste the real output. `docs/INCIDENTS.md` has five entries about
   green that was not evidence, which is more than any other category.
4. **Run the full tagged suite twice**, not just `go test ./...`. The untagged
   run skips the `integration` and `e2e` tiers silently, and Phase 2 shipped
   three bugs that only the tagged run caught.
5. **No AI attribution** in commit messages or PR descriptions. Check with
   `git log -1 --format=%B | grep -i "claude\|codex\|copilot\|co-authored\|generated with"`
   before pushing; it should exit 1.
6. `docs/PLAN.md`, `docs/DECISIONS.md` and `docs/INCIDENTS.md` are
   union-merged, so append to them freely.
   **`docs/PHASE3_IMPLEMENTATION.md` is not**, so two agents editing their own
   unit's status there will conflict. Merge locally, never through GitHub's
   web UI, which does not apply the union driver.
