// Package interceptors holds the shared gRPC interceptors every service
// installs. See doc.go for the full inventory and what still belongs here.
//
// This file implements the two pieces doc.go calls out as landing with the
// first services rather than waiting for Phase 4: panic recovery and
// deadline enforcement. Metrics, tracing and request-scoped logging remain
// Phase 4 work.
package interceptors

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryServerRecovery converts a panic inside a handler into a codes.Internal
// error instead of taking the pod down. Panics belong in main() during
// startup validation and nowhere else (docs/ENGINEERING.md section 4).
func UnaryServerRecovery() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.From(ctx).Error("panic recovered in grpc handler",
					"method", info.FullMethod, "panic", r, "stack", string(debug.Stack()))
				resp = nil
				err = status.Errorf(codes.Internal, "internal error handling %s", info.FullMethod)
			}
		}()
		return handler(ctx, req)
	}
}

// UnaryServerRequireDeadline rejects any call arriving without a context
// deadline. docs/ENGINEERING.md section 3 requires every outbound call in
// this system to carry one; this is where that gets verified on the
// receiving end rather than assumed.
func UnaryServerRequireDeadline() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if _, ok := ctx.Deadline(); !ok {
			return nil, status.Errorf(codes.InvalidArgument, "%s: request arrived with no deadline", info.FullMethod)
		}
		return handler(ctx, req)
	}
}

// UnaryClientDefaultDeadline applies def as the outbound deadline whenever
// the caller's context does not already carry one, so a call site that
// forgets context.WithTimeout cannot produce an unbounded outbound call
// (docs/ENGINEERING.md section 3). A context that already has a deadline,
// however short or long, is left untouched.
func UnaryClientDefaultDeadline(def time.Duration) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, def)
			defer cancel()
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
