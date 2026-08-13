package agentpolicy

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agenttools "agent_project/apps/server/internal/agent/tools"
	"agent_project/apps/server/internal/agent/tools/builtin"
)

// TestJSONEqualUsesJSONSemantics verifies PostgreSQL JSONB key normalization does not create false conflicts.
func TestJSONEqualUsesJSONSemantics(t *testing.T) {
	left := json.RawMessage(`{"base_version_id":"version-1","operations":[]}`)
	right := json.RawMessage(`{"operations":[],"base_version_id":"version-1"}`)
	if !jsonEqual(left, right) {
		t.Fatal("equivalent JSON objects with different key order must compare equal")
	}
	if jsonEqual(left, json.RawMessage(`{"base_version_id":"version-2","operations":[]}`)) {
		t.Fatal("different JSON objects must not compare equal")
	}
	if jsonEqual(json.RawMessage(`{"value":9007199254740992}`), json.RawMessage(`{"value":9007199254740993}`)) {
		t.Fatal("different large JSON integers must not compare equal")
	}
}

// TestApprovalRequestRejectsMissingTrustedScopeBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestApprovalRequestRejectsMissingTrustedScopeBeforeDatabaseAccess(t *testing.T) {
	store := NewApprovalStore(nil)
	_, err := store.RequestApproval(context.Background(), agenttools.SecurityContext{}, builtin.ApprovalInput{
		RunID: "run-1", StepID: "step-1", ToolName: "patch.commit", ToolVersion: "1.0.0",
		IdempotencyKey: "patch-1", Reason: "write document", Payload: []byte(`{}`),
	}, "patch-1")
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("expected trusted scope validation, got %v", err)
	}
}

// TestApprovalDecisionRejectsInvalidDecisionBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestApprovalDecisionRejectsInvalidDecisionBeforeDatabaseAccess(t *testing.T) {
	store := NewApprovalStore(nil)
	_, err := store.DecideApproval(context.Background(), DecisionParams{
		ApprovalID: "approval-1", Status: "approved_by_model", Reason: "model said yes", DecidedAt: time.Now().UTC(),
		Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
	})
	if err == nil || !strings.Contains(err.Error(), "approved/rejected") {
		t.Fatalf("expected deterministic decision status validation, got %v", err)
	}
}

// TestPolicyResolverFailsClosedForUnknownResourceTypeWithoutDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestPolicyResolverFailsClosedForUnknownResourceTypeWithoutDatabaseAccess(t *testing.T) {
	resolver := NewResolver(nil)
	allowed, err := resolver.AuthorizeResource(context.Background(), agenttools.SecurityContext{
		PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1",
	}, agenttools.ResourceRef{Type: "unknown", ID: "resource-1", Access: agenttools.AccessRead})
	if err != nil {
		t.Fatalf("authorize unknown resource: %v", err)
	}
	if allowed {
		t.Fatal("unknown resource type was authorized")
	}
}

// TestPolicyResolverRejectsDocumentOutsideDurableRunResourceBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestPolicyResolverRejectsDocumentOutsideDurableRunResourceBeforeDatabaseAccess(t *testing.T) {
	resolver := NewResolver(nil)
	allowed, err := resolver.AuthorizeResource(context.Background(), agenttools.SecurityContext{
		PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1", ResourceID: "resource-1",
	}, agenttools.ResourceRef{Type: "document", ID: "resource-2", Access: agenttools.AccessRead})
	if err != nil {
		t.Fatalf("reject resource outside durable run scope: %v", err)
	}
	if allowed {
		t.Fatal("document outside the durable run resource scope was authorized")
	}
}

// TestDurableRateLimiterRejectsMissingTrustedScopeBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestDurableRateLimiterRejectsMissingTrustedScopeBeforeDatabaseAccess(t *testing.T) {
	limiter := NewPostgresRateLimiter(nil, StaticRateLimitRules{Default: RateLimitRule{Limit: 1, Window: time.Minute}})
	_, err := limiter.Allow(context.Background(), agenttools.RateLimitRequest{ToolName: "web.search", ToolVersion: "1.0.0"})
	if err == nil || !strings.Contains(err.Error(), "principal") {
		t.Fatalf("expected principal validation before database access, got %v", err)
	}
}
