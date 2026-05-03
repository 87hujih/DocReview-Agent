package assistant

import "testing"

// TestActionGateAllowsWorkflowOnlyAfterExplicitAuthorization 验证只有显式授权后才允许 workflow promotion。
func TestActionGateAllowsWorkflowOnlyAfterExplicitAuthorization(t *testing.T) {
	state := RuntimeState{
		Message: "按这个方案改",
		ActiveResource: &resourceContext{
			ID:     "resource-1",
			Title:  "简历",
			Source: "upload",
		},
		PendingProposal: &SnapshotPendingProposal{
			ProposalID:        "proposal-1",
			Instruction:       "把第三个项目改成结果导向版本，并补量化指标",
			PlanGoal:          "强化第三个项目说服力",
			ProposedMessageID: "assistant-1",
		},
		AuthorizationState: &SnapshotAuthorizationState{Status: "pending"},
	}
	decision := &DeliberationDecision{
		ConversationMode:      "confirm",
		RequestedNextStep:     "promote_to_workflow",
		ProposalReady:         true,
		ProposedInstruction:   stringPointer("把第三个项目改成结果导向版本，并补量化指标"),
		ProposedPlanGoal:      stringPointer("强化第三个项目说服力"),
		AwaitingAuthorization: true,
	}
	resolution := PendingResolution{
		ResolvesProposal:      true,
		ExplicitAuthorization: true,
	}

	gate := DecideActionGate(state, decision, resolution, ApplyPolicy(state, decision))
	if !gate.AllowWorkflowPromotion {
		t.Fatalf("expected workflow promotion to be allowed, got %#v", gate)
	}
	if !gate.AllowTaskSuggestion {
		t.Fatalf("expected task suggestion to be allowed after authorization, got %#v", gate)
	}
	if gate.AuthorizationState == nil || gate.AuthorizationState.Status != "granted" {
		t.Fatalf("expected granted authorization state, got %#v", gate.AuthorizationState)
	}
}

// TestActionGateAllowsTaskSuggestionOnlyForAuthorizedMaterialReadyProposal 验证没有材料时，即使识别到授权也不能放行任务卡。
func TestActionGateAllowsTaskSuggestionOnlyForAuthorizedMaterialReadyProposal(t *testing.T) {
	state := RuntimeState{
		Message: "按这个方案改",
		PendingProposal: &SnapshotPendingProposal{
			ProposalID:        "proposal-1",
			Instruction:       "把第三个项目改成结果导向版本，并补量化指标",
			PlanGoal:          "强化第三个项目说服力",
			ProposedMessageID: "assistant-1",
		},
	}
	decision := &DeliberationDecision{
		ConversationMode:      "confirm",
		RequestedNextStep:     "promote_to_workflow",
		ProposalReady:         true,
		ProposedInstruction:   stringPointer("把第三个项目改成结果导向版本，并补量化指标"),
		ProposedPlanGoal:      stringPointer("强化第三个项目说服力"),
		AwaitingAuthorization: true,
	}
	resolution := PendingResolution{
		ResolvesProposal:      true,
		ExplicitAuthorization: true,
	}

	gate := DecideActionGate(state, decision, resolution, ApplyPolicy(state, decision))
	if gate.AllowWorkflowPromotion || gate.AllowTaskSuggestion {
		t.Fatalf("expected missing material to block workflow and task suggestion, got %#v", gate)
	}
}

