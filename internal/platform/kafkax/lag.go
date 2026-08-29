package kafkax

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// LagExporter periodically publishes a consumer group's per-partition lag
// as a gauge (docs/PHASE4_IMPLEMENTATION.md Unit B), for Prometheus and
// Alertmanager to page on. decision-engine is the only consumer group in
// the system worth watching, so this is one group at a time, not a
// generalised multi-group exporter.
type LagExporter struct {
	adm   *kadm.Client
	group string
	gauge *prometheus.GaugeVec
	log   *slog.Logger
}

// NewLagExporter dials its own admin client against brokers, separate from
// the consumer's own client: admin metadata calls (DescribeGroups,
// ListEndOffsets) should not compete with the consumer's fetch loop over
// the same connection. Registers kafka_consumer_lag into registry.
func NewLagExporter(brokers []string, group string, registry *prometheus.Registry, log *slog.Logger) (*LagExporter, error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return nil, fmt.Errorf("new kafka admin client: %w", err)
	}
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kafka_consumer_lag",
		Help: "Consumer group lag (messages fetched but not yet committed), by topic and partition.",
	}, []string{"topic", "partition"})
	registry.MustRegister(gauge)
	return &LagExporter{adm: kadm.NewClient(cl), group: group, gauge: gauge, log: log}, nil
}

// Close releases the underlying admin client.
func (e *LagExporter) Close() { e.adm.Close() }

// Run polls every interval until ctx is done. A single failed poll (broker
// hiccup, group mid-rebalance) logs a warning and is skipped rather than
// stopping the exporter: a stale reading for one interval is far less
// dangerous than an exporter that silently stops updating and reports a
// frozen, increasingly wrong number for the rest of the process's life.
func (e *LagExporter) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.poll(ctx)
		}
	}
}

func (e *LagExporter) poll(ctx context.Context) {
	lags, err := e.adm.Lag(ctx, e.group)
	if err != nil {
		e.log.Warn("kafka lag poll failed", "group", e.group, "err", err)
		return
	}
	e.record(lags)
}

// record is poll's body with the network call already done, so the mapping
// from kadm's result shape to gauge updates is testable without a broker.
func (e *LagExporter) record(lags kadm.DescribedGroupLags) {
	described, ok := lags[e.group]
	if !ok {
		e.log.Warn("kafka lag poll: group not described", "group", e.group)
		return
	}
	if err := described.Error(); err != nil {
		e.log.Warn("kafka lag poll: group describe/fetch error", "group", e.group, "err", err)
		return
	}
	for _, m := range described.Lag.Sorted() {
		if m.Err != nil || m.Lag < 0 {
			// Per-partition load error (e.g. end offset could not be
			// listed): skip rather than publish a fabricated number,
			// leaving the gauge at its last good value for this partition.
			e.log.Warn("kafka lag poll: partition load error", "group", e.group, "topic", m.Topic, "partition", m.Partition, "err", m.Err)
			continue
		}
		e.gauge.WithLabelValues(m.Topic, strconv.Itoa(int(m.Partition))).Set(float64(m.Lag))
	}
}
