package assistant

import "testing"

// TestPendingResolverTreatsDirectModifyAsClarificationAnswer 验证已有待确认澄清时，“直接修改”会被解析成执行分支选择，而不是全局关键词命中。
func TestPendingResolverTreatsDirectModifyAsClarificationAnswer(t *testing.T) {
	resolution := ResolvePendingState(RuntimeState{
		Message: "可以，直接修改吧",
		PendingClarification: &SnapshotPendingClarification{
			Kind:     "workflow_branch",
			Question: "你是想先给草案，还是直接修改？",
			Options:  []string{"先给我草案看看", "直接修改"},
		},
	})

	if !resolution.ResolvesClarification {
		t.Fatalf("expected clarification to be resolved, got %#v", resolution)
	}
	if resolution.SelectedOption != "workflow" {
		t.Fatalf("expected workflow option, got %#v", resolution.SelectedOption)
	}
	if resolution.ExplicitAuthorization {
		t.Fatalf("clarification answer should not be treated as proposal authorization, got %#v", resolution)
	}
}

// TestPendingResolverAcceptsUniqueProposalAuthorization 验证唯一 pending proposal 下，“按这个方案改”会被识别为明确授权。
func TestPendingResolverAcceptsUniqueProposalAuthorization(t *testing.T) {
	resolution := ResolvePendingState(RuntimeState{
		Message: "按这个方案改",
		PendingProposal: &SnapshotPendingProposal{
			ProposalID:        "proposal-1",
			Instruction:       "把第三个项目改成结果导向版本，并补量化指标",
			PlanGoal:          "强化第三个项目说服力",
			ProposedMessageID: "assistant-1",
		},
		AuthorizationState: &SnapshotAuthorizationState{Status: "pending"},
	})

	if !resolution.ResolvesProposal {
		t.Fatalf("expected proposal to be resolved, got %#v", resolution)
	}
	if !resolution.ExplicitAuthorization {
		t.Fatalf("expected explicit authorization, got %#v", resolution)
	}
}

// TestPendingResolverDowngradesToAdviceForDraftRequest 验证用户要求先看草案时，会回退到顾问/草案通道，而不是进入执行授权。
func TestPendingResolverDowngradesToAdviceForDraftRequest(t *testing.T) {
	resolution := ResolvePendingState(RuntimeState{
		Message: "先给我草案看看",
		PendingProposal: &SnapshotPendingProposal{
			ProposalID:        "proposal-1",
			Instruction:       "把第三个项目改成结果导向版本，并补量化指标",
			PlanGoal:          "强化第三个项目说服力",
			ProposedMessageID: "assistant-1",
		},
	})

	if !resolution.DowngradeToAdvice {
		t.Fatalf("expected draft request to downgrade to advice, got %#v", resolution)
	}
	if resolution.ExplicitAuthorization {
		t.Fatalf("draft request must not authorize execution, got %#v", resolution)
	}
}

// TestPendingResolverRejectsAmbiguousAuthorizationWhenProposalSelectionPending 验证多建议待选时，“按你的建议改”仍需继续澄清。
func TestPendingResolverRejectsAmbiguousAuthorizationWhenProposalSelectionPending(t *testing.T) {
	resolution := ResolvePendingState(RuntimeState{
		Message: "按你的建议改",
		PendingClarification: &SnapshotPendingClarification{
			Kind:     "proposal_selection",
			Question: "按哪条建议改？",
			Options:  []string{"按结果导向版本改", "按技术深度版本改"},
		},
	})

	if !resolution.NeedsFollowupQuestion {
		t.Fatalf("expected ambiguous proposal authorization to require follow-up, got %#v", resolution)
	}
	if resolution.ExplicitAuthorization {
		t.Fatalf("ambiguous proposal selection must not authorize execution, got %#v", resolution)
	}
}
