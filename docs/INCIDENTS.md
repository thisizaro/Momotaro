# Incident log (Momotaro)

**What broke, and what we did about it.** Append-only, newest at the bottom.
Any agent may append directly, this file uses git's `merge=union` driver
(see `.gitattributes`) so concurrent appends merge cleanly.

Log it here when something **actually broke**: a bug that cost real time, a
design assumption that turned out wrong, a deploy or merge that went
sideways, a test that was passing for the wrong reason. This is distinct
from `docs/DECISIONS.md`, which records what we *chose*; this records what
we *got wrong* and what we changed as a result.

Write it while it is fresh. Reconstructing this the night before a demo
produces a list of vague regrets rather than anything useful.

## Why this file exists

Partly engineering hygiene: a team that writes these stops repeating
mistakes. Partly because it is directly assessed. The hackathon's judging
criteria include **"Failure recovery: what broke, and what you did about
it"**, and the honest, specific version of that story is far more convincing
than a claim that nothing went wrong. Nothing going wrong across nine
services means nobody pushed hard enough.

## Format

```markdown
### YYYY-MM-DD, [short title]
**What happened:** [symptom, as observed]
**Root cause:** [what was actually wrong, once known]
**Fix:** [what changed]
**Prevention:** [test added, doc updated, rule changed. "Nothing" is a
valid but suspicious answer.]
```

Keep each one to a few lines. A dozen short honest entries beat two essays.

## Entries

<!-- Append new entries at the bottom. -->

### 2026-08-22, bitnami/kafka:3.9 does not exist
**What happened:** `docker compose up` failed with `failed to resolve
reference "docker.io/bitnami/kafka:3.9": not found`.
**Root cause:** Bitnami restructured their container catalog during 2025 and
withdrew the freely-pullable tags many tutorials still reference. The tag was
never going to resolve.
**Fix:** Switched to the official `apache/kafka:3.9.0` image. Its env vars use
`KAFKA_*` rather than Bitnami's `KAFKA_CFG_*`, and the CLI tools live under
`/opt/kafka/bin/`, so the healthcheck and topic-init command changed too.
**Prevention:** Prefer official upstream images over vendor rebuilds for
infrastructure. Vendor repackaging is a dependency on someone else's
business model, not just their engineering.

### 2026-08-22, kafka-init could not reach the broker it was healthchecking
**What happened:** Kafka reported healthy, but `kafka-init` retried
`Connection to node 0 (localhost/127.0.0.1:9092) could not be established`
forever and created no topics.
**Root cause:** A single listener advertising `localhost:9092`. In-network
clients connect to `kafka:9092`, the broker replies "reconnect to
localhost:9092", and inside the `kafka-init` container localhost is itself.
The broker's own healthcheck passed because for it localhost genuinely was
the broker.
**Fix:** Two listeners. `INTERNAL://kafka:29092` for in-network clients
(kafka-init, kafka-ui), `EXTERNAL://localhost:9092` for host clients
(services run from an IDE). Inter-broker traffic uses INTERNAL.
**Prevention:** Documented both addresses inline in `docker-compose.yml`. Any
new in-network Kafka client must use `kafka:29092`, and a healthcheck that
only proves the broker can reach itself proves very little.

### 2026-08-22, migration created every table then dropped them all
**What happened:** Applying `00001_initial_schema.sql` with
`psql -f` reported nothing but `CREATE TABLE`/`CREATE INDEX` and exited 0,
yet the database was empty afterwards.
**Root cause:** Two compounding mistakes. The migration file carries
`-- +goose Up` and `-- +goose Down` annotations, which are goose directives
and just comments to psql, so psql ran both halves: create everything, then
drop everything. And the check that "proved" it worked was
`... | grep ... | head; echo "exit: $?"`, where `$?` is the exit status of
`echo`, not psql, so a failure would have looked identical.
**Fix:** Added `scripts/migrate`, which uses goose as a pinned library rather
than a separately installed binary, so every agent runs the identical
migrator with no extra install. Verified the resulting schema by querying
`pg_tables`, `pg_indexes` and `pg_constraint` rather than trusting command
output.
**Prevention:** Never apply migrations with psql; use `make migrate-up`.
Stated in the command's doc comment. More generally: `$?` after a pipeline
reports the last command, so use `${PIPESTATUS[0]}` or check the artifact
rather than the exit code. Verifying the effect beats verifying the command.

