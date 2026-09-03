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
**Fix:** Added the two missing edges, carrying the same `TEMPORARY` comment
style the existing `NEW -> RECOVERED` edge already uses, to be removed
together with it once `Scoring` lands. Made `trail_complete` an actual
computation, running the same `incompleteTrail`/`impossibleTransition` checks
`VerifyInvariants` aggregates rather than a second implementation that could
drift from them. Verified against the live database: all four edges the
pipeline has actually written are now accepted.
**A second problem found while fixing the first:** the pre-existing
`TestGetRecordAuditReturnsRecordStateAndEntries` fixture opened with a
fabricated `UNSPECIFIED -> NEW` "ingested" audit entry. Nothing in the system
writes that. Ingestion writes no audit rows at all (section 10a), the
Decision Engine's first entry is always `NEW -> <state>`, and the state
machine explicitly forbids `UNSPECIFIED` as a from-state, which
`statemachine_test.go` even asserts. So that test had been asserting
`TrailComplete == true` over a trail the system considers invalid, and passed
only because the field was hardcoded. Making the field real turned it red
immediately, which is the textbook "test that was passing for the wrong
reason" from `ENGINEERING.md` section 12. Its fixture now uses the three
transitions the pipeline genuinely produces (classify, claim, outcome), the
same correction already applied to the end-to-end test.
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

### 2026-08-23, an e2e flake from ephemeral port reuse in test/e2e's freePort()
**What happened:** Verifying the Classifier's Phase 1 work (`SPEC.md`
section 13 asks to run `make test-integration` more than once), the fourth
of six consecutive `test/e2e` runs failed:
`POST /v1/batches: net/http: HTTP/1.x transport connection broken:
malformed HTTP response "\x00\x00\x06\x04\x00\x00\x00\x00\x00\x00\x05\x00\x00@\x00"`.
The other five runs, before and after, were green with no code changes in
between.

**Root cause:** `walking_skeleton_test.go`'s `freePort()` opens a TCP
listener on `:0`, reads the OS-assigned port, and closes the listener
immediately ("standard for this kind of test" per its own comment), before
the actual service binary binds it. The test calls `freePort()` thirteen
times back to back, once per service's gRPC/metrics/HTTP port, before
starting any subprocess. On a loaded machine, the OS's ephemeral port
allocator can hand the same just-freed port number to a later `freePort()`
call before the earlier call's owning process gets around to binding it.
That is what happened here: the API Gateway's HTTP port and the
Classifier's gRPC port collided, so the test's plain HTTP POST landed on
the Classifier's HTTP/2 gRPC listener instead and got an HTTP/2 SETTINGS
frame preamble back, which `net/http` correctly reports as a malformed
response. Not a Classifier bug: those bytes are what any HTTP/1.1 client
gets from any gRPC server, regardless of which two services happen to
collide.

**Fix:** Not applied here. `freePort()` and its thirteen call sites are
`test/e2e/**`, outside this task's scope (`services/classifier/**` only,
per `SPEC.md` section 10 and root `AGENTS.md`'s "stay inside your
service"), so it is raised here and in the Classifier PR rather than
touched directly. The actual fix belongs to whoever owns `test/e2e`: hold
each port's listener open until immediately before its owning subprocess
starts (pass the file descriptor via `net.FileListener`, or allocate and
bind each service's ports immediately before starting that one subprocess
instead of allocating all thirteen up front), so there is no window for
the OS to reissue a number to a different service.

**Prevention:** Confirmed via `git log` that this exact `freePort()`
implementation is unchanged since the original walking-skeleton commit
(`eb0029d`) and predates this task. Five of six runs were clean, including
two immediately before and three immediately after the failure with
identical code, which is what pins this on port-allocation timing rather
than Classifier logic. Same lesson as the `ConsumeKeyed` flake above: test
setup that allocates a shared, limited resource (here, TCP ports; there, a
hash bucket) in bulk before use deserves the same suspicion as any other
source of nondeterminism, verified by rerunning rather than trusted on a
single green run, per `ENGINEERING.md` section 12 and `SPEC.md` section
13's explicit instruction to run `make test-integration` more than once.
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

### 2026-08-23, the nudge path recorded a transition the state machine forbade
**What happened:** The new Phase 1 smoke test failed on its first run, on the
two nudge records only: `trail_complete = false`, and
`VerifyInvariants` reporting `impossible_transitions = 2` with
"entry 2: RECORD_STATE_NUDGED -> RECORD_STATE_NUDGED is not a valid
transition". The retry and escalation records were all fine.
**Root cause:** A genuine disagreement between the pipeline and the verifier,
and the first thing to exercise the nudge path end to end was this test. The
Decision Engine's scheduler claims a due nudge into `Nudged` before executing
it, then executing produces `OUTCOME_PENDING`, which `decideAfterExecute` also
maps to `Nudged` because the customer has not answered. So the trail
legitimately contains a `Nudged -> Nudged` self-edge, and the state machine,
written from `ARCHITECTURE.md` section 7's diagram, had no such edge.
**Fix:** Allowed the edge, rather than suppressing the entry. Checked what the
second entry actually carries before deciding: it holds the attempt number and
the send's real `cost_paise`, so dropping it would lose that spend from the
history the audit trail is supposed to be the source of truth for. Marked in
`statemachine.go` as a real edge rather than one of the temporary Phase 1
ones, since the claim-then-execute split is how the scheduler works and not a
phase artifact. Added a unit test for it, plus one asserting the self-edge
stays specific to `Nudged`: a `Retrying` self-edge would still mean something
had gone wrong, and blanket-allowing self-loops would have been the lazy fix
that hid that.
**Prevention:** Two things worth keeping. The bug was reachable only by
driving a nudge from the public API through to its parked state, which no unit
or single-record test did, so it survived the Decision Engine, Audit and
Executor work all landing individually green. A pipeline with branches needs a
test that walks each branch end to end, not just the happy one. And when a
verifier and the system disagree, the useful question is which of them is
wrong: here the verifier was, but that was only clear after checking what the
disputed audit entry actually contained. Reflexively silencing either side
would have been wrong in a way that was hard to notice later.

## 2026-08-24: wiring the guardrails silently escalated every record

**What broke.** Adding the guardrail layer to `HandleMessage` turned three
green Decision Engine integration tests red at once, all of them asserting a
record reached `RETRY_SCHEDULED` or `NUDGE_SCHEDULED` and all of them finding
`ESCALATED` instead. The log line said it plainly:
`guardrail_downgrade="recovery window closed: record is 14.8ms old, window is 0s"`.

**Why.** `GuardrailConfig` was added as a new field on `engine.Config`, and
every existing caller kept compiling while leaving it at its zero value. A
`RecoveryWindow` of `0` makes `age < window` false for every record ever
created, so a fresh record is already past its window, every spending action is
removed, and `permittedOrEscalate` downgrades all of them. The system does not
crash or log an error: it escalates 100% of records and reports success.

**Why it matters more than the test failure.** The tests caught this because
they assert specific states. Production would not have. A missing env var
would have produced an agent that looked healthy, processed every record, and
quietly did nothing, which is a worse outcome than refusing to start. The zero
value of a safety config is not a harmless default, it is the most restrictive
setting available.

**Fixed by** adding `GuardrailConfig.Validate`, rejecting any non-positive
field, and calling it in `loadConfig` at startup so a misconfigured deployment
fails loudly instead of silently (`ENGINEERING.md` §11 item 6, config validated
at startup). The test helper now sets the guardrails explicitly, with a comment
saying why leaving them out is not neutral.

**What to take from it.** Adding a field to a config struct is a source-
compatible change that is not a behaviour-compatible one. Every existing caller
silently opts into the zero value, so for anything whose zero value is
meaningful, the new field needs validation in the same change that introduces
it, not later.

## 2026-08-25: two services handed the same port by the e2e harness

**What broke.** `TestSmokeBatchReachesExpectedTerminalStates` failed in CI with
a readiness timeout, `waiting for 127.0.0.1:45671: context deadline exceeded`.
The real cause was three lines earlier in the captured output:
`executor ... "invalid configuration: GRPC_PORT and METRICS_PORT must differ,
both are 45671"`.

**Why.** `freePort` asked the kernel for `127.0.0.1:0`, read the assigned port,
and closed the listener before returning. That leaves the port genuinely free,
so a later call can be handed the same one. A stack needs thirteen ports, so
the chance of a collision is not small. The Executor received its own metrics
port for gRPC, refused to start (correctly, its config validation is doing its
job), and the harness then reported the readiness timeout rather than the
refusal.

**Why it was easy to misread.** The visible failure named a port and a
timeout, which looks like a slow service or a busy CI runner. Nothing in it
points at port allocation, and it appeared in the same CI run as two genuine,
expected failures from the economics scorer landing, so the obvious first
reading was that the scorer had broken the pipeline. It had not. Two of the
three failures were real, this one was pre-existing and unrelated.

**Fixed by** having `freePort` remember every port it has issued in the
process and hold each candidate listener open until a fresh port appears, so
the kernel cannot offer the same one twice inside one call.

**What to take from it.** A test helper that returns a resource it has already
released is handing out a promise it cannot keep. This one had been latent
since the harness was written and would have surfaced eventually, most likely
during a demo rehearsal. Also worth noting: config validation turned a silent
port clash into a loud refusal, which is the only reason this was diagnosable
at all.

### 2026-08-25, flaky test: kafkax.TestProducerSetsKeyToRecordID — UNRESOLVED, needs pickup
**Status: open.** This entry is a report only, no fix or test change has been
applied. Whoever picks up Phase 2/3 Kafka work should claim it, write the
regression test first per `ENGINEERING.md` §1, then the fix.

**What happened:** During a full verification run of the merged main branch
(`go test -race -tags='integration e2e' ./...` against a live docker-compose
stack), `TestProducerSetsKeyToRecordID` failed once:
```
Publish: publish to kafkax-test-<id> (key=<uuid>): UNKNOWN_TOPIC_OR_PARTITION:
This server does not host this topic-partition.
```
Every other package in the same run passed, including the e2e suite. Rerun
three times in isolation immediately after (`-run TestProducerSetsKeyToRecordID`),
it passed all three times. Flaky, not consistently broken.

**Suspected root cause, unconfirmed:** the test calls `ensureTopic` (an admin
`CreateTopic` call) and then immediately constructs a producer and publishes,
with no wait for the new topic's metadata/leadership to propagate. This is a
known Kafka timing race, producing right after creating a topic can outrun
the broker's own metadata cache, and is more visible on a single-broker KRaft
setup like this repo's `docker-compose.yml` than it would be on a
multi-broker cluster. Same shape of bug as this file's
`freePort`/`TestConcurrentClassifierCalls` entry: a resource assumed ready
the instant its creation call returns.

**Not fixed, deliberately.** Whoever addresses it should decide between:
- retrying `Publish` on `UNKNOWN_TOPIC_OR_PARTITION` a bounded number of times
  inside the test (or inside `kafkax.Producer` itself, if the same race could
  hit production code path on a freshly-created topic), or
- having `ensureTopic` poll until the topic is visible via metadata before
  returning, rather than returning as soon as the admin call acknowledges.

The second option is more likely correct for production `kafkax` code too,
not just the test, since anything that creates a topic and immediately
produces to it (the walking skeleton does exactly this shape at startup) is
theoretically exposed to the same race, just not yet observed there.

