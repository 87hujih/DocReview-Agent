package approval

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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

	jobCh := make(chan struct{}, 1)
	eventService := taskevents.New(eventRepo)
	service := NewService(pool, approvalRepo, jobRepo, taskRepo, jobCh, eventService)

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

	// 验证 worker 信号已发出
	select {
	case <-jobCh:
	default:
		t.Fatal("expected worker signal to be sent to channel")
	}

	// 从 DB 验证 job 是否正确创建
	pendingJobs, err := jobRepo.ListPending(ctx)
	if err != nil {
		t.Fatalf("list pending jobs: %v", err)
	}
	if len(pendingJobs) != 1 {
		t.Fatalf("expected 1 pending job, got %d", len(pendingJobs))
	}
	createdJob := pendingJobs[0]
	if createdJob.TaskID != task.ID {
		t.Fatalf("expected job task id %q, got %q", task.ID, createdJob.TaskID)
	}
	if createdJob.ApprovalID != approvalRecord.ID {
		t.Fatalf("expected job approval id %q, got %q", approvalRecord.ID, createdJob.ApprovalID)
	}
	if createdJob.Status != "pending" {
		t.Fatalf("expected job status %q, got %q", "pending", createdJob.Status)
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
	if len(events) != 3 {
		t.Fatalf("expected 3 task events, got %d", len(events))
	}
	if events[0].EventType != "task.status_changed" {
		t.Fatalf("expected first event type %q, got %q", "task.status_changed", events[0].EventType)
	}
	if events[1].EventType != "approval.approved" {
		t.Fatalf("expected second event type %q, got %q", "approval.approved", events[1].EventType)
	}
	if events[2].EventType != "job.queued" {
		t.Fatalf("expected third event type %q, got %q", "job.queued", events[2].EventType)
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
	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 1), eventService)
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
	if len(events) != 2 {
		t.Fatalf("expected 2 task events, got %d", len(events))
	}
	if events[0].EventType != "task.status_changed" {
		t.Fatalf("expected first event type %q, got %q", "task.status_changed", events[0].EventType)
	}
	if events[1].EventType != "approval.rejected" {
		t.Fatalf("expected second event type %q, got %q", "approval.rejected", events[1].EventType)
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

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil)
	if _, err := service.Approve(ctx, approvalRecord.ID); err != nil {
		t.Fatalf("first approve: %v", err)
	}

	if _, err := service.Approve(ctx, approvalRecord.ID); err == nil {
		t.Fatal("expected second approve to fail")
	}
}

func TestApproveConcurrentOnlyOneSucceeds(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "审批并发批准测试-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupApprovalResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "并发批准只能成功一次")
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

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil)
	startCh := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			_, err := service.Approve(ctx, approvalRecord.ID)
			errCh <- err
		}()
	}

	close(startCh)
	wg.Wait()
	close(errCh)

	var successCount, alreadyDecidedCount int
	for err := range errCh {
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, ErrApprovalAlreadyDecided):
			alreadyDecidedCount++
		default:
			t.Fatalf("unexpected concurrent approve error: %v", err)
		}
	}
	if successCount != 1 || alreadyDecidedCount != 1 {
		t.Fatalf("expected one success and one already-decided error, got success=%d already_decided=%d", successCount, alreadyDecidedCount)
	}

	pendingJobs, err := jobRepo.ListPending(ctx)
	if err != nil {
		t.Fatalf("list pending jobs: %v", err)
	}
	if len(pendingJobs) != 1 {
		t.Fatalf("expected 1 pending job after concurrent approve, got %d", len(pendingJobs))
	}
}

func TestApproveAndRejectConcurrentOnlyOneSucceeds(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "审批并发冲突测试-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupApprovalResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "批准和拒绝只能成功一个")
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

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil)
	startCh := make(chan struct{})
	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startCh
		_, err := service.Approve(ctx, approvalRecord.ID)
		errCh <- err
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-startCh
		_, err := service.Reject(ctx, approvalRecord.ID, "并发拒绝")
		errCh <- err
	}()

	close(startCh)
	wg.Wait()
	close(errCh)

	var successCount, alreadyDecidedCount int
	for err := range errCh {
		switch {
		case err == nil:
			successCount++
		case errors.Is(err, ErrApprovalAlreadyDecided):
			alreadyDecidedCount++
		default:
			t.Fatalf("unexpected concurrent approve/reject error: %v", err)
		}
	}
	if successCount != 1 || alreadyDecidedCount != 1 {
		t.Fatalf("expected one success and one already-decided error, got success=%d already_decided=%d", successCount, alreadyDecidedCount)
	}

	updatedApproval, err := approvalRepo.GetByID(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get updated approval: %v", err)
	}
	if updatedApproval == nil {
		t.Fatal("expected updated approval, got nil")
	}

	updatedTask, err := taskRepo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get updated task: %v", err)
	}
	if updatedTask == nil {
		t.Fatal("expected updated task, got nil")
	}

	pendingJobs, err := jobRepo.ListPending(ctx)
	if err != nil {
		t.Fatalf("list pending jobs: %v", err)
	}

	switch updatedApproval.Status {
	case "approved":
		if updatedTask.Status != models.StatusExecuting {
			t.Fatalf("expected approved task to be executing, got %q", updatedTask.Status)
		}
		if len(pendingJobs) != 1 {
			t.Fatalf("expected exactly 1 pending job after approve wins, got %d", len(pendingJobs))
		}
	case "rejected":
		if updatedTask.Status != models.StatusFailed {
			t.Fatalf("expected rejected task to be failed, got %q", updatedTask.Status)
		}
		if len(pendingJobs) != 0 {
			t.Fatalf("expected no pending jobs after reject wins, got %d", len(pendingJobs))
		}
	default:
		t.Fatalf("unexpected approval status after concurrent approve/reject: %q", updatedApproval.Status)
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

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, nil, nil)

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

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, nil, nil)

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
	if _, err := pool.Exec(ctx, `
		DELETE FROM execution_jobs
		WHERE task_id IN (SELECT id FROM tasks WHERE resource_id = $1)
		   OR new_version_id IN (SELECT id FROM resource_versions WHERE resource_id = $1)
	`, resourceID); err != nil {
		t.Fatalf("cleanup execution jobs for resource %q: %v", resourceID, err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM approvals
		WHERE task_id IN (SELECT id FROM tasks WHERE resource_id = $1)
	`, resourceID); err != nil {
		t.Fatalf("cleanup approvals for resource %q: %v", resourceID, err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM task_events
		WHERE task_id IN (SELECT id FROM tasks WHERE resource_id = $1)
	`, resourceID); err != nil {
		t.Fatalf("cleanup task events for resource %q: %v", resourceID, err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM task_artifacts
		WHERE task_id IN (SELECT id FROM tasks WHERE resource_id = $1)
	`, resourceID); err != nil {
		t.Fatalf("cleanup task artifacts for resource %q: %v", resourceID, err)
	}
	if _, err := pool.Exec(ctx, `
		DELETE FROM task_steps
		WHERE task_id IN (SELECT id FROM tasks WHERE resource_id = $1)
	`, resourceID); err != nil {
		t.Fatalf("cleanup task steps for resource %q: %v", resourceID, err)
	}
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
