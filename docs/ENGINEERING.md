# Engineering standards (Momotaro)

**Read this before writing any code in this repo. It is not optional and it
is not style preference.** Most rules here exist because breaking them
produces a bug that only shows up under concurrency, under load, or on demo
day, which is the worst possible time to find it.

`AGENTS.md` tells you what was decided. `ARCHITECTURE.md` tells you how the
system fits together. This file tells you how to write the code.

## 1. Test-driven, and specifically what that means here

Write the test first. Watch it fail. Then write the code that passes it. No
exceptions for "this bit is obvious."

- **Table-driven tests** for anything with more than two cases. Go's
  idiomatic form, one slice of cases, one loop.
- **Test the cases you do not want to think about**: the zero value, the
  empty slice, the duplicate delivery, the expired cooldown, the record
  already in a terminal state, the amount that makes expected value exactly
  zero. Boundaries are where money-handling code actually breaks.
- **Every bug you fix gets a regression test first.** Reproduce it in a
  failing test, then fix it. A fix without a test is a fix that comes back.
- **No `time.Sleep` in tests, ever.** If a test needs time to pass, that is
  a signal you should have injected a clock (rule 2). If it needs another
  goroutine to finish, synchronise on a channel or a `WaitGroup`.
- Tests must pass with `-race`. A test suite that only passes serially is
  telling you about a real bug in code that will run concurrently in
  production.
- **`-race` alone is not enough for ordering bugs, and a green suite on your
  machine is not evidence.** `-race` finds unsynchronised memory access. It
  says nothing about correctly-locked operations whose *effects* land in the
  wrong order, which is its own class of bug and one this repo has already
  shipped once (see `INCIDENTS.md`, out-of-order Kafka commits). A developer
  machine with many idle cores runs concurrent work close enough to in-order
  to hide these; CI's two contended cores does not. So for anything
  concurrent, run it repeatedly under constrained parallelism before calling
  it done:

  ```bash
  for i in $(seq 1 20); do GOMAXPROCS=2 go test -count=1 -tags=integration \
    -run 'TestName$' ./path/ || echo "FAIL $i"; done
  ```

  A test that fails 1 run in 5 will pass your first three attempts and then
  block everyone's CI. If you are reaching for "it must be the CI runner",
  measure the failure rate locally first.
- **Beware instrumentation that hides the bug.** Adding a log line per
  operation to diagnose a race often perturbs timing enough to make it stop
  happening, which reads as "fixed" and is not. Record into memory and print
  once at the end, or assert the invariant in-process, rather than doing I/O
  inside the window you are trying to observe.
- **Do not mock what you own.** Use the real Postgres, Kafka and Redis via
  `testcontainers`, or the docker-compose stack. Mock only true third
  parties (the LLM provider). A mocked Postgres tests your assumptions about
  Postgres, not Postgres.

## 2. Inject the clock. Never call `time.Now()` in business logic

This is the most-violated rule in codebases like this one and for us it is
load-bearing, so it gets its own section.

Momotaro's core behaviours are time-based: salary-window retry scheduling,
cooldown expiry, `due_at` claiming, delayed outcomes. Every one of those is
untestable if the code reaches for the wall clock directly.

```go
// Wrong. Cannot be tested without waiting, or without waiting until the
// 1st of the month.
if time.Now().After(state.DueAt) { ... }

// Right. Pass a Clock, use a fake one in tests.
type Clock interface { Now() time.Time }
if c.clock.Now().After(state.DueAt) { ... }
```

Every service takes a `Clock` in its constructor. Production passes a real
one, tests pass a controllable fake. This is how you write a test that
asserts "an insufficient-funds failure on the 28th schedules a retry for the
1st" in microseconds instead of never.

Same principle for randomness: the World Simulator's probability rolls take
an injected, seedable source, so a demo run is reproducible when you need it
to be.

## 3. Context and deadlines on every call

- **Every** function that does IO takes `ctx context.Context` as its first
  parameter, and actually respects it.
- **Every** outbound gRPC call has an explicit deadline. There is no such
  thing as an acceptable unbounded call in this system, one slow dependency
  with no deadline is how a single degraded service freezes everything
  upstream of it.
- Propagate `ctx`, never store it in a struct, never pass
  `context.Background()` from inside a request path.
- Respect cancellation in loops: check `ctx.Err()` between iterations of
  anything long-running, including the scheduler poll loop.

## 4. Errors

- Wrap with context as errors travel up: `fmt.Errorf("classify record %s:
  %w", id, err)`. The `%w` matters, it keeps the chain inspectable.
