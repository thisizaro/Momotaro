# AGENTS.md (api-gateway)

## What this service does

HTTP/WS edge. Translates external requests into internal gRPC. The only door in.

Full detail in `docs/ARCHITECTURE.md`. Read `AGENTS.md` at the repo root
first, and `docs/ENGINEERING.md` before writing any code.

## Interface

This service exposes **no inbound gRPC**. It is an HTTP/WebSocket
server and a gRPC *client*. Its external contract is
`docs/API_GATEWAY.md`, which is also the only doc the `web/` agent needs.

## Owns

- `services/api-gateway/**`, and nothing outside it.
- Postgres tables: see the ownership table in `docs/ARCHITECTURE.md` §10a.
  Write only what this service owns; read the rest.

## Must not touch

- Any other service's directory. `services/api-gateway/internal/**` is private to this
  service and the compiler enforces it in both directions.
- `proto/`, `migrations/`, `internal/platform/`. If you need a change in
  any of them, **stop and propose it** rather than making it. Another agent
  is probably working there.
- Any table this service does not own.

## Depends on

- Shared code comes from `internal/platform/` only (clock, config, logger,
  gRPC interceptors). Never copy those into this service.
- Cross-service calls are gRPC, always. Never an in-process import.

## Reminders

- TDD, injected clock, context deadlines on every outbound call, graceful
  shutdown, money as integer paise. `docs/ENGINEERING.md`.
- Log what breaks to `docs/INCIDENTS.md` while it is fresh.
- No AI attribution in commits or PR descriptions.