### 2026-08-22, migration runner hung with no output
**What happened:** `go run ./scripts/migrate up` produced no output and had
to be killed at the 120s timeout.
**Root cause:** `goose.RunContext(nil, ...)`. Goose derives from the context
it is given, and a nil context made it hang instead of returning an error.
**Fix:** A real `context.WithTimeout(context.Background(), 2*time.Minute)`.
**Prevention:** This is `docs/ENGINEERING.md` section 3 exactly, every
IO-doing call takes a real context with a deadline. The bounded timeout also
means a migration wedged on a table lock now fails loudly rather than
blocking CI indefinitely.

### 2026-08-22, dashboard rendered a blank white page
**What happened:** The Bolt-generated dashboard showed nothing but a white
page in the browser.
**Root cause:** `npm install` had never been run, so there was no
`node_modules` and the module graph could not resolve. Nothing to do with the
missing backend, which had been the initial suspicion: the app defaults to its
own mock engine and renders fully with no API at all.
**Fix:** `npm install`. Confirmed with `tsc --noEmit` (clean), `vite build`
(clean), and checking that `/src/main.tsx`, `/src/App.tsx` and
`/src/lib/mockEngine.ts` each returned HTTP 200 from the dev server.
**Prevention:** `npm install` is now the first line of `web/AGENTS.md`'s
running instructions, with the blank-page symptom named explicitly. Also
worth generalising: a white page means the bundle never executed, so suspect
the build or module resolution before suspecting data.

### 2026-08-22, npm 11 silently skipped esbuild's install script
**What happened:** After `npm install`, npm warned
`1 package has install scripts not yet covered by allowScripts: esbuild`.
Vite cannot run without esbuild's platform binary.
**Root cause:** npm 11 blocks lifecycle install scripts by default, a
supply-chain hardening change. The warning is easy to scroll past because the
install otherwise reports success.
**Fix:** In this case the `@esbuild/linux-x64` binary had been placed anyway
and `esbuild --version` worked, so no action was needed. If it had not,
`npm install --foreground-scripts` or approving the script is the fix.
**Prevention:** Read npm's warnings rather than only its exit code. Same
lesson as the migration incident above: a zero exit status is not proof the
thing you wanted actually happened.

### 2026-08-22, dashboard rendered blurred behind an empty white panel
**What happened:** On first load the dashboard content appeared blurred, with
a blank white panel pinned to the right of the viewport showing a loading
spinner. Initially misread as a layout or data problem.
**Root cause:** `RecordDrawer` returned its markup unconditionally. It renders
a `fixed inset-0 backdrop-blur-sm` backdrop and a right-pinned `max-w-lg`
white panel, so both were present on first paint with no record selected. The
backdrop caused the blur, the panel caused the white area, and the spinner was
the drawer's own `loading || !detail` branch. `App.tsx` tracked
`drawerRecordId` but never passed it, so the component had no way to know it
was closed.
**Fix:** Added an explicit `open` prop, passed as `drawerRecordId !== null`,
with an early `return null` when closed. The early return sits after the
`useEffect` so hook order stays stable, and the Escape listener is now only
attached while open.
**Prevention:** A component that renders a fixed-position overlay needs an
explicit open/closed prop; inferring visibility from whether data happens to
be loaded is how you get an overlay with nothing behind it. Worth checking
the other components for the same shape, though `RecordDrawer` was the only
one using `fixed inset-0`.

### 2026-08-22, walking-skeleton integration test would have replayed stale Kafka messages into a foreign-key error

**What happened:** Caught while writing the walking-skeleton integration
test, before it ever ran red in CI. Ingestion's own unit tests
(`services/ingestion/internal/server/server_test.go`) publish to the real
`raw.events` topic (auto-create is disabled cluster-wide, so there is no
"scratch" topic without asking for one) to prove the publish-on-submit path,
then clean up their `batch`/`record` rows afterward. Kafka does not support
deleting individual messages, so those published messages stay in the topic
forever, referencing record ids that no longer exist in Postgres.

