package assistant

// PolicyDecision 表示 assistant 在 deliberation 之后的硬边界裁决结果。
type PolicyDecision struct {
	AllowAnswer           bool
	AllowClarification    bool
	AllowTaskSuggestion   bool
	AllowWorkflowPlanning bool
	BlockedReason         string
}

// ApplyPolicy 对 deliberation 决策应用最小且可审计的产品硬边界。
func ApplyPolicy(state RuntimeState, decision *DeliberationDecision) PolicyDecision {
	if decision == nil {
		return PolicyDecision{
			AllowAnswer:   true,
			BlockedReason: "missing_deliberation_decision",
		}
	}

	switch decision.ResponseMode {
	case ResponseModeClarifyFirst:
		return PolicyDecision{AllowClarification: true}
	case ResponseModePlanThenAnswer, ResponseModeAnswerThenTaskCard:
		if state.ActiveResource == nil {
			return PolicyDecision{
				AllowAnswer:   true,
				BlockedReason: "missing_material",
			}
		}
		if !decision.WorkflowCommitment {
			return PolicyDecision{
				AllowClarification: true,
				BlockedReason:      "workflow_commitment_required",
			}
		}
		return PolicyDecision{
			AllowAnswer:           true,
			AllowTaskSuggestion:   true,
			AllowWorkflowPlanning: true,
		}
	default:
		return PolicyDecision{AllowAnswer: true}
	}
}
