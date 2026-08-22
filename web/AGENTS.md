# AGENTS.md (web/)

If you are building the dashboard: read `../docs/API_GATEWAY.md` and stop
there. That is the entire contract you need, HTTP endpoints, the WebSocket
live-update feed, auth header, error shape. You do not need
`../docs/ARCHITECTURE.md`, that document describes internal services
(gRPC, Kafka, Postgres) that are not reachable from here and shouldn't
influence anything you build.

Hard rule: this app talks to the API Gateway only, at whatever base URL is
configured for the environment. Never call an internal service directly,
even if you can see its address somewhere, that would break the boundary
the rest of the system is built around.

Status: not yet scaffolded. Framework/tooling choice is open, pick whatever
you'd use for a small, fast dashboard (recovered amount, recovery rate,
record table, one record's audit trail drill-down, a live-updating feed).
