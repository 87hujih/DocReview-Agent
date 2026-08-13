package tools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/policy"
	agenttools "agent_project/apps/server/internal/agent/tools"
)

// TestRuntimeRejectsSchemaInvalidInputAndAuditsClassifiedFailure 验证对应场景下的正常路径与失败路径。
func TestRuntimeRejectsSchemaInvalidInputAndAuditsClassifiedFailure(t *testing.T) {
	registry := agenttools.NewRegistry()
	tool := &countingTool{descriptor: runtimeTestDescriptor()}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	audit := &memoryAudit{}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: allowAuthorizer{}, Audit: audit, Limiter: allowLimiter{},
		Counter: fixedCounter{tokens: 1}, Artifacts: &memoryArtifacts{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}

	execution, err := runtime.Execute(context.Background(), agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: "document.read_nodes", ToolVersion: "1.0.0",
		Input: json.RawMessage(`{"unexpected":true}`), TraceID: "trace-1",
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("execute infrastructure error: %v", err)
	}
	if execution.Error == nil || execution.Error.Category != agenttools.ErrorInvalidInput {
		t.Fatalf("execution = %#v", execution)
	}
	if tool.executions != 0 {
		t.Fatalf("schema-invalid call executed tool %d times", tool.executions)
	}
	if audit.finish.Error == nil || audit.finish.Error.Category != agenttools.ErrorInvalidInput {
		t.Fatalf("audit finish = %#v", audit.finish)
	}
}

