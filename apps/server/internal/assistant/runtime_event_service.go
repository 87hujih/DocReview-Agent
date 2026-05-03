package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

type runtimeEventWriter interface {
	Add(ctx context.Context, params postgres.AssistantRuntimeEventCreateParams) (*postgres.AssistantRuntimeEvent, error)
}

// RuntimeRecordInput 描述一次 assistant runtime 事件记录请求。
type RuntimeRecordInput struct {
	SessionID string
	MessageID *string
	Source    string
	EventType string
	Payload   any
}

// RuntimeEventService 统一负责 assistant runtime 事件的校验与写入。
type RuntimeEventService struct {
	repo runtimeEventWriter
}

// NewRuntimeEventService 构造 assistant runtime 事件服务。
func NewRuntimeEventService(repo runtimeEventWriter) *RuntimeEventService {
	return &RuntimeEventService{repo: repo}
}

// Record 校验并写入一条 assistant runtime 事件。
func (s *RuntimeEventService) Record(ctx context.Context, input RuntimeRecordInput) (*postgres.AssistantRuntimeEvent, error) {
	params, err := buildRuntimeEventCreateParams(input)
	if err != nil {
		return nil, err
	}

	return s.repo.Add(ctx, params)
}

// buildRuntimeEventCreateParams 组装 assistant runtime 事件写库参数，统一边界校验与载荷归一化。
func buildRuntimeEventCreateParams(input RuntimeRecordInput) (postgres.AssistantRuntimeEventCreateParams, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return postgres.AssistantRuntimeEventCreateParams{}, fmt.Errorf("session_id 不能为空")
	}

	source := strings.TrimSpace(input.Source)
	if source == "" {
		return postgres.AssistantRuntimeEventCreateParams{}, fmt.Errorf("source 不能为空")
	}

	eventType := strings.TrimSpace(input.EventType)
	if eventType == "" {
		return postgres.AssistantRuntimeEventCreateParams{}, fmt.Errorf("event_type 不能为空")
	}

	payload, err := marshalRuntimePayload(input.Payload)
	if err != nil {
		return postgres.AssistantRuntimeEventCreateParams{}, err
	}

	return postgres.AssistantRuntimeEventCreateParams{
		SessionID: sessionID,
		MessageID: normalizeOptionalText(input.MessageID),
		Source:    source,
		EventType: eventType,
		Payload:   payload,
	}, nil
}

// marshalRuntimePayload 编码 assistant runtime 事件载荷，保持空载荷统一为 {}。
func marshalRuntimePayload(payload any) ([]byte, error) {
	if payload == nil {
		return []byte(`{}`), nil
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if len(encoded) == 0 {
		return []byte(`{}`), nil
	}

	return encoded, nil
}
