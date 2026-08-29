package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/thisizaro/Momotaro/internal/platform/clock"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
)

// InvariantGauges are the metrics checkOnce updates every tick
// (docs/ARCHITECTURE.md section 13: stopping_rule_violation_total and
// incomplete_audit_trail_total; ImpossibleTransitions rides along on the
// same response, one addition beyond that list,
// docs/PHASE4_IMPLEMENTATION.md Unit D). Gauges, not Counters, despite the
// "_total" names ARCHITECTURE.md already committed to: each tick reports
// the count found in THIS scan, which can fall as well as rise (a batch
// deleted, a bug fixed), unlike a true monotonic counter.
type InvariantGauges struct {
	StoppingRuleViolations prometheus.Gauge
	IncompleteAuditTrails  prometheus.Gauge
	ImpossibleTransitions  prometheus.Gauge
}

// NewInvariantGauges builds and registers the three gauges into registry.
func NewInvariantGauges(registry *prometheus.Registry) InvariantGauges {
	g := InvariantGauges{
		StoppingRuleViolations: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "stopping_rule_violation_total",
			Help: "Records found with a retry/contact cap exceeded in the most recent invariant scan. Must stay at zero.",
		}),
		IncompleteAuditTrails: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "incomplete_audit_trail_total",
			Help: "Records whose current state has no matching audit_entry, or one that disagrees with it, in the most recent scan. Must stay at zero.",
		}),
		ImpossibleTransitions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "audit_impossible_transitions_total",
			Help: "Records whose audit trail contains a transition the state machine does not allow, in the most recent scan. Must stay at zero.",
		}),
	}
	registry.MustRegister(g.StoppingRuleViolations, g.IncompleteAuditTrails, g.ImpossibleTransitions)
	return g
}

// Checker is the subset of Server this package's continuous verifier needs.
// *Server already satisfies it, so the watcher and the on-demand RPC always
// perform exactly the same check; there is no second code path to drift.
type Checker interface {
	VerifyInvariants(ctx context.Context, req *auditv1.VerifyInvariantsRequest) (*auditv1.VerifyInvariantsResponse, error)
}

// Watcher is the "continuously verifies" half of Audit's job
// (docs/ARCHITECTURE.md section 10a; services/audit/AGENTS.md).
// GetRecordAudit and VerifyInvariants are on-demand; Watcher.Run is what
// makes the check happen without anyone asking.
type Watcher struct {
	checker  Checker
	clock    clock.Clock
	interval time.Duration
	log      *slog.Logger
	gauges   InvariantGauges

	// beforeWait is a test-only hook, called immediately after each
	// interval's wait is registered with the clock and before Run blocks
	// on it. See watch_test.go's armReady.
	beforeWait func()
}

// NewWatcher returns a Watcher that checks every interval, using clk so the
// interval is testable without a real wait (docs/ENGINEERING.md section 2).
func NewWatcher(checker Checker, clk clock.Clock, interval time.Duration, log *slog.Logger, gauges InvariantGauges) *Watcher {
	return &Watcher{checker: checker, clock: clk, interval: interval, log: log, gauges: gauges}
}

// Run checks invariants across every batch once per interval until ctx is
// done. A checker error is logged and the loop continues: one failed
// database round trip should not silence every future check.
func (w *Watcher) Run(ctx context.Context) {
	for {
		wait := w.clock.After(w.interval)
		if w.beforeWait != nil {
			w.beforeWait()
		}
		select {
		case <-ctx.Done():
			return
		case <-wait:
		}
		w.checkOnce(ctx)
	}
}

func (w *Watcher) checkOnce(ctx context.Context) {
	resp, err := w.checker.VerifyInvariants(ctx, &auditv1.VerifyInvariantsRequest{})
	if err != nil {
		w.log.Error("invariant check failed", "err", err)
		return
	}

	w.gauges.StoppingRuleViolations.Set(float64(resp.GetStoppingRuleViolations()))
	w.gauges.IncompleteAuditTrails.Set(float64(resp.GetIncompleteAuditTrails()))
	w.gauges.ImpossibleTransitions.Set(float64(resp.GetImpossibleTransitions()))

	if resp.GetStoppingRuleViolations() > 0 || resp.GetIncompleteAuditTrails() > 0 || resp.GetImpossibleTransitions() > 0 {
		w.log.Error("invariant violation detected",
			"stopping_rule_violations", resp.GetStoppingRuleViolations(),
			"incomplete_audit_trails", resp.GetIncompleteAuditTrails(),
			"impossible_transitions", resp.GetImpossibleTransitions(),
			"records_checked", resp.GetRecordsChecked(),
		)
		return
	}

	w.log.Info("invariant check clean", "records_checked", resp.GetRecordsChecked())
}
