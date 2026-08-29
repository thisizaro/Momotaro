package interceptors

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/thisizaro/Momotaro/internal/platform/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUnaryServerMetricsRecordsSuccessByMethod(t *testing.T) {
	m := metrics.New()
	interceptor := UnaryServerMetrics(m)
	info := &grpc.UnaryServerInfo{FullMethod: "/svc/Method"}
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }

	if _, err := interceptor(context.Background(), nil, info, handler); err != nil {
		t.Fatalf("interceptor: %v", err)
	}

	if got := testutil.ToFloat64(m.RequestsTotal.WithLabelValues("/svc/Method", "OK")); got != 1 {
		t.Errorf("requests_total{method=/svc/Method,code=OK} = %v, want 1", got)
	}
}

// A failing handler must be recorded under its actual gRPC status code, not
// silently dropped or lumped in with successes -- an interceptor that only
// counts the happy path would make requests_total useless for alerting on
// error rate.
func TestUnaryServerMetricsRecordsErrorByCode(t *testing.T) {
	m := metrics.New()
	interceptor := UnaryServerMetrics(m)
	info := &grpc.UnaryServerInfo{FullMethod: "/svc/Method"}
	handler := func(ctx context.Context, req any) (any, error) {
		return nil, status.Error(codes.Internal, "boom")
	}

	if _, err := interceptor(context.Background(), nil, info, handler); err == nil {
		t.Fatal("interceptor swallowed the handler's error")
	}

	if got := testutil.ToFloat64(m.RequestsTotal.WithLabelValues("/svc/Method", "Internal")); got != 1 {
		t.Errorf("requests_total{method=/svc/Method,code=Internal} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.RequestsTotal.WithLabelValues("/svc/Method", "OK")); got != 0 {
		t.Errorf("requests_total{method=/svc/Method,code=OK} = %v, want 0: the error must not also count as a success", got)
	}
}

// A plain (non-status) error must still be recorded, under the code
// status.Code derives for it (Unknown), not dropped for lack of a *Status.
func TestUnaryServerMetricsRecordsPlainErrorAsUnknown(t *testing.T) {
	m := metrics.New()
	interceptor := UnaryServerMetrics(m)
	info := &grpc.UnaryServerInfo{FullMethod: "/svc/Method"}
	handler := func(ctx context.Context, req any) (any, error) {
		return nil, errPlain{}
	}

	if _, err := interceptor(context.Background(), nil, info, handler); err == nil {
		t.Fatal("interceptor swallowed the handler's error")
	}

	if got := testutil.ToFloat64(m.RequestsTotal.WithLabelValues("/svc/Method", "Unknown")); got != 1 {
		t.Errorf("requests_total{method=/svc/Method,code=Unknown} = %v, want 1", got)
	}
}

type errPlain struct{}

func (errPlain) Error() string { return "plain error, not a gRPC status" }
