package agentrun

import (
	"context"
	"strings"
	"testing"
)

// TestResolveToolScopeRejectsInvalidRunBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestResolveToolScopeRejectsInvalidRunBeforeDatabaseAccess(t *testing.T) {
	_, err := NewSecurityScopeStore(nil).ResolveToolScope(context.Background(), "not-a-uuid")
	if err == nil || !strings.Contains(err.Error(), "run_id") {
		t.Fatalf("expected run identity validation, got %v", err)
	}
}

// TestSecurityScopeQueryAcceptsOnlyPersistedTrustedDurableFacts 验证对应场景下的正常路径与失败路径。
func TestSecurityScopeQueryAcceptsOnlyPersistedTrustedDurableFacts(t *testing.T) {
	for _, fragment := range []string{
		"FROM agent_runs",
		"runtime_mode = 'durable'",
		"principal_type IS NOT NULL",
		"principal_id IS NOT NULL",
		"trust_source IS NOT NULL",
		"workspace_id IS NOT NULL",
		"resource_id IS NOT NULL",
	} {
		if !strings.Contains(resolveSecurityScopeSQL, fragment) {
			t.Fatalf("trusted scope query must contain %q", fragment)
		}
	}
}
