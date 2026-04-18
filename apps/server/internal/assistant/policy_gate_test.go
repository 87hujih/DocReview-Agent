package assistant

import "testing"

// TestPolicyGateBlocksTaskSuggestionWithoutMaterial 验证没有材料时 policy 不允许进入任务建议。
func TestPolicyGateBlocksTaskSuggestionWithoutMaterial(t *testing.T) {
	policy := ApplyPolicy(RuntimeState{
		Message: "直接开始改第三个项目，创建任务",
	}, &DeliberationDecision{
		RequestKind:        "workflow_command",
		ResponseMode:       ResponseModeAnswerThenTaskCard,
		ChatFulfillable:    false,
		WorkflowCommitment: true,
	})

	if policy.AllowTaskSuggestion {
		t.Fatalf("expected policy to block task suggestion without material, got %#v", policy)
	}
	if policy.AllowClarification {
		t.Fatalf("expected missing material to block directly instead of clarifying, got %#v", policy)
	}
	if policy.BlockedReason == "" {
		t.Fatalf("expected blocked reason for missing material, got %#v", policy)
	}
}

// TestPolicyGateKeepsReadbackInAnswerWithGrounding 验证阅读型请求会保留在 grounded answer 通道。
func TestPolicyGateKeepsReadbackInAnswerWithGrounding(t *testing.T) {
	policy := ApplyPolicy(RuntimeState{
		Message: "把第三个项目先输出一遍",
		ActiveResource: &resourceContext{
			ID:     "resource-1",
			Title:  "简历",
			Source: "upload",
		},
	}, &DeliberationDecision{
		RequestKind:        "readback",
		ResponseMode:       ResponseModeAnswerWithGrounding,
		ChatFulfillable:    true,
		WorkflowCommitment: false,
	})

	if !policy.AllowAnswer {
		t.Fatalf("expected grounded readback to stay answerable, got %#v", policy)
	}
	if policy.AllowTaskSuggestion || policy.AllowWorkflowPlanning || policy.AllowClarification {
		t.Fatalf("expected grounded readback to stay in answer lane, got %#v", policy)
	}
}

// TestPolicyGateTurnsAmbiguousTransformIntoClarifyFirst 验证模糊改写请求不会直接升级成任务建议。
func TestPolicyGateTurnsAmbiguousTransformIntoClarifyFirst(t *testing.T) {
	policy := ApplyPolicy(RuntimeState{
		Message: "整理成表格",
		ActiveResource: &resourceContext{
			ID:     "resource-1",
			Title:  "简历",
			Source: "upload",
		},
	}, &DeliberationDecision{
		RequestKind:        "workflow_command",
		ResponseMode:       ResponseModeAnswerThenTaskCard,
		ChatFulfillable:    true,
		WorkflowCommitment: false,
	})

	if !policy.AllowClarification {
		t.Fatalf("expected ambiguous transform to require clarification, got %#v", policy)
	}
	if policy.AllowTaskSuggestion {
		t.Fatalf("expected ambiguous transform to block task suggestion, got %#v", policy)
	}
	if policy.BlockedReason == "" {
		t.Fatalf("expected blocked reason for missing workflow commitment, got %#v", policy)
	}
}
