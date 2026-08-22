// Package interceptors holds the shared gRPC interceptors every service
// installs. They exist so cross-cutting behaviour is added once, in one
// place, rather than hand-wired per handler where it will be forgotten.
//
// # What belongs here
//
//   - metrics: request_duration_seconds histogram and requests_total per
//     method, via github.com/prometheus/client_golang. Added as an
//     interceptor specifically so no handler can be missed
//     (docs/ARCHITECTURE.md section 13)
//   - tracing: OpenTelemetry context propagation on every call, with
//     record_id as the trace id so one payment's full journey across seven
//     services is a single trace
//   - recovery: convert a panic into a gRPC error instead of taking the pod
//     down. Panics belong in main() during startup validation and nowhere
//     else (docs/ENGINEERING.md section 4)
//   - logging: attach the request-scoped logger to the context so handlers
//     deep in a call chain log with correlation fields already set. Use
//     platform/logger.Into for this
//   - deadline enforcement: reject or default a request arriving with no
//     deadline. Every outbound call in this system must have one
//     (docs/ENGINEERING.md section 3), and this is where that gets verified
//     rather than assumed
//
// # Client side too
//
// Both server and client interceptor chains live here. The client side is
// where the outbound deadline default and the retry policy belong, and
// getting the retry policy right matters: a client retry and a Kafka
// redelivery hit the same idempotency guard, which is exactly why that guard
// sits at the point of action rather than the point of delivery
// (docs/ARCHITECTURE.md section 11).
//
// # gRPC load balancing note
//
// Service dialing helpers belong here too, and they must use grpc-go's
// round_robin policy against a headless Kubernetes Service. A plain
// ClusterIP load-balances at the TCP connection level, and gRPC multiplexes
// many calls over one long-lived HTTP/2 connection, so ClusterIP silently
// pins a client to a single backend pod and defeats horizontal scaling. See
// docs/ARCHITECTURE.md section 12. Get this wrong and the HPA demo shows
// pods scaling up while throughput stays flat.
//
// # Status
//
// Not implemented. Built in Phase 4 (observability), except the recovery and
// deadline interceptors, which are cheap and should land with the first
// service in Phase 1.
package interceptors
