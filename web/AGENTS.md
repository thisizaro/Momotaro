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

## Mock mode, and why the app works with no backend

`src/lib/mockEngine.ts` is a complete in-memory stand-in for the Gateway: it
seeds a finished batch on construction, simulates a live run with delayed
outcomes, and computes the same aggregate shapes the real API returns.

`src/lib/api.ts` picks mock or live from one condition:

```ts
const USE_MOCK = !API_BASE || import.meta.env.VITE_USE_MOCK === 'true';
```

So with `VITE_API_BASE_URL` unset, everything runs on mocks. That is
deliberate and worth preserving: the dashboard can be built and demoed before
the Gateway exists, and UI work never blocks on backend progress. Copy
`.env.example` to `.env.local` and set the base URL to switch to the real
thing. Every call goes through `api.ts`, so no component needs to know which
mode it is in.

**Keep it that way.** If you add an endpoint, add it to both `api.ts` and
`mockEngine.ts`. A component that fetches directly, or a feature that only
works against a live backend, breaks the property that makes this app
demoable on its own.

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
