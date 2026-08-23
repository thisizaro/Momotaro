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

### 2026-08-23, `scanRecords` would have failed on every unscoped invariant check

**What happened:** Caught by hand while writing `services/audit/internal/
server/store.go`'s `scanRecords` query, before a test ever ran it: `psql`
reproduced `ERROR: invalid input syntax for type uuid: ""` for
`SELECT 1 WHERE '' = '' OR ''::uuid = ''::uuid;` before any Go code existed
to hit it.

**Root cause:** The query was `WHERE $1 = '' OR r.batch_id = $1::uuid`,
intending the empty-string branch to short-circuit the cast when no batch
filter is requested (`VerifyInvariantsRequest.batch_id` empty means "check
everything"). Postgres does not short-circuit `OR` the way a procedural
language does; both operands are evaluated regardless, so `''::uuid` runs
and errors every time `batch_id` is empty, i.e. on every unscoped
`VerifyInvariants` call, which is the common case.

**Fix:** Compare `r.batch_id::text = $1` instead of casting the parameter to
`uuid`. A text comparison never errors regardless of what `$1` contains, so
both the empty-string case and a malformed non-empty `$1` (see
`TestScanRecordsWithMalformedBatchIDMatchesNothingRatherThanErroring`)
degrade to "matches nothing" instead of an error; `Server.VerifyInvariants`
still validates the batch_id format up front with `uuid.Parse` so a typo
gets a clear `InvalidArgument` rather than a silent empty result.

**Prevention:** Added `TestScanRecordsWithEmptyBatchIDChecksEverything` and
`TestVerifyInvariantsEmptyBatchIDChecksEverythingWithoutErroring` as
regression tests. More generally: when a SQL query has an "optional filter"
shaped like `$1 = '' OR column = $1::sometype`, check the cast side against
an actually-empty parameter by hand (`psql`, not just reading the query) —
`OR` short-circuiting is a habit carried over from procedural code that SQL
does not share.
