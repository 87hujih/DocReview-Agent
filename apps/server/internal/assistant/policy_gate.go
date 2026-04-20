package assistant

import "strings"

// PolicyDecision 表示 assistant 在 deliberation 之后的硬边界裁决结果。
type PolicyDecision struct {
	AllowAnswer           bool
	AllowClarification    bool
	AllowTaskSuggestion   bool
	AllowWorkflowPlanning bool
	RequireVerifier       bool
	BlockedReason         string
}

// ApplyPolicy 只保留最小且可审计的产品硬边界，不再决定 workflow promotion / task suggestion。
func ApplyPolicy(state RuntimeState, decision *DeliberationDecision) PolicyDecision {
	if decision == nil {
		return PolicyDecision{
			AllowAnswer:   true,
			BlockedReason: "missing_deliberation_decision",
		}
	}

	policy := PolicyDecision{
		AllowAnswer: true,
	}
	if decision.NeedsClarification || decision.ResponseMode == ResponseModeClarifyFirst {
		policy.AllowClarification = true
	}
	if wantsWorkflowMaterials(decision) && state.ActiveResource == nil {
		policy.BlockedReason = "missing_material"
	}

	return policy
}

// wantsWorkflowMaterials 判断当前 deliberation 是否已经进入“必须具备材料才能继续”的高影响动作边界。
func wantsWorkflowMaterials(decision *DeliberationDecision) bool {
	if decision == nil {
		return false
	}

	return strings.TrimSpace(decision.RequestedNextStep) == "promote_to_workflow" ||
		decision.ProposalReady ||
		decision.WorkflowCommitment ||
		strings.TrimSpace(decision.ConversationMode) == "execute" ||
		decision.ResponseMode == ResponseModePlanThenAnswer ||
		decision.ResponseMode == ResponseModeAnswerThenTaskCard
}
