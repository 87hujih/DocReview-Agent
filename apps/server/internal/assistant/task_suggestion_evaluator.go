package assistant

import "strings"

type MaterialState string

const (
	MaterialStateMissing     MaterialState = "missing"
	MaterialStateNormalizing MaterialState = "normalizing"
	MaterialStateReady       MaterialState = "ready"
)

type IntentState string

const (
	IntentStateCapabilityQuery IntentState = "capability_query"
	IntentStateDiscussion      IntentState = "discussion"
	IntentStateExecution       IntentState = "execution"
)

type ReadinessState string

const (
	ReadinessStateNeedMaterial         ReadinessState = "need_material"
	ReadinessStateReadyButNotExecuting ReadinessState = "ready_but_not_executing"
	ReadinessStateReadyForTask         ReadinessState = "ready_for_task"
)

// TaskSuggestionEvaluationInput 归拢任务建议Evaluation所需的输入字段，避免调用方散落传参。
type TaskSuggestionEvaluationInput struct {
	CurrentMessage       string
	ActiveResource       *resourceContext
	ModelTaskInstruction *string
}

// TaskSuggestionDecision 承载任务建议Decision相关状态，明确助手链路中的数据边界。
type TaskSuggestionDecision struct {
	MaterialState         MaterialState
	IntentState           IntentState
	ReadinessState        ReadinessState
	NormalizedInstruction string
	ActiveResource        *resourceContext
}

// EvaluateTaskSuggestion 评估 `任务建议`，给出后续流程需要的判断结果。
func EvaluateTaskSuggestion(input TaskSuggestionEvaluationInput) TaskSuggestionDecision {
	decision := TaskSuggestionDecision{
		MaterialState:  MaterialStateMissing,
		IntentState:    classifyIntentState(input.CurrentMessage),
		ReadinessState: ReadinessStateNeedMaterial,
		ActiveResource: input.ActiveResource,
	}
	if input.ActiveResource != nil {
		decision.MaterialState = MaterialStateReady
	}

	switch decision.MaterialState {
	case MaterialStateReady:
		if decision.IntentState == IntentStateExecution {
			decision.ReadinessState = ReadinessStateReadyForTask
		} else {
			decision.ReadinessState = ReadinessStateReadyButNotExecuting
		}
	case MaterialStateNormalizing, MaterialStateMissing:
		decision.ReadinessState = ReadinessStateNeedMaterial
	}

	if decision.ReadinessState == ReadinessStateReadyForTask {
		if normalized := strings.TrimSpace(stringValue(input.ModelTaskInstruction)); normalized != "" {
			decision.NormalizedInstruction = normalized
		} else {
			decision.NormalizedInstruction = strings.TrimSpace(input.CurrentMessage)
		}
	}

	return decision
}

// classifyIntentState 对 `意图状态` 做分类，统一后续分支选择依据。
func classifyIntentState(message string) IntentState {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return IntentStateDiscussion
	}

	if isCapabilityQuery(trimmed) {
		return IntentStateCapabilityQuery
	}
	if ClassifyReadIntent(trimmed).ShouldEnterTaskFlow {
		return IntentStateExecution
	}
	return IntentStateDiscussion
}

// isCapabilityQuery 判断 `Capability查询` 是否满足当前流程的条件，避免同一谓词在多处分散实现。
func isCapabilityQuery(message string) bool {
	if containsAny(message, []string{
		"你能做什么",
		"什么时候适合创建任务",
		"什么时候需要创建任务",
		"需要什么材料",
		"还缺什么材料",
		"缺什么材料",
		"适不适合创建任务",
	}) {
		return true
	}

	return false
}

// isExecutionRequest 判断 `执行请求` 是否满足当前流程的条件，避免同一谓词在多处分散实现。
func isExecutionRequest(message string) bool {
	return ClassifyReadIntent(message).ShouldEnterTaskFlow
}

// containsAny 判断当前集合里是否包含 `Any`，把匹配规则收口在单点。
func containsAny(message string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(message, keyword) {
			return true
		}
	}

	return false
}

// stringValue 提取 `string` 的稳定值表示，统一空值处理。
func stringValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
