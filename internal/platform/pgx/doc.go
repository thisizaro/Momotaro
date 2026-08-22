// Package pgx holds the shared Postgres connection and transaction helpers.
//
// # Library decision: pgx v5
//
// Use github.com/jackc/pgx/v5 with pgxpool, not database/sql. Pinned here so
// services do not diverge. Reasons: it is the de facto standard for Postgres
// in Go, it speaks the binary protocol, and it gives real access to
// PostgreSQL-specific features. We need at least two of those features and
// database/sql obscures both.
//
// # What belongs here
//
//   - pool construction from config.Common.PostgresDSN, with sane limits
//   - a WithTx helper enforcing the transactional-write rule below
//   - the FOR UPDATE SKIP LOCKED claim query used by the scheduler worker
//     (docs/ARCHITECTURE.md section 7a)
//
// # The transactional-write rule
//
// A state change and its AUDIT_ENTRY row are written in ONE transaction, by
// the service that owns the change (docs/ARCHITECTURE.md section 10a).
// Either both land or neither does. There must be no window in which a state
// change exists without its audit record, because "100% of records have a
// complete audit trail" is a stated correctness invariant with an alert on
// it, not an aspiration.
//
// An earlier draft of this design wrote state to Postgres and then published
// the audit event to Kafka, which is the classic dual-write problem: a pod
// dying between the two lost the audit entry permanently. WithTx exists so
// that mistake is hard to make again.
//
// # Redis
//
// Redis is a separate concern and uses github.com/redis/go-redis/v9. Also
// pinned here to stop divergence. Its three distinct jobs (durable-ish
// idempotency fast path, retry budget and cooldown counters, dashboard cache)
// are described in docs/ARCHITECTURE.md section 10. Note that the real
// idempotency guarantee is a Postgres UNIQUE constraint, not a Redis key; see
// section 11.
//
// # Status
//
// Not implemented. Built alongside the first migration and the Decision
// Engine in Phase 1 (docs/PLAN.md).
package pgx