**Why this is logged rather than fixed on sight:** a flaky infra test is
exactly the kind of thing worth a deliberate regression test before a fix,
not a quick patch, per `ENGINEERING.md` §1 ("every bug you fix gets a
regression test first"). Logging it here so it is not lost, rather than
fixing it silently mid-verification.

### 2026-08-26, Unit F broke two existing e2e tests, and Unit L's own new test had two real gaps
**What happened:** Merging Units F (cause-aware retry timing) and L
(scheduler fake-clock/concurrency test) individually passed every check
each unit's own verification asked for. Running the full repo suite
(`go test -race -tags='integration e2e' ./...`) after merging both
surfaced three separate problems that neither unit's own testing caught.

**Problem 1: Unit F's salary-window timing broke tests that predate it.**
`TestSmokeBatchReachesExpectedTerminalStates` and Unit J's own
`TestSubmitBatchResubmitCreatesIndependentRecords` both submit an
`INSUFFICIENT_FUNDS` record and wait up to `pipelineWait` (30s) for it to
reach `RECOVERED`. Once Unit F landed, that bucket's retry can be scheduled
up to ~31 real days out (the next salary window), and the e2e harness
(`test/e2e/walking_skeleton_test.go`'s `commonEnv`) hardcoded
`DEMO_TIME_SCALE=1`, so nothing compressed the wait. Both tests genuinely
started waiting for a delay measured in weeks and timed out.
**Root cause:** cause-aware timing was reviewed and tested in isolation
(`schedule_test.go`, pure unit tests, no e2e dependency), so nothing in
Unit F's own verification exercised the e2e harness at all, and neither
Unit F's brief nor the smoke/rerun-safety tests (written before Unit F
existed) knew to account for a bucket whose retry timing is no longer a
small fixed delay.
**Fix:** `commonEnv` now sets `DEMO_TIME_SCALE=300000`, compressing the
worst-case ~31-day wait to under 9 seconds, comfortably inside
`pipelineWait`. Verified by rerunning the full e2e suite twice after the
change, both clean.

**Problem 2 and 3, in Unit L's own concurrency test.** Adversarial review
(removing `FOR UPDATE OF rs SKIP LOCKED` from `claimDue` and confirming the
test actually goes red) found the original two-racer version essentially
never caught the break: two `Scheduler`s racing one row rarely overlapped
at the database level, so the test passed 5/5 runs even with row locking
removed entirely. Raising the race to 25 concurrent `Scheduler.tick()`
calls fixed the detection rate, but introduced a worse problem: `tick()`
calls `claimDue` with `claimBatchSize=20`, a **system-wide** poll by
design, so 25 concurrent full ticks could claim up to 500 due records
across the whole database, not just the one row the test seeded. Under a
full `./...` run, that stole due records seeded by `test/e2e`'s own
subprocess-driven tests running at the same wall-clock time. Separately,
when contention was forced high enough to genuinely double-claim, the
test's fake Executor propagated a raw unique-violation error into the
Scheduler's real retry loop, which then waited on a fake clock nothing in
the test ever advances -- the test hung until its timeout instead of
failing with a clear assertion, which reads as an infra problem in CI, not
a locking bug.
**Fix:** rewrote the test to call `store.claimDue(ctx, now, 1)` directly,
25 times concurrently, instead of going through the full
`Scheduler.tick()` -> `process()` -> `Execute()` path. Each racer can now
claim at most one row, tightly bounding the blast radius, and bypassing
`process()`/`executeWithRetry` entirely removes the hang path, since
`claimDue` alone has no retry loop and no dependency on the fake clock
advancing. Verified: 60% catch rate over 20 runs against deliberately
broken locking (no hangs), 0/20 false positives against correct locking,
and two full clean runs of the entire repo suite (`go test -race
-tags='integration e2e' ./...`) afterward.

**Prevention:** the recurring lesson across all three problems is the same
one this file already has several entries about: a unit's own passing
tests are evidence about that unit in isolation, not about the merged
system. `PHASE2_IMPLEMENTATION.md`'s "Definition of done" for every unit
already says CI must be green "including the integration and e2e tiers,"
which in practice means the *full* suite, not just the files a unit
touched -- worth restating here since two independently-reviewed units
each looked complete on their own and still broke the full run when
combined.

### 2026-08-26, Unit M's own LLD said to run the full suite, and did not

Unit M deleted the three `TEMPORARY` state-machine edges
(`NEW -> RECOVERED`, `NEW -> RETRY_SCHEDULED`, `NEW -> NUDGE_SCHEDULED`,
see the 2026-08-23 entry above for why they existed) now that every record
routes through `Scoring`, and updated `statemachine_test.go` accordingly.
`go test ./services/audit/...` with no build tags was green.

That command does not compile the package's `integration`-tagged files.
Three fixtures elsewhere in the same package still built audit trails
through the removed edges: one in the unit-tier `verify_test.go` (a
clean-record case using `NEW -> RECOVERED` directly), and two behind the
`integration` tag (`get_record_audit_test.go`, `verify_invariants_test.go`)
that seed rows straight into Postgres rather than going through
`scheduleNew`. `go test -race -tags='integration e2e' ./...` caught all
three as `ImpossibleTransitions`/`TrailComplete` failures.

**Fix:** routed each fixture through `NEW -> SCORING -> ...`, matching what
`scheduleNew` (decision-engine `store.go`) actually writes -- notably, that
function applies the same rationale and source to every step it inserts,
not only the last, so the real shape has one more row than the old
fixtures assumed. Re-verified with a `-count=1` full-suite run, plus an
adversarial check: reintroduced `NEW -> RECOVERED` into the state machine
and confirmed the new rejection case in `statemachine_test.go` goes red,
then reverted.

**Prevention:** same lesson as the entry above, with a sharper edge this
time: Unit M's own LLD explicitly said "run the full e2e suite, and
confirm nothing regresses," and the unit-only `go test ./services/audit/...`
run that was actually done looked like it satisfied that instruction
without doing so, because Go silently skips build-tagged files it wasn't
told to include rather than erroring. A green run over the wrong test set
is not distinguishable from a green run over the right one unless someone
checks which files actually compiled.

### 2026-08-27, Unit H found a double-scaling bug and a missing state-machine edge

Building `test/e2e/batch_invariants_test.go` (docs/PHASE2_IMPLEMENTATION.md
Unit H) required a record's `TRANSIENT_BANK` retry to become due after a
real, observable delay rather than instantly, so a fabricated near-cap
history could be seeded into Postgres before the live scheduler claimed it.
Passing a large `RETRY_DELAY` produced an immediate claim anyway, both at a
plain "10s" and, when that was suspected to be double-compressed by
`DEMO_TIME_SCALE=300000`, at a compensating "3000000s".

Root cause: `services/decision-engine/cmd/main.go` scaled `RetryDelay` by
`DEMO_TIME_SCALE` before assigning it to `engine.Config`/`SchedulerConfig`,
and `schedule.go`'s `retryDueAt` scaled the same value again internally (it
needs the raw duration plus the raw scale factor together, for the
`INSUFFICIENT_FUNDS` salary-window branch, which does not go through
`cfg.Scale`). The effective delay was the requested value divided by the
scale factor twice, not once. Invisible in production, where
`DEMO_TIME_SCALE` defaults to 1 and both scale calls are no-ops; invisible in
every e2e test before this one too, since none of them needed a real,
observable delay window -- they only wanted something to happen soon, and
"soon squared" is still soon. Confirmed by reading `retryDueAt`'s scaling
math directly rather than guessing at another compensating constant.
**Fix:** stopped pre-scaling `RetryDelay` in `main.go`; `retryDueAt` remains
the single place that applies `TimeScale` to it.

With the delay fixed, the seeded records reached their live claim as
intended, and a *second*, unrelated real bug surfaced: `Audit.VerifyInvariants`
reported `SCORING -> ESCALATED` as an impossible transition for both
records, even though the trail was exactly what the Decision Engine's own
`permittedOrEscalate` (guardrails.go) legitimately produces whenever a
re-entry to `Scoring` finds every spending action guardrail-blocked. This
edge is a real, permanent output of Unit E's re-scoring loop plus the
guardrail caps, not a fabricated or temporary one -- it was simply never
produced by anything before this test, because nothing earlier ever pushed a
record's retry or contact history far enough to exhaust a budget mid
re-score. **Fix:** added `{SCORING, ESCALATED}: true` to
`services/audit/internal/server/statemachine.go`'s `allowedTransitions`,
with a regression case in `statemachine_test.go`.

A third issue was self-inflicted rather than a product bug: the first
version of the contacts-cap scenario seeded three fake `RETRY` rows already
at the cap and expected the live claim to therefore execute a `NUDGE` --
but the scheduler executes whatever `pending_action` the record's real,
unseeded first classify already decided (always `RETRY` for
`TRANSIENT_BANK`), and guardrails only re-check *after* that attempt fails.
Fixed by seeding the same "one retry short of the cap" shape already
validated for the retries scenario, plus two prior contacts, so the single
live claim fails as intended and the guardrails correctly gate what happens
next.

**Prevention:** the double-scaling bug is the same class of gap as
`docs/INCIDENTS.md` 2026-08-26's `DEMO_TIME_SCALE` finding for Unit F: a
compressed-time knob that nothing had ever needed to reason about precisely
before. The missing edge is the same class as 2026-08-23's original
`NEW -> RETRY_SCHEDULED` gap and 2026-08-26's temporary-edge cleanup: a
state diagram is only as complete as the code paths that have actually been
exercised against it, and a guardrail cap that nothing has ever pushed a
record hard enough to hit will look complete right up until something does.

### 2026-08-27, Unit K's LLD assumption about a "partially processed" window was wrong, and its restart readiness probe checked a port nothing opens

Two findings while building `test/e2e/crash_safety_test.go`
(docs/PHASE2_IMPLEMENTATION.md Unit K), both caught before merge rather than
shipped silently.

**First:** the LLD's premise -- "submit a batch, wait until partially
processed, SIGKILL the Decision Engine" -- assumed a natural window would
exist. An early, unthrottled version of the test submitted an 8-record
batch and called the kill immediately after; every record had already
reached `RECORD_STATE_RECOVERED` before the kill call even ran. Classify
through execute for a small batch completes in well under a second against
the deterministic stub, the same fact Unit H's entry above already
documents for a different reason. Fixed the same way: `RETRY_DELAY` is set
to a value that scales down to a real ~6s window under
`DEMO_TIME_SCALE=300000` (now single-scaled, per the fix above), so every
record is genuinely still `RETRY_SCHEDULED`, unclaimed, at the moment of
the crash.

**Second:** the restart mechanism's own readiness check
(`stack.restartDecisionEngine`, `test/e2e/harness_test.go`) waited for the
restarted process to accept a TCP connection on `GRPC_PORT`, mirroring how
every other service in the harness is confirmed ready. This never
succeeded -- 20s timeout, every run, on a freshly-allocated port that
should have been immediately available. Root cause: `services/decision-
engine/cmd/main.go` never opens `GRPC_PORT` (or any port) at all. The
Decision Engine is a pure Kafka consumer and scheduler; nothing else in the
pipeline calls it over gRPC, so it has no server to serve. `GRPC_PORT` is
only present in its environment because the shared config loader
(`internal/platform/config`) requires the value from every service
regardless of whether that service uses it. Confirmed by grepping
`main.go` for a `Listen` call and finding none, rather than adding a longer
timeout and hoping. **Fix:** removed the readiness probe from
`restartDecisionEngine` entirely; every caller already has to poll
`record_state`/Audit for the restarted process actually resuming work, the
same as for any other asynchronous effect in this harness.

Both problems are visible only once you actually run the thing you are
building a safety net for, not once you assume it will work the way the
LLD describes. Verified afterward with an adversarial check: deliberately
deleted one record's `audit_entry` rows after a clean pass (the exact shape
a real crash-induced gap would leave) and confirmed
`Audit.VerifyInvariants` catches it as a single `IncompleteAuditTrails`
violation, then reverted before committing -- this is the same file's own
recurring lesson, now for the sixth time in three days: a test that has
never been run against the real system, or an assumption that has never
been checked against the real code, reads exactly like a correct one until
someone runs it.

### 2026-08-28, GitHub SSH on port 22 is blocked on this network, and `go test ./services/<one>/...` was green while the repo did not compile

Two unrelated things from Unit A, both cheap to hit again.

**Port 22 to github.com is blocked** on the network this project is currently
developed from (college wifi). Symptom is not an error, it is a hang: `git
fetch` and `git push` sit until they time out, exit 124 or 128, with no
message pointing at the network. Confirmed with
`bash -c 'cat < /dev/null > /dev/tcp/github.com/22'` failing while both
`github.com:443` and `ssh.github.com:443` succeed.

Two workarounds, neither requiring a config change:

```bash
# GitHub's alternate SSH endpoint. HostKeyAlias matters: without it the key
# comes back under a different hostname and verification fails.
GIT_SSH_COMMAND="ssh -o HostName=ssh.github.com -o Port=443 -o HostKeyAlias=github.com" \
  git push origin main

# Or HTTPS through the gh credential helper, which is already authenticated.
git -c credential.helper='!gh auth git-credential' push https://github.com/thisizaro/Momotaro.git main
```

Note `gh auth status` reports "Git operations protocol: ssh", so having `gh`
authenticated does **not** on its own make `git push` work: the remote is an
SSH URL and git never consults gh for it. The permanent fix, if this network
is the normal one, is a `Host github.com` block in `~/.ssh/config` with
`Hostname ssh.github.com` and `Port 443`.

**Separately**: Unit A changed `provider.NewChain`'s signature.
`go test ./services/classifier/...` passed, and the repo did not compile.
`services/classifier/internal/server/server_test.go` builds a real chain too,
and its package was not in the path being tested at the moment the signature
changed. `go vet ./...` across the whole repo caught it immediately.

This is the same shape as the 2026-08-26 and 2026-08-27 entries, one level
out: there, a package-scoped or untagged run hid a failure in a tier it did
not compile. Here a package-scoped run hid a failure in a package it did not
compile. The rule generalises: **after changing any shared signature, run
`go vet ./...` before believing a scoped test run**, and the full tagged suite
before believing anything.

### 2026-08-28, an httptest handler that waits only on r.Context() hangs the whole package

Unit B. The test for "a hung provider must surface as
`context.DeadlineExceeded`" used the obvious handler:

```go
h := func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
```

The client times out at 100ms and the assertion passes, but the **package**
hangs until Go's test timeout kills it. `httptest.Server.Close()`, registered
via `t.Cleanup`, blocks until every in-flight handler returns, and the server
did not observe the client's disconnect: the handler sat on `r.Context()`
forever.

The panic trace pointed at the `Close()` line in a shared helper rather than
at the test, which is what made it non-obvious. `go test -timeout 25s` is what
turned a two-minute mystery into a stack trace naming the line.

Fix is a safety valve, so the handler returns whether or not the server ever
notices:

```go
select {
case <-r.Context().Done():
case <-time.After(time.Second):
}
```

The first version used a 5s valve and the package took 11s for two subtests,
because the valve is what actually fires. 1s is enough and cuts it to 3s. A
slow suite is a suite people stop running.

**Also worth knowing**: reverting a deliberately-broken file and immediately
re-running `go test` in the *same* shell invocation reported a stale FAIL. Two
separate runs afterwards, and five more for good measure, were all clean. If
an adversarial check's revert looks like it failed, run it again as its own
command before believing it.

