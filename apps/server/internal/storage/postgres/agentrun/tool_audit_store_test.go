package agentrun

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agenttools "agent_project/apps/server/internal/agent/tools"
)

// TestToolAuditBeginRejectsInvalidCallBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestToolAuditBeginRejectsInvalidCallBeforeDatabaseAccess(t *testing.T) {
	store, err := NewToolAuditStore(NewRepository(nil), "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("new audit store: %v", err)
	}
	// 开启事务，确保后续状态变更以原子方式提交。
	_, err = store.Begin(context.Background(), agenttools.AuditStart{
		Call:       agenttools.Call{RunID: "run-1"},
		Descriptor: agenttools.Descriptor{Name: "document.read_nodes", Version: "1.0.0"},
		StartedAt:  time.Now().UTC(),
	})
	if err == nil || !strings.Contains(err.Error(), "step_id") {
		t.Fatalf("expected call validation, got %v", err)
	}
}

// TestToolAuditInputWrapsMalformedJSONForClassifiedRuntimeAudit 验证对应场景下的正常路径与失败路径。
func TestToolAuditInputWrapsMalformedJSONForClassifiedRuntimeAudit(t *testing.T) {
	wrapped := normalizeToolAuditInput(json.RawMessage(`{"valid":true} {"smuggled":true}`))
	var envelope map[string]any
	if err := json.Unmarshal(wrapped, &envelope); err != nil {
		t.Fatalf("wrapped audit input is not JSON: %v", err)
	}
	if envelope["_tool_runtime_invalid_json"] != true || envelope["raw"] == "" {
		t.Fatalf("malformed input was not retained in an explicit audit envelope: %#v", envelope)
	}
}

// TestToolAuditLeaseCoversAllBoundedAttemptsAndBackoff 验证对应场景下的正常路径与失败路径。
func TestToolAuditLeaseCoversAllBoundedAttemptsAndBackoff(t *testing.T) {
	descriptor := agenttools.Descriptor{
		Timeout:     20 * time.Second,
		RetryPolicy: agenttools.RetryPolicy{MaxAttempts: 3, MaxBackoff: 5 * time.Second},
	}
	if got, want := toolAuditLeaseDuration(time.Minute, descriptor), 75*time.Second; got != want {
		t.Fatalf("tool audit lease=%s, want %s", got, want)
	}
}

// TestToolAuditFinishRejectsMissingLeaseTokenBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestToolAuditFinishRejectsMissingLeaseTokenBeforeDatabaseAccess(t *testing.T) {
	store, err := NewToolAuditStore(NewRepository(nil), "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("new audit store: %v", err)
	}
	err = store.Finish(context.Background(), agenttools.AuditFinish{
		ID: "call-1", Status: agenttools.AuditFailed,
		Error:       &agenttools.ToolError{Category: agenttools.ErrorInvalidInput, Message: "bad"},
		CompletedAt: time.Now().UTC(),
	})
	if err == nil || !strings.Contains(err.Error(), "lease") {
		t.Fatalf("expected lease validation, got %v", err)
	}
}
