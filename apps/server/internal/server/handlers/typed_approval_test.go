package handlers

import (
	"bytes"
	"context"
	"testing"

	"agent_project/apps/server/internal/agent/identity"
	"agent_project/apps/server/internal/agent/tools/builtin"
	"agent_project/apps/server/internal/storage/postgres/agentpolicy"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type fakeTypedApprovalDecider struct{ params agentpolicy.DecisionParams }

// DecideApproval 执行该函数负责的核心处理逻辑。
func (decider *fakeTypedApprovalDecider) DecideApproval(_ context.Context, params agentpolicy.DecisionParams) (builtin.Approval, error) {
	decider.params = params
	return builtin.Approval{ID: params.ApprovalID, Status: params.Status}, nil
}

type fakeApprovalIdentity struct{ scope identity.WorkspaceScope }

// Authenticate 执行该函数负责的核心处理逻辑。
func (adapter fakeApprovalIdentity) Authenticate(context.Context, identity.Request, string) (identity.WorkspaceScope, error) {
	return adapter.scope, nil
}

// TestTypedApprovalDecisionUsesExternalTrustedUserAndExactStatus 验证对应场景下的正常路径与失败路径。
func TestTypedApprovalDecisionUsesExternalTrustedUserAndExactStatus(t *testing.T) {
	decider := &fakeTypedApprovalDecider{}
	handler := NewTypedApprovalHandler(decider, fakeApprovalIdentity{scope: identity.WorkspaceScope{
		WorkspaceID: "00000000-0000-4000-8000-000000000010", Trusted: true, TrustSource: "edge-hmac-v1",
		Principal: identity.Principal{Type: "user", ID: "00000000-0000-4000-8000-000000000011"},
	}})
	h := server.New()
	h.POST("/api/agent/approvals/:id/approve", handler.Approve)
	response := ut.PerformRequest(h.Engine, "POST", "/api/agent/approvals/00000000-0000-4000-8000-000000000020/approve", &ut.Body{Body: bytes.NewBufferString(`{"reason":"reviewed"}`), Len: 21},
		ut.Header{Key: identity.HeaderWorkspaceID, Value: "00000000-0000-4000-8000-000000000010"}).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected approval success, got %d: %s", response.StatusCode(), response.Body())
	}
	if decider.params.Status != "approved" || decider.params.Security.PrincipalType != "user" || decider.params.Reason != "reviewed" {
		t.Fatalf("decision did not use trusted external scope: %+v", decider.params)
	}
}

// TestTypedApprovalRejectsMissingReasonBeforeDecision 验证对应场景下的正常路径与失败路径。
func TestTypedApprovalRejectsMissingReasonBeforeDecision(t *testing.T) {
	decider := &fakeTypedApprovalDecider{}
	handler := NewTypedApprovalHandler(decider, fakeApprovalIdentity{scope: identity.WorkspaceScope{
		WorkspaceID: "00000000-0000-4000-8000-000000000010", Trusted: true, TrustSource: "edge-hmac-v1",
		Principal: identity.Principal{Type: "user", ID: "00000000-0000-4000-8000-000000000011"},
	}})
	h := server.New()
	h.POST("/api/agent/approvals/:id/reject", handler.Reject)
	response := ut.PerformRequest(h.Engine, "POST", "/api/agent/approvals/00000000-0000-4000-8000-000000000020/reject", &ut.Body{Body: bytes.NewBufferString(`{}`), Len: 2},
		ut.Header{Key: identity.HeaderWorkspaceID, Value: "00000000-0000-4000-8000-000000000010"}).Result()
	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected missing reason rejection, got %d", response.StatusCode())
	}
	if decider.params.Status != "" {
		t.Fatal("invalid request reached the external approval store")
	}
}