// TestActionGateDowngradesToAdviceWhenProposalNotReadyOrDraftRequested 验证草案请求或 proposal 不成熟时会回退到 advice，而不是继续 promotion。
func TestActionGateDowngradesToAdviceWhenProposalNotReadyOrDraftRequested(t *testing.T) {
	state := RuntimeState{
		Message: "先给我草案看看",
		ActiveResource: &resourceContext{
			ID:     "resource-1",
			Title:  "简历",
			Source: "upload",
		},
	}
	decision := &DeliberationDecision{
		ConversationMode:      "confirm",
		RequestedNextStep:     "request_authorization",
		ProposalReady:         true,
		ProposedInstruction:   stringPointer("把第三个项目改成结果导向版本，并补量化指标"),
		ProposedPlanGoal:      stringPointer("强化第三个项目说服力"),
		AwaitingAuthorization: true,
	}
	resolution := PendingResolution{
		DowngradeToAdvice: true,
	}

	gate := DecideActionGate(state, decision, resolution, ApplyPolicy(state, decision))
	if gate.ConversationMode != "advise" {
		t.Fatalf("expected advice mode, got %#v", gate)
	}
	if gate.AllowWorkflowPromotion || gate.AllowTaskSuggestion {
		t.Fatalf("expected advice downgrade to block workflow and task suggestion, got %#v", gate)
	}
}

// TestActionGateRejectsLegacyWorkflowPromotionWithoutAdvisorContract 验证旧的 response_mode/workflow_commitment 不能再直接放行 workflow。
func TestActionGateRejectsLegacyWorkflowPromotionWithoutAdvisorContract(t *testing.T) {
	state := RuntimeState{
		Message: "直接开始改第三个项目",
		ActiveResource: &resourceContext{
			ID:     "resource-1",
			Title:  "简历",
			Source: "upload",
		},
	}
	decision := &DeliberationDecision{
		RequestKind:         "workflow_command",
		ResponseMode:        ResponseModePlanThenAnswer,
		WorkflowCommitment:  true,
		ChatFulfillable:     false,
		EvidenceSufficiency: "sufficient",
		Confidence:          0.9,
		Reasons:             []string{"旧契约里这会被直接当成 workflow candidate"},
	}

	gate := DecideActionGate(state, decision, PendingResolution{}, ApplyPolicy(state, decision))
	if gate.AllowWorkflowPromotion || gate.AllowTaskSuggestion {
		t.Fatalf("expected legacy workflow-only decision to stay blocked until advisor contract is explicit, got %#v", gate)
	}
}

// TestActionGateDirectWorkflowPromotionCarriesAuthorizedProposalState 验证单轮直接进入 workflow 时也必须先补齐 proposal / authorization 真源。
func TestActionGateDirectWorkflowPromotionCarriesAuthorizedProposalState(t *testing.T) {
	state := RuntimeState{
		Message: "直接开始改第三个项目，创建任务",
		ActiveResource: &resourceContext{
			ID:     "resource-1",
			Title:  "简历",
			Source: "upload",
		},
	}
	decision := &DeliberationDecision{
		RequestKind:         "workflow_command",
		ResponseMode:        ResponseModePlanThenAnswer,
		ConversationMode:    "execute",
		RequestedNextStep:   "promote_to_workflow",
		ProposalReady:       true,
		ProposedInstruction: stringPointer("把第三个项目改成结果导向版本，并补量化指标"),
		ProposedPlanGoal:    stringPointer("强化第三个项目说服力"),
		ChatFulfillable:     false,
		WorkflowCommitment:  true,
		Confidence:          0.9,
		Reasons:             []string{"用户已经明确要求直接进入执行"},
	}

	gate := DecideActionGate(state, decision, PendingResolution{}, ApplyPolicy(state, decision))
	if !gate.AllowWorkflowPromotion || !gate.AllowTaskSuggestion {
		t.Fatalf("expected direct workflow promotion to stay allowed, got %#v", gate)
	}
	if gate.PendingProposal == nil || gate.PendingProposal.Instruction != "把第三个项目改成结果导向版本，并补量化指标" {
		t.Fatalf("expected gate to carry pending proposal, got %#v", gate.PendingProposal)
	}
	if gate.AuthorizationState == nil || gate.AuthorizationState.Status != "granted" {
		t.Fatalf("expected gate to carry granted authorization state, got %#v", gate.AuthorizationState)
	}
}