**Root cause:** A brand-new Kafka consumer group with no committed offsets
starts from the earliest message in the topic (by design: `kafkax.Consumer`
defaults to `AtStart`, precisely so a fresh Decision Engine deployment never
skips a real record). Had the integration test pointed the Decision Engine
at the shared `raw.events` topic with its real `decision-engine` consumer
group, it would have replayed every one of those historical test messages
first. `Engine.HandleMessage` would try to `INSERT INTO record_state` for a
`record_id` that no longer exists, hit the foreign-key constraint, return an
error, and `kafkax.Consumer.Consume` stops the whole loop on the first
handler error (there is no DLQ yet, deliberately out of scope for the
skeleton) — so the consumer would die before ever reaching the test's actual
message, and the failure would look like "the pipeline doesn't work" rather
than "the test fixture was polluted."

**Fix:** Made the raw.events topic name and the Decision Engine's consumer
group name overridable via `RAW_EVENTS_TOPIC` / `RAW_EVENTS_CONSUMER_GROUP`
env vars (both default to the production names, so normal operation is
unaffected). The integration test provisions a fresh, uniquely-named topic
via a new `kafkax.EnsureTopic` helper and a fresh consumer group per run, so
it can never see another test's leftovers. See `docs/DECISIONS.md`.

**Prevention:** Any test that publishes to a topic with auto-create disabled
and no way to delete individual messages should use an isolated topic, not
the production one, the same way a DB test uses its own rows and cleans them
up. A fresh consumer group defaulting to `AtStart` is the right choice for
Decision Engine in production (never skip a real record on first deploy);
that same default is exactly what makes topic-sharing across test runs
dangerous, so keep the two environments' topics separate rather than trying
to make one default safe for both.

### 2026-08-23, CI failed: infra-dependent tests ran in a job with no infra
**What happened:** After the walking skeleton merged, CI's `build-test` job
failed with `dial tcp 127.0.0.1:5432: connection refused` across six
packages. It passed for everyone locally.
**Root cause:** `build-test` runs `go test -race ./...` on a bare checkout,
and only the separate `integration` job starts docker compose. The skeleton's
`pgx`, `kafkax` and four service test packages connect to real Postgres and
Kafka and call `t.Fatalf` when they cannot, which is correct per
`ENGINEERING.md` §1 ("do not mock what you own"). The job structure simply
did not match the testing policy. Two things hid it locally: the compose
stack was already running, and Go's test cache kept reporting a stale `ok`
even after the stack came down. It only reproduced with `-count=1`.
**Fix:** Put every infra-dependent test file behind `//go:build integration`
so `build-test` runs pure unit tests only, and the `integration` job runs the
rest. Added `make test-integration`, which brings the stack up first.
**Prevention:** A cached `ok` is not evidence. Reproduce CI locally with
`-count=1` and the stack **down**. Same family as the migration incident: a
zero exit code, or a cached pass, is not proof the thing you wanted happened.

### 2026-08-23, the walking-skeleton e2e test was never running in CI
**What happened:** Found while fixing the above. CI's integration job ran
`go test -tags=integration ./...`, but `test/e2e/walking_skeleton_test.go` is
tagged `//go:build e2e`. The tags did not match, so the job reported success
having executed none of it.
**Root cause:** `ci.yml` was written before any e2e test existed and guessed
the tag name. A green job that silently tests nothing is worse than a red
one, because nobody investigates it.
**Fix:** `-tags='integration e2e'`. Verified the e2e test genuinely passes:
one record through all six real service binaries to RECORD_STATE_RECOVERED.
Also moved the integration job to run on pull requests as well as merges,
since with the DB tests now behind a tag a PR could otherwise go green
without any of them running.
**Prevention:** When adding a build tag to tests, grep CI for the tag. A job
whose test count silently drops to zero looks identical to one that passed.

### 2026-08-23, `make help` printed filenames after every target
**What happened:** Immediately after merging the `.env` fix, `make help`
started printing `Makefile:test` instead of `test` for every line.
**Root cause:** `include .env` adds a second entry to `$(MAKEFILE_LIST)`, and
`grep` prefixes output with the filename once given more than one file.
**Fix:** `grep -hE` to suppress filename prefixes.
**Prevention:** Harmless cosmetically, but a good reminder that `include`
changes `MAKEFILE_LIST` for every target that reads it, not just the include
site.

