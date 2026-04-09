package postgres

import (
	"testing"
	"time"
)

func TestTaskEventRepoAddAndList(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	eventRepo := NewTaskEventRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "事件仓储测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "请记录任务事件")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	runID := "run-123"
	firstPayload := []byte(`{"phase":"created"}`)
	firstEvent, err := eventRepo.Add(ctx, TaskEventCreateParams{
		TaskID:    task.ID,
		RunID:     &runID,
		Source:    "task_service",
		Level:     "info",
		EventType: "task.created",
		Message:   "任务已创建",
		Payload:   firstPayload,
	})
	if err != nil {
		t.Fatalf("add first event: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	secondPayload := []byte(`{"step":"planner"}`)
	secondEvent, err := eventRepo.Add(ctx, TaskEventCreateParams{
		TaskID:    task.ID,
		RunID:     &runID,
		StepName:  "planner",
		Source:    "orchestrator",
		Level:     "info",
		EventType: "step.started",
		Message:   "规划步骤开始",
		Payload:   secondPayload,
	})
	if err != nil {
		t.Fatalf("add second event: %v", err)
	}

	eventsByTask, err := eventRepo.ListByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list events by task: %v", err)
	}
	if len(eventsByTask) != 2 {
		t.Fatalf("expected 2 events by task, got %d", len(eventsByTask))
	}
	if eventsByTask[0].ID != firstEvent.ID {
		t.Fatalf("expected first event id %q, got %q", firstEvent.ID, eventsByTask[0].ID)
	}
	if eventsByTask[1].ID != secondEvent.ID {
		t.Fatalf("expected second event id %q, got %q", secondEvent.ID, eventsByTask[1].ID)
	}
	if eventsByTask[0].RunID == nil || *eventsByTask[0].RunID != runID {
		t.Fatalf("expected run id %q, got %#v", runID, eventsByTask[0].RunID)
	}
	if !jsonEqual(eventsByTask[0].Payload, firstPayload) {
		t.Fatalf("expected first payload %s, got %s", firstPayload, eventsByTask[0].Payload)
	}
	if !jsonEqual(eventsByTask[1].Payload, secondPayload) {
		t.Fatalf("expected second payload %s, got %s", secondPayload, eventsByTask[1].Payload)
	}

	eventsByRunID, err := eventRepo.ListByRunID(ctx, runID)
	if err != nil {
		t.Fatalf("list events by run id: %v", err)
	}
	if len(eventsByRunID) != 2 {
		t.Fatalf("expected 2 events by run id, got %d", len(eventsByRunID))
	}
}
