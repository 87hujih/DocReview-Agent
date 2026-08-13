package agentcutover

import (
	"context"
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/cutover"
)

// TestComparisonInsertIsIdempotentAndNeverMutatesLegacyDomainState 验证对应场景下的正常路径与失败路径。
func TestComparisonInsertIsIdempotentAndNeverMutatesLegacyDomainState(t *testing.T) {
	for _, fragment := range []string{
		"INSERT INTO agent_cutover_comparisons", "ON CONFLICT (workspace_id, request_id, comparison_kind) DO NOTHING",
		"SELECT status", "legacy_result_hash", "typed_result_hash",
	} {
		if !strings.Contains(insertComparisonSQL+selectComparisonSQL, fragment) {
			t.Fatalf("comparison persistence must contain %q", fragment)
		}
	}
	for _, forbidden := range []string{"UPDATE assistant_messages", "INSERT INTO agent_runs", "document_patch_commits"} {
		if strings.Contains(insertComparisonSQL, forbidden) {
			t.Fatalf("shadow comparison must not mutate domain state: %s", forbidden)
		}
	}
}

// TestComparisonRecorderRejectsInvalidScopeBeforeDatabase 验证对应场景下的正常路径与失败路径。
func TestComparisonRecorderRejectsInvalidScopeBeforeDatabase(t *testing.T) {
	err := NewRecorder(nil).Record(context.Background(), cutover.Comparison{
		RequestID: "request-1", WorkspaceID: "not-a-uuid", ResourceID: "not-a-uuid", Status: cutover.ComparisonUnavailable,
	})
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Fatalf("expected invalid comparison scope, got %v", err)
	}
}
