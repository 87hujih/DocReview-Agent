package orchestration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/orchestration"
	agenttools "agent_project/apps/server/internal/agent/tools"
)

// TestRuntimeToolExecutorBindsRunAndStepIntoTrustedSecurityContext 验证对应场景下的正常路径与失败路径。
func TestRuntimeToolExecutorBindsRunAndStepIntoTrustedSecurityContext(t *testing.T) {
	tool := &capturingTool{}
	registry := agenttools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatal(err)
	}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: adapterAllow{}, Audit: adapterAudit{}, Limiter: adapterLimit{},
		Counter: adapterCounter{}, Artifacts: adapterArtifacts{},
	})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := orchestration.NewRuntimeToolExecutor(runtime, adapterScope{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = executor.Execute(context.Background(), orchestration.ToolRequest{
		RunID: "trusted-run", StepID: "trusted-step", ToolName: "capture", ToolVersion: "1.0.0", Input: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if tool.call.Security.RunID != "trusted-run" || tool.call.Security.StepID != "trusted-step" || tool.call.Security.ResourceID != "resource-1" {
		t.Fatalf("execution provenance was not bound by worker request: %+v", tool.call.Security)
	}
}

type capturingTool struct{ call agenttools.Call }

// Descriptor 执行该函数负责的核心处理逻辑。
func (*capturingTool) Descriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: "capture", Version: "1.0.0", Description: "capture trusted execution scope",
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		RiskLevel:    agenttools.RiskLow, Timeout: time.Second,
		RetryPolicy: agenttools.RetryPolicy{MaxAttempts: 1}, IdempotencyMode: agenttools.IdempotencyNone,
		MaxResultTokens: 10, DataClassification: agenttools.DataInternal,
	}
}

// Execute 执行该函数负责的核心处理逻辑。
func (tool *capturingTool) Execute(_ context.Context, call agenttools.Call) (agenttools.Result, error) {
	tool.call = call
	return agenttools.Result{Output: json.RawMessage(`{}`), Provenance: []agenttools.Provenance{{
		SourceType: "test", SourceID: "capture", TrustLevel: "trusted",
	}}}, nil
}

type adapterScope struct{}

// ResolveToolScope 执行该函数负责的核心处理逻辑。
func (adapterScope) ResolveToolScope(context.Context, string) (agenttools.SecurityContext, error) {
	return agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1", ResourceID: "resource-1", RunID: "forged", StepID: "forged"}, nil
}

type adapterAllow struct{}

// 鉴权执行该函数负责的核心处理逻辑。
func (adapterAllow) Authorize(context.Context, agenttools.AuthorizationRequest) (agenttools.PolicyDecision, error) {
	return agenttools.PolicyDecision{Outcome: agenttools.PolicyAllow, ReasonCode: "test"}, nil
}

type adapterAudit struct{}

// Begin 执行该函数负责的核心处理逻辑。
func (adapterAudit) Begin(context.Context, agenttools.AuditStart) (agenttools.AuditRecord, error) {
	return agenttools.AuditRecord{ID: "call-1", Acquired: true, Status: agenttools.AuditRunning, ClaimedBy: "test", LeaseGeneration: 1}, nil
}

// Finish 执行该函数负责的核心处理逻辑。
func (adapterAudit) Finish(context.Context, agenttools.AuditFinish) error { return nil }

type adapterLimit struct{}

// Allow 执行该函数负责的核心处理逻辑。
func (adapterLimit) Allow(context.Context, agenttools.RateLimitRequest) (agenttools.RateLimitDecision, error) {
	return agenttools.RateLimitDecision{Allowed: true}, nil
}

type adapterCounter struct{}

// CountJSON 执行该函数负责的核心处理逻辑。
func (adapterCounter) CountJSON(json.RawMessage) int { return 1 }

type adapterArtifacts struct{}

// 写入执行该函数负责的核心处理逻辑。
func (adapterArtifacts) Write(context.Context, agenttools.ArtifactWrite) (agenttools.ArtifactReference, error) {
	return agenttools.ArtifactReference{}, nil
}
