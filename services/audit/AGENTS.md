# AGENTS.md (audit)

## What this service does

Serves record audit trails and continuously verifies the correctness invariants.

Full detail in `docs/ARCHITECTURE.md`. Read `AGENTS.md` at the repo root
first, and `docs/ENGINEERING.md` before writing any code.

## Interface

`proto/audit/v1/audit.proto` is the source of truth for this
service's API. Do not describe the interface in prose here, point at the
proto. Changing it requires its own PR, merged before any code that depends
on the new shape.

## Owns

- `services/audit/**`, and nothing outside it.
- Postgres tables: see the ownership table in `docs/ARCHITECTURE.md` §10a.
  Write only what this service owns; read the rest.

## Must not touch

- Any other service's directory. `services/audit/internal/**` is private to this
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
