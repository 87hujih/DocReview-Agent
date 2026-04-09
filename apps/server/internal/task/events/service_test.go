package events

import (
	"bytes"
	"context"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

type stubTaskEventWriter struct {
	lastInput postgres.TaskEventCreateParams
}

func (s *stubTaskEventWriter) Add(_ context.Context, input postgres.TaskEventCreateParams) (*postgres.TaskEvent, error) {
	s.lastInput = input
	return &postgres.TaskEvent{
		ID:        "event-123",
		TaskID:    input.TaskID,
		RunID:     input.RunID,
		StepName:  input.StepName,
		Source:    input.Source,
		Level:     input.Level,
		EventType: input.EventType,
		Message:   input.Message,
		Payload:   input.Payload,
	}, nil
}

func TestRecordMarshalsPayloadAndTrimsFields(t *testing.T) {
	writer := &stubTaskEventWriter{}
	service := New(writer)

	runID := "run-123"
	event, err := service.Record(context.Background(), RecordInput{
		TaskID:    " task-123 ",
		RunID:     &runID,
		StepName:  " planner ",
		Source:    " orchestrator ",
		Level:     " info ",
		EventType: " step.started ",
		Message:   " 规划步骤开始 ",
		Payload: map[string]any{
			"attempt": 1,
		},
	})
	if err != nil {
		t.Fatalf("record event: %v", err)
	}

	if event.TaskID != "task-123" {
		t.Fatalf("expected task id %q, got %q", "task-123", event.TaskID)
	}
	if writer.lastInput.StepName != "planner" {
		t.Fatalf("expected step name %q, got %q", "planner", writer.lastInput.StepName)
	}
	if writer.lastInput.Source != "orchestrator" {
		t.Fatalf("expected source %q, got %q", "orchestrator", writer.lastInput.Source)
	}
	if writer.lastInput.Level != "info" {
		t.Fatalf("expected level %q, got %q", "info", writer.lastInput.Level)
	}
	if writer.lastInput.EventType != "step.started" {
		t.Fatalf("expected event type %q, got %q", "step.started", writer.lastInput.EventType)
	}
	if writer.lastInput.Message != "规划步骤开始" {
		t.Fatalf("expected message %q, got %q", "规划步骤开始", writer.lastInput.Message)
	}
	if !bytes.Equal(writer.lastInput.Payload, []byte(`{"attempt":1}`)) {
		t.Fatalf("expected payload %s, got %s", []byte(`{"attempt":1}`), writer.lastInput.Payload)
	}
}

func TestRecordRejectsEmptyTaskID(t *testing.T) {
	service := New(&stubTaskEventWriter{})

	_, err := service.Record(context.Background(), RecordInput{
		TaskID:    " ",
		Source:    "orchestrator",
		Level:     "info",
		EventType: "task.created",
		Message:   "任务已创建",
	})
	if err == nil {
		t.Fatal("expected empty task id to fail")
	}
}
