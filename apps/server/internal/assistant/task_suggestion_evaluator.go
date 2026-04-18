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

type TaskSuggestionEvaluationInput struct {
	CurrentMessage       string
	ActiveResource       *resourceContext
	ModelTaskInstruction *string
}

type TaskSuggestionDecision struct {
	MaterialState         MaterialState
	IntentState           IntentState
	ReadinessState        ReadinessState
	NormalizedInstruction string
	ActiveResource        *resourceContext
}

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

func classifyIntentState(message string) IntentState {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return IntentStateDiscussion
	}

	if isCapabilityQuery(trimmed) {
		return IntentStateCapabilityQuery
	}
	if ClassifyReadIntent(trimmed).ShouldTriggerTaskFlow {
		return IntentStateExecution
	}
	return IntentStateDiscussion
}

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

func isExecutionRequest(message string) bool {
	return matchesExecutionRequest(strings.TrimSpace(message))
}

func containsAny(message string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(message, keyword) {
			return true
		}
	}

	return false
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
