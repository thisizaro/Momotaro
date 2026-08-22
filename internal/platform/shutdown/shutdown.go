// Package shutdown provides the one graceful-shutdown mechanism every
// service uses.
//
// docs/ENGINEERING.md section 6: on SIGTERM, stop accepting new work, finish
// in-flight work within a grace period, commit Kafka offsets, close
// connections, then exit. Run ties that grace period to the root context a
// service already cancels on SIGTERM/SIGINT via signal.NotifyContext, so
// every service wires shutdown the same way instead of hand-rolling it.
package shutdown

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Func is one cleanup action run during shutdown: closing a gRPC server,
// flushing a Kafka producer, closing a Postgres pool. It receives a context
// bounded by the grace period and should return as soon as it has finished
// draining, not wait for that context to expire.
type Func func(ctx context.Context) error

// Run blocks until ctx is done (a service's root context, cancelled on
// SIGTERM/SIGINT), then runs every fn per Close.
func Run(ctx context.Context, grace time.Duration, fns ...Func) error {
	<-ctx.Done()
	return Close(grace, fns...)
}

// Close runs every fn concurrently, each given a context bounded by grace.
// Errors from all of them are joined so a caller sees every failure at
// once rather than one restart-per-broken-dependency.
//
// If a fn ignores its context and hangs, Close still returns once grace
// elapses: Kubernetes SIGKILLs after its own grace period regardless, so
// blocking process exit forever on one hung dependency is strictly worse
// than reporting it and moving on.
func Close(grace time.Duration, fns ...Func) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	done := make(chan []error, 1)
	go func() {
		errs := make([]error, len(fns))
		var wg sync.WaitGroup
		for i, fn := range fns {
			wg.Add(1)
			go func(i int, fn Func) {
				defer wg.Done()
				errs[i] = fn(shutdownCtx)
			}(i, fn)
		}
		wg.Wait()
		done <- errs
	}()

	select {
	case errs := <-done:
		return errors.Join(errs...)
	case <-shutdownCtx.Done():
		return fmt.Errorf("shutdown grace period (%s) exceeded: %w", grace, shutdownCtx.Err())
	}
}
