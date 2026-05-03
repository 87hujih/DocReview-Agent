package assistant

import "testing"

// TestPolicyGateBlocksTaskSuggestionWithoutMaterial 验证没有材料时 policy 不允许进入任务建议。
func TestPolicyGateBlocksTaskSuggestionWithoutMaterial(t *testing.T) {
	policy := ApplyPolicy(RuntimeState{
		Message: "直接开始改第三个项目，创建任务",
	}, &DeliberationDecision{
		RequestKind:          "workflow_command",
		ResponseMode:         ResponseModeAnswerThenTaskCard,
		ChatFulfillable:      false,
		WorkflowCommitment:   true,
		ConversationMode:     "execute",
		RequestedNextStep:    "promote_to_workflow",
		ProposalReady:        true,
		AwaitingAuthorization: true,
	})

	if !policy.AllowAnswer {
		t.Fatalf("expected policy to keep reply lane open, got %#v", policy)
	}
	if policy.BlockedReason != "missing_material" {
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
	if policy.AllowClarification {
		t.Fatalf("expected grounded readback to stay in answer lane, got %#v", policy)
	}
	if policy.BlockedReason != "" {
		t.Fatalf("expected grounded readback to avoid workflow boundary block reason, got %#v", policy)
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
		RequestKind:         "workflow_command",
		ResponseMode:        ResponseModeClarifyFirst,
		ChatFulfillable:     true,
		WorkflowCommitment:  false,
		NeedsClarification:  true,
		ClarificationQuestion: stringPointer("你是想先给草案，还是直接修改？"),
	})

	if !policy.AllowClarification {
		t.Fatalf("expected ambiguous transform to require clarification, got %#v", policy)
	}
	if !policy.AllowAnswer {
		t.Fatalf("expected clarification to stay in assistant text lane, got %#v", policy)
	}
}

// TestPolicyGateKeepsAuthorizedProposalDecisionInReplyLane 验证 policy 不再决定 workflow verifier / task suggestion，只保留最小硬边界。
func TestPolicyGateRequiresVerifierForWorkflowPromotion(t *testing.T) {
	policy := ApplyPolicy(RuntimeState{
		Message: "直接开始改第三个项目，创建任务",
		ActiveResource: &resourceContext{
			ID:     "resource-1",
			Title:  "简历",
			Source: "upload",
		},
	}, &DeliberationDecision{
		RequestKind:          "workflow_command",
		ResponseMode:         ResponseModePlanThenAnswer,
		ChatFulfillable:      false,
		WorkflowCommitment:   true,
		ConversationMode:     "execute",
		RequestedNextStep:    "promote_to_workflow",
		ProposalReady:        true,
		AwaitingAuthorization: false,
	})

	if !policy.AllowAnswer {
		t.Fatalf("expected authorized proposal decision to stay replyable, got %#v", policy)
	}
	if policy.AllowClarification {
		t.Fatalf("expected policy to stop owning clarification/task gate decisions, got %#v", policy)
	}
	if policy.BlockedReason != "" {
		t.Fatalf("expected ready resource to avoid hard block, got %#v", policy)
	}
}
