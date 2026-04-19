package assistant

import (
	"context"
	"encoding/json"

	"agent_project/apps/server/internal/storage/postgres"
)

type runtimeSampleUpsertRepo interface {
	Upsert(ctx context.Context, params postgres.AssistantRuntimeSampleUpsertParams) error
}

// RuntimeLearningProjector 负责把 assistant runtime 事件折叠成可查询的学习样本。
type RuntimeLearningProjector struct {
	repo runtimeSampleUpsertRepo
}

type runtimeDeliberationEventPayload struct {
	RequestKind  string `json:"request_kind"`
	ResponseMode string `json:"response_mode"`
}

type runtimePlannerEventPayload struct {
	ShouldEnterWorkflow bool `json:"should_enter_workflow"`
}

type runtimeVerifierEventPayload struct {
	ApproveWorkflow bool `json:"approve_workflow"`
	DowngradeToChat bool `json:"downgrade_to_chat"`
}

type runtimeClarificationOutcomePayload struct {
	Outcome string `json:"outcome"`
}

// NewRuntimeLearningProjector 构造 assistant runtime 学习样本投影器。
func NewRuntimeLearningProjector(repo runtimeSampleUpsertRepo) *RuntimeLearningProjector {
	return &RuntimeLearningProjector{repo: repo}
}

// Project 根据事件类型把 assistant runtime 事件折叠为学习样本。
func (p *RuntimeLearningProjector) Project(ctx context.Context, event *postgres.AssistantRuntimeEvent) error {
	if p == nil || p.repo == nil || event == nil || event.MessageID == nil {
		return nil
	}

	params, ok, err := buildRuntimeSamplePatch(event)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	return p.repo.Upsert(ctx, params)
}

// buildRuntimeSamplePatch 根据事件生成样本 upsert patch。
func buildRuntimeSamplePatch(event *postgres.AssistantRuntimeEvent) (postgres.AssistantRuntimeSampleUpsertParams, bool, error) {
	params := postgres.AssistantRuntimeSampleUpsertParams{
		SessionID:         event.SessionID,
		DecisionMessageID: *event.MessageID,
		Payload:           append([]byte(nil), event.Payload...),
	}

	switch event.EventType {
	case RuntimeEventTypeDeliberationDecided:
		var payload runtimeDeliberationEventPayload
		if err := decodeRuntimeEventPayload(event.Payload, &payload); err != nil {
			return postgres.AssistantRuntimeSampleUpsertParams{}, false, err
		}
		params.RequestKind = optionalValue(payload.RequestKind)
		params.ResponseMode = optionalValue(payload.ResponseMode)
		return params, true, nil
	case RuntimeEventTypePlannerUsed:
		var payload runtimePlannerEventPayload
		if err := decodeRuntimeEventPayload(event.Payload, &payload); err != nil {
			return postgres.AssistantRuntimeSampleUpsertParams{}, false, err
		}
		params.PlannerUsed = boolPointer(true)
		if payload.ShouldEnterWorkflow {
			params.PromotedToWorkflow = boolPointer(true)
		}
		return params, true, nil
	case RuntimeEventTypeVerifierUsed:
		var payload runtimeVerifierEventPayload
		if err := decodeRuntimeEventPayload(event.Payload, &payload); err != nil {
			return postgres.AssistantRuntimeSampleUpsertParams{}, false, err
		}
		params.VerifierUsed = boolPointer(true)
		if payload.ApproveWorkflow {
			params.PromotedToWorkflow = boolPointer(true)
		}
		if payload.DowngradeToChat {
			params.FinalOutcome = optionalValue(RuntimeFinalOutcomeWorkflowDowngraded)
		}
		return params, true, nil
	case RuntimeEventTypeClarificationPrompted:
		params.ClarificationAsked = boolPointer(true)
		return params, true, nil
	case RuntimeEventTypeClarificationResolvedChat:
		params.ClarificationOutcome = optionalValue("resolved_to_chat")
		params.FinalOutcome = optionalValue(RuntimeFinalOutcomeClarificationToChat)
		return params, true, nil
	case RuntimeEventTypeClarificationResolvedFlow:
		params.ClarificationOutcome = optionalValue("resolved_to_workflow")
		params.PromotedToWorkflow = boolPointer(true)
		params.FinalOutcome = optionalValue(RuntimeFinalOutcomeClarificationToFlow)
		return params, true, nil
	case RuntimeEventTypeTaskSuggestionCreated:
		params.TaskSuggestionCreated = boolPointer(true)
		params.PromotedToWorkflow = boolPointer(true)
		params.FinalOutcome = optionalValue(RuntimeFinalOutcomeTaskSuggestionCreated)
		return params, true, nil
	case RuntimeEventTypeTaskSuggestionConfirmed:
		params.TaskSuggestionConfirmed = boolPointer(true)
		params.FinalOutcome = optionalValue(RuntimeFinalOutcomeTaskSuggestionConfirmed)
		return params, true, nil
	case RuntimeEventTypeTaskSuggestionIgnored:
		params.TaskSuggestionIgnored = boolPointer(true)
		params.FinalOutcome = optionalValue(RuntimeFinalOutcomeTaskSuggestionIgnored)
		return params, true, nil
	case RuntimeEventTypeUserCorrected:
		params.UserCorrected = boolPointer(true)
		params.FinalOutcome = optionalValue(RuntimeFinalOutcomeUserCorrected)
		return params, true, nil
	case RuntimeEventTypeWorkflowPromoted:
		params.PromotedToWorkflow = boolPointer(true)
		return params, true, nil
	case RuntimeEventTypeWorkflowDowngraded:
		params.FinalOutcome = optionalValue(RuntimeFinalOutcomeWorkflowDowngraded)
		return params, true, nil
	default:
		return postgres.AssistantRuntimeSampleUpsertParams{}, false, nil
	}
}

// decodeRuntimeEventPayload 解码 assistant runtime 事件载荷，统一处理空对象。
func decodeRuntimeEventPayload(payload []byte, target any) error {
	if len(payload) == 0 {
		return nil
	}

	return json.Unmarshal(payload, target)
}

func optionalValue(value string) *string {
	return normalizeOptionalText(&value)
}

func boolPointer(value bool) *bool {
	return &value
}
