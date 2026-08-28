# Unit F: populate `ClassifyRequest.history` and `instrument_history`

**Branch**: `svc/decision-engine/classify-history`
**Worktree**: `../Momotaro-unit-f`
**Depends on**: nothing. Start immediately.
**Docker stack needed**: yes, for the integration tier.

---

## 1. Read before you write code

1. `AGENTS.md` (repo root) and `services/decision-engine/AGENTS.md`.
2. `docs/PHASE3_IMPLEMENTATION.md`, **Unit F** for the LLD and **Flaw 5** for
   why this is not optional. That document is the spec; this file is the
   brief.
3. `services/classifier/SPEC.md` **section 3** ("What the caller actually
   sends today"). It documented this exact gap during Phase 1, said "do not
   fix this by populating history yourself", and named the Decision Engine as
   the right owner. Section 10 item 1 raised it as a cross-service item to
   pick up later. You are later.
4. `docs/ARCHITECTURE.md` section 10a (table ownership: the Decision Engine is
   a permitted *reader* of `intervention_attempt`, which the Executor owns).
5. The code: `services/decision-engine/internal/engine/clients.go`,
   `store.go` (particularly `loadAttemptHistory`, around line 303), and
   `proto/classifier/v1/classifier.proto`'s `ClassifyRequest`.

## 2. Why this matters, in one paragraph

`clients.classify` builds `&classifierv1.ClassifyRequest{Record: record}` and
nothing else, so `history` and `instrument_history` have been empty since
Phase 1. The classifier's rules engine reads `failure_code` and `type`. That
means when Unit B puts a real model behind the same request, **the model will
receive exactly the inputs the lookup table receives** and produce a value
from exactly the same closed vocabulary of seven buckets. It cannot add
information the table does not already encode; it can only add latency, cost
and variance. This unit is what gives the reasoning layer something to reason
about. Without it, Unit B is decorative.

## 3. What to build

Two new queries in `store.go`:

```go
// oldest first, per the proto's own contract
func (s *store) loadAttemptRows(ctx, recordID) ([]*commonv1.InterventionAttempt, error)

// attempts on OTHER records sharing this instrument, most recent first, capped
func (s *store) loadInstrumentHistory(ctx, instrumentRef, excludeRecordID string, limit int) ([]*commonv1.InterventionAttempt, error)
```

Then thread both onto the request in `clients.classify`.

Five things that are easy to get wrong:

- **`loadAttemptHistory` is not reusable here.** It returns aggregate counts
  and a cooldown timestamp for the guardrails, not rows. Write new queries.
- **Do not merge the two paths.** The guardrails need counts; the classifier
  needs rows. One query serving both would couple a compliance check to a
  prompt-shaping read, and the guardrail counters are deliberately derived
  rather than cached (`docs/DECISIONS.md` 2026-08-24).
- **Cap the instrument query at 10, most recent first.** A popular instrument
  accumulates rows indefinitely, and an unbounded read is both a growing
  per-record query and a growing prompt. Say `10` in a comment, do not leave
  it as a magic number.
- **`instrument_ref` is nullable.** Empty means skip the query entirely, not
  query for `''`. `record_instrument_idx` (migration `00001`, line 34) is a
  partial index on `instrument_ref IS NOT NULL`, so it is already indexed. No
  migration needed.
- **Exclude the record's own rows from `instrument_history`.** They are
  already in `history`, and duplicating them tells the model the same fact
  twice with more weight.

## 4. Two things not to change, so they are not re-litigated mid-branch

**Do not add `failure_code` to the `InterventionAttempt` proto message.** The
column exists on the table but the proto message has five fields and none is
it. Action type, outcome and timestamp are enough to separate "this rail is
flaky right now" from "this card is dead", and adding a field would serialise
this unit behind a proto PR for no gain.

**The trust model does not move.** Giving the model history does not touch a
single guardrail. `ARCHITECTURE.md` section 5's last bullet still holds: caps,
cooldowns and escalation are enforced in the Decision Engine *after* the
classifier answers and are never influenced by what it recommended. You are
widening an input, not moving a decision.

## 5. Files

**You own**: `services/decision-engine/internal/engine/store.go`,
`clients.go`, `engine.go`, and their tests.

**Do not touch**: `proto/`, `migrations/`, `internal/platform/`, any other
service. Unit E is working in `migrations/` and `proto/` right now and Unit A
is in `services/classifier/`. If you think you need a change there, **stop and
say so**.

Note for the reviewer's benefit: Unit E's third PR will also touch
`store.go` (adding a column write in `scheduleNew`). It is deliberately
sequenced after you.

## 6. Definition of done

- Integration test: a record with two prior attempts produces a
  `ClassifyRequest` carrying both, **oldest first**, with the right action
  type and outcome on each.
- A record whose instrument has attempts on two *other* records receives
  those, and its own rows do not appear in `instrument_history`.
- `instrument_ref` NULL sends an empty `instrument_history` and issues no
  second query.
- The cap holds, proven by seeding more than the cap.
- **All six `test/e2e/` tests green and unchanged.** The rules engine ignores
  both fields, so nothing observable changes yet. That is the point: F is safe
  to merge before B. If an e2e test needed editing, something is wrong.
- `services/classifier/SPEC.md` section 3 updated. It currently tells the next
  agent that both fields are always empty, which stops being true here.
- Report the added query cost in the PR. Two extra indexed reads per record on
  the classify path; at `PRD.md` section 10's 50 records/sec that is 100 extra
  reads per second. Measure it, state it. If it is material, the instrument
  query is the one to cache, not the per-record one.

## 7. Prove your test can fail

Assert the attempt count, then seed a third attempt and confirm the test goes
red **at the old count**. Do the same for the cap by seeding cap+1. Paste the
real failure output into the PR.

This repo has five `docs/INCIDENTS.md` entries about tests that were green for
the wrong reason. The cheap defence is mechanical and it is not optional.

## 8. Verify

```bash
gofmt -l . && go vet ./...
go test -race -count=1 ./services/decision-engine/...
go test -count=1 -race -tags='integration e2e' ./...
```

The last two need `make up` and `make migrate-up`.

**Read the shared-stack rules in `docs/PHASE3_IMPLEMENTATION.md` "Shared
hazards" before running the tagged tiers.** There is one docker stack on this
machine and every worktree shares it. **Never run `make down-clean`.** The
Decision Engine's scheduler polls `record_state` system-wide by design, so two
agents running integration tests at the same time can claim each other's
seeded rows. Coordinate before a tagged run.

Run the tagged suite **twice**. `docs/INCIDENTS.md` 2026-08-26 and 2026-08-27
are both bugs that a single green run did not surface.

## 9. Commit and PR

- Branch `svc/decision-engine/classify-history`, already checked out.
- **No AI attribution** in commits or PR descriptions. Verify with
  `git log -1 --format=%B | grep -i "claude\|codex\|copilot\|co-authored\|generated with"`
  (should exit 1) before pushing.
- Update `docs/PHASE3_IMPLEMENTATION.md`'s Unit F status, and add a
  "What actually shipped" section if reality diverged from the LLD.
- Log anything that cost real time to `docs/INCIDENTS.md` (union-merged).