// TestRuntimeRejectsTrailingJSONValueBeforeToolExecution 验证对应场景下的正常路径与失败路径。
func TestRuntimeRejectsTrailingJSONValueBeforeToolExecution(t *testing.T) {
	registry := agenttools.NewRegistry()
	tool := &countingTool{descriptor: runtimeTestDescriptor()}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: allowAuthorizer{}, Audit: &memoryAudit{}, Limiter: allowLimiter{},
		Counter: fixedCounter{tokens: 1}, Artifacts: &memoryArtifacts{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	execution, err := runtime.Execute(context.Background(), agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: tool.descriptor.Name, ToolVersion: tool.descriptor.Version,
		Input:    json.RawMessage(`{"node_ids":["node-1"]} {"smuggled":true}`),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Error == nil || execution.Error.Category != agenttools.ErrorInvalidInput || tool.executions != 0 {
		t.Fatalf("trailing JSON execution=%#v calls=%d", execution, tool.executions)
	}
}

// TestRuntimeAppliesResourceLevelPolicyBeforeToolExecution 验证对应场景下的正常路径与失败路径。
func TestRuntimeAppliesResourceLevelPolicyBeforeToolExecution(t *testing.T) {
	registry := agenttools.NewRegistry()
	descriptor := runtimeTestDescriptor()
	descriptor.InputSchema = json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string","minLength":1},"node_ids":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["resource_id","node_ids"],"additionalProperties":false}`)
	descriptor.ResourceSelectors = []agenttools.ResourceSelector{{Type: "document", InputField: "resource_id", Access: agenttools.AccessRead}}
	tool := &countingTool{descriptor: descriptor}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry:   registry,
		Authorizer: policy.NewEngine(runtimePermission{allowed: true}, runtimeResource{allowed: false}, runtimeApproval{}),
		Audit:      &memoryAudit{}, Limiter: allowLimiter{}, Counter: fixedCounter{tokens: 1}, Artifacts: &memoryArtifacts{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	execution, err := runtime.Execute(context.Background(), agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: descriptor.Name, ToolVersion: descriptor.Version,
		Input:    json.RawMessage(`{"resource_id":"other-workspace-resource","node_ids":["node-1"]}`),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Error == nil || execution.Error.Category != agenttools.ErrorPolicyBlocked || execution.Error.Message != "resource_scope_denied" {
		t.Fatalf("execution = %#v", execution)
	}
	if tool.executions != 0 {
		t.Fatalf("resource-denied tool executed %d times", tool.executions)
	}
}

// TestRuntimeRejectsToolResultWithoutProvenance 验证对应场景下的正常路径与失败路径。
func TestRuntimeRejectsToolResultWithoutProvenance(t *testing.T) {
	registry := agenttools.NewRegistry()
	tool := &countingTool{
		descriptor: runtimeTestDescriptor(),
		result:     agenttools.Result{Output: json.RawMessage(`{"nodes":[]}`)},
	}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{Registry: registry, Authorizer: allowAuthorizer{}, Audit: &memoryAudit{}, Limiter: allowLimiter{}, Counter: fixedCounter{tokens: 1}, Artifacts: &memoryArtifacts{}})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	execution, err := runtime.Execute(context.Background(), agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: "document.read_nodes", ToolVersion: "1.0.0",
		Input:    json.RawMessage(`{"node_ids":["node-1"]}`),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Error == nil || execution.Error.Category != agenttools.ErrorTerminalUpstream {
		t.Fatalf("unattributed result was accepted: %#v", execution)
	}
}

// TestRuntimeRetriesOnlyClassifiedRetryableFailure 验证对应场景下的正常路径与失败路径。
func TestRuntimeRetriesOnlyClassifiedRetryableFailure(t *testing.T) {
	registry := agenttools.NewRegistry()
	descriptor := runtimeTestDescriptor()
	descriptor.RetryPolicy = agenttools.RetryPolicy{MaxAttempts: 2, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	tool := &scriptedTool{
		descriptor: descriptor,
		steps: []scriptedStep{
			{err: &agenttools.ToolError{Category: agenttools.ErrorRetryableUpstream, Message: "temporary"}},
			{result: agenttools.Result{
				Output:     json.RawMessage(`{"nodes":[]}`),
				Provenance: []agenttools.Provenance{{SourceType: "document", SourceID: "resource-1", TrustLevel: "untrusted"}},
			}},
		},
	}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: allowAuthorizer{}, Audit: &memoryAudit{}, Limiter: allowLimiter{},
		Counter: fixedCounter{tokens: 1}, Artifacts: &memoryArtifacts{}, Sleeper: noWaitSleeper{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	execution, err := runtime.Execute(context.Background(), agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: descriptor.Name, ToolVersion: descriptor.Version,
		Input:    json.RawMessage(`{"node_ids":["node-1"]}`),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Status != agenttools.AuditSucceeded || execution.Attempts != 2 || execution.Result == nil {
		t.Fatalf("execution = %#v", execution)
	}
}

// TestRuntimeClassifiesAttemptTimeoutAndStopsAtMaxAttempts 验证对应场景下的正常路径与失败路径。
func TestRuntimeClassifiesAttemptTimeoutAndStopsAtMaxAttempts(t *testing.T) {
	registry := agenttools.NewRegistry()
	descriptor := runtimeTestDescriptor()
	descriptor.Timeout = 5 * time.Millisecond
	descriptor.RetryPolicy = agenttools.RetryPolicy{MaxAttempts: 2, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	tool := &blockingTool{descriptor: descriptor}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: allowAuthorizer{}, Audit: &memoryAudit{}, Limiter: allowLimiter{},
		Counter: fixedCounter{tokens: 1}, Artifacts: &memoryArtifacts{}, Sleeper: noWaitSleeper{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	execution, err := runtime.Execute(context.Background(), agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: descriptor.Name, ToolVersion: descriptor.Version,
		Input:    json.RawMessage(`{"node_ids":["node-1"]}`),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Error == nil || execution.Error.Category != agenttools.ErrorTimeout || execution.Attempts != 2 || tool.executions != 2 {
		t.Fatalf("timeout execution=%#v calls=%d", execution, tool.executions)
	}
}

// TestRuntimeDoesNotRetryTerminalFailure 验证对应场景下的正常路径与失败路径。
func TestRuntimeDoesNotRetryTerminalFailure(t *testing.T) {
	registry := agenttools.NewRegistry()
	descriptor := runtimeTestDescriptor()
	descriptor.RetryPolicy = agenttools.RetryPolicy{MaxAttempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
	tool := &countingTool{descriptor: descriptor, err: &agenttools.ToolError{Category: agenttools.ErrorNotFound, Message: "node missing"}}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: allowAuthorizer{}, Audit: &memoryAudit{}, Limiter: allowLimiter{},
		Counter: fixedCounter{tokens: 1}, Artifacts: &memoryArtifacts{}, Sleeper: noWaitSleeper{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	execution, err := runtime.Execute(context.Background(), agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: descriptor.Name, ToolVersion: descriptor.Version,
		Input:    json.RawMessage(`{"node_ids":["node-1"]}`),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Error == nil || execution.Error.Category != agenttools.ErrorNotFound || execution.Attempts != 1 || tool.executions != 1 {
		t.Fatalf("terminal execution=%#v calls=%d", execution, tool.executions)
	}
}

// TestRuntimePersistsCancellationWithDetachedAuditContext 验证对应场景下的正常路径与失败路径。
func TestRuntimePersistsCancellationWithDetachedAuditContext(t *testing.T) {
	registry := agenttools.NewRegistry()
	parent, cancel := context.WithCancel(context.Background())
	tool := &cancelingTool{descriptor: runtimeTestDescriptor(), cancel: cancel}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	audit := &contextCheckingAudit{}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: allowAuthorizer{}, Audit: audit, Limiter: allowLimiter{},
		Counter: fixedCounter{tokens: 1}, Artifacts: &memoryArtifacts{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	execution, err := runtime.Execute(parent, agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: tool.descriptor.Name, ToolVersion: tool.descriptor.Version,
		Input:    json.RawMessage(`{"node_ids":["node-1"]}`),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("cancellation audit must not inherit cancelled context: %v", err)
	}
	if execution.Status != agenttools.AuditCancelled || execution.Error == nil || execution.Error.Category != agenttools.ErrorCancelled || audit.finish.Status != agenttools.AuditCancelled {
		t.Fatalf("cancelled execution=%#v audit=%#v", execution, audit.finish)
	}
}

// TestRuntimeRateLimitDenialIsClassifiedAndAudited 验证对应场景下的正常路径与失败路径。
func TestRuntimeRateLimitDenialIsClassifiedAndAudited(t *testing.T) {
	registry := agenttools.NewRegistry()
	tool := &countingTool{descriptor: runtimeTestDescriptor()}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	audit := &memoryAudit{}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: allowAuthorizer{}, Audit: audit,
		Limiter: fixedLimiter{decision: agenttools.RateLimitDecision{Allowed: false, RetryAfter: time.Second}},
		Counter: fixedCounter{tokens: 1}, Artifacts: &memoryArtifacts{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	execution, err := runtime.Execute(context.Background(), agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: "document.read_nodes", ToolVersion: "1.0.0",
		Input:    json.RawMessage(`{"node_ids":["node-1"]}`),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Error == nil || execution.Error.Category != agenttools.ErrorRateLimited || tool.executions != 0 {
		t.Fatalf("rate limited execution = %#v, tool executions=%d", execution, tool.executions)
	}
	if audit.finish.Error == nil || audit.finish.Error.Category != agenttools.ErrorRateLimited {
		t.Fatalf("rate limit audit = %#v", audit.finish)
	}
}

// TestRuntimeStoresOversizedResultAsArtifact 验证对应场景下的正常路径与失败路径。
func TestRuntimeStoresOversizedResultAsArtifact(t *testing.T) {
	registry := agenttools.NewRegistry()
	descriptor := runtimeTestDescriptor()
	descriptor.MaxResultTokens = 3
	tool := &countingTool{descriptor: descriptor, result: agenttools.Result{
		Output:     json.RawMessage(`{"nodes":[{"content":"large sensitive document body"}]}`),
		Provenance: []agenttools.Provenance{{SourceType: "document", SourceID: "resource-1", TrustLevel: "untrusted"}},
	}}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	artifacts := &memoryArtifacts{}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: allowAuthorizer{}, Audit: &memoryAudit{}, Limiter: allowLimiter{},
		Counter: fixedCounter{tokens: 50}, Artifacts: artifacts,
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	execution, err := runtime.Execute(context.Background(), agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: descriptor.Name, ToolVersion: descriptor.Version,
		Input:    json.RawMessage(`{"node_ids":["node-1"]}`),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Result == nil || execution.Result.Artifact == nil || execution.Result.Artifact.ID != "artifact-1" {
		t.Fatalf("bounded result = %#v", execution)
	}
	if string(execution.Result.Output) == string(artifacts.written.Content) {
		t.Fatal("full artifact content remained in model-facing output")
	}
}

// TestRuntimeRejectsWriteToolWithoutIdempotencyKey 验证对应场景下的正常路径与失败路径。
func TestRuntimeRejectsWriteToolWithoutIdempotencyKey(t *testing.T) {
	registry := agenttools.NewRegistry()
	descriptor := runtimeTestDescriptor()
	descriptor.Name = "patch.commit"
	descriptor.RiskLevel = agenttools.RiskMedium
	descriptor.IdempotencyMode = agenttools.IdempotencyRequired
	descriptor.InputSchema = json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string"},"patch":{"type":"string"}},"required":["resource_id","patch"],"additionalProperties":false}`)
	descriptor.OutputSchema = json.RawMessage(`{"type":"object","properties":{"version_id":{"type":"string"}},"required":["version_id"],"additionalProperties":false}`)
	descriptor.ResourceSelectors = []agenttools.ResourceSelector{{Type: "document", InputField: "resource_id", Access: agenttools.AccessWrite}}
	tool := &countingTool{descriptor: descriptor}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	audit := &memoryAudit{}
	runtime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: allowAuthorizer{}, Audit: audit, Limiter: allowLimiter{},
		Counter: fixedCounter{tokens: 1}, Artifacts: &memoryArtifacts{},
	})
	if err != nil {
		t.Fatalf("new runtime: %v", err)
	}
	execution, err := runtime.Execute(context.Background(), agenttools.Call{
		RunID: "run-1", StepID: "step-1", ToolName: descriptor.Name, ToolVersion: descriptor.Version,
		Input:    json.RawMessage(`{"resource_id":"resource-1","patch":"change"}`),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if execution.Error == nil || execution.Error.Category != agenttools.ErrorInvalidInput || tool.executions != 0 {
		t.Fatalf("write without idempotency key = %#v executions=%d", execution, tool.executions)
	}
}

// runtimeTestDescriptor 执行该函数负责的核心处理逻辑。
func runtimeTestDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: "document.read_nodes", Version: "1.0.0", Description: "Read selected nodes",
		InputSchema:         json.RawMessage(`{"type":"object","properties":{"node_ids":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["node_ids"],"additionalProperties":false}`),
		OutputSchema:        json.RawMessage(`{"type":"object","properties":{"nodes":{"type":"array"}},"required":["nodes"],"additionalProperties":false}`),
		RequiredPermissions: []string{"document.read"}, RiskLevel: agenttools.RiskLow,
		Timeout: time.Second, RetryPolicy: agenttools.RetryPolicy{MaxAttempts: 1},
		IdempotencyMode: agenttools.IdempotencyNone, MaxResultTokens: 100,
		DataClassification: agenttools.DataInternal,
	}
}

type countingTool struct {
	descriptor agenttools.Descriptor
	executions int
	result     agenttools.Result
	err        error
}

type scriptedStep struct {
	result agenttools.Result
	err    error
}

type scriptedTool struct {
	descriptor agenttools.Descriptor
	steps      []scriptedStep
	index      int
}

type blockingTool struct {
	descriptor agenttools.Descriptor
	executions int
}

type cancelingTool struct {
	descriptor agenttools.Descriptor
	cancel     context.CancelFunc
}

// Descriptor 执行该函数负责的核心处理逻辑。
func (tool *cancelingTool) Descriptor() agenttools.Descriptor { return tool.descriptor }

// Execute 执行该函数负责的核心处理逻辑。
func (tool *cancelingTool) Execute(ctx context.Context, _ agenttools.Call) (agenttools.Result, error) {
	tool.cancel()
	<-ctx.Done()
	return agenttools.Result{}, ctx.Err()
}

// Descriptor 执行该函数负责的核心处理逻辑。
func (t *blockingTool) Descriptor() agenttools.Descriptor { return t.descriptor }

// Execute 执行该函数负责的核心处理逻辑。
func (t *blockingTool) Execute(ctx context.Context, _ agenttools.Call) (agenttools.Result, error) {
	t.executions++
	<-ctx.Done()
	return agenttools.Result{}, ctx.Err()
}

// Descriptor 执行该函数负责的核心处理逻辑。
func (t *scriptedTool) Descriptor() agenttools.Descriptor { return t.descriptor }

// Execute 执行该函数负责的核心处理逻辑。
func (t *scriptedTool) Execute(context.Context, agenttools.Call) (agenttools.Result, error) {
	step := t.steps[t.index]
	t.index++
	return step.result, step.err
}

type noWaitSleeper struct{}

// Sleep 执行该函数负责的核心处理逻辑。
func (noWaitSleeper) Sleep(context.Context, time.Duration) error { return nil }

type allowLimiter struct{}

// Allow 执行该函数负责的核心处理逻辑。
func (allowLimiter) Allow(context.Context, agenttools.RateLimitRequest) (agenttools.RateLimitDecision, error) {
	return agenttools.RateLimitDecision{Allowed: true}, nil
}

type fixedLimiter struct{ decision agenttools.RateLimitDecision }

// Allow 执行该函数负责的核心处理逻辑。
func (l fixedLimiter) Allow(context.Context, agenttools.RateLimitRequest) (agenttools.RateLimitDecision, error) {
	return l.decision, nil
}

type fixedCounter struct{ tokens int }

// CountJSON 执行该函数负责的核心处理逻辑。
func (c fixedCounter) CountJSON(json.RawMessage) int { return c.tokens }

type memoryArtifacts struct {
	written agenttools.ArtifactWrite
}

// 写入 执行该函数负责的核心处理逻辑。
func (a *memoryArtifacts) Write(_ context.Context, input agenttools.ArtifactWrite) (agenttools.ArtifactReference, error) {
	a.written = input
	return agenttools.ArtifactReference{ID: "artifact-1", URI: "artifact://artifact-1", ContentHash: "sha256:artifact", TokenCount: input.TokenCount}, nil
}

// Descriptor 执行该函数负责的核心处理逻辑。
func (t *countingTool) Descriptor() agenttools.Descriptor { return t.descriptor }

// Execute 执行该函数负责的核心处理逻辑。
func (t *countingTool) Execute(context.Context, agenttools.Call) (agenttools.Result, error) {
	t.executions++
	return t.result, t.err
}

type allowAuthorizer struct{}

// 鉴权 执行该函数负责的核心处理逻辑。
func (allowAuthorizer) Authorize(context.Context, agenttools.AuthorizationRequest) (agenttools.PolicyDecision, error) {
	return agenttools.PolicyDecision{Outcome: agenttools.PolicyAllow, ReasonCode: "test_allow"}, nil
}

type memoryAudit struct {
	finish agenttools.AuditFinish
}

type contextCheckingAudit struct{ finish agenttools.AuditFinish }

// Begin 执行该函数负责的核心处理逻辑。
func (audit *contextCheckingAudit) Begin(context.Context, agenttools.AuditStart) (agenttools.AuditRecord, error) {
	return agenttools.AuditRecord{ID: "call-1", Acquired: true, Status: agenttools.AuditRunning}, nil
}

// Finish 执行该函数负责的核心处理逻辑。
func (audit *contextCheckingAudit) Finish(ctx context.Context, finish agenttools.AuditFinish) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	audit.finish = finish
	return nil
}

type runtimePermission struct{ allowed bool }

// HasPermission 执行该函数负责的核心处理逻辑。
func (r runtimePermission) HasPermission(context.Context, agenttools.SecurityContext, string) (bool, error) {
	return r.allowed, nil
}

type runtimeResource struct{ allowed bool }

// AuthorizeResource 执行该函数负责的核心处理逻辑。
func (r runtimeResource) AuthorizeResource(context.Context, agenttools.SecurityContext, agenttools.ResourceRef) (bool, error) {
	return r.allowed, nil
}

type runtimeApproval struct{}

// VerifyApproval 执行该函数负责的核心处理逻辑。
func (runtimeApproval) VerifyApproval(context.Context, agenttools.ApprovalCheck) (bool, error) {
	return false, nil
}

// Begin 执行该函数负责的核心处理逻辑。
func (a *memoryAudit) Begin(context.Context, agenttools.AuditStart) (agenttools.AuditRecord, error) {
	return agenttools.AuditRecord{ID: "call-1", Acquired: true, Status: agenttools.AuditPending}, nil
}

// Finish 执行该函数负责的核心处理逻辑。
func (a *memoryAudit) Finish(_ context.Context, finish agenttools.AuditFinish) error {
	a.finish = finish
	return nil
}
