package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"

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
	if _, err := pool.Exec(ctx, `DELETE FROM tasks WHERE resource_id = $1`, resourceID); err != nil {
		t.Fatalf("cleanup tasks for resource %q: %v", resourceID, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM resources WHERE id = $1`, resourceID); err != nil {
		t.Fatalf("cleanup resource %q: %v", resourceID, err)
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
