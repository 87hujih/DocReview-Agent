package documentruntime

import (
	"context"
	"strings"
	"testing"

	agenttools "agent_project/apps/server/internal/agent/tools"
)

// TestDocumentRuntimeQueriesStayOnCurrentCanonicalResource 验证对应场景下的正常路径与失败路径。
func TestDocumentRuntimeQueriesStayOnCurrentCanonicalResource(t *testing.T) {
	for _, fragment := range []string{
		"canonical_documents", "document_nodes", "resource_id = $1", "version.version_number DESC", "LIMIT 1",
	} {
		if !strings.Contains(currentVersionSQL+readNodesSQL+searchNodesSQL, fragment) {
			t.Fatalf("canonical document queries must contain %q", fragment)
		}
	}
}

// TestNodeAuthorizationUsesTrustedRunStepMembershipAndExactEvidence 验证对应场景下的正常路径与失败路径。
func TestNodeAuthorizationUsesTrustedRunStepMembershipAndExactEvidence(t *testing.T) {
	if !strings.Contains(authorizeScopeSQL, "run.resource_id = $2") {
		t.Fatal("commit authorization must bind the exact durable run resource")
	}
	for _, fragment := range []string{
		"run.runtime_mode = 'durable'", "run.workspace_id = $1", "run.id = $4", "step.id = $5",
		"membership.status = 'active'", "document_nodes", "agent_observations", "evidence_id",
	} {
		if !strings.Contains(authorizeScopeSQL+authorizeNodesSQL+authorizeEvidenceSQL, fragment) {
			t.Fatalf("node authorization SQL must contain %q", fragment)
		}
	}
}

// TestNodeAuthorizationRejectsIncompleteTrustedScopeBeforeDatabase 验证对应场景下的正常路径与失败路径。
func TestNodeAuthorizationRejectsIncompleteTrustedScopeBeforeDatabase(t *testing.T) {
	_, err := New(nil).ResolveDocumentAuthorization(context.Background(), agenttools.SecurityContext{
		PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1",
	}, "resource-1", []string{"node-1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "trusted") {
		t.Fatalf("expected trusted run/step scope failure, got %v", err)
	}
}
