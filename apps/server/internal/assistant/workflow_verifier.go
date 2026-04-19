package assistant

import (
	"context"
	"fmt"
)

// workflowVerifier 定义 assistant runtime 在 workflow promotion 场景下的复核接口。
type workflowVerifier interface {
	Verify(ctx context.Context, state RuntimeState, decision *DeliberationDecision, plan *WorkflowPlanDecision) (*WorkflowVerificationDecision, error)
}

// normalizeWorkflowVerificationDecision 归一化 verifier 输出，避免互斥结论继续向后传播。
func normalizeWorkflowVerificationDecision(value *WorkflowVerificationDecision) (*WorkflowVerificationDecision, error) {
	if value == nil {
		return nil, fmt.Errorf("workflow verifier 结果不能为空")
	}

	normalized := *value
	normalized.ClarificationQuestion = normalizeOptionalText(value.ClarificationQuestion)
	normalized.RevisedInstruction = normalizeOptionalText(value.RevisedInstruction)
	normalized.Reasons = normalizeDecisionReasons(value.Reasons)

	if normalized.Confidence < 0 || normalized.Confidence > 1 {
		return nil, fmt.Errorf("workflow verifier confidence 超出范围: %v", normalized.Confidence)
	}

	outcomeCount := 0
	if normalized.ApproveWorkflow {
		outcomeCount++
	}
	if normalized.DowngradeToChat {
		outcomeCount++
	}
	if normalized.NeedsClarification {
		outcomeCount++
	}
	if outcomeCount != 1 {
		return nil, fmt.Errorf("workflow verifier 必须且只能返回一种结论")
	}

	switch {
	case normalized.NeedsClarification:
		normalized.ApproveWorkflow = false
		normalized.DowngradeToChat = false
		normalized.RevisedInstruction = nil
		if normalized.ClarificationQuestion == nil {
			return nil, fmt.Errorf("workflow verifier clarification 缺少 question")
		}
	case normalized.DowngradeToChat:
		normalized.ApproveWorkflow = false
		normalized.NeedsClarification = false
		normalized.ClarificationQuestion = nil
		normalized.RevisedInstruction = nil
	case normalized.ApproveWorkflow:
		normalized.DowngradeToChat = false
		normalized.NeedsClarification = false
		normalized.ClarificationQuestion = nil
		if len(normalized.Reasons) == 0 {
			return nil, fmt.Errorf("workflow verifier approve 缺少 reasons")
		}
	}

	return &normalized, nil
}

// passthroughWorkflowVerifier 在未显式注入 verifier 时复用 planner 结果维持现有行为。
type passthroughWorkflowVerifier struct{}

// Verify 根据 planner 已有结论生成兼容性的 verifier 结果，避免主链因缺省依赖失效。
func (passthroughWorkflowVerifier) Verify(
	_ context.Context,
	_ RuntimeState,
	_ *DeliberationDecision,
	plan *WorkflowPlanDecision,
) (*WorkflowVerificationDecision, error) {
	if plan == nil {
		return normalizeWorkflowVerificationDecision(&WorkflowVerificationDecision{
			DowngradeToChat: true,
			Confidence:      0,
			Reasons:         []string{"missing workflow plan"},
		})
	}

	switch {
	case plan.NeedsClarification:
		return normalizeWorkflowVerificationDecision(&WorkflowVerificationDecision{
			NeedsClarification:    true,
			ClarificationQuestion: normalizeOptionalText(plan.ClarificationQuestion),
			Confidence:            plan.Confidence,
			Reasons:               normalizeDecisionReasons(plan.Reasons),
		})
	case plan.ChatFulfillable || !plan.ShouldEnterWorkflow:
		return normalizeWorkflowVerificationDecision(&WorkflowVerificationDecision{
			DowngradeToChat: true,
			Confidence:      plan.Confidence,
			Reasons:         normalizeDecisionReasons(plan.Reasons),
		})
	default:
		return normalizeWorkflowVerificationDecision(&WorkflowVerificationDecision{
			ApproveWorkflow:    true,
			RevisedInstruction: normalizeOptionalText(plan.CandidateInstruction),
			Confidence:         plan.Confidence,
			Reasons:            normalizeDecisionReasons(plan.Reasons),
		})
	}
}
