package agentrun

import (
	"context"
	"strings"
	"testing"

	agentcontext "agent_project/apps/server/internal/agent/context"
)

// TestContextStoreValidatesManifestBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestContextStoreValidatesManifestBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)
	_, err := repo.Save(context.Background(), agentcontext.Manifest{
		TokenBudget: 100, ReservedOutputTokens: 20, Tokenizer: "test-v1",
		ContentHash: "sha256:test",
	})
	if err == nil || !strings.Contains(err.Error(), "run_id") {
		t.Fatalf("expected manifest identity validation, got %v", err)
	}
}