### 2026-08-28, a concurrency test that went red on demand still only caught the bug 4 times in 20

Unit D. The circuit breaker's half-open state must admit **exactly one** trial
request. The test raced 25 goroutines against it and, with the locking
deliberately broken, went red. Green on correct code, red on broken code, done.

Except the catch rate was measured rather than assumed, and it was **4/20**.
Four runs in five, the bug would have shipped.

The cause is subtle and is the interesting part. With broken locking the first
racer *is* admitted, calls the provider, fails, and `record()` immediately
re-opens the circuit. The other 24 then arrive and are refused, correctly, but
for the **wrong reason**: they hit the "circuit is open" branch rather than the
"a trial is already in flight" branch. The test could not tell those apart, so
it saw one admitted call and passed. Only when the goroutine scheduling
happened to let a second racer through before `record()` ran did it notice.

Fix: hold the trial genuinely in flight. The fake rung gained a gate channel;
the test starts 25 racers, sleeps briefly so every one of them reaches
`admit()` while the trial is still blocked, then releases. Now all 25 are in
the half-open window, which is the state under test. Re-measured: **20/20
against broken locking, 0/20 false positives**.

This is the same lesson as 2026-08-26 (Unit L's scheduler test caught nothing
at 2 racers and needed 25 plus a redesign), and it generalises past
concurrency: **a test going red once when you break the code is not evidence
that it will go red when someone else breaks it.** Measure the catch rate over
enough runs to see the distribution, and check *why* the assertion fires, not
just that it does. A test can be right for the wrong reason and that is
indistinguishable from being right, until it isn't.

The wall-clock sleep in the fixed version is deliberate and fails safe: a
longer sleep only makes the pile-up more complete, and the assertion is a call
count, not a duration.

### 2026-08-28, the LLD put a shared encoding inside one of the two services that share it

Unit E, caught while implementing rather than after. The plan said to put
`encodeHops`/`decodeHops` in `services/audit/internal/server/hops.go`. That
cannot work: the **Decision Engine writes** `audit_entry.provider_hops` and the
**Audit Service reads** it, and `internal/` visibility means neither can import
the other. Following the plan literally would have produced an encoder in one
service and a decoder in the other with nothing keeping the delimiter in step.

That failure mode is nasty because it is silent. A divergence would not break a
build or fail a test; it would write audit rows that come back malformed or,
worse, subtly wrong. The round-trip test that catches it is only possible if
both halves live in one package.

Moved to `internal/platform/hopcodec`, matching the precedent set when
`kafkax` was added mid-Phase-1 for being "generic infrastructure, not one
service's business logic".

The general lesson: **when a plan assigns file ownership, check whether the
data crosses a service boundary.** A shared column has at least two owners by
definition, and the encoding of that column belongs to neither of them.

### 2026-08-28, an e2e test that pointed the classifier at a fake Groq and never called it

Unit C's e2e half, caught before merge because the test asserted its own
precondition rather than trusting the setup. The first version pointed
`GROQ_BASE_URL` at an httptest.Server that always returns 500, set
`LLM_PROVIDER_CHAIN=groq,rules` on the classifier, submitted a record, and
asserted the audit trail showed `SOURCE_RULES_FALLBACK` with a `{groq,error}`
hop. It passed, but for the wrong reason: the guard added specifically to
catch this (`if groqCalls == 0 { t.Fatal(...) }`) fired, because the fake
server had received zero requests.

The cause is a Unit H interaction Unit C's own LLD never mentions. The
classifier's `LLM_PROVIDER_CHAIN` decides which rungs *exist*, but the
Decision Engine's `LLM_SAMPLE_RATE` (default `0.0`) decides whether any given
record is even allowed to reach them: it sets `ClassifyRequest.
force_rules_only`, and the chain filters every non-rules rung out before
calling any of them (`onlyRulesRung`, `provider/chain.go`). A test that only
overrides the classifier's environment gets a chain with `groq` in it and a
request that is never allowed to use it, so it reaches `SOURCE_RULES_FALLBACK`
by never trying `groq` at all, indistinguishable from reaching it by trying
`groq` and having it fail. Exactly the "test that asserts a fallback
happened, against a stack where the primary was never going to answer
anyway, proves nothing" failure Unit C's own brief warned about, just from an
unexpected direction.

Fix: `startStackWithEnv` in `test/e2e/harness_test.go` takes a second env map
for the Decision Engine, and the test sets `LLM_SAMPLE_RATE=1.0` there so this
record is unconditionally sampled. Re-run: the fake server logs one request,
the audit trail shows `{groq,error}` then `{rules,ok}`, and the "prove it can
fail" check (make the fake server return a valid classification instead)
correctly turns the `SOURCE_RULES_FALLBACK` assertion red.

The general lesson: **a test asserting "X happened via the fallback path" must
also assert the primary path was actually reachable**, not just that the
final state looks like a fallback. Two independent code paths can produce the
same observable outcome, and a per-record cost-safety gate (Unit H) is exactly
the kind of thing that silently makes the primary unreachable without erroring
anywhere.

## 2026-08-29: a naive llm_fallback_total would have alerted on completely normal operation

Building Phase 4 Unit D's Alertmanager rules (`docs/PHASE4_IMPLEMENTATION.md`),
the first version of the classifier's new `llm_fallback_total` counter
incremented on any `resp.GetSource() == SOURCE_RULES_FALLBACK`. That is
correct-looking and wrong: `force_rules_only` (the per-record cost-safety
sampling gate, `docs/PHASE3_IMPLEMENTATION.md` Unit H) strips every non-rules
rung out of the chain *before* it ever runs, and also answers with
`SOURCE_RULES_FALLBACK`. With the demo profile's default
`LLM_SAMPLE_RATE=0.15`, roughly 85% of records hit this path on a perfectly
healthy run. `LLMFallbackRateHigh > 0.5` would have fired constantly, on
every normal batch, for a reason that has nothing to do with the LLM
degrading.

Caught before it ever shipped, by writing the negative test first
(`TestClassifyDoesNotIncrementFallbackCounterWhenNoLLMRungExists`) rather than
only the positive one: a rules-only chain must not increment the counter,
and the first implementation failed that test immediately. Fix:
`llmWasAttempted(resp.GetHops())` checks whether any hop names a rung other
than `"rules"` -- only true when a real LLM rung was actually tried and
still lost to `rules` -- and the counter only increments when that is also
true.

The general lesson: **two config-driven code paths that reach the same
`Source` value are not the same event**, and a metric meant to alert on one
of them needs to distinguish "asked and failed" from "never asked" using
something more specific than the field the rest of the system already
uses for a coarser purpose (here, the hop list, not `Source`).

## 2026-08-29: `alerting.alertmanager_configs` is not a real Prometheus config key

Writing Phase 4 Unit D's `deploy/observability/prometheus.yml`, the
`alerting:` block was written as `alertmanager_configs:` on the reasonable
assumption that the YAML key matches Prometheus's internal Go field name
(`AlertmanagerConfigs`). `docker compose up` for the observability stack
started the container without any visible top-level failure; the actual
error, `parsing YAML file ...: field alertmanager_configs not found in type
config.plain`, was sitting in `docker logs momotaro-prometheus-1`, not in
`docker compose`'s own output, so it would have been easy to miss and read
the silent absence of scrape/alert traffic as a networking problem instead
of a config one.

Found by running `docker run ... --entrypoint promtool prom/prometheus
check config` directly against the file rather than only inferring from
container logs -- `promtool` names the exact line and the exact wrong key.
Fix: the real YAML key is `alertmanagers`, plural, not
`alertmanager_configs`.

The general lesson: **when a container "starts" but nothing it should be
doing is happening, check that container's own logs before assuming the
network topology is at fault**, and prefer the tool's own config
validator (`promtool check config`, `amtool check-config`) over hand-
verifying YAML structure against a half-remembered schema.

## 2026-08-29: a volume mounted inside another read-only volume fails outright

Building Phase 4 Unit E's Grafana provisioning, the first version of
`docker-compose.observability.yml` mounted two separate host directories
into the `grafana` container: `./grafana/provisioning` at
`/etc/grafana/provisioning:ro`, and `./grafana/dashboards` at
`/etc/grafana/provisioning/dashboards/files:ro` -- the second path sitting
*inside* the first mount's own tree. `docker compose up` failed the
container outright at creation: `error mounting ...: create mountpoint for
/etc/grafana/provisioning/dashboards/files mount: ... read-only file
system`. Docker cannot create a mountpoint for the second volume inside a
directory tree the first, read-only mount already owns.

Fix: moved the dashboard JSON files to live inside the provisioning tree
itself (`grafana/provisioning/dashboards/files/*.json`), so a single mount
of `./grafana/provisioning` covers datasources, the dashboard provider
config, and the dashboards themselves. No second volume needed.

The general lesson: **a bind mount's target path must not fall inside
another bind mount's own tree**, especially when the outer one is
read-only; nest configuration files under one mount root instead of
composing several mounts that overlap.

## 2026-08-29: host.docker.internal does not reach this machine's WSL2 distro at all

Unit C's own write-up already flagged that the `host.docker.internal`
scrape path could not be exercised inside the agent session's tool
environment, and reasoned that this was probably a sandboxing artifact
rather than a real problem, since it is Docker's own documented mechanism.
It was wrong to stop there. Asked the user to check Prometheus's Targets
page from their own browser: every target showed `down` for them too, with
the identical `connect: connection refused` error, which ruled out "just
the agent's sandboxed shell" as the explanation.

Diagnosed by testing three things directly rather than guessing further:
`docker exec` from a throwaway container to `host.docker.internal` (DNS
resolved to `192.168.65.254`, TCP refused), the WSL2 distro's own `eth0`
address from `ip addr show` (`172.25.75.22` in this case, reachable and
serving real `/metrics` output when curled directly from a container on
the same compose network), and `--network host` (also refused, meaning
host networking is not usable here either). Root cause: this machine runs
Docker Desktop with the WSL2 backend in NAT networking mode, where
`host.docker.internal` resolves to Docker Desktop's own internal VM, not
to the WSL2 distro `make run-<service>` actually binds ports in. The two
are peers, not the same host, from a container's point of view.

