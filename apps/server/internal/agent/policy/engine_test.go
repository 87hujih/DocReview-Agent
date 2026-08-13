package policy_test

import (
	"context"
	"encoding/json"
	"testing"

	"agent_project/apps/server/internal/agent/policy"
	agenttools "agent_project/apps/server/internal/agent/tools"
)

// TestHighRiskToolCannotApproveItself 验证对应场景下的正常路径与失败路径。
func TestHighRiskToolCannotApproveItself(t *testing.T) {
	engine := policy.NewEngine(
		permissionResolver{allowed: true},
		resourceResolver{allowed: true},
		approvalVerifier{approved: true},
	)
	request := agenttools.AuthorizationRequest{
		Descriptor: agenttools.Descriptor{
			Name: "patch.commit", Version: "1.0.0", RequiredPermissions: []string{"document.write"},
			RiskLevel: agenttools.RiskHigh,
		},
		Call: agenttools.Call{
			Security:       agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
			Input:          json.RawMessage(`{"resource_id":"resource-1","approved":true}`),
			IdempotencyKey: "commit-1",
		},
		Resources: []agenttools.ResourceRef{{Type: "document", ID: "resource-1", Access: agenttools.AccessWrite}},
	}

	decision, err := engine.Authorize(context.Background(), request)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if decision.Outcome != agenttools.PolicyRequireApproval || decision.ReasonCode != "approval_required" {
		t.Fatalf("model self-approval changed policy: %#v", decision)
	}
}

// TestPolicyDeniesResourceOutsideWorkspaceGrant 验证对应场景下的正常路径与失败路径。
func TestPolicyDeniesResourceOutsideWorkspaceGrant(t *testing.T) {
	engine := policy.NewEngine(
		permissionResolver{allowed: true},
		resourceResolver{allowed: false},
		approvalVerifier{approved: true},
	)
	decision, err := engine.Authorize(context.Background(), agenttools.AuthorizationRequest{
		Descriptor: agenttools.Descriptor{Name: "document.read_nodes", Version: "1.0.0", RequiredPermissions: []string{"document.read"}, RiskLevel: agenttools.RiskLow},
		Call:       agenttools.Call{Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"}},
		Resources:  []agenttools.ResourceRef{{Type: "document", ID: "other-tenant-resource", Access: agenttools.AccessRead}},
	})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if decision.Outcome != agenttools.PolicyDeny || decision.ReasonCode != "resource_scope_denied" {
		t.Fatalf("cross-resource decision = %#v", decision)
	}
}

// TestPolicyAllowsHighRiskToolOnlyWithMatchingExternalApproval 验证对应场景下的正常路径与失败路径。
func TestPolicyAllowsHighRiskToolOnlyWithMatchingExternalApproval(t *testing.T) {
	engine := policy.NewEngine(permissionResolver{allowed: true}, resourceResolver{allowed: true}, approvalVerifier{approved: true})
	decision, err := engine.Authorize(context.Background(), agenttools.AuthorizationRequest{
		Descriptor: agenttools.Descriptor{
			Name: "patch.commit", Version: "1.0.0", RequiredPermissions: []string{"document.write"}, RiskLevel: agenttools.RiskHigh,
		},
		Call: agenttools.Call{
			ApprovalID: "approval-1", IdempotencyKey: "commit-1",
			Security: agenttools.SecurityContext{PrincipalType: "user", PrincipalID: "user-1", WorkspaceID: "workspace-1"},
		},
		Resources: []agenttools.ResourceRef{{Type: "document", ID: "resource-1", Access: agenttools.AccessWrite}},
	})
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if decision.Outcome != agenttools.PolicyAllow || decision.ReasonCode != "authorized" {
		t.Fatalf("approved decision = %#v", decision)
	}
}

type permissionResolver struct{ allowed bool }

// HasPermission 执行该函数负责的核心处理逻辑。
func (r permissionResolver) HasPermission(context.Context, agenttools.SecurityContext, string) (bool, error) {
	return r.allowed, nil
}

type resourceResolver struct{ allowed bool }

// AuthorizeResource 执行该函数负责的核心处理逻辑。
func (r resourceResolver) AuthorizeResource(context.Context, agenttools.SecurityContext, agenttools.ResourceRef) (bool, error) {
	return r.allowed, nil
}

type approvalVerifier struct{ approved bool }

// VerifyApproval 执行该函数负责的核心处理逻辑。
func (v approvalVerifier) VerifyApproval(context.Context, agenttools.ApprovalCheck) (bool, error) {
	return v.approved, nil
}
