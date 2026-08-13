package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agenttools "agent_project/apps/server/internal/agent/tools"
)

// TestRegistryDiscoversAndResolvesVersionedTool 验证对应场景下的正常路径与失败路径。
func TestRegistryDiscoversAndResolvesVersionedTool(t *testing.T) {
	registry := agenttools.NewRegistry()
	tool := echoTool{descriptor: agenttools.Descriptor{
		Name: "document.read_nodes", Version: "1.0.0", Description: "Read selected document nodes",
		InputSchema:         json.RawMessage(`{"type":"object","properties":{"node_ids":{"type":"array","items":{"type":"string"}}},"required":["node_ids"],"additionalProperties":false}`),
		OutputSchema:        json.RawMessage(`{"type":"object","properties":{"nodes":{"type":"array"}},"required":["nodes"],"additionalProperties":false}`),
		RequiredPermissions: []string{"document.read"}, RiskLevel: agenttools.RiskLow,
		Timeout: 1000, RetryPolicy: agenttools.RetryPolicy{MaxAttempts: 1},
		IdempotencyMode: agenttools.IdempotencyNone, MaxResultTokens: 500,
		DataClassification: agenttools.DataInternal,
	}}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	resolved, err := registry.Resolve("document.read_nodes", "1.0.0")
	if err != nil || resolved.Descriptor().Name != "document.read_nodes" {
		t.Fatalf("resolve: tool=%#v err=%v", resolved, err)
	}
	descriptors := registry.Discover()
	if len(descriptors) != 1 || descriptors[0].Version != "1.0.0" {
		t.Fatalf("discover = %#v", descriptors)
	}
	if err := registry.Register(tool); !errors.Is(err, agenttools.ErrDuplicateTool) {
		t.Fatalf("duplicate registration must fail, got %v", err)
	}
}

// TestRegistryRejectsSchemaItCannotEnforce 验证对应场景下的正常路径与失败路径。
func TestRegistryRejectsSchemaItCannotEnforce(t *testing.T) {
	registry := agenttools.NewRegistry()
	tool := echoTool{descriptor: agenttools.Descriptor{
		Name: "unsafe.schema", Version: "1.0.0", Description: "Must fail closed",
		InputSchema:  json.RawMessage(`{"type":"object","unevaluatedProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		RiskLevel:    agenttools.RiskLow, Timeout: 1000,
		RetryPolicy: agenttools.RetryPolicy{MaxAttempts: 1}, IdempotencyMode: agenttools.IdempotencyNone,
		MaxResultTokens: 10, DataClassification: agenttools.DataInternal,
	}}
	if err := registry.Register(tool); !errors.Is(err, agenttools.ErrUnsupportedSchema) {
		t.Fatalf("unsupported schema must fail closed, got %v", err)
	}
}

// TestRegistryRejectsMalformedSchemaConstraintPlacement 验证对应场景下的正常路径与失败路径。
func TestRegistryRejectsMalformedSchemaConstraintPlacement(t *testing.T) {
	descriptor := runtimeRegistryDescriptor()
	descriptor.Name = "unsafe.constraint"
	descriptor.InputSchema = json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minItems":1}}}`)
	if err := agenttools.NewRegistry().Register(echoTool{descriptor: descriptor}); err == nil {
		t.Fatal("schema constraint on incompatible type was accepted")
	}
}

// TestRegistrySnapshotsDescriptorAgainstPostRegistrationMutation 验证对应场景下的正常路径与失败路径。
func TestRegistrySnapshotsDescriptorAgainstPostRegistrationMutation(t *testing.T) {
	descriptor := runtimeRegistryDescriptor()
	tool := &mutableDescriptorTool{descriptor: descriptor}
	registry := agenttools.NewRegistry()
	if err := registry.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	tool.descriptor.RequiredPermissions[0] = "admin.override"
	tool.descriptor.InputSchema[0] = '['
	discovered := registry.Discover()
	if len(discovered) != 1 || discovered[0].RequiredPermissions[0] != "document.read" || discovered[0].InputSchema[0] != '{' {
		t.Fatalf("registered descriptor was mutable after validation: %#v", discovered)
	}
}

// runtimeRegistryDescriptor 执行该函数负责的核心处理逻辑。
func runtimeRegistryDescriptor() agenttools.Descriptor {
	return agenttools.Descriptor{
		Name: "document.read_nodes", Version: "1.0.0", Description: "Read nodes",
		InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		RequiredPermissions: []string{"document.read"}, RiskLevel: agenttools.RiskLow,
		Timeout: 1000, RetryPolicy: agenttools.RetryPolicy{MaxAttempts: 1},
		IdempotencyMode: agenttools.IdempotencyNone, MaxResultTokens: 10, DataClassification: agenttools.DataInternal,
	}
}

type echoTool struct {
	descriptor agenttools.Descriptor
	result     json.RawMessage
	err        error
}

type mutableDescriptorTool struct{ descriptor agenttools.Descriptor }

// Descriptor 执行该函数负责的核心处理逻辑。
func (tool *mutableDescriptorTool) Descriptor() agenttools.Descriptor { return tool.descriptor }

// Execute 执行该函数负责的核心处理逻辑。
func (*mutableDescriptorTool) Execute(context.Context, agenttools.Call) (agenttools.Result, error) {
	return agenttools.Result{}, nil
}

// Descriptor 执行该函数负责的核心处理逻辑。
func (t echoTool) Descriptor() agenttools.Descriptor { return t.descriptor }

// Execute 执行该函数负责的核心处理逻辑。
func (t echoTool) Execute(context.Context, agenttools.Call) (agenttools.Result, error) {
	return agenttools.Result{Output: t.result}, t.err
}
