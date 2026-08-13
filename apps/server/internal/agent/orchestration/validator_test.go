package orchestration_test

import (
	"encoding/json"
	"testing"

	"agent_project/apps/server/internal/agent/orchestration"
)

// TestActionValidatorOwnsToolRoutingAndPrerequisites 验证对应场景下的正常路径与失败路径。
func TestActionValidatorOwnsToolRoutingAndPrerequisites(t *testing.T) {
	validator := orchestration.NewActionValidator()
	base := orchestration.State{Goal: &orchestration.GoalState{Objective: "review document"}}

	valid, err := validator.Validate(base, orchestration.Decision{
		Action: orchestration.ActionRetrieveEvidence, Reason: "need evidence",
		ToolName: "retrieval.search", ToolInput: json.RawMessage(`{"resource_id":"resource-1","query":"policy","limit":5}`),
		ExpectedObservation: "evidence", Confidence: 0.8,
	})
	if err != nil || valid.NextNode != orchestration.NodeRetrieveEvidence || valid.ToolVersion != "2.0.0" {
		t.Fatalf("validate retrieval: action=%#v err=%v", valid, err)
	}

	invalid := []orchestration.Decision{
		testDecision(orchestration.ActionRetrieveEvidence, "web.search"),
		testDecision(orchestration.ActionAnalyze, ""),
		testDecision(orchestration.ActionGeneratePatch, ""),
		testDecision(orchestration.ActionRequestApproval, "workflow.request_approval"),
	}
	for index, decision := range invalid {
		if _, err := validator.Validate(base, decision); err == nil {
			t.Fatalf("invalid action %d was authorized: %#v", index, decision)
		}
	}
}

// TestActionValidatorProducesDeterministicWaitAndFinishTransitions 验证对应场景下的正常路径与失败路径。
func TestActionValidatorProducesDeterministicWaitAndFinishTransitions(t *testing.T) {
	validator := orchestration.NewActionValidator()
	state := orchestration.State{Goal: &orchestration.GoalState{Objective: "review document"}}
	wait, err := validator.Validate(state, testDecision(orchestration.ActionRequestUserInput, ""))
	if err != nil || !wait.WaitForInput || wait.NextNode != "" {
		t.Fatalf("wait transition=%#v err=%v", wait, err)
	}
	finish, err := validator.Validate(state, testDecision(orchestration.ActionFinish, ""))
	if err != nil || finish.NextNode != orchestration.NodeRenderOutcome || finish.ToolName != "" {
		t.Fatalf("finish transition=%#v err=%v", finish, err)
	}
}

// TestActionValidatorAllowsApprovalOnlyForValidatedPatch 验证对应场景下的正常路径与失败路径。
func TestActionValidatorAllowsApprovalOnlyForValidatedPatch(t *testing.T) {
	validator := orchestration.NewActionValidator()
	state := orchestration.State{
		Goal:  &orchestration.GoalState{Objective: "review document"},
		Patch: &orchestration.PatchState{Generated: true, Valid: true},
	}
	action, err := validator.Validate(state, orchestration.Decision{
		Action: orchestration.ActionRequestApproval, ToolName: "workflow.request_approval",
		ToolInput: json.RawMessage(`{"run_id":"run-1"}`), Reason: "publish patch",
		ExpectedObservation: "pending approval", Confidence: 0.9,
	})
	if err != nil || action.NextNode != orchestration.NodeRequestApproval {
		t.Fatalf("approval action=%#v err=%v", action, err)
	}
}

// testDecision 执行该函数负责的核心处理逻辑。
func testDecision(action orchestration.DecisionAction, toolName string) orchestration.Decision {
	return orchestration.Decision{
		Action: action, ToolName: toolName, ToolInput: json.RawMessage(`{}`),
		Reason: "test reason", ExpectedObservation: "test observation", Confidence: 0.8,
	}
}
