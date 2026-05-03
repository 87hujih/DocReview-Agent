package assistant

import "strings"

// ActionGateDecision 表示 advisor runtime 对 workflow promotion / task suggestion 的最终闸门裁决。
type ActionGateDecision struct {
	ConversationMode      string
	AllowWorkflowPromotion bool
	AllowTaskSuggestion   bool
	PendingClarification  *SnapshotPendingClarification
	PendingProposal       *SnapshotPendingProposal
	AuthorizationState    *SnapshotAuthorizationState
	BlockedReason         string
}

// DecideActionGate 根据当前 advisor state、deterministic pending resolution 与最小 policy 边界收敛最终动作。
func DecideActionGate(
	state RuntimeState,
	decision *DeliberationDecision,
	resolution PendingResolution,
	policy PolicyDecision,
) ActionGateDecision {
	gate := ActionGateDecision{
		ConversationMode: strings.TrimSpace(defaultConversationMode(decision)),
	}

	if resolution.NeedsFollowupQuestion {
		gate.ConversationMode = "confirm"
		gate.PendingClarification = clonePendingClarification(state.PendingClarification)
		gate.BlockedReason = "followup_required"
		return gate
	}

	if resolution.DowngradeToAdvice {
		gate.ConversationMode = "advise"
		gate.BlockedReason = "downgrade_to_advice"
		return gate
	}

	if resolution.ExplicitAuthorization && state.PendingProposal != nil {
		gate.ConversationMode = "execute"
		gate.PendingProposal = clonePendingProposal(state.PendingProposal)
		gate.AuthorizationState = &SnapshotAuthorizationState{
			Status:               "granted",
			GrantedForProposalID: strings.TrimSpace(state.PendingProposal.ProposalID),
		}
		if state.ActiveResource == nil {
			gate.BlockedReason = "missing_material"
			return gate
		}
		gate.AllowWorkflowPromotion = true
		gate.AllowTaskSuggestion = true
		return gate
	}

	if requestsImmediateWorkflowPromotion(decision) {
		gate.ConversationMode = "execute"
		gate.PendingProposal = buildPendingProposalFromDecision(decision)
		if gate.PendingProposal == nil {
			gate.BlockedReason = "missing_proposal"
			return gate
		}
		gate.AuthorizationState = &SnapshotAuthorizationState{
			Status:               "granted",
			GrantedForProposalID: strings.TrimSpace(gate.PendingProposal.ProposalID),
		}
		if state.ActiveResource == nil {
			gate.BlockedReason = "missing_material"
			return gate
		}
		gate.AllowWorkflowPromotion = true
		gate.AllowTaskSuggestion = true
		return gate
	}

	if decision != nil && decision.ProposalReady {
		gate.ConversationMode = "confirm"
		gate.PendingProposal = buildPendingProposalFromDecision(decision)
		gate.AuthorizationState = &SnapshotAuthorizationState{Status: "pending"}
		if gate.PendingProposal == nil {
			gate.BlockedReason = "missing_proposal"
		}
		return gate
	}

	if policy.AllowClarification {
		gate.ConversationMode = "explore"
		gate.PendingClarification = buildPendingClarification(state.PendingClarification, decision)
		gate.BlockedReason = strings.TrimSpace(policy.BlockedReason)
	}

	return gate
}

// requestsImmediateWorkflowPromotion 判断当前 deliberation 是否已经给出可直接进入 workflow promotion 的明确结论。
func requestsImmediateWorkflowPromotion(decision *DeliberationDecision) bool {
	if decision == nil {
		return false
	}
	if decision.AwaitingAuthorization {
		return false
	}

	requestedNextStep := strings.TrimSpace(decision.RequestedNextStep)
	conversationMode := strings.TrimSpace(decision.ConversationMode)
	return requestedNextStep == "promote_to_workflow" &&
		conversationMode == "execute" &&
		decision.ProposalReady
}

// buildPendingProposalFromDecision 把 deliberation 产出的 proposal 线索归一成可持久化的 pending proposal。
func buildPendingProposalFromDecision(decision *DeliberationDecision) *SnapshotPendingProposal {
	if decision == nil || !decision.ProposalReady {
		return nil
	}

	instruction := derefOptionalString(decision.ProposedInstruction)
	if instruction == "" {
		instruction = derefOptionalString(decision.CandidateTaskInstruction)
	}
	if instruction == "" {
		return nil
	}

	proposal := &SnapshotPendingProposal{
		Instruction:                   instruction,
		PlanGoal:                      derefOptionalString(decision.ProposedPlanGoal),
		RequiresExplicitAuthorization: true,
	}
	if proposal.PlanGoal == "" {
		proposal.PlanGoal = derefOptionalString(decision.CandidatePlanGoal)
	}

	return proposal
}

// buildPendingClarification 统一把当前轮需要继续澄清的问题投影成 pending clarification。
func buildPendingClarification(
	existing *SnapshotPendingClarification,
	decision *DeliberationDecision,
) *SnapshotPendingClarification {
	if clarification := clonePendingClarification(existing); clarification != nil {
		return clarification
	}
	if decision == nil || decision.ClarificationQuestion == nil {
		return nil
	}

	question := strings.TrimSpace(*decision.ClarificationQuestion)
	if question == "" {
		return nil
	}

	return &SnapshotPendingClarification{
		Kind:     "clarification",
		Question: question,
	}
}

// defaultConversationMode 返回当前轮最合理的默认对话模式，避免空值继续向后传播。
func defaultConversationMode(decision *DeliberationDecision) string {
	if decision == nil {
		return "explore"
	}
	if mode := strings.TrimSpace(decision.ConversationMode); mode != "" {
		return mode
	}
	if requestsImmediateWorkflowPromotion(decision) {
		return "execute"
	}
	if decision.ChatFulfillable {
		return "advise"
	}

	return "explore"
}