Fix: `deploy/observability/prometheus.yml` became a template
(`prometheus.yml.tmpl`) with `HOST_IP_PLACEHOLDER` standing in for the
scrape target host, rendered into a gitignored `prometheus.generated.yml`
by `make up-observability` via `sed`. `HOST_IP` defaults to
`host.docker.internal` (correct and unchanged for native Linux Engine and
Docker Desktop's mirrored networking mode) and is overridable per machine
(`HOST_IP=$(hostname -I | awk '{print $1}')`) for setups like this one.
Confirmed after the fix: all six real services show `health: "up"` in
Prometheus, and Grafana's dashboards show live, moving numbers from real
traffic.

The general lesson: **"this is the standard documented pattern, so it
should work" is not verification**, especially for something that could
only be partially tested from inside the tool's own environment. The
honest move once a real gap is found (not just an unverifiable one) is to
ask the person who can actually check from a genuinely independent vantage
point, rather than writing the caveat down and moving on. Also worth
knowing on its own terms: Docker Desktop + WSL2 has more than one
networking mode, and `host.docker.internal` is not guaranteed to mean "the
WSL2 distro my shell is in" under all of them.

## 2026-08-29: a test fixture that never produces real NULL hid a scan bug until a live smoke test found it

Building Phase 5 Unit A's `ReportDelayedOutcome`
(`docs/PHASE5_IMPLEMENTATION.md`), `store.loadNudged`'s first version
scanned `record_state.pending_action` into a plain `string`. That column
is legitimately SQL `NULL` for any record past `NUDGED`
(`nullIfUnspecified` stores `ACTION_TYPE_UNSPECIFIED` as `NULL`, not the
literal string, store.go) — exactly the case `loadNudged` exists to
handle gracefully, since a big part of its job is reporting "this record
already moved on to state X" for a stale or duplicate delayed-outcome
report. Scanning `NULL` into a non-nullable `string` panics.

The integration test written alongside it
(`TestResumeNudgeDiscardsWhenNotInNudgedState`) seeds a record in
`RECOVERED` state specifically to exercise this path, and did not catch
the bug: its fixture helper, `seedScheduled` (shared with
`scheduler_test.go`, several tests deep), inserts `pendingAction.String()`
directly regardless of value, so an `ACTION_TYPE_UNSPECIFIED` record ends
up with the literal string `"ACTION_TYPE_UNSPECIFIED"` in the column, not
real `NULL`. The fixture doesn't replicate what production code
(`nullIfUnspecified`) actually does, so a bug that only manifests on real
`NULL` sailed through the whole suite green.

Found by dialing the actual running `decision-engine` binary with a
throwaway gRPC client, after seeding a record directly in Postgres and
calling `ReportDelayedOutcome` twice in a row — the first call correctly
moved it to `RECOVERED`; the second call, checking whether a now-terminal
record correctly discards a repeat report, crashed instead. Fixed:
`pendingAction` scans into `*string`, nil-checked before converting to the
enum. The test itself was also fixed, forcing real `NULL` via a direct
`UPDATE ... SET pending_action=NULL` rather than trusting the shared
fixture — confirmed to fail with the exact panic before the code fix, and
pass after.

The general lesson: **a shared test fixture that approximates production
behaviour instead of reproducing it exactly can hide a real bug
indefinitely**, especially the NULL-vs-empty-string kind, which is often
invisible until the specific state that produces a real NULL is reached.
Passing integration tests against real Postgres are not automatically
proof against this — the gap here was in what the *fixture* wrote, not in
whether the test used a real database. Live-testing the actual running
binary, not just the test suite, is what caught it; it's worth doing at
least once per unit that touches new SQL, not assuming coverage implies
correctness.

## 2026-08-29: a frozen contract shipped with an unstated JSON convention and one factually wrong claim

Caught by an external review of the Unit O PR (`docs/API_GATEWAY.md`,
`docs/PHASE5_IMPLEMENTATION.md`), before merge, not by CI, since neither
defect is the kind a test suite catches in a markdown file.

**The JSON convention was never stated, and the doc's own promises could
not all hold under any single choice.** The frozen contract claimed, in
three different places, that `recovered_delta_paise: 0` always renders,
that `rationale`/`message_text` are always present as empty strings, and
that `from_state` is sometimes absent entirely. `protojson`'s default drops
every zero value, which breaks the first two claims; its `EmitUnpopulated`
option drops nothing, which breaks the third. The actual Gateway code uses
neither, it hand-writes Go structs with explicit `json` tags
(`services/api-gateway/internal/httpapi/handler.go`), which was never
written down as the rule. One live route had already drifted from even
that: `submitBatchResponse.Rejected` carried `json:"rejected,omitempty"`,
so an empty map vanished from the wire while the doc's own example showed
`"rejected": {}`. Fixed by adding an explicit wire convention (hand-written
structs, no `omitempty`, ever) and removing the stray tag.

**The `from_state`-can-be-absent claim was not an oversight to state more
carefully, it was wrong.** `audit_entry.from_state` is `NOT NULL`
(`migrations/00001_initial_schema.sql`), and every record's first
transition is `RECORD_STATE_NEW -> RECORD_STATE_SCORING`
(`services/decision-engine/internal/engine/state.go`), never an
unspecified from-state; 2026-08-23's entry in this file already documents
that nothing in the system writes one. A frontend agent building against
the old claim would have written a real branch for a case that cannot
occur.

**General lesson, distinct from the earlier NULL-scan incident (2026-08-29,
above) but the same shape**: a contract document can read as internally
consistent, cite real files, and still be wrong in a way its author does
not notice, because writing the doc and checking it against the schema are
different acts. A second reviewer, or a deliberate self-check against the
actual migration and code (not just the proto comments), catches this kind
of thing; nothing about "it looks thorough" is evidence that it is correct.
The same review also caught a real demo-flow risk worth its own note: the
dashboard's generate button already calls the exact request form
(`count`) that this contract defines as never carrying `GROUND_TRUTH`, so
the most obvious action on screen would, once wired up, produce a batch
with neither of this phase's headline numbers. Addressed in
`docs/API_GATEWAY.md` and `docs/PRD.md` section 12 in the same fix.

## 2026-08-30: `audit.events` was fully architected and never had a publisher

Starting Phase 5 Unit F's streaming half (`docs/PHASE5_IMPLEMENTATION.md`),
expecting to write a Kafka consumer in Reporting for a topic the system had
been describing since Phase 0. `docker-compose.yml` creates `audit.events`
(12 partitions). `docs/ARCHITECTURE.md` sections 6a, 8, and 10a all describe
it in detail: which service publishes, what it is for, why losing a
message is safe. Nothing in the whole Go module referenced the topic name
or published to it. A `grep` across every service found zero hits before
this unit started writing code.

**Why this stayed invisible for four phases.** `docs/ARCHITECTURE.md`
section 10a's own rule -- "Postgres is the sole source of truth for
history... Kafka never feeds numbers directly into a report" -- meant every
number Reporting has shipped so far (Units F's unary half, K) was computed
by reading Postgres directly, with no code path that would ever notice a
missing Kafka message. `audit.events`' only two jobs (cache invalidation,
driving `StreamBatchUpdates`) both belong to features that had not been
built yet, so nothing exercised the gap. It is the same shape as Unit A's
prerequisite ("Decision Engine had no gRPC server at all," discovered by
the Phase 5 pre-planning audit) and Unit D's e2e regression (a path nothing
had exercised end to end): a component can be fully specified in checked-in
docs and still not exist in code, and unit/integration tests over the
*existing* features cannot catch it, because none of them depend on it.

**A second, smaller stale claim found in the same pass.**
`docs/ARCHITECTURE.md`'s table-ownership table (section 10a) stated
`AUDIT_ENTRY` is written by "Decision Engine and Executor." A `grep` for
`audit_entry` across `services/executor` returns nothing; it never has.
Every audit row in the running system today is written by Decision Engine,
transactionally with the `RECORD_STATE` change it belongs to (Executor
owns `INTERVENTION_ATTEMPT` only). Corrected the table and the section 8
topic-map diagram, which carried the same claim as an `EXE --> audit.events`
arrow.

**Fixed as its own PR, before Reporting's consumer**: a new shared
`internal/platform/auditevent` package (the wire schema two services must
agree on -- a genuine cross-service contract, not something either service
should own privately) and Decision Engine publishing to it, best-effort,
right after each of its four `RECORD_STATE`-writing transactions commits
(`scheduleNew`, `recordOutcome`, `recordRescore`, `applyResumedOutcome`).
A publish failure is logged, never propagated as a failure of the caller's
own operation, per section 10a's own contract for what losing this topic
is allowed to cost. Verified with real integration tests reading the topic
back (`waitForAuditEvent`, mirroring the existing `waitForDeadLetter`
pattern) rather than asserting against the publisher's return value alone.
The e2e harness gained its own isolated `audit.events` topic per stack
(`AUDIT_EVENTS_TOPIC`), matching the existing `raw.events`/
`raw.events.dlq` isolation and the same reason (a shared topic across test
runs eventually replays history against records that no longer exist).

**Lesson**: "the architecture doc describes it" is not evidence a
capability exists. Before starting a unit whose job is to *consume*
something, check that anything actually *produces* it -- a `grep` across
the whole module for the topic name costs seconds and would have caught
this on day one of Phase 0 rather than at the start of Phase 5's last
backend unit.

### 2026-08-30, no Node/npm available in the web/ contract-alignment pass's environment
**What happened:** Asked to run `npm run typecheck`, `npm run build`, and
`npm run dev` after aligning `web/` with the newly-frozen `API_GATEWAY.md`
contract. No `node`/`npm`/`npx` binary was found anywhere on `PATH` or in
common install locations (Volta's shim dir exists but is empty, no global
npm folder, no `node_modules`), so none of the three commands could be run.
**Root cause:** The environment this pass ran in has never had Node
provisioned, unrelated to the code changes themselves.
**Fix:** None available from inside this pass. Did a manual, file-by-file
review instead: re-read every changed file, grepped the whole `src/` tree
for leftover old field names/enum spellings from the pre-freeze shapes, and
hand-checked the type-inference spots most likely to fool `strict` mode
(a `??` fallback object literal without a contextual type, in the
`accuracy.confusion` aggregation in `mockEngine.ts`, which needed an
explicit annotation to avoid inferring `{}`). This is real but weaker
evidence than a clean `tsc`/`vite build`/rendered `npm run dev` page.
**Prevention:** Whoever picks this branch up next should run
`npm install && npm run typecheck && npm run build && npm run dev` before
trusting it fully — flagged explicitly in the task's own summary rather
than silently reported as verified.
**Resolved same day**: the diff was transferred (via a git patch, both
clones shared the same remote at the same commit) into an environment with
Node already provisioned. `npm run typecheck`, `npm run build`, and
`npm run lint` all ran clean; the dev server boots and serves a 200. The
manual review this pass did instead turned out accurate — no leftover
pre-freeze spellings found on independent re-grep either.

### 2026-08-30, `decision-engine` crashed on a stale `raw.events` message, taking the whole process down

**What happened:** Manually smoke-testing every `make run-<service>` target
one at a time (docs/PLAN.md Phase 5 done, verifying local dev ergonomics).
`decision-engine` started cleanly (both gRPC and metrics servers up), then
1.3 seconds later exited fatally:
```
consume raw.events: handle raw.events[0]@4: load attempt history for
7b70ef3f-8d50-4756-9157-9cffd1d2cdd6: no rows in result set
```

**Root cause:** `engine.HandleMessage`'s `loadAttemptHistory` call
(`services/decision-engine/internal/engine/store.go`) runs `FROM record r
... WHERE r.id = $1`, which returns zero rows if the `record` row does not
exist. This message's `record_id` had no matching Postgres row -- almost
certainly a stale message left on the plain, unisolated `raw.events` topic
by earlier manual live-verification runs against the shared local stack
this session (`docs/PHASE5_IMPLEMENTATION.md` Unit F/G both did live
verification against this exact topic), whose Postgres data no longer
exists. Under the system's own write path this cannot happen -- Ingestion
always writes the `record` row before publishing to Kafka -- so this was
environmental drift on a long-lived shared local broker, not something a
fresh clone would hit.

**But the crash itself is a real gap, independent of how this instance was
triggered.** `kafkax.ConsumeKeyed`'s own doc comment states the contract
plainly: "a non-nil error is meant for infrastructure failures, not
per-record business outcomes." `HandleMessage` honors this correctly for
two failure kinds (malformed JSON, classify-failure-after-retries -- both
dead-lettered, both tested), but four other Postgres lookups in the same
function (`recordStateExists`, `loadAttemptRows`, `loadInstrumentHistory`,
`loadAttemptHistory`) just `return err` unclassified. Any one bad/orphaned
row hitting any of these four crashes the whole service, and since
`ConsumeKeyed` won't commit past a failed message, restarting just replays
the same message and crashes again -- an unrecoverable crash loop that
wedges the entire Kafka partition behind one bad record, not just skips it.

**Why no test caught this:** no test ever constructs "a raw.events message
exists for a record Postgres doesn't have," because the system's own write
path makes that combination structurally impossible to produce from inside
the code. That is exactly the class of gap that stays invisible: an
assumption that holds for every path the code itself takes is never
defended against for a path something external could still create.

