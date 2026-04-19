package assistant

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

// TestRuntimeEventServiceBuildsNormalizedEventPayload 验证`runtimeEventServiceBuildsNormalizedEventPayload`在特定边界条件下的行为，防止同类回归。
func TestRuntimeEventServiceBuildsNormalizedEventPayload(t *testing.T) {
	repo := &fakeRuntimeEventRepo{}
	service := NewRuntimeEventService(repo)
	messageID := " message-1 "

	event, err := service.Record(context.Background(), RuntimeRecordInput{
		SessionID: " session-1 ",
		MessageID: &messageID,
		Source:    " deliberation ",
		EventType: RuntimeEventTypeDeliberationDecided,
		Payload: map[string]any{
			"request_kind":  "readback",
			"response_mode": "answer_with_grounding",
		},
	})
	if err != nil {
		t.Fatalf("record runtime event: %v", err)
	}
	if event == nil {
		t.Fatal("expected runtime event to be returned")
	}
	if repo.lastParams == nil {
		t.Fatal("expected runtime event repo to receive create params")
	}
	if repo.lastParams.SessionID != "session-1" {
		t.Fatalf("expected trimmed session id %q, got %q", "session-1", repo.lastParams.SessionID)
	}
	if repo.lastParams.MessageID == nil || *repo.lastParams.MessageID != "message-1" {
		t.Fatalf("expected trimmed message id %q, got %#v", "message-1", repo.lastParams.MessageID)
	}
	if repo.lastParams.Source != "deliberation" {
		t.Fatalf("expected trimmed source %q, got %q", "deliberation", repo.lastParams.Source)
	}
	if repo.lastParams.EventType != RuntimeEventTypeDeliberationDecided {
		t.Fatalf("expected event type %q, got %q", RuntimeEventTypeDeliberationDecided, repo.lastParams.EventType)
	}
	if !assistantJSONEqual(repo.lastParams.Payload, []byte(`{"request_kind":"readback","response_mode":"answer_with_grounding"}`)) {
		t.Fatalf("expected normalized runtime payload, got %s", string(repo.lastParams.Payload))
	}
}

type fakeRuntimeEventRepo struct {
	lastParams *postgres.AssistantRuntimeEventCreateParams
}

// Add 实现 runtime 事件仓储测试替身，返回可供断言的伪事件。
func (r *fakeRuntimeEventRepo) Add(_ context.Context, params postgres.AssistantRuntimeEventCreateParams) (*postgres.AssistantRuntimeEvent, error) {
	copied := params
	if params.MessageID != nil {
		messageID := *params.MessageID
		copied.MessageID = &messageID
	}
	copied.Payload = append([]byte(nil), params.Payload...)
	r.lastParams = &copied

	return &postgres.AssistantRuntimeEvent{
		ID:        "event-1",
		SessionID: copied.SessionID,
		MessageID: copied.MessageID,
		Source:    copied.Source,
		EventType: copied.EventType,
		Payload:   copied.Payload,
	}, nil
}

func assistantJSONEqual(left []byte, right []byte) bool {
	var leftValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false
	}

	var rightValue any
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false
	}

	return reflect.DeepEqual(leftValue, rightValue)
}
