package approval

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
	"agent_project/apps/server/internal/task/models"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestApproveCreatesJobAndUpdatesTask(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "审批服务测试-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupApprovalResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "请批准修订方案")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	jobCh := make(chan postgres.ExecutionJob, 1)
	eventService := taskevents.New(eventRepo)
	service := NewService(approvalRepo, jobRepo, taskRepo, jobCh, eventService)

	updatedApproval, err := service.Approve(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if updatedApproval.Status != "approved" {
		t.Fatalf("expected approval status %q, got %q", "approved", updatedApproval.Status)
	}
	if updatedApproval.DecidedAt == nil || updatedApproval.DecidedAt.IsZero() {
		t.Fatal("expected decided_at to be set")
	}

	select {
	case job := <-jobCh:
		if job.TaskID != task.ID {
			t.Fatalf("expected job task id %q, got %q", task.ID, job.TaskID)
		}
		if job.ApprovalID != approvalRecord.ID {
			t.Fatalf("expected job approval id %q, got %q", approvalRecord.ID, job.ApprovalID)
		}
		if job.Status != "pending" {
			t.Fatalf("expected job status %q, got %q", "pending", job.Status)
		}
	default:
		t.Fatal("expected created job to be sent to channel")
	}

	updatedTask, err := taskRepo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get updated task: %v", err)
	}
	if updatedTask == nil {
		t.Fatal("expected updated task, got nil")
	}
	if updatedTask.Status != models.StatusExecuting {
		t.Fatalf("expected task status %q, got %q", models.StatusExecuting, updatedTask.Status)
	}

	events, err := eventRepo.ListByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list task events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 task events, got %d", len(events))
	}
	if events[0].EventType != "approval.approved" {
		t.Fatalf("expected first event type %q, got %q", "approval.approved", events[0].EventType)
	}
	if events[1].EventType != "job.queued" {
		t.Fatalf("expected second event type %q, got %q", "job.queued", events[1].EventType)
	}
}

func TestRejectUpdatesApprovalAndTask(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "审批拒绝测试-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupApprovalResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "请拒绝当前方案")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	eventService := taskevents.New(eventRepo)
	service := NewService(approvalRepo, jobRepo, taskRepo, make(chan postgres.ExecutionJob, 1), eventService)
	reason := "方案不完善"

	updatedApproval, err := service.Reject(ctx, approvalRecord.ID, reason)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if updatedApproval.Status != "rejected" {
		t.Fatalf("expected approval status %q, got %q", "rejected", updatedApproval.Status)
	}
	if updatedApproval.RejectReason == nil || *updatedApproval.RejectReason != reason {
		t.Fatalf("expected reject reason %q, got %#v", reason, updatedApproval.RejectReason)
	}

	updatedTask, err := taskRepo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get updated task: %v", err)
	}
	if updatedTask == nil {
		t.Fatal("expected updated task, got nil")
	}
	if updatedTask.Status != models.StatusFailed {
		t.Fatalf("expected task status %q, got %q", models.StatusFailed, updatedTask.Status)
	}
	if updatedTask.ErrorMessage == nil || *updatedTask.ErrorMessage != reason {
		t.Fatalf("expected task error message %q, got %#v", reason, updatedTask.ErrorMessage)
	}

	events, err := eventRepo.ListByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list task events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 task event, got %d", len(events))
	}
	if events[0].EventType != "approval.rejected" {
		t.Fatalf("expected event type %q, got %q", "approval.rejected", events[0].EventType)
	}
}

func TestApproveAlreadyDecidedReturnsError(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "审批重复测试-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupApprovalResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "请只批准一次")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	service := NewService(approvalRepo, jobRepo, taskRepo, make(chan postgres.ExecutionJob, 2), nil)
	if _, err := service.Approve(ctx, approvalRecord.ID); err != nil {
		t.Fatalf("first approve: %v", err)
	}

	if _, err := service.Approve(ctx, approvalRecord.ID); err == nil {
		t.Fatal("expected second approve to fail")
	}
}

func TestGetApprovalReturnsRecord(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "审批详情服务测试-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupApprovalResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "读取审批详情")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	service := NewService(approvalRepo, jobRepo, taskRepo, nil, nil)

	found, err := service.GetApproval(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if found.ID != approvalRecord.ID {
		t.Fatalf("expected approval id %q, got %q", approvalRecord.ID, found.ID)
	}
}

func TestGetJobReturnsRecord(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "执行作业详情服务测试-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupApprovalResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "读取执行作业详情")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	service := NewService(approvalRepo, jobRepo, taskRepo, nil, nil)

	found, err := service.GetJob(ctx, jobRecord.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if found.ID != jobRecord.ID {
		t.Fatalf("expected job id %q, got %q", jobRecord.ID, found.ID)
	}
}

func newApprovalTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	ctx := approvalTestContext(t)
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

func cleanupApprovalResource(t *testing.T, pool *pgxpool.Pool, resourceID string) {
	t.Helper()

	ctx := approvalTestContext(t)
	if _, err := pool.Exec(ctx, `DELETE FROM tasks WHERE resource_id = $1`, resourceID); err != nil {
		t.Fatalf("cleanup tasks for resource %q: %v", resourceID, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM resources WHERE id = $1`, resourceID); err != nil {
		t.Fatalf("cleanup resource %q: %v", resourceID, err)
	}
}

func approvalTestContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func approvalUniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
