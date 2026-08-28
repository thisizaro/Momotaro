# Unit E (PRs 1 and 2 only): the provider-hops migration and proto change

**Branch**: `infra/audit-entry-provider-hops`, then `proto/audit-entry-hops`
**Worktree**: `../Momotaro-unit-e`
**Depends on**: nothing. Start immediately.
**Docker stack needed**: yes, briefly, to apply and roll back the migration.

> **Scope fence, read this first.** Unit E is three sequenced PRs. **You are
> doing the first two only: the migration, then the proto change.** The third
> PR (decision-engine and audit service code) is held back because it touches
> `services/decision-engine/internal/engine/store.go`, which Unit F owns right
> now. Do not write service code. Stop after the proto PR is green.

---

## 1. Read before you write code

1. `AGENTS.md` (repo root).
2. `docs/PHASE3_IMPLEMENTATION.md`, **Unit E** for the full design and
   **Flaw 1** for why this exists. That document is the spec; this file is the
   brief.
3. `docs/ARCHITECTURE.md` **section 12a** (migrations: additive only, own PR,
   merged before dependent code) and **section 9** (proto changes: own PR,
   merged before dependent code, `buf breaking` enforced in CI).
4. `migrations/00004_record_state_ev_snapshot.sql` as the format and
   comment-density example to match.
5. `proto/audit/v1/audit.proto` (`AuditEntry`) and
   `proto/common/v1/common.proto` (`ProviderHop`, around line 128).

## 2. Why this is two PRs and not one

CI mechanically enforces it. The `proto` job fails any PR that mixes proto and
service changes ("proto/gen is stale or this PR mixes proto and service
changes"), and `ARCHITECTURE.md` section 12a requires a migration to merge
before anything depending on it. Trying to combine them wastes a CI cycle.

## 3. PR 1: the migration

`migrations/00005_audit_entry_provider_hops.sql`, goose format, additive only.

```sql
ALTER TABLE audit_entry ADD COLUMN provider_hops TEXT;
```

**TEXT, not JSONB**, and the comment in the migration should say why: every
enum in this schema is already stored as TEXT (`record_state.current_state`,
`pending_action`, `intervention_attempt.action_type`, `outcome`,
`audit_entry.source`), and both halves of a hop are closed vocabularies once
Unit A lands. The encoding is `provider:result` pairs joined by `,`, for
example `groq:timeout,gemini:ok`, which stays readable in `psql` without a
JSON operator. That matters for a project whose pitch is an auditable trail.

Write a real `-- +goose Down` that drops the column with `IF EXISTS`, matching
`00004`.

The migration comment should also record the two semantics decisions so the
next reader does not have to infer them from code:

- **NULL means no classification happened**, never the empty string. Those are
  different facts.
- The column will be written on **every step** of a multi-step transition, the
  same way `store.scheduleNew` already writes `rationale` and `source` on every
  step rather than only the last. Uniformity beats tidiness here: a caller
  reading any single entry should get the same picture `rationale` gives.

**Verify both directions** against the live stack:

```bash
make up && make migrate-up && make migrate-status
# then prove the down direction works, and re-apply
```

Read the shared-stack rules in `docs/PHASE3_IMPLEMENTATION.md` "Shared
hazards" first. **Never run `make down-clean`**: it destroys volumes other
worktrees are using. Note that a migration you apply is applied for everyone
on this machine, which is exactly why this PR goes first.

## 4. PR 2: the proto change

Branch off main again after PR 1 merges.

Add to `AuditEntry` in `proto/audit/v1/audit.proto`:

```proto
  // Every classifier provider rung actually attempted for the classification
  // behind this transition, in order. Empty when no classification happened.
  repeated common.v1.ProviderHop hops = 11;
```

Field number 11 (10 is `message_text`). Additive, so `buf breaking` passes.

Also extend the `ProviderHop.result` comment in
`proto/common/v1/common.proto` to name the full vocabulary Unit A introduces:
`"ok"`, `"error"`, `"timeout"`, `"schema_invalid"`, `"circuit_open"`,
`"rate_limited"`, `"deadline_exhausted"`. It is a comment-only change and it
is carried here deliberately, because a comment-only proto edit still trips
CI's "generated code is up to date" job and Unit A was told not to make itself
into a proto PR over it.

Then regenerate and commit the output:

```bash
make tools        # if you do not have buf pinned locally yet
cd proto && buf generate && cd ..
make proto-lint
make proto-breaking
git status --short proto/gen    # the regenerated files must be committed
```

CI regenerates and diffs `proto/gen`, so an uncommitted regeneration fails the
build.

## 5. Explicitly out of scope

| Thing | Where it belongs |
|---|---|
| `services/decision-engine/internal/engine/store.go` writing the column | Unit E PR 3, after Unit F merges |
| `services/audit/internal/server/` reading it back, `hops.go` encode/decode | Unit E PR 3 |
| Any test that needs the column to be populated | Unit E PR 3 |
| Changing how the chain produces hops | Unit A |

If you finish both PRs and want more, say so rather than starting PR 3. The
collision with Unit F is real and merge conflicts in `store.go` cost more than
the wait.

## 6. Files

**You own**: `migrations/00005_audit_entry_provider_hops.sql`,
`proto/audit/v1/audit.proto`, `proto/common/v1/common.proto`, `proto/gen/**`.

**Do not touch**: anything under `services/`, `test/`, `internal/platform/`.

## 7. Definition of done

- Migration applies cleanly, and the down direction is exercised, not just
  written. `make migrate-status` clean afterward.
- `buf lint` and `buf breaking` both pass. `proto/gen` regenerated and
  committed in the same PR.
- `go build ./...` and `go test -race ./...` still green. Nothing consumes the
  new field yet, so this should be uneventful; if it is not, something else is
  wrong and worth reporting.
- Both PRs merged to main, in order.

## 8. Prove it can fail

There is no behavioural test to break here, so the honest equivalent is:

- Deliberately write the migration without the `Down` direction, confirm your
  rollback check actually notices, then fix it.
- Deliberately leave `proto/gen` unregenerated and confirm CI's
  "generated code is up to date" step fails. This is worth doing once because
  it is the check most likely to bite a later unit.

Paste both into the PR.

## 9. Commit and PR

- Two branches, merged in order: `infra/audit-entry-provider-hops`, then
  `proto/audit-entry-hops`.
- **No AI attribution** in commits or PR descriptions. Verify with
  `git log -1 --format=%B | grep -i "claude\|codex\|copilot\|co-authored\|generated with"`
  (should exit 1) before pushing.
- Note in `docs/PHASE3_IMPLEMENTATION.md` Unit E which PRs are done and that
  PR 3 remains open.
