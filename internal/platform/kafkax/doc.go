// Package kafkax holds the shared Kafka producer and consumer helpers.
//
// # Library decision: franz-go
//
// Use github.com/twmb/franz-go. Pinned here so nine services cannot end up
// with three different Kafka clients. Reasons: pure Go with no cgo (which
// keeps the distroless runtime images working), and it exposes the
// per-partition fetch control the Decision Engine's keyed worker pool needs.
// sarama makes that pattern awkward; confluent-kafka-go needs cgo.
//
// # What belongs here
//
//   - a producer wrapper that always sets the message key to record_id, so
//     per-record ordering is preserved by construction rather than by each
//     caller remembering (docs/ARCHITECTURE.md section 8)
//   - OpenTelemetry context injection and extraction on message headers, so a
//     trace survives the async hop into Reporting
//   - the offset-commit tracker described below
//
// # The offset-commit tracker is the dangerous part
//
// The Decision Engine processes records concurrently via a keyed worker pool
// (docs/ARCHITECTURE.md section 8a), so records complete OUT OF ORDER.
// Committing the offset of whatever finished most recently would silently
// discard the records still in flight behind it whenever a pod dies.
//
// The rule: track completed offsets per partition and commit only the
// highest CONTIGUOUS completed prefix. An unfinished record at offset N pins
// the commit point at N-1 regardless of how many later offsets have
// finished.
//
// Anything simpler trades data loss for less code, which is not a trade
// available to us on money movement. This is the single easiest place in the
// project to introduce a bug that only appears under a crash mid-load, so it
// gets thorough tests before the consumer is wired to anything real.
//
// # Status
//
// Not implemented. Built with the Decision Engine in Phase 1
// (docs/PLAN.md), because the consumer and the tracker have to be designed
// against each other.
package kafkax