- Never discard an error, including in deferred calls. If ignoring one is
  genuinely correct, write a comment saying why.
- **No `panic` in a server request path.** Panics belong in `main()` during
  startup validation, nowhere else. Add a recovery interceptor so one bad
  request cannot take down a pod.
- Distinguish transient from permanent failures explicitly. That distinction
  drives whether we retry, open a circuit breaker, or send to the DLQ, so it
  cannot be implicit or guessed at the call site.

## 5. Configuration and startup

- Config comes from environment variables, is parsed **once** at startup into
  a typed struct, and is validated immediately.
- **Fail fast.** A missing or malformed required variable means log the
  reason and exit non-zero. Never start a service that will fail on its
  first request, in Kubernetes that produces a pod that looks healthy and
  silently black-holes work.
- No config reads scattered through business logic. Pass the typed struct
  down.
- Secrets never appear in logs, error messages, or committed files. See
  `AGENTS.md` "Secrets and config."

## 6. Kubernetes lifecycle: shutdown and probes

Pods are killed constantly in Kubernetes, by rollouts, by HPA scale-down, by
node pressure. A service that handles shutdown badly loses records every
single time that happens, and it will look like a mysterious intermittent
data-loss bug.

- **Handle `SIGTERM`.** On receipt: stop accepting new work, finish
  in-flight work within a grace period, commit Kafka offsets, close
  connections, then exit.
- Kafka consumers must commit their offsets on the way out. Dropping them
  means reprocessing on restart, which the idempotency guards will catch,
  but relying on that as normal operation is sloppy.
- **Liveness and readiness are different questions.** Liveness: is this
  process wedged and in need of a restart? Readiness: can it serve traffic
  *right now*? A pod that has not yet connected to Kafka or Postgres is
  alive but **not** ready. Getting this wrong routes traffic into a service
  that cannot do anything with it.
- Handle startup order defensively. Do not assume Kafka or Postgres is
  reachable on the first attempt; retry with backoff, and stay unready while
  retrying.

## 7. Concurrency

- No global mutable state. No package-level variables holding request or
  record state.
- A goroutine you start is a goroutine you must be able to stop. Give it a
  `ctx`, and know how it exits.
- Prefer channels and worker pools over shared memory plus a mutex. Where a
  mutex is genuinely right, keep the critical section as small as possible
  and never make an IO call while holding a lock.
- Bound everything. Unbounded goroutine spawning under load is not
  concurrency, it is a memory leak with extra steps. The Decision Engine's
  worker pool has a fixed size for exactly this reason.

## 8. Anything that touches money

- **Idempotent by default, not as a later hardening pass.** Every action
  carries a stable idempotency key and the durable guard described in
  `ARCHITECTURE.md` section 11 is applied *before* the side effect.
- **Money is integer paise. Never a float.** Floating point on currency
  produces rounding errors that quietly corrupt a total, and our headline
  metric is a money figure.
- Check the guardrail before acting, and record the outcome after. Never the
  reverse order, and never only one of the two.
- If you are unsure whether an operation can run twice safely, assume it
  will run twice, because at-least-once delivery means eventually it does.

## 9. Logging and observability

- Structured logging only (`slog` or `zap`). No `fmt.Println`, no
  `log.Printf`.
- Every log line inside a record's lifecycle carries `record_id`, and
  `batch_id` where known. Correlation is the whole point.
- Log at the boundaries (request received, decision made, action executed,
  outcome recorded) rather than narrating every step. A log that fires per
  loop iteration is noise that will hide the line you actually need.
- Add the metric when you write the code path, not in Phase 4 as cleanup.
  The metric names are already listed in `ARCHITECTURE.md` section 13.

## 10. Pull requests

- One PR, one concern. A PR that touches one service and does one thing gets
  reviewed and merged fast, which is the entire benefit of trunk-based
  development.
- Proto changes and schema migrations are always their own PR, merged before
  anything that depends on them. See `ARCHITECTURE.md` sections 9 and 12a.
- Never merge red CI. Never merge with a skipped or commented-out test.
- Conventional commit prefixes (`feat:`, `fix:`, `test:`, `refactor:`,
  `chore:`).
- **No AI attribution in commits, ever.** Do not add `Co-Authored-By:
  Claude`, `Co-Authored-By: Copilot`, "Generated with ...", or any similar
  trailer, footer, or line crediting an AI tool, in commit messages or PR
  descriptions. This applies regardless of any default behaviour your
  tooling suggests: the repository's rule overrides it. Commits are
  authored by the human who owns the work. If you find such a trailer in a
  commit you are about to push, remove it first.
