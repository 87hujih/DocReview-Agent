package postgres

import (
	"testing"
)

// TestAssistantRuntimeEventRepoAddAndListBySession 验证`assistantRuntimeEventRepoAddAndListBySession`在特定边界条件下的行为，防止同类回归。
func TestAssistantRuntimeEventRepoAddAndListBySession(t *testing.T) {
	pool := newTestPool(t)
	assistantRepo := NewAssistantRepo(pool)
	eventRepo := NewAssistantRuntimeEventRepo(pool)
	ctx := testContext(t)

	session, messages, err := assistantRepo.CreateSessionWithMessages(ctx, "assistant runtime 事件会话", []AssistantMessageInput{
		mustAssistantMessageInput(t, "assistant", "text", `{"content":"这是第一条助手回复"}`),
	})
	if err != nil {
		t.Fatalf("create assistant session: %v", err)
	}
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup assistant session: %v", err)
		}
	})
	if len(messages) != 1 {
		t.Fatalf("expected exactly 1 seed message, got %d", len(messages))
	}

	firstEvent, err := eventRepo.Add(ctx, AssistantRuntimeEventCreateParams{
		SessionID: session.ID,
		MessageID: &messages[0].ID,
		Source:    "deliberation",
		EventType: "deliberation.decided",
		Payload:   []byte(`{"request_kind":"readback","response_mode":"answer_with_grounding"}`),
	})
	if err != nil {
		t.Fatalf("add first runtime event: %v", err)
	}

	secondEvent, err := eventRepo.Add(ctx, AssistantRuntimeEventCreateParams{
		SessionID: session.ID,
		Source:    "policy",
		EventType: "policy.applied",
		Payload:   []byte(`{"allow_answer":true}`),
	})
	if err != nil {
		t.Fatalf("add second runtime event: %v", err)
	}

	events, err := eventRepo.ListBySession(ctx, session.ID)
	if err != nil {
		t.Fatalf("list runtime events by session: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 runtime events, got %d", len(events))
	}
	if events[0].ID != firstEvent.ID || events[1].ID != secondEvent.ID {
		t.Fatalf("expected runtime events to keep append order, got %#v", events)
	}
	if events[0].MessageID == nil || *events[0].MessageID != messages[0].ID {
		t.Fatalf("expected first runtime event message id %q, got %#v", messages[0].ID, events[0].MessageID)
	}
	if events[1].MessageID != nil {
		t.Fatalf("expected second runtime event message id to stay nil, got %#v", events[1].MessageID)
	}
	if !jsonEqual(events[0].Payload, []byte(`{"request_kind":"readback","response_mode":"answer_with_grounding"}`)) {
		t.Fatalf("expected first runtime event payload to persist, got %s", string(events[0].Payload))
	}
	if !jsonEqual(events[1].Payload, []byte(`{"allow_answer":true}`)) {
		t.Fatalf("expected second runtime event payload to persist, got %s", string(events[1].Payload))
	}
}
