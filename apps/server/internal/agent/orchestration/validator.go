package orchestration

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ValidatedAction struct {
	NextNode     NodeType
	ToolName     string
	ToolVersion  string
	ToolInput    json.RawMessage
	WaitForInput bool
}

type ActionValidator struct {
	toolVersions map[DecisionAction]string
}

// NewActionValidator 校验依赖并创建对应实例。
func NewActionValidator() *ActionValidator {
	return &ActionValidator{toolVersions: map[DecisionAction]string{
		ActionRetrieveEvidence: "2.0.0",
		ActionReadNodes:        "1.0.0",
		ActionRequestApproval:  "1.0.0",
	}}
}

// Validate 校验输入及领域约束。
func (validator *ActionValidator) Validate(state State, decision Decision) (ValidatedAction, error) {
	if validator == nil {
		return ValidatedAction{}, fmt.Errorf("动作校验器不能为空")
	}
	if state.Goal == nil || strings.TrimSpace(state.Goal.Objective) == "" {
		return ValidatedAction{}, fmt.Errorf("understood 目标不能为空之前 deciding 一个动作")
	}
	if err := validateDecisionValue(decision); err != nil {
		return ValidatedAction{}, err
	}
	toolInput := append(json.RawMessage(nil), decision.ToolInput...)
	// 根据当前状态或类型选择对应的处理分支。
	switch decision.Action {
	case ActionRetrieveEvidence:
		return validator.toolAction(decision, "retrieval.search", NodeRetrieveEvidence, toolInput)
	case ActionReadNodes:
		return validator.toolAction(decision, "document.read_nodes", NodeReadDocumentNodes, toolInput)
	case ActionAnalyze:
		if len(state.Observations) == 0 {
			return ValidatedAction{}, fmt.Errorf("证据或文档观察结果不能为空之前分析")
		}
		return semanticAction(decision, NodeAnalyzeEvidence)
	case ActionGeneratePatch:
		if len(state.Findings) == 0 {
			return ValidatedAction{}, fmt.Errorf("类型化的发现项不能为空之前补丁生成")
		}
		return semanticAction(decision, NodeGeneratePatch)
	case ActionRequestApproval:
		if state.Patch == nil || !state.Patch.Generated || !state.Patch.Valid {
			return ValidatedAction{}, fmt.Errorf("一个以确定性方式已校验的补丁不能为空之前审批")
		}
		return validator.toolAction(decision, "workflow.request_approval", NodeRequestApproval, toolInput)
	case ActionRequestUserInput:
		if decision.ToolName != "" || !isEmptyObject(toolInput) {
			return ValidatedAction{}, fmt.Errorf("request_user_input 不能调用工具")
		}
		return ValidatedAction{WaitForInput: true, ToolInput: json.RawMessage(`{}`)}, nil
	case ActionFinish:
		return semanticAction(decision, NodeRenderOutcome)
	default:
		return ValidatedAction{}, fmt.Errorf("不支持的决策动作 %q", decision.Action)
	}
}

// toolAction 执行该函数负责的核心处理逻辑。
func (validator *ActionValidator) toolAction(decision Decision, expectedTool string, node NodeType, input json.RawMessage) (ValidatedAction, error) {
	if decision.ToolName != expectedTool {
		return ValidatedAction{}, fmt.Errorf("动作 %s 只能使用工具 %s", decision.Action, expectedTool)
	}
	return ValidatedAction{
		NextNode: node, ToolName: expectedTool, ToolVersion: validator.toolVersions[decision.Action], ToolInput: input,
	}, nil
}

// semanticAction 执行该函数负责的核心处理逻辑。
func semanticAction(decision Decision, node NodeType) (ValidatedAction, error) {
	if decision.ToolName != "" || !isEmptyObject(decision.ToolInput) {
		return ValidatedAction{}, fmt.Errorf("语义动作 %s 不能调用工具", decision.Action)
	}
	return ValidatedAction{NextNode: node, ToolInput: json.RawMessage(`{}`)}, nil
}

// validateDecisionValue 校验输入及领域约束。
func validateDecisionValue(decision Decision) error {
	if !decision.Action.Valid() || strings.TrimSpace(decision.Reason) == "" || strings.TrimSpace(decision.ExpectedObservation) == "" ||
		decision.Confidence < 0 || decision.Confidence > 1 {
		return fmt.Errorf("决策动作、原因、预期的观察结果、或置信度无效")
	}
	var object map[string]any
	if err := json.Unmarshal(decision.ToolInput, &object); err != nil || object == nil {
		return fmt.Errorf("决策 tool_input 必须是 JSON 对象")
	}
	return nil
}

// isEmptyObject 执行该函数负责的核心处理逻辑。
func isEmptyObject(raw json.RawMessage) bool {
	var object map[string]any
	return json.Unmarshal(raw, &object) == nil && len(object) == 0
}
