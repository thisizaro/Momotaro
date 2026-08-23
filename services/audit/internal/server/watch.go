package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/clock"
	auditv1 "github.com/thisizaro/Momotaro/proto/gen/audit/v1"
)

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

	// beforeWait is a test-only hook, called immediately after each
	// interval's wait is registered with the clock and before Run blocks
	// on it. See watch_test.go's armReady.
	beforeWait func()
}

// NewWatcher returns a Watcher that checks every interval, using clk so the
// interval is testable without a real wait (docs/ENGINEERING.md section 2).
func NewWatcher(checker Checker, clk clock.Clock, interval time.Duration, log *slog.Logger) *Watcher {
	return &Watcher{checker: checker, clock: clk, interval: interval, log: log}
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
