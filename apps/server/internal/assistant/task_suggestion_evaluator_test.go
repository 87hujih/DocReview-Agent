package assistant

import "testing"

// TestEvaluateTaskSuggestionMarksCapabilityQuestionAsNeedMaterial 验证`evaluateTaskSuggestion`在流程控制路径下的行为，防止同类回归。
func TestEvaluateTaskSuggestionMarksCapabilityQuestionAsNeedMaterial(t *testing.T) {
	decision := EvaluateTaskSuggestion(TaskSuggestionEvaluationInput{
		CurrentMessage: "你能做什么？什么时候适合创建任务？",
	})

	if decision.MaterialState != MaterialStateMissing {
		t.Fatalf("expected material state %q, got %q", MaterialStateMissing, decision.MaterialState)
	}
	if decision.IntentState != IntentStateCapabilityQuery {
		t.Fatalf("expected intent state %q, got %q", IntentStateCapabilityQuery, decision.IntentState)
	}
	if decision.ReadinessState != ReadinessStateNeedMaterial {
		t.Fatalf("expected readiness state %q, got %q", ReadinessStateNeedMaterial, decision.ReadinessState)
	}
	if decision.NormalizedInstruction != "" {
		t.Fatalf("expected empty normalized instruction, got %q", decision.NormalizedInstruction)
	}
}

// TestEvaluateTaskSuggestionMarksOptimizationQuestionAsDiscussion 验证`evaluateTaskSuggestion`在流程控制路径下的行为，防止同类回归。
func TestEvaluateTaskSuggestionMarksOptimizationQuestionAsDiscussion(t *testing.T) {
	decision := EvaluateTaskSuggestion(TaskSuggestionEvaluationInput{
		CurrentMessage: "在项目介绍表达方面有什么需要优化的吗？",
		ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
	})

	if decision.MaterialState != MaterialStateReady {
		t.Fatalf("expected material state %q, got %q", MaterialStateReady, decision.MaterialState)
	}
	if decision.IntentState != IntentStateDiscussion {
		t.Fatalf("expected intent state %q, got %q", IntentStateDiscussion, decision.IntentState)
	}
	if decision.ReadinessState != ReadinessStateReadyButNotExecuting {
		t.Fatalf("expected readiness state %q, got %q", ReadinessStateReadyButNotExecuting, decision.ReadinessState)
	}
	if decision.NormalizedInstruction != "" {
		t.Fatalf("expected empty normalized instruction, got %q", decision.NormalizedInstruction)
	}
}

// TestEvaluateTaskSuggestionMarksExplicitRewriteRequestAsExecution 验证`evaluateTaskSuggestion`在流程控制路径下的行为，防止同类回归。
func TestEvaluateTaskSuggestionMarksExplicitRewriteRequestAsExecution(t *testing.T) {
	decision := EvaluateTaskSuggestion(TaskSuggestionEvaluationInput{
		CurrentMessage: "请直接把这份简历改成产品经理版本",
		ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
	})

	if decision.MaterialState != MaterialStateReady {
		t.Fatalf("expected material state %q, got %q", MaterialStateReady, decision.MaterialState)
	}
	if decision.IntentState != IntentStateExecution {
		t.Fatalf("expected intent state %q, got %q", IntentStateExecution, decision.IntentState)
	}
	if decision.ReadinessState != ReadinessStateReadyForTask {
		t.Fatalf("expected readiness state %q, got %q", ReadinessStateReadyForTask, decision.ReadinessState)
	}
	if decision.NormalizedInstruction != "请直接把这份简历改成产品经理版本" {
		t.Fatalf("expected normalized instruction to keep execution request, got %q", decision.NormalizedInstruction)
	}
}

// TestEvaluateTaskSuggestionDoesNotPromoteReadOnlyCurrentFileRequest 验证`evaluateTaskSuggestionDoesNotPromoteReadOnlyCurrentFileRequest`在特定边界条件下的行为，防止同类回归。
func TestEvaluateTaskSuggestionDoesNotPromoteReadOnlyCurrentFileRequest(t *testing.T) {
	decision := EvaluateTaskSuggestion(TaskSuggestionEvaluationInput{
		CurrentMessage: "把第三个项目先输出一遍",
		ActiveResource: &resourceContext{ID: "resource-1", Title: "简历", Source: "upload"},
	})

	if decision.MaterialState != MaterialStateReady {
		t.Fatalf("expected material state %q, got %q", MaterialStateReady, decision.MaterialState)
	}
	if decision.IntentState != IntentStateDiscussion {
		t.Fatalf("expected intent state %q for read-only request, got %q", IntentStateDiscussion, decision.IntentState)
	}
	if decision.ReadinessState != ReadinessStateReadyButNotExecuting {
		t.Fatalf("expected readiness state %q, got %q", ReadinessStateReadyButNotExecuting, decision.ReadinessState)
	}
	if decision.NormalizedInstruction != "" {
		t.Fatalf("expected no normalized instruction for read-only request, got %q", decision.NormalizedInstruction)
	}
}
