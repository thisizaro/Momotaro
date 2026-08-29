package interceptors

import (
	"context"
	"time"

	"github.com/thisizaro/Momotaro/internal/platform/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// UnaryServerMetrics records every call's duration and result code into m.
// Added as an interceptor, per doc.go, specifically so no handler can be
// missed: a handler that forgets to instrument itself is not a failure mode
// this depends on not happening.
func UnaryServerMetrics(m *metrics.Metrics) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err).String()
		m.RequestDuration.WithLabelValues(info.FullMethod, code).Observe(time.Since(start).Seconds())
		m.RequestsTotal.WithLabelValues(info.FullMethod, code).Inc()
		return resp, err
	}
}
