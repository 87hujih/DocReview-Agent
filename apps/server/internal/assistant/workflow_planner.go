package assistant

import (
	"context"
	"fmt"
	"strings"
)

// workflowPlanner 定义 assistant runtime 在 workflow 候选场景下的独立规划接口。
type workflowPlanner interface {
	Plan(ctx context.Context, state RuntimeState, decision *DeliberationDecision) (*WorkflowPlanDecision, error)
}

// WorkflowPlanDecision 表示 workflow planner 对“是否进入任务流”的结构化结论。
type WorkflowPlanDecision struct {
	ShouldEnterWorkflow   bool     `json:"should_enter_workflow"`
	ChatFulfillable       bool     `json:"chat_fulfillable"`
	NeedsClarification    bool     `json:"needs_clarification"`
	ClarificationQuestion *string  `json:"clarification_question,omitempty"`
	CandidateInstruction  *string  `json:"candidate_instruction,omitempty"`
	CandidatePlanGoal     *string  `json:"candidate_plan_goal,omitempty"`
	MissingMaterials      []string `json:"missing_materials,omitempty"`
	Confidence            float64  `json:"confidence"`
	Reasons               []string `json:"reasons"`
}

// normalizeWorkflowPlanDecision 归一化 workflow planner 输出，避免矛盾字段继续向后传播。
func normalizeWorkflowPlanDecision(value *WorkflowPlanDecision) (*WorkflowPlanDecision, error) {
	if value == nil {
		return nil, fmt.Errorf("workflow planner 结果不能为空")
	}

	normalized := *value
	normalized.ClarificationQuestion = normalizeOptionalText(value.ClarificationQuestion)
	normalized.CandidateInstruction = normalizeOptionalText(value.CandidateInstruction)
	normalized.CandidatePlanGoal = normalizeOptionalText(value.CandidatePlanGoal)
	normalized.MissingMaterials = normalizeWorkflowPlanMissingMaterials(value.MissingMaterials)
	normalized.Reasons = normalizeDecisionReasons(value.Reasons)

	if normalized.Confidence < 0 || normalized.Confidence > 1 {
		return nil, fmt.Errorf("workflow planner confidence 超出范围: %v", normalized.Confidence)
	}

	switch {
	case normalized.ChatFulfillable:
		normalized.ShouldEnterWorkflow = false
		normalized.NeedsClarification = false
		normalized.ClarificationQuestion = nil
		normalized.CandidateInstruction = nil
		normalized.CandidatePlanGoal = nil
		normalized.MissingMaterials = nil
	case normalized.NeedsClarification:
		normalized.ShouldEnterWorkflow = false
		normalized.ChatFulfillable = false
		normalized.CandidateInstruction = nil
		normalized.CandidatePlanGoal = nil
		if normalized.ClarificationQuestion == nil {
			return nil, fmt.Errorf("workflow planner clarification 缺少 question")
		}
	case normalized.ShouldEnterWorkflow:
		normalized.ChatFulfillable = false
		normalized.NeedsClarification = false
		normalized.ClarificationQuestion = nil
		if len(normalized.Reasons) == 0 {
			return nil, fmt.Errorf("workflow planner promotion 缺少 reasons")
		}
		if normalized.CandidateInstruction == nil {
			return nil, fmt.Errorf("workflow planner promotion 缺少 candidate instruction")
		}
	default:
		normalized.CandidateInstruction = nil
		normalized.CandidatePlanGoal = nil
		normalized.MissingMaterials = nil
	}

	return &normalized, nil
}

// normalizeWorkflowPlanMissingMaterials 清理缺失材料列表，避免空白项污染后续判断。
func normalizeWorkflowPlanMissingMaterials(items []string) []string {
	if len(items) == 0 {
		return nil
	}

	normalized := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}

	if len(normalized) == 0 {
		return nil
	}

	return normalized
}
