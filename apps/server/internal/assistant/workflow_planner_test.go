package assistant

import (
	"reflect"
	"testing"
)

// TestWorkflowPlanDecisionNormalizeKeepsValidInstruction 验证合法 planner 决策会保留 workflow instruction。
func TestWorkflowPlanDecisionNormalizeKeepsValidInstruction(t *testing.T) {
	decision, err := normalizeWorkflowPlanDecision(&WorkflowPlanDecision{
		ShouldEnterWorkflow: true,
		CandidateInstruction: stringPointer("  请把第三个项目改成产品经理版本  "),
		CandidatePlanGoal:    stringPointer("  产出可执行的修订任务  "),
		Confidence:           0.92,
		Reasons: []string{
			"  用户明确要求开始执行  ",
			"  当前资源已足够  ",
		},
	})
	if err != nil {
		t.Fatalf("normalize workflow plan decision: %v", err)
	}

	if !decision.ShouldEnterWorkflow {
		t.Fatalf("expected workflow promotion to be preserved, got %#v", decision)
	}
	if decision.ChatFulfillable {
		t.Fatalf("expected workflow promotion to disable chat fulfilment, got %#v", decision)
	}
	if decision.CandidateInstruction == nil || *decision.CandidateInstruction != "请把第三个项目改成产品经理版本" {
		t.Fatalf("expected normalized candidate instruction, got %#v", decision.CandidateInstruction)
	}
	if decision.CandidatePlanGoal == nil || *decision.CandidatePlanGoal != "产出可执行的修订任务" {
		t.Fatalf("expected normalized candidate plan goal, got %#v", decision.CandidatePlanGoal)
	}
	if len(decision.Reasons) != 2 || decision.Reasons[0] != "用户明确要求开始执行" {
		t.Fatalf("expected normalized reasons, got %#v", decision.Reasons)
	}
}

// TestWorkflowPlanDecisionRejectsMissingReasonForWorkflowPromotion 验证进入 workflow 的 planner 决策必须携带原因。
func TestWorkflowPlanDecisionRejectsMissingReasonForWorkflowPromotion(t *testing.T) {
	_, err := normalizeWorkflowPlanDecision(&WorkflowPlanDecision{
		ShouldEnterWorkflow:  true,
		CandidateInstruction: stringPointer("请把第三个项目改成产品经理版本"),
		Confidence:           0.88,
	})
	if err == nil {
		t.Fatal("expected missing reasons error")
	}
}

// TestWorkflowPlanDecisionTreatsChatFulfillableAsNoWorkflow 验证可直接聊天完成的请求不会继续进入 workflow。
func TestWorkflowPlanDecisionTreatsChatFulfillableAsNoWorkflow(t *testing.T) {
	decision, err := normalizeWorkflowPlanDecision(&WorkflowPlanDecision{
		ShouldEnterWorkflow:  true,
		ChatFulfillable:      true,
		NeedsClarification:   true,
		ClarificationQuestion: stringPointer("还缺什么材料？"),
		CandidateInstruction: stringPointer("请把第三个项目改成产品经理版本"),
		CandidatePlanGoal:    stringPointer("产出可执行的修订任务"),
		MissingMaterials:     []string{"原始简历"},
		Confidence:           0.61,
		Reasons:              []string{"当前在聊天里就能直接完成"},
	})
	if err != nil {
		t.Fatalf("normalize workflow plan decision: %v", err)
	}

	if decision.ShouldEnterWorkflow || decision.NeedsClarification {
		t.Fatalf("expected chat-fulfillable decision to stay out of workflow, got %#v", decision)
	}
	if decision.ClarificationQuestion != nil || decision.CandidateInstruction != nil || decision.CandidatePlanGoal != nil {
		t.Fatalf("expected chat-fulfillable decision to clear workflow-only fields, got %#v", decision)
	}
	if len(decision.MissingMaterials) != 0 {
		t.Fatalf("expected chat-fulfillable decision to clear missing materials, got %#v", decision.MissingMaterials)
	}
}

// TestWorkflowPlannerInterfaceDoesNotDependOnTaskRepo 验证 planner interface 只依赖 runtime state 与 deliberation 结果。
func TestWorkflowPlannerInterfaceDoesNotDependOnTaskRepo(t *testing.T) {
	method, ok := reflect.TypeOf((*workflowPlanner)(nil)).Elem().MethodByName("Plan")
	if !ok {
		t.Fatal("expected workflowPlanner to expose Plan")
	}

	expected := "func(context.Context, assistant.RuntimeState, *assistant.DeliberationDecision) (*assistant.WorkflowPlanDecision, error)"
	if method.Type.String() != expected {
		t.Fatalf("expected planner signature %q, got %q", expected, method.Type.String())
	}
}
