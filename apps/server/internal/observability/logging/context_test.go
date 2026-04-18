package logging

import (
	"context"
	"testing"
)

// TestRequestIDRoundTrip 验证`requestIDRoundTrip`在特定边界条件下的行为，防止同类回归。
func TestRequestIDRoundTrip(t *testing.T) {
	ctx := WithRequestID(context.Background(), "req-123")

	if got := RequestID(ctx); got != "req-123" {
		t.Fatalf("expected request id %q, got %q", "req-123", got)
	}
}

// TestTaskAndRunIDRoundTrip 验证`taskAndRunIDRoundTrip`在特定边界条件下的行为，防止同类回归。
func TestTaskAndRunIDRoundTrip(t *testing.T) {
	ctx := context.Background()
	ctx = WithTaskID(ctx, "task-123")
	ctx = WithRunID(ctx, "run-123")
	ctx = WithStepName(ctx, "planner")

	if got := TaskID(ctx); got != "task-123" {
		t.Fatalf("expected task id %q, got %q", "task-123", got)
	}

	if got := RunID(ctx); got != "run-123" {
		t.Fatalf("expected run id %q, got %q", "run-123", got)
	}

	if got := StepName(ctx); got != "planner" {
		t.Fatalf("expected step name %q, got %q", "planner", got)
	}
}
