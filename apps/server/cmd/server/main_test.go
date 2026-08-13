package main

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/agent/cutover"
)

// TestServerWiresDurableOnlyTurnPipeline guards the production seam against
// accidentally reintroducing a legacy or shadow runner.
func TestServerWiresDurableOnlyTurnPipeline(t *testing.T) {
	runner := serverTestRunner{}
	pipeline, err := buildDurableOnlyTurnPipeline(runner)
	if err != nil {
		t.Fatalf("build durable-only turn pipeline: %v", err)
	}
	result, err := pipeline.Execute(context.Background(), cutover.Request{RequestID: "request-1", Message: "review"}, nil)
	if err != nil {
		t.Fatalf("execute durable-only turn pipeline: %v", err)
	}
	if result.Mode != cutover.ModeDurable {
		t.Fatalf("expected durable mode, got %q", result.Mode)
	}
}

type serverTestRunner struct{}

func (serverTestRunner) Execute(context.Context, cutover.Request, cutover.Observer) (cutover.Result, error) {
	return cutover.Result{}, nil
}
