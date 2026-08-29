# AGENTS.md (web)

The Momotaro merchant ops dashboard.

If you are building here: read `../docs/API_GATEWAY.md` and stop there. That
is the entire contract you need, HTTP endpoints, the WebSocket live-update
feed, the auth header, the error shape. You do **not** need
`../docs/ARCHITECTURE.md`; it describes internal services (gRPC, Kafka,
Postgres) that are unreachable from here and should not influence anything
you build.

Hard rule: this app talks to the API Gateway only, at whatever base URL is
configured. Never call an internal service directly, even if you can find its
address. That boundary is a real security property, not tidiness: only the
Gateway is built to defend against untrusted external input.

## Stack

Vite + React 18 + TypeScript + Tailwind, icons from `lucide-react`. Import
project modules with the `@/` alias (`@/components/Foo`), configured in both
`vite.config.ts` and `tsconfig.app.json`. No other UI or icon libraries.

## Running it

```bash
npm install       # required; a missing node_modules renders a blank page
npm run dev       # http://localhost:5173
npm run typecheck
npm run build
```

## Mock mode: development scaffolding, not a demo mode

`src/lib/mockEngine.ts` is a complete in-memory stand-in for the Gateway: it
seeds a finished batch on construction, simulates a live run with delayed
outcomes, and computes the same aggregate shapes the real API returns.

`src/lib/api.ts` picks mock or live from one condition:

```ts
const USE_MOCK = !API_BASE || import.meta.env.VITE_USE_MOCK === 'true';
```

So with `VITE_API_BASE_URL` unset, everything runs on mocks. That was
deliberate: `web/` was scaffolded in Phase 0 against the written
`../docs/API_GATEWAY.md` contract precisely so UI work could start in
parallel with backend work rather than waiting on it. It did its job.

**Two things to be clear about, because this is the one place in the repo
where "mock" is ambiguous.**

First, **mock mode is for development, never for a demo.** Every number the
dashboard displays in a demo must have come through the real pipeline. This
app renders what the backend computed; it computes nothing itself and must
never appear to. If a panel has no live data behind it, the panel is not
finished, and shipping it against `mockEngine.ts` would be presenting a
frontend simulation as a distributed system.

Second, this is **completely different** from `demo/world-simulator`, which
you will hear called a simulator too. That one substitutes for a bank, which
we cannot have, permanently and by design, and it is what makes recovery
outcomes *measurable against a known answer*. It is a strength of the system
and gets said out loud. `mockEngine.ts` substitutes for our own backend,
temporarily. Do not conflate them.

**Keep mock mode working.** If you add an endpoint, add it to both `api.ts`
and `mockEngine.ts`. Being able to develop without the whole stack running is
genuinely useful and losing it would be a regression. It just stops being the
default once the Gateway serves the route.

## You own `web/`, and only `web/`

Backend agents are working in parallel in `services/`, `demo/`, `proto/` and
`migrations/`. **Do not edit anything outside `web/`**, with two exceptions:
you may append to `../docs/INCIDENTS.md` and tick your own boxes in
`../docs/PLAN.md`, both of which use git's `merge=union` driver so concurrent
edits do not conflict.

`../docs/API_GATEWAY.md` is the contract between you and them, and it is
**frozen**: treat it as read-only. If you need a field or an endpoint that is
not in it, or you find a place where it is wrong or ambiguous, say so and
stop rather than inventing a shape, because whatever you invent will not be
what the Gateway agent implements. A contract mismatch found in a doc costs
minutes; found on stage it costs the demo.

## Shape

- `src/App.tsx` owns state and data fetching
- `src/components/*` presentational, fed by props
- `src/lib/api.ts` the only place `fetch` appears
- `src/lib/mockEngine.ts` the mock backend
- `src/types.ts` shared types, mirroring the Gateway contract

## Conventions

- Money arrives as integer **paise**, never a float. Format for display with
  the helpers in `src/lib/format.ts`; never do currency arithmetic in a
  component.
- Never commit real credentials. `.env.local` is gitignored.
- No AI attribution in commit messages or PR descriptions.
- Log anything that breaks and cost you time to `../docs/INCIDENTS.md`.
