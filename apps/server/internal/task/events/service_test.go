package events

import (
	"bytes"
	"context"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"

	"github.com/jackc/pgx/v5"
)

// stubTaskEventWriter 作为任务事件Writer的测试替身，用于在用例里提供可控的依赖行为。
type stubTaskEventWriter struct {
	lastInput postgres.TaskEventCreateParams
}

// Add 实现测试替身需要的 `Add` 接口方法，为用例分支提供可控返回。
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

// AddTx 实现测试替身需要的 `AddTx` 接口方法，为用例分支提供可控返回。
func (s *stubTaskEventWriter) AddTx(_ context.Context, _ pgx.Tx, input postgres.TaskEventCreateParams) (*postgres.TaskEvent, error) {
	s.lastInput = input
	return &postgres.TaskEvent{
		ID:        "event-tx-123",
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

// TestRecordMarshalsPayloadAndTrimsFields 验证`recordMarshalsPayloadAndTrimsFields`在特定边界条件下的行为，防止同类回归。
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

// TestRecordRejectsEmptyTaskID 验证`record`在非法输入或失败路径下的行为，防止同类回归。
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

// TestRecordTxMarshalsPayloadAndTrimsFields 验证`recordTxMarshalsPayloadAndTrimsFields`在特定边界条件下的行为，防止同类回归。
func TestRecordTxMarshalsPayloadAndTrimsFields(t *testing.T) {
	writer := &stubTaskEventWriter{}
	service := New(writer)

	event, err := service.RecordTx(context.Background(), nil, RecordInput{
		TaskID:    " task-456 ",
		StepName:  " approval ",
		Source:    " approval_service ",
		Level:     " WARN ",
		EventType: " approval.rejected ",
		Message:   " 审批已拒绝 ",
		Payload: map[string]any{
			"reason": "内容有误",
		},
	})
	if err != nil {
		t.Fatalf("record tx event: %v", err)
	}

	if event.TaskID != "task-456" {
		t.Fatalf("expected task id %q, got %q", "task-456", event.TaskID)
	}
	if writer.lastInput.StepName != "approval" {
		t.Fatalf("expected step name %q, got %q", "approval", writer.lastInput.StepName)
	}
	if writer.lastInput.Source != "approval_service" {
		t.Fatalf("expected source %q, got %q", "approval_service", writer.lastInput.Source)
	}
	if writer.lastInput.Level != "warn" {
		t.Fatalf("expected level %q, got %q", "warn", writer.lastInput.Level)
	}
	if writer.lastInput.EventType != "approval.rejected" {
		t.Fatalf("expected event type %q, got %q", "approval.rejected", writer.lastInput.EventType)
	}
	if writer.lastInput.Message != "审批已拒绝" {
		t.Fatalf("expected message %q, got %q", "审批已拒绝", writer.lastInput.Message)
	}
	if !bytes.Equal(writer.lastInput.Payload, []byte(`{"reason":"内容有误"}`)) {
		t.Fatalf("expected payload %s, got %s", []byte(`{"reason":"内容有误"}`), writer.lastInput.Payload)
	}
}

// TestRecordTxRejectsEmptyTaskID 验证`recordTx`在非法输入或失败路径下的行为，防止同类回归。
func TestRecordTxRejectsEmptyTaskID(t *testing.T) {
	service := New(&stubTaskEventWriter{})

	_, err := service.RecordTx(context.Background(), nil, RecordInput{
		TaskID:    " ",
		Source:    "approval_service",
		Level:     "info",
		EventType: "approval.approved",
		Message:   "审批已通过",
	})
	if err == nil {
		t.Fatal("expected empty task id to fail in tx mode")
	}
}
