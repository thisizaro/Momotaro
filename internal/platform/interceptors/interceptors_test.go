package interceptors

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var testMethodInfo = &grpc.UnaryServerInfo{FullMethod: "/momotaro.test.v1.TestService/Method"}

func TestUnaryServerRecoveryPassesThroughNormalCalls(t *testing.T) {
	interceptor := UnaryServerRecovery()
	wantResp := "ok"

	resp, err := interceptor(context.Background(), "req", testMethodInfo,
		func(ctx context.Context, req any) (any, error) { return wantResp, nil })

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp != wantResp {
		t.Errorf("resp = %v, want %v", resp, wantResp)
	}
}

func TestUnaryServerRecoveryPassesThroughHandlerErrors(t *testing.T) {
	interceptor := UnaryServerRecovery()
	wantErr := status.Error(codes.NotFound, "not found")

	_, err := interceptor(context.Background(), "req", testMethodInfo,
		func(ctx context.Context, req any) (any, error) { return nil, wantErr })

	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

func TestUnaryServerRecoveryConvertsPanicToInternalError(t *testing.T) {
	interceptor := UnaryServerRecovery()

	resp, err := interceptor(context.Background(), "req", testMethodInfo,
		func(ctx context.Context, req any) (any, error) { panic("simulated handler bug") })

	if resp != nil {
		t.Errorf("resp = %v, want nil", resp)
	}
	if status.Code(err) != codes.Internal {
		t.Fatalf("err = %v, want codes.Internal", err)
	}
}

// A panic that isn't a string/error (e.g. a struct) must still be recovered
// safely, not re-panic while formatting.
func TestUnaryServerRecoveryHandlesNonStringPanicValues(t *testing.T) {
	interceptor := UnaryServerRecovery()

	_, err := interceptor(context.Background(), "req", testMethodInfo,
		func(ctx context.Context, req any) (any, error) { panic(struct{ Code int }{Code: 42}) })

	if status.Code(err) != codes.Internal {
		t.Fatalf("err = %v, want codes.Internal", err)
	}
}

func TestUnaryServerRequireDeadlineRejectsMissingDeadline(t *testing.T) {
	interceptor := UnaryServerRequireDeadline()
	called := false

	_, err := interceptor(context.Background(), "req", testMethodInfo,
		func(ctx context.Context, req any) (any, error) { called = true; return nil, nil })

	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("err = %v, want codes.InvalidArgument", err)
	}
	if called {
		t.Error("handler was called despite the missing deadline")
	}
}

func TestUnaryServerRequireDeadlineAllowsCallsWithADeadline(t *testing.T) {
	interceptor := UnaryServerRequireDeadline()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := interceptor(ctx, "req", testMethodInfo,
		func(ctx context.Context, req any) (any, error) { return "ok", nil })

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp != "ok" {
		t.Errorf("resp = %v, want ok", resp)
	}
}

func TestUnaryClientDefaultDeadlineAppliesDefaultWhenMissing(t *testing.T) {
	interceptor := UnaryClientDefaultDeadline(2 * time.Second)

	var gotDeadline time.Time
	var hadDeadline bool
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		gotDeadline, hadDeadline = ctx.Deadline()
		return nil
	}

	before := time.Now()
	err := interceptor(context.Background(), "/svc/Method", nil, nil, nil, invoker)
	after := time.Now()

	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !hadDeadline {
		t.Fatal("invoker's context had no deadline")
	}
	if gotDeadline.Before(before.Add(2*time.Second)) || gotDeadline.After(after.Add(2*time.Second)) {
		t.Errorf("deadline = %v, want approximately 2s from now (window %v..%v)", gotDeadline, before.Add(2*time.Second), after.Add(2*time.Second))
	}
}

func TestUnaryClientDefaultDeadlineRespectsExistingDeadline(t *testing.T) {
	interceptor := UnaryClientDefaultDeadline(10 * time.Second)

	explicitDeadline := time.Now().Add(500 * time.Millisecond)
	callerCtx, cancel := context.WithDeadline(context.Background(), explicitDeadline)
	defer cancel()

	var gotDeadline time.Time
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		gotDeadline, _ = ctx.Deadline()
		return nil
	}

	if err := interceptor(callerCtx, "/svc/Method", nil, nil, nil, invoker); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if !gotDeadline.Equal(explicitDeadline) {
		t.Errorf("deadline = %v, want the caller's own %v (must not be overridden)", gotDeadline, explicitDeadline)
	}
}

func TestUnaryClientDefaultDeadlinePropagatesInvokerError(t *testing.T) {
	interceptor := UnaryClientDefaultDeadline(time.Second)
	wantErr := errors.New("dial failed")

	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return wantErr
	}

	err := interceptor(context.Background(), "/svc/Method", nil, nil, nil, invoker)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}
