package assistant

import (
	"reflect"
	"strings"
	"testing"
)

// TestWorkflowVerificationDecisionNormalizeApprovesValidPromotion 验证合法的 workflow promotion 会被 verifier 保留。
func TestWorkflowVerificationDecisionNormalizeApprovesValidPromotion(t *testing.T) {
	decision, err := normalizeWorkflowVerificationDecision(&WorkflowVerificationDecision{
		ApproveWorkflow:    true,
		RevisedInstruction: stringPointer("把第三个项目改成更聚焦产品岗位的版本"),
		Confidence:         0.92,
		Reasons:            []string{"用户明确要求开始执行", "planner 已确认材料充足"},
	})
	if err != nil {
		t.Fatalf("normalize workflow verification decision: %v", err)
	}

	if !decision.ApproveWorkflow || decision.DowngradeToChat || decision.NeedsClarification {
		t.Fatalf("unexpected normalized decision: %#v", decision)
	}
	if decision.RevisedInstruction == nil || *decision.RevisedInstruction != "把第三个项目改成更聚焦产品岗位的版本" {
		t.Fatalf("expected revised instruction to be preserved, got %#v", decision.RevisedInstruction)
	}
}

// TestWorkflowVerificationDecisionRejectsApproveWithoutReason 验证 verifier 放行 workflow 时必须说明原因。
func TestWorkflowVerificationDecisionRejectsApproveWithoutReason(t *testing.T) {
	_, err := normalizeWorkflowVerificationDecision(&WorkflowVerificationDecision{
		ApproveWorkflow: true,
		Confidence:      0.81,
	})
	if err == nil {
		t.Fatal("expected approve decision without reasons to fail")
	}
	if !strings.Contains(err.Error(), "reasons") {
		t.Fatalf("expected missing reasons error, got %v", err)
	}
}

// TestWorkflowVerificationDecisionAllowsDowngradeToChat 验证 verifier 可以把 promotion 收回聊天通道。
func TestWorkflowVerificationDecisionAllowsDowngradeToChat(t *testing.T) {
	decision, err := normalizeWorkflowVerificationDecision(&WorkflowVerificationDecision{
		DowngradeToChat:       true,
		ClarificationQuestion: stringPointer("ignored"),
		RevisedInstruction:    stringPointer("ignored"),
		Confidence:            0.74,
		Reasons:               []string{"当前请求仍可在聊天中直接完成"},
	})
	if err != nil {
		t.Fatalf("normalize workflow verification decision: %v", err)
	}

	if decision.ApproveWorkflow || !decision.DowngradeToChat || decision.NeedsClarification {
		t.Fatalf("unexpected downgrade decision: %#v", decision)
	}
	if decision.ClarificationQuestion != nil || decision.RevisedInstruction != nil {
		t.Fatalf("expected downgrade decision to clear workflow-only fields, got %#v", decision)
	}
}

// TestWorkflowVerifierInterfaceConsumesPlannerResultInsteadOfRawMessageOnly 验证 verifier 接口显式消费 planner 结果。
func TestWorkflowVerifierInterfaceConsumesPlannerResultInsteadOfRawMessageOnly(t *testing.T) {
	method, ok := reflect.TypeOf((*workflowVerifier)(nil)).Elem().MethodByName("Verify")
	if !ok {
		t.Fatal("expected workflowVerifier to expose Verify")
	}

	expected := "func(context.Context, assistant.RuntimeState, *assistant.DeliberationDecision, *assistant.WorkflowPlanDecision) (*assistant.WorkflowVerificationDecision, error)"
	if actual := method.Type.String(); actual != expected {
		t.Fatalf("unexpected workflow verifier signature: %s", actual)
	}
}