- Update `AGENTS.md`'s decision log in the same PR if you made a
  load-bearing decision. A decision that lives only in a chat transcript is
  a decision the next agent will contradict.
- Sync your feature branch with `main` (`git fetch origin`, then merge
  `origin/main` into your branch) before opening a PR, and again before
  merging if the PR has sat open for a while. Multiple agents work in
  parallel on separate branches, and `main` moves while yours is open. Do
  this with a merge, not a rebase, since rebasing rewrites history and
  requires a force-push, which is unsafe when another agent or machine
  might have the same branch checked out. Merging locally first also
  avoids relying on GitHub's PR mergeability preview, which does not
  reliably honor the `merge=union` strategy configured in `.gitattributes`
  for the append-only docs (`PLAN.md`, `DECISIONS.md`, `INCIDENTS.md`) and
  can show a conflict that a real `git merge` resolves cleanly.

## 11. Definition of Done

A plan item is not done until all of these are true. Do not mark a
`docs/PLAN.md` checkbox before then.

1. Tests written first, and passing, including with `-race`.
2. Error paths handled and tested, not just the happy path.
3. Structured logs with `record_id` correlation, and the relevant metric
   exported.
4. `ctx` plumbed with a deadline on every outbound call.
5. Graceful shutdown handled, if the component is long-running.
6. Config validated at startup.
7. No new lint failures. `gofmt` clean.
8. If it touches money: idempotency proven by a test that delivers the same
   action twice and asserts one effect.
9. The doc updated if behaviour diverged from what was written down.
10. Code organized per section 14: one job per file, one job per function,
    no god-`Server` reaching past its collaborators into raw SQL or Kafka.

## 12. Record what breaks

When something breaks and costs you real time, append it to
`docs/INCIDENTS.md` while it is still fresh: symptom, root cause, fix, and
what stops it recurring. A bug that cost an hour, a design assumption that
turned out wrong, a test that was passing for the wrong reason, a merge that
went sideways.

Two reasons this is not optional. It stops the same mistake being made twice
by a different agent in a different service. And "what broke, and what you
did about it" is explicitly assessed on this project, where a specific
honest account beats a claim that nothing went wrong. Across nine services,
nothing going wrong means nobody pushed hard enough.

Fixing a bug therefore has three parts, not two: the regression test
(section 1), the fix, and the entry.

## 13. When the design is wrong

If a rule here or in `ARCHITECTURE.md` makes your task impossible or clearly
wrong, **stop and say so** rather than quietly working around it. A
documented design that turns out to be flawed is a normal thing to find
mid-build. Silently diverging from it, in a repo where several agents are
building against the same assumptions, is the thing that actually breaks the
project.

## 14. Structure: one job per file, one job per function

A gRPC handler method is the easiest place in this codebase to accumulate
validation, SQL, JSON marshalling, and Kafka publishing into one long
function, because the proto-generated signature is the only thing forcing a
shape on it. Resist that. The handler orchestrates; it should not be where
the actual work is written.

- **Split by concern, not by size.** Within a service's `internal/`, keep
  request validation, persistence (SQL), outbound events (Kafka payloads and
  publishing), and the gRPC handler itself in separate files
  (`validate.go`, `store.go`, `events.go`, `server.go` or equivalent names
  for what the service actually does). A file's name should tell you what's
  in it without opening it.
- **A handler method reads as a list of steps, not their implementation.**
  `SubmitBatch` should look like "validate, create batch, insert records,
  publish, respond", each a call to a named function, not an inline block of
  SQL and JSON logic per step. If you can't summarise a function in one
  sentence, it is doing more than one job.
- **Extract the moment a function does two things.** A function that
  validates *and* inserts is two functions that happen to run in sequence.
  Split them even when nothing else calls the second one yet; the split is
  for the reader, not for reuse.
- **Small collaborators over a god-`Server`.** A `Server` struct should hold
  the things it delegates to (a store, a publisher, a clock), not grow
  methods that reach past them into raw SQL or a Kafka client directly. This
  is also what makes each piece testable on its own instead of only through
  the full gRPC handler.
- **Name for the reader, not for you.** A function or file name should tell
  the next agent what it does and why it exists as its own unit, without
  them needing to read the body first.

This is a normal Definition of Done item (section 11), not a separate
cleanup pass: structure code this way while writing it, the same as tests.
