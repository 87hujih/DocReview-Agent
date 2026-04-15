package service

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/workflow"
	"agent_project/apps/server/internal/testsupport/postgrescleanup"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateTaskRecordsCreatedEvent(t *testing.T) {
	pool := newTaskServiceTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	eventService := taskevents.New(eventRepo)
	service := New(taskRepo, resourceRepo, nil, eventService)
	ctx := taskServiceTestContext(t)

	resource, err := resourceRepo.Create(ctx, "任务服务事件测试-"+taskServiceUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupTaskServiceResource(t, pool, resource.ID)
	})

	if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "当前版本内容", "original"); err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := service.CreateTask(ctx, resource.ID, "请记录创建事件")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	events, err := eventRepo.ListByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list task events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 task event, got %d", len(events))
	}
	if events[0].EventType != "task.created" {
		t.Fatalf("expected event type %q, got %q", "task.created", events[0].EventType)
	}
	if events[0].Source != "task_service" {
		t.Fatalf("expected source %q, got %q", "task_service", events[0].Source)
	}
}

func TestCreateTaskMarksTaskFailedWhenRunnerQueueFull(t *testing.T) {
	pool := newTaskServiceTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	eventService := taskevents.New(eventRepo)
	runner := workflow.NewOrchestratorRunner(0, 1, 0, nil, taskRepo)
	if err := runner.Enqueue("occupied-task"); err != nil {
		t.Fatalf("prepare full queue: %v", err)
	}
	service := New(taskRepo, resourceRepo, runner, eventService)
	ctx := taskServiceTestContext(t)

	resource, err := resourceRepo.Create(ctx, "任务服务队列已满测试-"+taskServiceUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupTaskServiceResource(t, pool, resource.ID)
	})

	if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "当前版本内容", "original"); err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := service.CreateTask(ctx, resource.ID, "请模拟队列已满")
	if err != nil {
		t.Fatalf("expected failed task instead of error, got %v", err)
	}
	if task == nil {
		t.Fatal("expected task to be returned")
	}
	assertTaskCreateFailedResult(t, task, "任务队列已满，无法调度执行")
	assertStoredTaskFailed(t, ctx, taskRepo, task.ID, "任务队列已满，无法调度执行")
	assertTaskFailureEvents(t, ctx, eventRepo, task.ID, []string{"task.created", "task.status_changed", "task.failed"})
}

func TestCreateTaskMarksTaskFailedWhenRunnerStopped(t *testing.T) {
	pool := newTaskServiceTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	eventService := taskevents.New(eventRepo)
	runner := workflow.NewOrchestratorRunner(0, 1, 0, nil, taskRepo)
	runner.Stop()
	service := New(taskRepo, resourceRepo, runner, eventService)
	ctx := taskServiceTestContext(t)

	resource, err := resourceRepo.Create(ctx, "任务服务执行器关闭测试-"+taskServiceUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupTaskServiceResource(t, pool, resource.ID)
	})

	if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "当前版本内容", "original"); err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := service.CreateTask(ctx, resource.ID, "请模拟执行器停止")
	if err != nil {
		t.Fatalf("expected failed task instead of error, got %v", err)
	}
	if task == nil {
		t.Fatal("expected task to be returned")
	}
	assertTaskCreateFailedResult(t, task, "工作流执行器已停止，无法调度执行")
	assertStoredTaskFailed(t, ctx, taskRepo, task.ID, "工作流执行器已停止，无法调度执行")
	assertTaskFailureEvents(t, ctx, eventRepo, task.ID, []string{"task.created", "task.status_changed", "task.failed"})
}

func newTaskServiceTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	ctx := taskServiceTestContext(t)
	cfg := appconfig.Load()
	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Skipf("database not available: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	return pool
}

func cleanupTaskServiceResource(t *testing.T, pool *pgxpool.Pool, resourceID string) {
	t.Helper()

	ctx := taskServiceTestContext(t)
	if err := postgrescleanup.CleanupResourceTree(ctx, pool, resourceID); err != nil {
		t.Fatalf("cleanup resource tree for resource %q: %v", resourceID, err)
	}
}

func taskServiceTestContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func taskServiceUniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func assertTaskCreateFailedResult(t *testing.T, task *postgres.Task, expectedError string) {
	t.Helper()

	if task.Status != "failed" {
		t.Fatalf("expected task status %q, got %q", "failed", task.Status)
	}
	if task.ErrorMessage == nil || *task.ErrorMessage != expectedError {
		t.Fatalf("expected task error %q, got %#v", expectedError, task.ErrorMessage)
	}
}

func assertStoredTaskFailed(t *testing.T, ctx context.Context, taskRepo *postgres.TaskRepo, taskID string, expectedError string) {
	t.Helper()

	task, err := taskRepo.GetByID(ctx, taskID)
	if err != nil {
		t.Fatalf("get task by id: %v", err)
	}
	if task == nil {
		t.Fatalf("expected stored task %q to exist", taskID)
	}
	assertTaskCreateFailedResult(t, task, expectedError)
}

func assertTaskFailureEvents(
	t *testing.T,
	ctx context.Context,
	eventRepo *postgres.TaskEventRepo,
	taskID string,
	expectedTypes []string,
) {
	t.Helper()

	events, err := eventRepo.ListByTask(ctx, taskID)
	if err != nil {
		t.Fatalf("list task events: %v", err)
	}
	if len(events) != len(expectedTypes) {
		t.Fatalf("expected %d task events, got %d", len(expectedTypes), len(events))
	}

	actualTypes := make([]string, 0, len(events))
	for _, event := range events {
		actualTypes = append(actualTypes, event.EventType)
	}
	if !slices.Equal(actualTypes, expectedTypes) {
		t.Fatalf("expected event types %v, got %v", expectedTypes, actualTypes)
	}
}
