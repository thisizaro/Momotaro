// Package metrics holds the Prometheus registry every service exposes on
// its own /metrics endpoint (docs/PLAN.md Phase 4, ARCHITECTURE.md §13).
//
// Each service owns one Metrics and one registry, scraped as its own job by
// Prometheus (deploy/observability/prometheus.yml), so the metric labels
// identify the method and outcome only: which service a sample came from is
// the scrape target's job label, not something duplicated onto the metric
// itself.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics is the two vectors internal/platform/interceptors/doc.go names
// (request_duration_seconds, requests_total), plus Go/process defaults so a
// service's memory and GC behaviour is visible from the same endpoint.
type Metrics struct {
	registry        *prometheus.Registry
	RequestDuration *prometheus.HistogramVec
	RequestsTotal   *prometheus.CounterVec
}

// New builds a fresh registry for one service and registers everything.
func New() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,
		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "request_duration_seconds",
			Help:    "gRPC unary call duration in seconds, by method and result code.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "code"}),
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "requests_total",
			Help: "gRPC unary calls handled, by method and result code.",
		}, []string{"method", "code"}),
	}
	registry.MustRegister(
		m.RequestDuration,
		m.RequestsTotal,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

// Handler serves this registry in the Prometheus text exposition format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Registry exposes the underlying registry so a service can register its
// own additional collectors (decision-engine's Kafka consumer-lag gauge)
// without this package needing to know about every metric every service adds.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}