**Fix:** not done in this pass, tracked in `docs/BACKLOG.md` ("Classify
Postgres 'no rows' errors as dead-letter-worthy in
`decision-engine.HandleMessage`"). Deliberately not fixed reactively here
per docs/ENGINEERING.md section 1 -- this needs tests-first, same as
everything else in this codebase, not a same-session patch.

**Immediate mitigation used**: reset the local dev environment
(`make down-clean && make up && make migrate-up`) rather than hand-editing
Kafka state, removing the stale message along with ~1,360 other
accumulated test-run topics found on the same broker (see the next entry).

**Lesson**: a documented handler contract ("classify your own errors,
don't propagate everything") is only as good as every call site actually
following it. Grep for every `return err` in a Kafka handler against the
contract's own doc comment, not just the two paths that happened to get
built with dead-lettering in mind from the start.

### 2026-08-30, ~1,360 accumulated Kafka topics on the shared local dev broker

**What happened:** Kafka UI showed roughly 55 pages of topics. Broken down:
1,056 `decision-engine-test-<uuid>` (one per `go test` run of that
package's own integration suite), ~190 `e2e-raw-events*`/`e2e-audit-events*`
(one set per `go test -tags=e2e` run), 72 `kafkax-test-<uuid>`, and a
handful of `raw.events.f-livecheck`/`.g-livecheck` (manual live-verification
runs during Phase 5 Units F/G).

**Root cause:** every test tier that touches Kafka deliberately creates its
own isolated, uniquely-named topic per run (correct, and exactly what
prevents concurrent/repeated test runs from interfering with each other --
`docs/AGENTS.md`'s own testing conventions). Nothing ever deletes an old
one afterward, and this session ran the full suite, the e2e suite, and
several manual live-checks repeatedly across multiple days against the same
long-lived local broker. Pure accumulation, not a bug in any of the tests
themselves.

**Fix:** none needed in code. `make down-clean && make up` gives a fully
fresh broker (Kafka has no named volume in `docker-compose.yml`, so its
data does not survive a `docker compose down -v` regardless). Confirmed
this is genuinely machine-portable -- no absolute or machine-specific state
in the base stack -- so the same reset works identically on any clone.

**Prevention:** worth considering a `make kafka-tidy` target (delete every
topic matching `decision-engine-test-*`/`e2e-*`/`kafkax-test-*`) if this
keeps recurring during heavy local test iteration, tracked in
`docs/BACKLOG.md` rather than built speculatively now.

### 2026-08-30, CI's post-merge run on `main` failed for a PR that never touched the failing code

**What happened:** the automatic CI run GitHub triggers on every push to
`main` (distinct from the PR's own `pull_request`-triggered run, which had
already passed) went red right after merging #75
(`infra/batchgen-make-target`) -- two failures in
`services/decision-engine/internal/engine`:
`TestSchedulerDeadLettersAfterExecuteRetriesExhausted` ("executor called 0
times, want at least maxExecuteAttempts=3") and
`TestSchedulerFiresOnceWhenFakeClockPassesDueAt` ("current_state before
due_at = RETRYING, want RETRY_SCHEDULED").

**Root cause:** not a regression. #75 only touched `Makefile` and
`scripts/batchgen/main.go` (confirmed via `git diff` across the whole merge
range, zero lines in `decision-engine`), and the PR's own pre-merge check
already ran this exact suite against this exact code and passed. Reproduced
locally instead: the two named tests in isolation, then the whole
`decision-engine/internal/engine` package three more times -- four clean
runs, zero failures. The failure shape (a record further along the state
machine than a single direct `tick()` call should produce, an executor call
count of zero where a background process apparently consumed it instead)
matches this codebase's own documented, accepted tradeoff: the scheduler's
claim-due-work query runs system-wide with no per-test isolation
(`docs/AGENTS.md` testing conventions already account for this). Under
CI's shared, more contended runner, a background scheduler loop from one
test occasionally claims a record a *different* test in the same binary
just seeded, before that test can check it. Same general flakiness
category this session already hit once before with a different pair of
tests (`TestSchedulerForwardsEVSnapshotToExecute`/
`TestSchedulerRetryLoopTerminatesViaEconomicsWhenPriorsRunOut`, that time
from a literal leftover manual process rather than in-CI cross-test
interference).

**Fix:** none needed; `main` is not actually broken. Logged for the record
as a second confirmed data point on this flakiness source, not because it
needs action now.

**Lesson**: a red push-triggered run on `main` right after a merge is not
automatically evidence the merge was bad -- check whether the PR's own
pre-merge run (against the identical code) already passed, and check the
actual diff for whether it could plausibly have caused the specific
failure, before assuming a regression.

**Recurred within the hour, same test, on the PR documenting the first
occurrence**: this very PR's own `pull_request`-triggered check (touching
only this file) failed `TestSchedulerFiresOnceWhenFakeClockPassesDueAt`
again. A docs-only PR failing a decision-engine test rules out any
content correlation entirely -- confirms this is purely environmental,
not something worth chasing per-PR. Resolved by `gh run rerun --failed`,
which passed clean on retry. Two occurrences in under 20 minutes is
frequent enough that the underlying test-isolation gap (see root cause
above) may be worth actually fixing rather than continuing to document
and retry -- flagged in `docs/BACKLOG.md` for whoever picks it up, rather
than fixed reactively in this pass.

## 2026-08-31: DEMO_TIME_SCALE compressed the recovery window into nothing, escalating 73% of a batch

**What happened.** A 100-record batch, seeded with `make batchgen COUNT=100
SEED=7` against a fresh stack with `DEMO_TIME_SCALE=300000`, settled in under
five seconds with **82 records `ESCALATED` and 18 `RECOVERED`**. Reported
recovery was Rs 171,488 against a naive-baseline expectation of Rs 487,848,
so the dashboard showed a blind retry-everything policy beating the agent
2.8x. The first reading, from the dashboard alone, was that the economics
layer was broken or the priors were badly wrong.

**Root cause.** Neither. Grouping `audit_entry.reason` for escalations gave it
immediately:

```
recovery window closed: record is 10.204043678s old, window is 2.016s   x73
classifier recommended escalation                                       x9
```

`RECOVERY_WINDOW` is 7 days. `services/decision-engine/cmd/main.go` passes it
through `cfg.Scale()` like every other duration, and 604800s / 300000 =
**2.016s**. But `guardrails.go`'s check is `if age < cfg.RecoveryWindow`,
where `age` is real wall-clock time since `record.created_at`. A record needs
several real seconds to be classified, priced and scheduled, by which point it
is already older than its compressed window. The guardrail then correctly
concludes the record is too stale to spend money on, removes every action, and
escalates.

So 73 of 100 records were escalated before the economics layer ever priced
them. The agent did not lose on expected value, it forfeited. The giveaway was
in the report all along and easy to misread as a good result: **total spend was
Rs 11**, which looks like impressive efficiency and actually means the agent
barely acted at all.

**The conceptual error.** `DEMO_TIME_SCALE` compresses *waits we schedule*
(retry delays, contact cooldowns), which is correct: those are durations we
choose, and compressing them is the entire point of a demo scale factor. It
must not compress a window that is compared against *elapsed real time*,
because we cannot compress the wall clock the age is measured on. Worse, the
compression amplifies real latency into logical time: at scale 300000, ten real
seconds of ordinary processing "spends" 34 logical days of a 7-day window.
There is no consistent reconciliation of the two clocks without a real logical
clock, which is a far larger change (noted in `docs/BACKLOG.md`).

**Fix.** Stop scaling `RecoveryWindow`. Every other scaled duration
(`RetryDelay`, `NudgeDelay`, `ContactCooldown`, `RetryMandateLeadTime`) is a
future wait and stays scaled. Regression test: a record processed under a large
`DEMO_TIME_SCALE` with realistic processing latency must not be escalated for
window closure.

**Prevention.** This is the same family as this file's 2026-08-24 entry, where
a zero-valued `RecoveryWindow` silently escalated every record: a safety
config whose wrong value does not crash anything, it just makes the agent
quietly refuse to work while reporting success. Two lessons worth keeping.
**A duration compared against wall-clock elapsed time is not the same kind of
value as a duration we wait out, and one scale factor must not be applied to
both.** And when a headline metric looks bad, group the audit `reason` column
before theorising about the model: the answer was one SQL query away, and the
initial hypothesis (bad priors, broken economics) would have cost a day.

## 2026-08-31: `configs/demo.env` cannot be applied, because the Makefile overrides it

**What happened.** Investigating the incident above, the documented way to run
a demo,

```bash
set -a; source configs/demo.env; set +a
make run-decision-engine
```

turned out to do nothing at all. Verified directly:

```
shell has:   DEMO_TIME_SCALE=300000  LLM_PROVIDER_CHAIN=groq,gemini,rules
make passes: DEMO_TIME_SCALE=1       LLM_PROVIDER_CHAIN=rules
```

**Root cause.** The Makefile does `include .env` and `export`. In GNU Make, a
variable assigned inside a makefile takes precedence over the same variable in
the environment. So `.env`'s `DEMO_TIME_SCALE=1` silently beats whatever was
sourced, on every `make run-*` target, always.

This means `configs/demo.env` has never worked as documented since it was
added, and every demo run to date has used real-time waits and no LLM chain.
It is the reason the batch in the incident above was misconfigured, and the
reason an earlier run appeared to show the baseline beating the agent.

**Fix.** Command-line variables are the one form that outranks a makefile
assignment, so `make run-x DEMO_TIME_SCALE=300000` works today as a
workaround. The real fix is a `PROFILE` variable: include `.env` first, then
`configs/$(PROFILE).env` after it, since a later include wins. Plus
`make demo-up` / `make demo-down`, so nine services cannot be started with nine
different configurations by hand.

**Prevention.** A config mechanism nobody has executed end to end is not a
config mechanism. This one was reasoned about, written into three documents,
and never once run. The check is two lines and should have been part of adding
the profile:

```bash
make --eval='__show:; @echo $(DEMO_TIME_SCALE)' __show
```

Related, worth flagging separately: `configs/demo.env` sets
`LLM_PROVIDER_CHAIN=groq,gemini,rules`, contradicting `docs/DECISIONS.md`
2026-08-28, which measured Gemini's latency and concluded the default chain
must be `groq,rules`. The live run confirmed that measurement:
`groq:circuit_open,gemini:timeout,rules:ok` appeared six times in the audit
trail. The chain in the demo profile should match the decision.

## 2026-08-31: one orphaned Kafka message permanently wedges the decision-engine

**What happened.** After a clean demo run, the dashboard's "Generate Sample
Data" button produced a batch that was accepted (`accepted_count: 2`) and then
never processed: no `record_state` rows, no audit entries, and a report showing
`total_records: 2` with zero in every other column. The button was not the
problem. The decision-engine had exited 13 minutes earlier and stayed dead:

```
fatal: consume raw.events: handle raw.events[8]@6:
       load attempt history for 10187b3b-...: no rows in result set
```

**Root cause.** The full test suite (`go test -tags='integration e2e' ./...`)
was run while the demo stack was live. Integration tests create records,
publish them to the real `raw.events`, and delete their rows in cleanup. Kafka
cannot delete individual messages, so those messages outlive the rows they
reference. The live decision-engine consumed one, `loadAttemptHistory` found no
such record, the handler returned that as an error, and
`kafkax.ConsumeKeyed`'s documented contract treats a non-nil handler error as
an infrastructure failure that stops the whole loop. The process exited.

**It does not recover.** Restarting produced an immediate second death on a
different poisoned offset (`raw.events[4]@5`, record `87888af6-...`). Every
restart consumes the next orphaned message and dies again. That is a crash
loop, and the only way out is clearing the offsets or purging the topic.

**Why the DLQ did not catch it, which is the actual defect.** The dead-letter
path exists for exactly this and is wired into `HandleMessage`'s classify call
and the scheduler's Execute call. It does not cover a *store* failure. A
missing record is a permanent data condition, not a transient infrastructure
one: retrying cannot help, because the row is never coming back. The handler
reports it the same way it would report a dropped database connection, and
`ConsumeKeyed` correctly treats that as fatal. So the classification of the
error is wrong, not the consumer's contract.

This directly violates `docs/PRD.md` section 10: **"No poison record stalls the
pipeline; DLQ, never silently dropped, never counted as a business outcome."**
A judge who submits a record and then deletes it, or any redeploy against a
topic with history, reproduces it.

**Fix (applied, Unit U).** Distinguish "this record does not exist" from "the
database is unreachable" at the store boundary, and dead-letter the former
rather than returning it as an infrastructure error. `loadAttemptHistory` now
classifies `pgx.ErrNoRows` as `store.ErrRecordNotFound`, and `HandleMessage`
routes that to the existing dead-letter path with a clear reason instead of
returning it to `ConsumeKeyed`. Regression test:
`TestConsumeKeyedDeadLettersMissingRecordAndKeepsGoing`
(`services/decision-engine/internal/engine/poison_record_integration_test.go`)
runs the real `ConsumeKeyed` loop, publishes a message for a record id that
does not exist followed by a normal one, and asserts the poison message is
dead-lettered, its offset is committed (a fresh consumer in the same group
sees nothing outstanding), and the loop goes on to schedule the second
record. Verified against the old behaviour: reverting the classification
change makes this test fail (it never receives the dead letter, because the
consumer already died), confirming the test actually catches the bug.

**Prevention, two separate lessons.** First, **do not run the test suite
against a live demo stack**; the tests share `raw.events` with it and their
cleanup poisons it. That deserves a note in `README.md` next to
`make test-integration`. Second, and more important: **an error type is a
routing decision.** Every handler error that reaches `ConsumeKeyed` is being
asserted to be transient and infrastructure-shaped. Any error that is actually
permanent and data-shaped will stop the pipeline instead of dead-lettering, and
this one sat undetected because nothing had previously deleted a record out
from under an unconsumed message. Worth auditing the whole handler path for
other permanent-but-reported-as-fatal conditions.

**Related gap found while diagnosing.** `make down-clean` does remove Kafka's
volume, so this was not stale data from an earlier session. It was generated
live, during the run, by our own test suite.

## 2026-09-01: auditing Unit U's fix found a second, narrower window onto the same bug

**What happened.** While auditing the rest of `HandleMessage`'s path per the
entry above ("worth auditing the whole handler path for other
permanent-but-reported-as-fatal conditions"), `recordStateExists`,
`loadAttemptRows` and `loadInstrumentHistory` all turned out fine:
`recordStateExists` uses `SELECT EXISTS(...)`, which always returns exactly
one row, and the other two use `pool.Query`, which returns an empty result
set with no error rather than `pgx.ErrNoRows` (that error is specific to
`QueryRow`). But `store.scheduleNew`'s `INSERT INTO record_state` was still
exposed: `record_state.record_id` is `REFERENCES record(id)`, so if the
`RECORD` row is deleted after `loadAttemptHistory` succeeds but before this
insert runs, later in the same `HandleMessage` call (there is a full
`Classify` RPC round trip in between), the insert fails with a Postgres
foreign-key violation. That error was being wrapped and returned exactly the
same way as any other database error, which `ConsumeKeyed` still treats as
fatal.

**Why this is the same bug, not a new one.** It is the identical permanent,
data-shaped condition ("this record does not exist") reached through a
narrower timing window instead of already being true when the message is
first read. Realistic exposure: an integration test's cleanup deleting a
record concurrently with a live decision-engine processing the very message
that record produced, which is precisely the scenario the entry above
already describes for the read side.

**Fix.** `store.go` gained `isForeignKeyViolation`, checking for Postgres
SQLSTATE `23503` via `pgconn.PgError`, and `scheduleNew`'s insert now maps
that to the same `store.ErrRecordNotFound` sentinel `loadAttemptHistory`
uses, so `HandleMessage` dead-letters it through the identical path.
Regression test: `TestScheduleNewReturnsErrRecordNotFoundWhenRecordMissing`
(`services/decision-engine/internal/engine/record_not_found_integration_test.go`)
calls `scheduleNew` directly against a record id with no `RECORD` row and
asserts the foreign-key violation comes back wrapped in
`ErrRecordNotFound`, not as a bare error.

**Prevention.** Same lesson as the entry above, restated because it held up
under a second look: a store method's error is a routing decision for
everything that calls it through `ConsumeKeyed`, and every SQL statement in
that call path has to be checked for what it does when its target row does
not exist, not just the one the stack trace happened to name.

### 2026-09-01, flaky in CI: TestSchedulerNeverDoubleClaimsTheSameRecord and TestBatchCorrectnessInvariants, RESOLVED 2026-09-02 (#105)

> **Resolved, and it was not a flaky test.** Root cause and fix are in the
> 2026-09-02 entry below, "the integration job was never flaky". The
> recommended fix recorded in this entry, scoping assertions to each test's
> own seeded `record_id`, was aimed at the symptom and would not have worked:
> the records were being claimed by a real decision-engine running in another
> package's test binary, so no assertion change could have prevented it. Left
> in place rather than rewritten, because a wrong diagnosis that looked
> reasonable for a day is worth keeping visible.

**Status: open.** Report only, no fix or test change applied. Whoever next
touches the Decision Engine scheduler should claim this, write the regression
test first per `docs/ENGINEERING.md` section 1, then fix it.

**What happened.** PR #87 (Unit AA, which adds a `due_at` field to Reporting's
`RecordSummary` and passes it through the Gateway) failed the `integration`
job on two tests it does not touch:

```
--- FAIL: TestSchedulerNeverDoubleClaimsTheSameRecord (0.19s)
    scheduler_test.go:617: attempt_count = 0, want 1: a second tick must not
                           reclaim a terminal record
    scheduler_test.go:624: audit_entry rows = 0, want 2 (one claim + one
                           outcome, not doubled by a second tick)
--- FAIL: TestBatchCorrectnessInvariants (42.24s)  [test/e2e]
```

Re-running the identical job with no code change passed. So it is flaky, not
broken.

**Why the diff could not have caused it.** Unit AA touches
`proto/reporting/v1`, `services/reporting`, `services/api-gateway` and `web/`.
Both failures are in `services/decision-engine` and `test/e2e`. Unit U had
merged just before and was the obvious suspect, so `loadAttemptHistory` was
re-read directly: its query is `FROM record r LEFT JOIN intervention_attempt
ia ... WHERE r.id = $1`, so a record that exists with no attempts still yields
one all-NULL row and `pgx.ErrNoRows` genuinely means the record is absent. U's
new `ErrRecordNotFound` classification cannot fire for a healthy new record.
That was checked rather than assumed.

**Suspected root cause, unconfirmed.** `attempt_count = 0` with **zero** audit
rows means the seeded record was never claimed at all, rather than claimed
twice. That is the signature this file already documents twice: the
scheduler's `claimDue` is deliberately **system-wide**, unscoped by batch or
record, because that is genuinely its production job (2026-08-23, "Decision
Engine scheduler tests flaked on a shared executor call count"), and
`go test ./...` runs packages concurrently against one shared Postgres. A
scheduler tick belonging to another package, most plausibly `test/e2e`'s own
subprocess-driven engine running at the same wall-clock moment, can claim this
test's row first. The companion `test/e2e` failure in the same run is
consistent with the two interfering with each other rather than with either
being independently wrong.

**Background rate.** `main` itself shows 1 failure in its last 12 runs, so
this is not new to this PR and has been quietly present for a while.

**Not fixed, deliberately.** Whoever picks it up should choose between:
- scoping the assertion to the specific `record_id` the test seeded, the fix
  already applied once for the same class of problem on 2026-08-23, and
  checking whether `scheduler_test.go:617` and `:624` still assert on
  something shared; or
- giving scheduler integration tests an isolated claim domain, for example a
  dedicated batch plus a `claimDue` variant scoped to it in tests only, which
  is a larger change and risks testing something other than the production
  query.

The first is more likely correct and is the smaller change.

**Why logged rather than fixed on sight.** A flaky test is exactly the kind of
thing that deserves a deliberate regression test before a fix, and the catch
rate needs measuring rather than assuming: this file's 2026-08-28 entry
records a concurrency test that went red on demand and still only caught its
bug 4 times in 20. Fixing this silently mid-review, on a PR that did not cause
it, would repeat the error the 2026-08-23 entry warns about.

**Operational note for now.** A red `integration` job on a PR whose diff
cannot reach the failing package is more likely this than a regression.
Re-run it once before investigating, and if it passes, check whether the
failing test asserts on anything the system-wide `claimDue` can touch.

### 2026-09-01, dashboard silently rendered 20 of 100 records
**What happened:** Against a real 100-record batch on a live Gateway,
`GET /v1/batches/{batch_id}/records` returned `records: 20`,
`total_count: 100`, `next_page_token: "20"`, exactly as documented
(`docs/API_GATEWAY.md`). `web/src/lib/api.ts`'s `getBatchRecords` sent no
`page_size` and never looked at `next_page_token`, so the dashboard rendered
only the first page and stopped. Confirmed against the live stack: the true
state distribution for the seeded 100-record batch was 51 recovered / 35
closed-uneconomic / 14 escalated, but the truncated first page showed 9 / 10
/ 1, a badly distorted picture, not just a smaller one.

**Blast radius.** `StateDistribution`, `TimelineView`, and `RecordsTable`
in `web/src/App.tsx` all share the one `records` array `getBatchRecords`
returned, so all three drew conclusions from a fifth of the batch. The
metric tiles directly above them come from `GET .../report`, which does
cover the full batch, so the state-distribution chart and the retry
timeline actively contradicted the tiles right above them on screen. This
was not a missing feature, it was two adjacent panels asserting different
totals for the same batch.

**Root cause.** No `page_size` on the request, and `next_page_token` was
read off the response but never fed back into a follow-up request. A
single-page client against a paginating server.

**Why the mock hid it.** `mockEngine.ts`'s `getBatchRecords` returned every
record for a batch in one response with `next_page_token` hardcoded to
`''`, regardless of how many records existed. Every dashboard dev cycle and
every prior review of this code ran against a mock that could not have
exposed a pagination bug even if the client were completely broken, which
it was. The bug only existed on the multi-page path, and mock mode had no
multi-page path.

**Fix.** `getBatchRecords` now requests `page_size=100` (docs/API_GATEWAY.md
documents `page_size` as optional but does not publish the Gateway's own
maximum, so this is a conservative, live-verified value, not a documented
ceiling) and follows `next_page_token` in a loop
(`collectAllRecordPages`) until the Gateway returns an empty token, capped
at 50 pages (5,000 records) so a very large batch cannot hang the dashboard
or trigger an unbounded number of requests. Hitting the cap sets
`truncated: true` on the result instead of silently stopping again; `App.tsx`
renders `RecordsTruncatedBanner` when that happens, stating how many of how
many records loaded, so a partial view is never presented as a complete
one. `mockEngine.ts`'s `getBatchRecords` now paginates for real (offset-style
`page_token`, default page size 20 to match the Gateway's own default),
so mock mode exercises the identical fetch-and-follow code path `api.ts`
uses against the live Gateway.

**Prevention.** Added `web/src/lib/api.test.ts` (fetch-and-follow loop
against a fake multi-page responder: asserts every record arrives, the
token sequence is correct, and the loop terminates and reports
`truncated` at the page cap instead of looping forever) and
`web/src/lib/mockEngine.test.ts` (asserts the mock itself now spans
multiple pages for its seeded batch, with no duplicate records across
pages). Both were run red against the pre-fix code first. The general
lesson: a mock that returns a paginated endpoint's entire result set in one
response, no matter how many records exist, is not a stand-in for that
endpoint, it is a stand-in for a different, unpaginated one, and it will
certify a client that is broken against the real contract. Any mock for a
paginated endpoint needs its own default page size and its own
`next_page_token`, not an escape hatch that returns everything at once.

### 2026-09-01, `in_flight_count == 0` means "not started" as well as "finished"

**Status: no code change, a note for whoever polls a batch next.** Caught
twice in one session while verifying other work, both times producing a
confident wrong reading.

**What happened.** Verifying that a batch seeded through the new
`POST /v1/demo/batches` route gets an accuracy score, the check polled
`GET /v1/batches/{id}/report` until `in_flight_count` reached 0, then read the
result. It reported `accuracy: ABSENT`, `recovered: 0`, `uneconomic: 0`, which
looked like the route had failed to write ground truth and flatly contradicted
the implementing agent's own measurement of 93.3%.

Querying Postgres directly showed the opposite: 15 ground-truth rows, and the
batch settled at 5 recovered, 8 closed-uneconomic, 2 escalated. Re-reading the
same report endpoint a moment later returned `93.3% over 15 records`. The
implementation was right and the verification was wrong.

**Root cause.** `in_flight_count` is computed with
`COUNT(*) FILTER (WHERE rs.current_state = ANY($2))` over a `LEFT JOIN` from
`record` to `record_state`. A record that has been inserted but not yet
consumed from `raw.events` has **no `record_state` row at all**, so it matches
neither the in-flight states nor any terminal one. It counts in nothing.

So `in_flight_count == 0` is true in two completely different situations: the
batch has finished, and the batch has not started. Polling it as a completion
signal reads the empty report in the gap between seeding and the first
consumer picking the records up.

**Why it matters beyond one bad check.** The same shape bit a manual
verification of the old dashboard button earlier in the same session, where
"records accepted, nothing processed" was initially read as the button being
broken when the decision engine had actually crashed. Anything that polls for
"is this batch done", a test, a script, a person watching a demo, is exposed.

**How to poll correctly instead.** Compare against `total_records` rather than
trusting a zero:

```
settled = recovered + escalated + closed_uneconomic + processing_failures
done    = (settled + in_flight_count) == total_records && in_flight_count == 0
```

That distinguishes "no records have state yet" from "every record reached a
terminal state". Or wait for a non-zero `in_flight_count` first, then wait for
it to return to zero.

**Not fixed in code, deliberately.** `in_flight_count` is correct for what it
says: how many records are currently in an in-flight state. The bug is in
treating it as a completion oracle. Adding a `started_count` or a batch-level
status would be a contract change (`docs/API_GATEWAY.md` is frozen) for a
problem that a correct poll already solves, so this is a note rather than a
change. If a future unit does add batch status to the contract, this entry is
the argument for it.

## 2026-09-02: the live event stream had no reconnect, and a clean close read as failure

**Symptom.** A red "Disconnected" badge sat in the dashboard header on a
system that was working. It was logged as a cosmetic label bug and priced at
one hour.

**What it actually was.** Three defects in `subscribeToBatch`
(`web/src/lib/api.ts`), found only by reading the code rather than the
symptom:

1. **No reconnection logic of any kind.** Once the WebSocket closed for any
   reason (a service restart, a proxy idle timeout, a brief network blip) it
   stayed closed for the life of the page. No event ever streamed again.
2. **A normal close was reported as a failure.** The Gateway closes with
   `websocket.StatusNormalClosure` when the upstream report stream ends,
   i.e. when the batch finishes. `ws.onclose` fired
   `onConnectionChange(false)` unconditionally, so a successfully completed
   run painted a red failure indicator on itself.
3. **A teardown race.** The cleanup called `ws.close()` and then set state
   synchronously, but `onclose` fires asynchronously afterwards and could
   clobber the newly selected batch's state back to disconnected.

**Why nobody noticed defect 1.** A separate two-second polling loop kept
refreshing the report and records, so the numbers on screen kept moving while
the stream itself was dead. The dashboard looked alive. This is the
instructive part: a redundant path masked the failure of the primary one, and
the only visible trace was a badge everyone had learned to ignore.

**Fix (#101).** Branch on the close code instead of treating every close as
identical. 1000 means the run finished: report `complete`, do not reconnect,
because nothing more will ever arrive. Any other code reconnects with
exponential backoff from 1s to a 30s cap, reporting `reconnecting` (amber)
and degrading to `disconnected` (red) only after three consecutive failures,
while still retrying underneath so it self-heals. A `closed` flag captured in
the closure is set before `ws.close()` and checked in every handler, so a
late callback after teardown is a no-op. The reconnect timer is cleared on
cleanup.

**Lessons.**

- **A cosmetic symptom is not evidence of a cosmetic cause.** This was
  triaged from what it looked like. Reading the code moved it from a
  one-hour label fix to a real reliability bug, and the estimate was wrong
  because the diagnosis was.
- **Redundancy hides failure.** The polling loop is worth keeping, but it
  meant a dead stream produced no visible symptom beyond a badge. Anything
  with a silent fallback needs its own explicit health signal.
- **`web/` still has no CI.** No typecheck, build or test job has ever run
  against it, which is also how the records pagination bug survived. The job
  is committed on branch `ci/frontend-checks` and needs
  `gh auth refresh -h github.com -s workflow` to push. Until then every
  frontend change is verified by hand and nothing catches the next one.

## 2026-09-02: Unit AD shipped twice and still did not deliver reproducibility

**What was claimed.** That seeding a batch makes a run reproducible, so the
same seed gives the same recovered total.

**What was true after the first fix (#99).** Nothing measurable had changed.
The roll derived from `hash(seed, record_id, attempt_number)`, which is a
deterministic function, but `record_id` is `uuid.NewString()` at generation
time, fresh every run regardless of seed. The function was never fed the same
inputs twice. Two same-seed runs still differed by Rs 153,632.

**How it passed review and CI.** The unit tests asserted that `seededRand`
returns the same output for the same inputs. That is true, and it is not the
claim. Nobody tested the claim actually being made, which is that two runs
with one seed produce the same result. The reviewer (me) checked the
concurrency design closely, because that was the hard part and it was
correct, and did not ask whether the inputs were stable. It surfaced only
because the user asked to verify by hand on a live stack.

**Second fix (#104).** Added `GROUND_TRUTH.roll_key`, derived from
`(seed, ordinal index in batch)` and written by both `SeedBatch` and
`scripts/batchgen`. `SimulateOutcome` keys the roll off that. Record ids stay
random uuids deliberately: making them deterministic would collide on the
primary key when the same seed is used twice, which had already happened
twice on the live stack. Proven red first: 7 of 25 ordinal positions diverged
under an identical seed before the change.

**What is still not reproducible, measured on a live stack.** Two same-seed
runs, all 100 records compared by ordinal:

```
amounts, failure codes                      0 diffs
hidden ground truth (p, delay, true bucket) 0 diffs
EV score and p at decision                  0 diffs
classification (root cause bucket)          3 diffs
final record state                          9 diffs
```

Of the 97 records that classified identically, 8 still ended differently. The
audit trail shows the mechanism exactly: two runs produced a byte-identical
first decision (`nudge EV 369793 paise, p=0.0900`), both nudged, both failed,
and then one re-scored to `no permitted action has positive expected value`
while the other re-scored to `nudge EV 143787 paise, p=0.0350` and recovered.
The economics did not differ. The **set of permitted actions** did.

**Root cause of the residual variance.** `schedule.go` enforces TRAI TCCCPR
2018's contact-hour window, `[10:00, 21:00)` IST, against the real clock,
while `DEMO_TIME_SCALE=300000` makes one real second about 3.5 simulated
days. A few hundred milliseconds of ordinary scheduler jitter therefore moves
simulated time across the window boundary and changes whether a nudge is a
permitted action. The remaining 3 diffs are live LLM sampling at
`LLM_SAMPLE_RATE=0.15`.

Neither is a defect introduced by #99 or #104. Both are consequences of
earlier deliberate choices: enforce a real regulatory window, and compress
time hard enough to finish a multi-day recovery cycle in seconds. Those two
choices are in tension, and nothing had previously forced the tension into
the open.

**Lessons.**

- **Test the claim, not the function.** A test that a pure function is pure
  will always pass and can never fail the way the system fails. Write the
  test at the level the promise is made: two runs, one seed, same total.
- **Verify a headline claim on the real system before recording it as done.**
  Both a subagent and a reviewer signed this off on green CI. One live run
  disproved it in about five minutes.
- **Compressed time is a correctness surface, not a demo convenience.** This
  is the second incident caused by `DEMO_TIME_SCALE` interacting with a
  wall-clock comparison, after the RecoveryWindow escalation on 2026-08-31.
  Any guardrail compared against a real clock needs to be audited against the
  scale factor, not just the ones that failed loudly.

## 2026-09-02: the integration job was never flaky, two schedulers shared one database

**Symptom.** The `integration` job failed intermittently from 2026-09-01,
blocked three PRs, and on 2026-09-02 reached `main`. Failures looked random:

```
run A (PR):    TestSchedulerFiresOnceWhenFakeClockPassesDueAt, TestBatchCorrectnessInvariants
run B (PR):    green, same branch, same content
run C (main):  four scheduler tests, plus TestBatchCorrectnessInvariants
```

Failure messages were all of one shape: `executor called 0 times`,
`Execute was never called`, `attempt_count = 0, want 1`.

**Root cause.** `claimDue` (`services/decision-engine/internal/engine/store.go`)
claims work with no batch or record filter:

```sql
WHERE rs.due_at IS NOT NULL AND rs.due_at <= $1
  AND rs.current_state IN ($2, $3)
ORDER BY rs.due_at LIMIT $4
```

`test/e2e/walking_skeleton_test.go` starts the **real decision-engine as a
subprocess**, and its scheduler calls exactly that. `go test ./...` runs
packages in parallel, so e2e's live scheduler and
`services/decision-engine/internal/engine`'s integration tests ran
concurrently against one Postgres and raced for the same rows. Whichever
ticked first claimed the other's records; the loser failed.

That explains everything that read as randomness. **Which** tests fail
depends on who wins the race. And it never reproduces on a developer machine
because the windows do not overlap there: the exact CI command, `-race`, all
packages, e2e included, passed locally four times in a row while `main` was
red.

**Fix (#105).** Split the job into two passes: everything except `test/e2e`,
then `test/e2e` alone. Only those two are serialised, so the other 49
packages still run in parallel. Rejected `-p 1`, which serialises all of them
permanently to resolve a conflict between two. `claimDue` was deliberately
left alone: claiming any due record is correct for a real scheduler, and
narrowing it to make tests pass would trade a test problem for a product one.

Also added a guard asserting the e2e pass actually ran tests, because
splitting one command into two tag-specific ones reintroduces the risk the
old comment warned about, and a tag mismatch that tests nothing while
reporting green is the 2026-08-23 incident above.

**Lessons.**

- **"Flaky" is a description, not a diagnosis.** This sat labelled a flake
  for a day with a plausible-sounding recommended fix (scope assertions to
  the test's own `record_id`) that would not have worked, because the
  interference came from a different test binary entirely. Naming something
  flaky ends the investigation, which is precisely when it should start.
- **A test that cannot reproduce locally is telling you about the
  environment, not lying to you.** The useful question was not "why is this
  test bad" but "what does CI do that I do not", and the answer was in the
  workflow file: parallel packages against shared infrastructure.
- **Shared mutable infrastructure is the real defect.** One Postgres for
  every package is the underlying problem; serialising two packages is a
  targeted fix, not a general one. A database per package is the durable
  answer and is parked in `docs/BACKLOG.md`.

## 2026-09-02: frontend `Source` type was missing the value most audit entries actually carry

Found while redesigning the record drawer (Unit AN). The task description
that started this work already named the symptom: "almost every entry ends
with `system · source: SOURCE_UNSPECIFIED`, repeated a dozen times." That
value should not have been renderable at all: `web/src/types.ts`'s `Source`
type listed only `SOURCE_LLM`, `SOURCE_RULES_FALLBACK`,
`SOURCE_TEMPLATE_FALLBACK`, the 3 values `docs/API_GATEWAY.md` documents,
because those are the only ones a *composed message* carries. But `source`
is set on every audit entry, not just ones with a message, and
`common.v1.Source`'s zero value, `SOURCE_UNSPECIFIED`, is real: Go's
`e.GetSource().String()` in `api-gateway/internal/httpapi/audit.go` renders
it for any entry that never set a source at all, which is most state
transitions in a trail. The frontend type quietly assumed a narrower wire
contract than the one actually in effect, and nothing caught it because
nothing compared the two: the drawer just rendered whatever string arrived,
typed or not, and TypeScript has no way to check a `string` value against a
union it was never told to include.

**Fix**: widened `Source` to the real 4-value enum. Frontend-only, no
backend or proto change; `docs/API_GATEWAY.md` (frozen, read-only) was left
alone since it correctly documents the 3 values a message carries, it was
the frontend type that silently narrowed further than that. Full reasoning
in `docs/DECISIONS.md` 2026-09-02.

**Lesson.** A closed-vocabulary TS type copied from a doc's list of "the
interesting values" rather than the wire enum's actual full set will
silently mistype the boring/default one, and a UI that stringifies whatever
arrives without checking against its own type will not surface the gap.
Worth checking, next time a frontend union looks copied from a table in a
doc: does the doc's table claim to be the complete value set, or just the
complete set of one particular sub-case?

## 2026-09-02: the live WebSocket had never worked, and three rounds of green tests said otherwise

**Symptom.** A red "Disconnected" badge in the dashboard header on a healthy
system. Triaged as cosmetic, estimated at one hour.

**What was actually wrong, in the order it was found.**

Unit AG (#101) found and fixed three real defects: `subscribeToBatch` had no
reconnect logic at all, a clean close on batch completion was reported as a
failure, and teardown raced with the next subscription. All three were
genuine, all three were fixed, the unit tests passed, CI was green, and the
badge was still red.

The fourth defect was the one causing the symptom. `websocket.Accept` in
`services/api-gateway/internal/httpapi/live.go` was called without
`OriginPatterns`, and `coder/websocket` refuses cross-origin handshakes by
default with **HTTP 403**, before any handler logic runs. The dashboard is
served from `http://localhost:5173` and the Gateway listens on `:8090`, so
every browser connection had always been rejected. Measured directly:

```
Origin: http://localhost:5173   ->  403
Origin: http://localhost:8090   ->  101 Switching Protocols
```

Note 403, not the 401 that `offersSubprotocol` returns for a bad key. The API
key was never the problem.

**So the live event stream had never functioned in any dev or demo run**, from
the day it was built until 2026-09-02.

**Fix (#106).** `WS_ALLOWED_ORIGINS`, a config-driven origin allowlist,
defaulting to unset which means same-origin only, so production behaviour is
unchanged for an operator who sets nothing. Set to the dashboard origin in
`configs/demo.env`. Verified afterwards in a real browser: 379 frames
received in twelve seconds, badge green, feed populating with real state
transitions.

**Why three rounds of review missed it.**

- **The tests exercised the state machine against a fake socket.** They
  proved reconnect, backoff and close-code handling were correct, and they
  were correct. Nothing tested that a handshake against the real Gateway
  could succeed, so the fact that it never had was invisible.
- **`live_test.go` does perform real handshakes**, but from an `httptest`
  server, which is same-origin by construction. The one condition that fails
  in production was the one condition the test could not reproduce.
- **A polling loop masked it.** The dashboard refreshes report and records
  every two seconds, so every number on screen kept moving while the stream
  was dead. This is the same lesson as the 2026-09-01 pagination bug and is
  now the second incident where redundancy hid the failure of the path it was
  backing up.
- **The reviewer (me) accepted a category error.** Having verified the three
  defects were real and fixed, I treated the unit as done. The symptom that
  started it, a red badge, was still present and I did not go back and check
  the original symptom against the real system.

**Lessons.**

- **Fixing every defect you found is not the same as fixing the symptom.**
  Close the loop by reproducing the original report, not by confirming your
  own changes work.
- **A test double cannot fail the way the real dependency fails.** Where a
  boundary has environment-specific behaviour, origin checks, TLS, auth
  handshakes, at least one test or one manual check must cross the real
  boundary.
- **Verify UI changes in a real browser against the real backend before
  calling them done.** This is now standing practice for this repo. Every
  frontend unit since has been checked that way, and it is how #106, the
  drawer truncation and the timeline overplotting were all found.

## 2026-09-03: a compiled binary has been checked into git since the first commit

Found while building Unit AJ's `scripts/loadgen`: `go build ./...` at the
repo root writes one binary per `main` package into the current directory
(`migrate`, `batchgen`, now `loadgen`), and one of those, `migrate` (an ELF
executable, several megabytes, no extension), is tracked in git, committed
in `fad9093` ("feat: platform packages, first migration, and local infra
stack"), the very first commit that added `scripts/migrate`. It has ridden
along in every commit since. Running `go build ./...` (or `make build`,
which is exactly that) locally regenerates it as an untracked modification
to a tracked binary file, which is how this surfaced: a routine `go build
./...` sanity check during this unit left `git status` showing a binary
diff that had nothing to do with the change being made.

**Partly fixed here, the rest left for whoever picks it up.** `.gitignore`
now has explicit entries for `/migrate`, `/batchgen`, `/loadgen` at the
root, so the new tool this unit adds (`scripts/loadgen`) cannot repeat this,
and a future `go build ./...` regenerating `/migrate` locally no longer
shows as a confusing modification to a tracked binary. Actually untracking
the already-committed `/migrate` blob (a `git rm --cached`) is left alone:
it is a repo-history change unrelated to Unit AJ's scope, and the binary
itself has caused no observed failure, `go run ./scripts/migrate ...` is
what every Makefile target and doc actually invokes, never this file.

**Lesson.** `go build ./...` from the repo root is not a no-op sanity
check, it is exactly what `make build` runs, so both leave one bare,
extension-less binary per `scripts/<name>` `main` package sitting in the
working tree. `.gitignore`'s pre-existing binary rules (`bin/`, `*.exe`,
`*.so`, `*.dylib`) never covered that shape, which is how `/migrate` got
committed in the first place and why it took until this unit to notice.
Add the new entry above whenever a future `scripts/<newname>` main package
is added, rather than waiting to rediscover this the same way.

## 2026-09-03: DECISION_ENGINE_ADDR made required with nothing setting it for api-gateway, and the wrong port sitting in .env

Unit Y (`docs/PHASE5_5_IMPLEMENTATION.md`) added `POST /v1/webhooks/payment-downtime`
to the Gateway, which needs a gRPC client to the Decision Engine. The PR
added `DecisionEngineAddr: l.Str("DECISION_ENGINE_ADDR")` to api-gateway's
config, `l.Str` meaning required, and shipped three separate gaps around it,
caught by CI on the PR (all nine e2e tests failing, not a flake):

1. `test/e2e/harness_test.go`'s api-gateway env block never set
   `DECISION_ENGINE_ADDR` (only World Simulator's block did), so the
   Gateway subprocess died at startup with "invalid configuration:
   DECISION_ENGINE_ADDR is required but not set" and every e2e test timed
   out waiting for a Gateway that was never coming up.
2. `Makefile`'s `run-api-gateway` target set `INGESTION_ADDR`,
   `REPORTING_ADDR`, `AUDIT_ADDR` and `WORLD_SIMULATOR_ADDR` but not
   `DECISION_ENGINE_ADDR`, so `make run-api-gateway` and `make demo-up`
   would have hit the same fatal startup error outside CI too.
3. `.env`/`.env.example` had `DECISION_ENGINE_ADDR=localhost:9090`, which is
   ingestion's port, not the Decision Engine's (`9196` per
   `run-decision-engine`). Because `grpc.NewClient` dials lazily, had (2)
   not existed this would NOT have failed at startup: the Gateway would have
   come up looking healthy and every downtime webhook would have dialled
   ingestion's gRPC server instead. In practice this specific value is
   never read as-is (the Makefile always overrides it, see the fix below),
   but the whole placeholder block reads as more real than it is if nobody
   already knows that.

**Fixed**: `harness_test.go` now sets `DECISION_ENGINE_ADDR` for
api-gateway from the same `deGRPCPort` World Simulator's block already
uses; `run-api-gateway` now sets `DECISION_ENGINE_ADDR=localhost:9196`
alongside its other overrides; `.env`/`.env.example` gained a comment
above the whole service-to-service address block explaining that every
value there is the single-service `:9090` placeholder, not that service's
real port, and that only the Makefile's `run-<service>` targets carry the
real fixed ports. `services/api-gateway/cmd/main_test.go` gained
`TestRunAPIGatewayMakeTargetSetsEveryRequiredAddr`, parsing the Makefile's
own `run-api-gateway` recipe and asserting it exports every env var
`loadConfig` requires, so a future required config addition that forgets
this target fails a fast unit test instead of nine slow e2e ones.

**Lesson, same shape as the 2026-08-31 `configs/demo.env` entry and the
`LLM_SAMPLE_RATE` two-knob problem**: this repo has three places that can
each independently claim to configure a service (`.env`, a `Makefile`
`run-<service>` target, and a test harness), and adding a new *required*
config value means updating all three that actually run the service, not
just the one being worked in at the time. `l.Str` (required) is still the
right call for a cross-service address an unconditional production route
depends on, see `docs/DECISIONS.md`; the gap was in the three call sites,
not in requiring the value.

## 2026-09-03: a regression test read the gitignored `.env`, green locally and red on every CI runner

The same PR as the entry directly above this one (`DECISION_ENGINE_ADDR`
made required with three call sites not catching up) added a test to guard
against it happening again: `TestRunAPIGatewayMakeTargetSetsEveryRequiredAddr`
merged the real `.env` with the `run-api-gateway` recipe's own overrides and
called `loadConfig` against the result. It passed on the machine that wrote
it. CI failed both `build-test` and `integration` immediately:

```
--- FAIL: TestRunAPIGatewayMakeTargetSetsEveryRequiredAddr (0.00s)
    makefile_test.go:148: open /home/runner/work/Momotaro/Momotaro/.env:
    no such file or directory
```

`.env` is gitignored (`AGENTS.md`: "the real `.env` is gitignored, never
committed"), so it exists on every developer machine that has ever run
`make demo-up` and on none of CI's. A test built and run against a local
checkout with a real `.env` present cannot see this failure mode at all,
which is precisely why it shipped.

**This is the mirror image of the 2026-09-02 WebSocket origin-check
incident above, not a new class of bug.** That one was a test suite passing
in CI because CI could not reproduce a production-only condition (a
cross-origin browser request). This one is a test passing on a developer
machine because a developer machine has a file CI does not. Same shape,
opposite direction: **whether a test's result depends on which environment
it runs in is itself worth asking, independent of whether the test looks
correct.**

**Fix.** `envFromDotFile`, and the one test that used it, were pointed at
`.env.example` instead, which is tracked and carries the same values for
every field `loadConfig` needs (diffed to confirm; the two files differ only
in real secrets `.env` fills in that `loadConfig` never reads, and in a few
comment blocks). This gives equivalent coverage while running everywhere,
which is why it was chosen over the simpler fix of skipping the test when
`.env` is absent: a test that runs in CI beats one that documents why it
cannot. Verified by moving the real `.env` aside and running the entire
suite (`go test ./...`) with it genuinely absent, matching CI's own
condition exactly rather than assuming the fix worked.

**Lesson.** A test that reaches for a real config file rather than a
checked-in fixture inherits that file's own guarantees, or lack of them.
`.env` has none: gitignored by design, so its presence is a property of the
machine, not the repository. Anything a test needs to see should be a
tracked file (`.env.example`, here) or something the test constructs itself,
never a file whose entire purpose is to hold values nobody commits.

## 2026-09-03: a newly required variable breaks every existing .env, three times running

**Symptom, the third time.** After Unit Z (#119) merged, `make demo-up` left
the Gateway dead:

```
"fatal" err="invalid configuration:
  - WEBHOOK_SECRET is required but not set"
```

**The pattern, not the instance.** `.env` is gitignored. A PR that adds a
newly required variable can update `.env.example`, which is tracked, and
cannot touch anyone's actual `.env`. So the change is correct, CI is green,
and every existing working tree breaks the next time it starts the stack.

This has now happened three times in two days:

| Unit | Variable | How it surfaced |
|---|---|---|
| AI (#113) | `LLM_ROUTE_CONFIDENCE_THRESHOLD` | silent: sample rate set, zero live calls, no error |
| Y (#118) | `DECISION_ENGINE_ADDR` | all nine e2e tests failed in CI |
| Z (#119) | `WEBHOOK_SECRET` | Gateway refused to start locally after merge |

Only the middle one was caught by CI, and only because the e2e harness
happens to start a real Gateway. The first was silent and needed a startup
warning added to catch it. The third was caught by starting the stack by
hand after merging.

**Fix.** `make check-env` reports keys present in `.env.example` and absent
from `.env`. Deliberately informational, exit 0, never a build failure:
some of those keys are optional with defaults and some are set only in
`configs/demo.env` under `PROFILE=demo`, so failing on them would cry wolf
and train everyone to ignore the one time it matters. Documented in
`README.md` next to the other run instructions.

**Lessons.**

- **A gitignored file is a config surface a PR cannot reach.** Anything
  required that lives there will break existing environments no matter how
  careful the PR is. The tracked file and the real file drift by
  construction.
- **Prefer a loud failure to a silent one, but prefer neither.** Unit Y and
  Z both failed loudly, which is right, and both still cost time. Unit AI
  failed silently and cost more. The startup warning added for AI is the
  pattern worth repeating: name the variable and say what will not work.
- **Verify by starting the product after a merge, not only by watching CI.**
  CI does not run `make demo-up`. Every one of these was found by a human
  running the thing.

## 2026-09-03: live webhook traffic exposed three bugs at once

**How it surfaced.** A `make loadgen RATE=2 EVENTS=200` run against a live
stack, then reading the dashboard. None of this shows up in a seeded demo
batch, and CI cannot see any of it: all three need continuous traffic that
arrived through the public webhook API.

### 1. Records that arrive by webhook never settle

**Symptom.** The webhook batch read `RECOVERY RATE 0.0%`, `RECOVERED Rs 0`,
of Rs 19.9L at risk. In the database, 220 records: **146 stuck in `NUDGED`,
55 in `RETRYING`, 19 escalated, and zero terminal**.

**Cause.** Webhook records have no `GROUND_TRUTH` row, because nothing
seeded them. `store.loadRecordProfile` inner-joins that table, so it
returned `errNoGroundTruth`, and `SimulateOutcome` turned that into a gRPC
`NotFound`. The Executor therefore had no outcome to record and the record
sat in flight forever.

The original code was deliberate and its comment said so: World Simulator
"exists solely to answer against that sealed profile, has nothing to roll
and must not guess a probability." That rule is right about not fabricating
an answer key. It is wrong about refusing to answer at all, because in a
demo there is no real bank behind the webhook, so refusing is not
neutrality, it is a record that never finishes.

**Fix.** `unseededProfile` derives a plausible profile from the record's own
failure code, via a new `syntheticgen.BucketForFailureCode`. It writes **no
`GROUND_TRUTH` row**, so accuracy and the baseline comparison stay correctly
absent for webhook traffic, which is exactly the distinction Unit AJ set out
to draw. An unrecognised code falls back to `USER_ACTION_NEEDED` rather than
stranding the record, since real gateways invent codes we have never seen.

### 2. The sampling ceiling did not cover nudge composition

**Symptom.** 479 Groq attempts for one run, of which **361 hit an open
circuit breaker**, 44 were rate limited and 3 timed out.

**Cause.** `LLM_SAMPLE_RATE` was applied in `clients.classify` and nowhere
else. `clients.composeNudge` went straight to the provider chain. With 146
records nudged, composition alone blew through Groq's free tier, opened the
breaker, and then **classification degraded to rules as well**. An
unbudgeted path spent the budget the budgeted path was protecting.

**Fix.** `composeNudge` consults the same `llmBudget`. When the ceiling is
spent it sets `force_template_only` and the Classifier's terminal rung
answers, which is a real Hinglish message either way.

### 3. Simulated time on the timeline axis became absurd

**Symptom.** `day 3347 of the 7-day recovery window`.

**Cause.** The axis frames elapsed time as a position in the recovery
window, which holds for a seeded batch that settles in seconds and reaches
day 22 or day 44. Continuous traffic runs for real minutes, and at 300000x
16 real minutes is over nine simulated years.

**Fix.** Keep the window framing up to 99 simulated days, where it still
reads as a position in a recovery cycle, and beyond that report the span
plainly in the unit a person would say out loud. A first attempt at this
switched at 7 days and broke an existing test asserting `day 10`; that test
was right and the threshold was wrong.

**Lessons.**

- **A demo mode and a production mode diverge in ways neither one's tests
  cover.** Every seeded-batch test passed throughout. The webhook path has
  its own integration test and that passed too. What nobody had was a long
  continuous run watched on the dashboard.
- **A budget that covers one caller is not a budget.** Check every path to
  an expensive dependency, not only the one the limit was written for.
- **A unit that is correct at demo scale can be nonsense at another scale.**
  The time framing was not wrong, it was unbounded.

## 2026-09-03: TestGatewayReportRoutesAndLiveRelay fails reading the live WebSocket message, on a clean `main` too

> **Corrected after this entry was written.** The supervising session
> re-ran this same test against an unmodified `main` in its own worktree,
> separately from the run this entry describes: **4 consecutive passes**,
> 25 to 38 seconds each. The two failing runs below both took 73 seconds,
> roughly double. That duration gap, not the failure itself, is the real
> signal: this reads as a resource-contention timeout under load, the same
> shape as the `TestSmokeBatchReachesExpectedTerminalStates` flake reported
> lower in this same entry, not a deterministic regression. The "Status"
> and "Lesson" sections below overclaimed certainty ("consistently
> fails", "not a one-off flake", "a real regression to chase, not retry
> past") from two data points taken in one loaded environment. Left
> unedited below since the investigation itself, isolating the failure
> against a clean worktree before blaming this unit's diff, was the right
> instinct and is worth keeping visible; only the conclusion drawn from it
> was too strong. Downgraded to: **watch, not chase**, alongside the
> existing scheduler flake (2026-09-01), until it fails a third time with a
> duration that does not point at contention.

**Symptom, while verifying Unit AM** (`docs/DEMO_READINESS.md`, the
read-only config panel). `go test -race -count=1 -tags=e2e ./test/e2e/...`
consistently fails one test:

```
--- FAIL: TestGatewayReportRoutesAndLiveRelay (73.13s)
    gateway_report_test.go:143: read live update: failed to read JSON
                                 message: failed to get reader: context
                                 deadline exceeded
```

The rest of the package passes silently (no other test's log printed, and
the batch itself is submitted, classified and scheduled fine before the
failure: `record classified and scheduled ... state=RECORD_STATE_
RETRY_SCHEDULED` appears in the log immediately before the timeout). Two
back-to-back runs on this unit's branch both failed identically, so it is
not a one-off flake on this branch.

**Checked before assuming it was pre-existing.** Ran the identical command
against a clean `main` checkout in a separate `git worktree` (no changes
from this unit present at all): same failure, same line, same timeout.
This unit's diff (`GetAgentConfig` on the Decision Engine, `GET
/v1/demo/config` on the Gateway, the new `AgentConfigPanel` section) never
touches `audit.events` publishing, `Reporting.StreamBatchUpdates`, or the
Gateway's WebSocket relay (`live.go`), so this is not something this unit
introduced.

**Status: open, not fixed here.** Left as a report only, the same posture
`docs/INCIDENTS.md` 2026-09-01's scheduler-flake entry takes: whoever next
touches the live-relay path should treat this as a real regression to
chase, not retry past. Every other e2e test in the package (`TestWalking
SkeletonReachesRecovered`, `TestCrashSafetyDecisionEngineRestart`,
`TestFallbackFromFailedLLMToRules`, `TestBatchCorrectnessInvariants`,
`TestSmokeBatchReachesExpectedTerminalStates`,
`TestSubmitEventIdempotencyDeduplicatesRecord`,
`TestSubmitBatchResubmitCreatesIndependentRecords`) was confirmed passing
on this unit's branch, run explicitly rather than assumed from the
package-level `FAIL`.

**Lesson.** A package-level `FAIL` is not evidence that a diff broke
something; the one failing test has to be isolated and checked against an
unmodified `main` before concluding either way. Trusting a red package
result at face value here would have meant chasing a bug in code this unit
never touched.

**A second test, `TestSmokeBatchReachesExpectedTerminalStates`, also failed
once** in a run that exercised several tests back to back under `-race`,
with every one of its subtests individually reported `PASS` and no
assertion failure printed, the shape of a resource-contention flake under
`-race` running several full nine-process stacks concurrently, matching
`docs/INCIDENTS.md` 2026-09-01's already-documented scheduler flake. Run
alone it passed cleanly (51.22s, all seven subtests green). Not
re-investigated further per that entry's own guidance: re-run once, do not
let re-running become reflexive.
