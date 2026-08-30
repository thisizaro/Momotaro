// Package auditevent defines the wire shape of audit.events
// (docs/ARCHITECTURE.md sections 8, 10a): the notification Kafka topic a
// record's state-owning service publishes to after each RECORD_STATE +
// AUDIT_ENTRY transaction commits, and Reporting consumes to drive
// StreamBatchUpdates.
//
// Lives here, not in either service's own package, because it is a genuine
// cross-service contract: the Decision Engine (producer) and Reporting
// (consumer) must agree on its shape, and internal/platform is the only
// code this project shares across service trees (AGENTS.md "Repo
// structure"). Plain JSON, not a proto message: this is a Kafka payload
// between two services that never call each other over gRPC, not an RPC
// contract, and proto/gen exists for the latter.
package auditevent

import "time"

// Topic is audit.events' name, fixed by docs/ARCHITECTURE.md section 8.
const Topic = "audit.events"

// Event is one RECORD_STATE transition, already committed to Postgres by
// the time this is published. Never a system of record itself
// (docs/ARCHITECTURE.md section 10a): losing this message costs a stale
// cache or a missed live-dashboard tick, never a wrong number, because
// every reader that needs the truth reads Postgres.
type Event struct {
	RecordID  string `json:"record_id"`
	BatchID   string `json:"batch_id"`
	FromState string `json:"from_state"`
	ToState   string `json:"to_state"`
	// RecoveredDeltaPaise is the record's amount when this transition is
	// the one that recovered it, 0 otherwise, so a live dashboard can
	// animate a running total without a refetch (mirrors
	// reporting.v1.BatchUpdate.recovered_delta_paise, which this event
	// becomes on the way out through StreamBatchUpdates).
	RecoveredDeltaPaise int64     `json:"recovered_delta_paise"`
	Timestamp           time.Time `json:"timestamp"`
}