### 2026-08-23, `ci / proto` failed on every PR regardless of diff

**What happened:** The `proto` job's breaking-change-check step failed with
`could not read Username for 'https://github.com': No such device or
address` on pull requests that never touched `proto/` at all, including one
that only touched `internal/platform/interceptors`.

**Root cause:** The workflow step ran
`buf breaking --against "https://github.com/<repo>.git#branch=main,subdir=proto"`,
which tells `buf` to make a **fresh anonymous HTTPS clone** of the repo to
get the comparison history. This repo is private, so that clone has no
credentials and fails immediately, on every PR, independent of what changed.
The step's own `fetch-depth: 0` checkout had already fetched full history
for every branch (including `origin/main`) into the local working copy
specifically so this comparison would not need a network clone at all, but
the `run:` command never used it, it duplicated the check with a broken
remote-clone variant instead of calling the already-correct
`make proto-breaking` target (`buf breaking --against
'../.git#branch=origin/main,subdir=proto'`, a local git reference, no clone,
no credentials needed).

**Fix:** Changed the step to `run: make proto-breaking`, reusing the
Makefile target every contributor already runs locally before pushing.
Verified by running `make proto-lint`, `make proto-breaking`, and `buf
generate` (checking for a `proto/gen` diff) locally in sequence, exactly as
the CI job does, immediately before and after the fix.

**Prevention:** When a CI step needs the same check a Makefile target already
performs, call the target rather than re-deriving the command inline, the
Makefile version is the one that actually gets exercised and fixed when it
breaks locally. More generally: a check that fails identically regardless of
the diff under review is a broken check, not a broken PR, first move should
be reproducing it locally against a diff-free branch before suspecting the
change in front of you.

### 2026-08-23, ConsumeKeyed's own shutdown signal could abort its final commits
**What happened:** An integration test for the new keyed worker pool
(`kafkax.ConsumeKeyed`) closed a consumer, opened a fresh one in the same
group, and expected nothing to be redelivered. One message was, intermittently.
**Root cause:** The commit call for a just-finished record used the same
`ctx` the caller cancels to stop the fetch loop. Cancelling that context to
begin a graceful shutdown could race with, and abort, the `CommitRecords`
call for the record whose completion triggered the shutdown, silently
losing that commit. The first version of the test also raced itself: it
tore down the consumer as soon as handler *call counts* reached the
expected total, which is not the same as the resulting commits having
actually landed.
**Fix:** Commits now run on their own bounded context
(`context.WithTimeout(context.WithoutCancel(ctx), commitTimeout)`), so the
signal that starts shutdown cannot abort the commits shutdown depends on to
not lose progress. Fixed the test to wait for `ConsumeKeyed` itself to
return (which internally waits for every worker via a `WaitGroup`) rather
than for a handler call count, since only the former actually implies the
commit finished.
**Prevention:** For anything with a "finish in-flight work, then stop"
shutdown contract, the operation that must survive shutdown (here, the
final commit) needs a context that outlives the cancellation signal that
triggers it, not the same one. And in tests, synchronise on the real
completion signal (a function actually returning) rather than a proxy for
it (a counter reaching a value), the two are not always the same moment.

### 2026-08-23, Decision Engine scheduler tests flaked on a shared executor call count
**What happened:** `TestSchedulerClaimsDueRetryAndRecordsSuccess` failed
intermittently under `make test-integration` (the whole-repo `go test ./...`
run) with "executor called 2 times, want 1", despite the test only ever
seeding one due record.
**Root cause:** `go test ./...` runs different packages concurrently by
default. The scheduler's `claimDue` query is deliberately unscoped, it polls
`record_state`/`record` system-wide by `due_at`, because that is genuinely
its production job. Running at the same wall-clock moment as
`test/e2e`'s walking-skeleton test, which drives a real decision-engine
binary against the same shared Postgres, let this test's `tick()` also
claim (and execute against its own fake) a record that belonged to the
other package's test entirely.
**Fix:** Stopped asserting the shared fake's global call count in scheduler
tests. Assert only what is scoped to the record_id each test actually
seeded (`current_state`, `attempt_count`, `audit_entry` rows for that id),
which stays correct regardless of what else `claimDue` happens to pick up
in the same tick.
**Prevention:** A query that is intentionally system-wide (no record/batch
scoping) cannot be tested as if it only ever sees this test's own data,
once anything else with write access to the same tables can run
concurrently. Assert on the specific row you seeded, not on a shared
collaborator's total call count, whenever the code under test shares
mutable state with other tests by design.

