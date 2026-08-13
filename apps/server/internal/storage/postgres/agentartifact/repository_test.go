package agentartifact

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestCreateRejectsMissingWorkspaceBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestCreateRejectsMissingWorkspaceBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)
	_, _, err := repo.CreateOrGet(context.Background(), CreateParams{
		IdempotencyKey: "artifact-1", DataClassification: "internal",
		ContentJSON: json.RawMessage(`{"value":"test"}`), TokenCount: 1,
		ProvenanceJSON: json.RawMessage(`[]`),
	})
	if err == nil || !strings.Contains(err.Error(), "workspace_id") {
		t.Fatalf("expected workspace validation, got %v", err)
	}
}

// TestGetRejectsMissingWorkspaceBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestGetRejectsMissingWorkspaceBeforeDatabaseAccess(t *testing.T) {
	_, err := NewRepository(nil).Get(context.Background(), "", "artifact-1")
	if err == nil || !strings.Contains(err.Error(), "workspace_id") {
		t.Fatalf("expected workspace validation, got %v", err)
	}
}
