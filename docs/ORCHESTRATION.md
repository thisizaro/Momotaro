# Orchestration: running multiple agents on Momotaro

For the person coordinating the build. Contains the current state, the
immediate next steps, and two copy-paste templates: the prompt to give a new
agent, and the per-service `AGENTS.md` boilerplate.

## Where the project stands

- **Design and planning: complete.** `docs/PRD.md`,
  `docs/ARCHITECTURE.md`, `docs/ENGINEERING.md`, `docs/API_GATEWAY.md` are
  settled. `docs/DECISIONS.md` has the chronology and reasoning.
- **Code: none yet.** `services/`, `demo/`, `scripts/` are empty.
- **Next: Phase 0 in `docs/PLAN.md`**, which is deliberately sequential.

## The one sequencing rule that matters

**Finish and merge Phase 0 before allocating services to agents.** Every
Phase 0 item is a shared foundation: the protos every service compiles
against, `internal/platform/` (clock, logger, config, interceptors), the
first migration, the root `go.mod`. Fan out before those exist and you get
one `Clock` implementation per agent and a day of reconciliation.

Two exceptions that can start immediately, in parallel with Phase 0:

- **`web/`**, which only needs `docs/API_GATEWAY.md` and can mock responses.
- **`scripts/`** (batch generator, loadgen), which only needs the schema.

The last Phase 0 item is the **walking skeleton**: one record through all 7
services with everything hardcoded. That is the gate. Once it is green,
every agent deepens their own service against an integration that already
works, instead of everyone integrating for the first time on day three.

## Suggested allocation once Phase 0 is merged

With 2 to 3 agents, run them in this order of priority. `decision-engine`
is roughly 3x any other service (state machine + keyed consumer + offset
management + scheduler worker), so start it first and expect it to run
longest.

| Priority | Scope | Notes |
|---|---|---|
| 1 | `services/decision-engine` | critical path, start first |
| 2 | `services/classifier` | rules first, provider chain in Phase 3 |
| 3 | `services/executor` + `demo/` simulators | naturally paired |
| 4 | `services/api-gateway` + `services/ingestion` | both thin |
| 5 | `services/audit` + `services/reporting` | both read-side |
| 6 | `web/` | can run from day one |

## Template: prompt for a new agent

Fill in the bracketed parts.

```
You're working on the Momotaro repo. Your scope is [services/classifier]
and nothing else.

Read in this order before writing any code:
1. AGENTS.md (orientation, locked decisions)
2. docs/ENGINEERING.md (mandatory: TDD, clock injection, context
   deadlines, error handling, graceful shutdown, money handling, and the
   Definition of Done that gates every PLAN.md checkbox)
3. docs/ARCHITECTURE.md sections [2, 2a, 5, 5a, 9]
4. [services/classifier]/AGENTS.md (your boundary contract)
5. proto/[classifier]/v1/[classifier].proto (your interface, the source of
   truth, not prose)

Your tasks are exactly these items from docs/PLAN.md [Phase 1]:
[- paste the specific checkbox lines]

Rules:
- Do not modify other services, proto/, migrations/, or
  internal/platform/. If you need a change in any of them, stop and
  propose it. Another agent is probably working there.
- TDD: write the failing test first, then the code. Tests must pass with
  -race.
- Branch svc/[classifier]/<short-task>, one PR per concern, CI green
  before merge.
- Tick your own PLAN.md boxes and append to docs/DECISIONS.md when you
  settle something load-bearing. Both files union-merge, so that is safe.
- If a documented design turns out to be wrong or impossible, say so
  rather than working around it silently.
```

## Template: per-service `AGENTS.md`

One of these at the root of each service directory. It is the service's
fence, not a summary.

```markdown
# AGENTS.md ([service-name])

## What this service does
[Two sentences. Full detail in docs/ARCHITECTURE.md section N.]

## Interface
`proto/[name]/v1/[name].proto` is the source of truth for this service's
API. Never describe the interface in prose here, point at the proto.
Changing it requires its own PR, merged before any code depending on it.

## Owns
- `services/[name]/**`
- Postgres tables it may WRITE: [from ARCHITECTURE.md section 10a]
- Postgres tables it may only READ: [...]

## Must not touch
- Any other service's directory
- `proto/`, `migrations/`, `internal/platform/` (propose, don't edit)
- Tables it does not own

## Depends on
- [e.g. Classifier via gRPC, Postgres, Redis]
- Shared code comes from `internal/platform/` only

## Relevant architecture sections
[section numbers, so the agent reads the right 200 lines not all 900]
```

## Operational notes

- **`docs/PLAN.md` and `docs/DECISIONS.md` union-merge** (see
  `.gitattributes`), so agents update them directly with no orchestrator
  bottleneck. Occasionally a duplicated line appears if two agents edit the
  same line; delete it and move on. Do not let agents reorder or
  restructure either file, union merge handles additions well and rewrites
  badly.
- **Docker build context is the repo root** for every service:
  `docker build -f services/<name>/Dockerfile -t momotaro/<name> .`
  An agent that assumes its own directory is the context will produce a
  Dockerfile that fails confusingly.
- **A service PR whose diff touches `proto/gen/` is wrong** and should be
  split. Generated code changes only in proto PRs.
- Still-open decisions worth settling early: the LLM provider(s) (deferred
  on cost grounds, the provider-chain interface means it does not block
  anything), and the concrete numbers for retry caps, cooldowns, and the
  cost/probability tables in Phase 2.