### 2026-08-23, the invariant verifier rejects every record the pipeline produces
**What happened:** Found by inspection, not by a failing test, while writing
the Executor's Phase 1 spec. `services/audit/internal/server/statemachine.go`'s
`allowedTransitions` map has no `NEW -> RETRY_SCHEDULED` or
`NEW -> NUDGE_SCHEDULED` edge, but the Decision Engine's Phase 1 code writes
`NEW -> RETRY_SCHEDULED` on every classified record. So `VerifyInvariants`
reports a non-zero `impossible_transitions` for the entire normal pipeline
output, an invariant `docs/ARCHITECTURE.md` section 13 says must stay at zero
and alerts on at critical severity. Confirmed against the live database:
6 `NEW -> RECORD_STATE_RETRY_SCHEDULED` rows in `audit_entry`.
**Root cause:** Two things at once. The state machine was written against
`docs/ARCHITECTURE.md` section 7's diagram, where every record passes through
`Scoring` before being scheduled, so `NEW` only ever leads to `SCORING`,
`ESCALATED`, or (temporarily) `RECOVERED`. But `Scoring` is the Phase 2
economics gate and does not exist yet, so Phase 1's Decision Engine schedules
straight out of `NEW`. The Audit verifier and the Decision Engine state
machine were built by different agents and merged within minutes of each
other (PRs #9 and #11), so neither run of `make test-integration` had both
halves present.
**Why no test caught it:** `GetRecordAudit` hardcodes `TrailComplete: true`
rather than computing it, so `test/e2e/walking_skeleton_test.go`'s
`TrailComplete` assertion passes regardless of the trail's actual validity. And
Audit's own `VerifyInvariants` tests seed their own records using transitions
that are already in the allowed map, so they never exercise what the real
pipeline emits.
**Fix:** Not yet applied; recorded here and in `services/executor/SPEC.md`
section 10 so the next agent in either service does not rediscover it or
assume they caused it. Belongs to whoever owns the Audit service: add the two
missing edges carrying the same `TEMPORARY` comment style the existing
`NEW -> RECOVERED` edge already uses (they stop being produced once `Scoring`
lands), and make `TrailComplete` an actual computation over the trail.
**Prevention:** Two lessons. A verifier whose expectations come from the
target design rather than the current phase's behaviour will disagree with a
correct system, so a phase-gated state machine needs its temporary edges
added at the same time the phase's code lands, not after. And a response
field that is hardcoded `true` is not an assertion, it is a comment: an e2e
test checking it proves nothing. Compute it or do not claim it.

### 2026-08-23, a 1-in-4 flaky test failed CI on a docs-only merge
**What happened:** The `integration` job failed on the push run for the
merge of PR #12, a documentation-only change that touched no Go code at all.
The same commit's own PR check had passed minutes earlier, and every one of
the previous eleven CI runs was green. The failure was
`TestConsumeKeyedProcessesDifferentKeysConcurrently`: "the free key's
handler never ran while the blocked key's handler was stuck; pool did not
run concurrently".
**Root cause:** The test was flaky by construction, and had been since it
was written. It published two messages under `uuid.NewString()` keys, then
asserted that blocking one key's handler did not block the other's. But
`ConsumeKeyed` dispatches with `workerFor(key, poolSize)`, which is
`fnv(key) % poolSize`, and the test used a pool of 4. Two unrelated random
keys therefore land on the same worker with probability 1/4, and when they
do the blocked handler parks that worker while the second message sits in
its queue, so the free handler genuinely never runs. The test then fails,
correctly reporting an absence of concurrency that its own key choice
caused. Measured by running it 14 times in a loop: 4 failures, 29%, against
a predicted 25%.
**Why it took a docs PR to surface:** nothing about the failure depended on
the diff, so it was pure luck which run drew the short straw. It also means
the earlier "verified green, ran it multiple times" reports on the
ConsumeKeyed and Decision Engine work were weaker evidence than they
sounded: roughly a 1-in-4 coin was being flipped each run and kept landing
the right way.
**Fix:** The test now picks its two keys by asking `workerFor` for a pair
that routes to different workers, instead of hoping two random ones do, and
takes the pool size from a single constant shared with the `ConsumeKeyed`
call so the two can never drift apart. Also added the unit tests
`workerFor` never had: stability for a repeated key (which is what the
whole per-key ordering guarantee rests on), staying inside `[0, poolSize)`,
and actually spreading across the pool rather than collapsing onto worker 0.
That absence of coverage on a load-bearing four-line function is the reason
the flaw reached CI at all. Verified with 20 consecutive passes of the
previously-flaky test, where the old version would have failed about five
times.
**Prevention:** Two things. A test that asserts *parallelism* must control
the thing that determines parallelism; if work is distributed by
`hash(key) % n`, then the keys are not incidental test fixtures, they are
the fixture that decides whether the test is testing anything. Random
identifiers are the right default for isolation (topics, consumer groups,
record ids) and the wrong default the moment a value feeds a routing or
sharding decision. And when a test fails on a diff that could not possibly
have caused it, the failure is evidence about the *test*, not about the
environment: the first instinct here was to suspect the slower CI runner,
which would have wasted the afternoon tuning timeouts that were never the
problem.

### 2026-08-23, ConsumeKeyed committed offsets out of order, redelivering records
**What happened:** After the flaky-test fix above, the `integration` job kept
failing on every push to `main` while every PR check passed. Three
consecutive merges (#12, #13, #14) and the Classifier's PR all failed the
same way once the first bug was out of the picture:
`TestConsumeKeyedCommitsSoRedeliveryDoesNotHappen`, reporting offset 8 or 9
of 0..9 redelivered after what the test believed was a clean pass. So a
second, unrelated bug was hiding behind the first, and the PR-versus-push
pattern made it look environmental when it was not.
**Root cause:** A real correctness bug in `ConsumeKeyed`, not a test problem.
`commitTracker` correctly hands out strictly increasing offsets while holding
its mutex, but each worker then called `CommitRecords` *itself*, concurrently,
after releasing that mutex. Kafka offset commits are last-write-wins per
partition, so a lower offset landing after a higher one moves the group's
committed offset backwards and redelivers everything above it. Proved rather
than inferred, by recording the order commits actually completed in:
`commit-completion-order=[1 2 4 5 6 7 9 8]`, with no commit errors. Offset 9
committed, then 8, so the group's committed offset ended at 9 instead of 10
and record 9 came back. The contiguous-prefix design was sound; issuing its
output in parallel threw the guarantee away.
**Fix:** One dedicated committer goroutine. Workers hand finished records to a
buffered channel, and a single goroutine folds them through the tracker and
commits sequentially, which makes out-of-order commits structurally
impossible rather than merely unlikely. It is also faster than before, since
a worker no longer blocks on its own commit's network round trip. The
committer keeps draining after a commit failure rather than returning, or a
worker blocked sending to a channel nobody reads would deadlock
`ConsumeKeyed`'s `wg.Wait`. Verified 20 for 20 at `GOMAXPROCS=2` against a
test made four times more sensitive, where the old code failed 3 in 12.
**Two things that made this take much longer than it should have:**
First, the initial diagnosis attempt logged to stderr on every commit, which
perturbed the timing enough that the bug stopped reproducing at all: ten
clean runs, looking like the problem had gone away. Recording the sequence in
memory and printing once at the end caught it on the first run. Second, the
earlier flaky-test fix was reported as having resolved the CI failures on the
strength of one green PR check, when the push run for that very merge had
already failed for this different reason.
**Prevention:** Three lessons, the first two now written into
`ENGINEERING.md` section 1. `-race` does not find ordering bugs: it checks for
unsynchronised access, not for correctly-locked operations whose effects land
in the wrong order, so concurrent code needs repeated runs under
`GOMAXPROCS=2` before it is called done. Diagnostic I/O inside the window you
are observing can hide the very thing you are looking for. And when a fix is
claimed to have resolved a CI failure, the evidence is a green run of the
job that was failing, on the branch that failed, not a green run of a
different job on a different ref. One green check is not a trend.
