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

	"agent_project/apps/server/internal/assistant"
	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/models"
	"agent_project/apps/server/internal/testsupport/postgrescleanup"
	"agent_project/apps/server/internal/testsupport/postgrestest"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestApproveCreatesJobAndUpdatesTask 验证`approve`在写入或副作用路径下的行为，防止同类回归。
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
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "请批准修订方案")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	jobCh := make(chan struct{}, 1)
	eventService := taskevents.New(eventRepo)
	service := NewService(pool, approvalRepo, jobRepo, taskRepo, jobCh, eventService, nil, nil)

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

	// 从 DB 验证当前审批是否正确创建了唯一 job
	createdJob, err := jobRepo.GetByApprovalID(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get job by approval id: %v", err)
	}
	if createdJob == nil {
		t.Fatal("expected created job, got nil")
	}
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

// TestRejectUpdatesApprovalAndTask 验证`reject`在写入或副作用路径下的行为，防止同类回归。
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
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "请拒绝当前方案")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	eventService := taskevents.New(eventRepo)
	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 1), eventService, nil, nil)
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
	if updatedApproval.DecidedAt == nil || updatedApproval.DecidedAt.IsZero() {
		t.Fatal("expected decided_at to be set")
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

// TestApproveProjectsExecutingTaskIntoContextSnapshot 验证`approve`在写入或副作用路径下的行为，防止同类回归。
func TestApproveProjectsExecutingTaskIntoContextSnapshot(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	assistantRepo := postgres.NewAssistantRepo(pool)
	snapshotRepo := postgres.NewSessionContextSnapshotRepo(pool)
	projector := assistant.NewSessionContextProjector(snapshotRepo)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "审批快照通过-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	session, task, suggestionMessageID := seedApprovalAssistantTask(t, ctx, assistantRepo, snapshotRepo, taskRepo, resource.ID, "请批准修订方案")
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup session: %v", err)
		}
	})
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	eventService := taskevents.New(eventRepo)
	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 1), eventService, projector, nil)

	if _, err := service.Approve(ctx, approvalRecord.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	snapshot, err := snapshotRepo.GetBySessionID(ctx, session.ID)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if snapshot == nil || snapshot.LatestTaskStatus == nil || *snapshot.LatestTaskStatus != models.StatusExecuting {
		t.Fatalf("expected snapshot latest_task_status %q, got %#v", models.StatusExecuting, snapshot)
	}
	if snapshot.LatestTaskSourceMessageID == nil || *snapshot.LatestTaskSourceMessageID != suggestionMessageID {
		t.Fatalf("expected snapshot source message id %q, got %#v", suggestionMessageID, snapshot.LatestTaskSourceMessageID)
	}
}

// TestRejectProjectsFailedTaskIntoContextSnapshot 验证`reject`在写入或副作用路径下的行为，防止同类回归。
func TestRejectProjectsFailedTaskIntoContextSnapshot(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	assistantRepo := postgres.NewAssistantRepo(pool)
	snapshotRepo := postgres.NewSessionContextSnapshotRepo(pool)
	projector := assistant.NewSessionContextProjector(snapshotRepo)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "审批快照拒绝-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	session, task, suggestionMessageID := seedApprovalAssistantTask(t, ctx, assistantRepo, snapshotRepo, taskRepo, resource.ID, "请拒绝当前方案")
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup session: %v", err)
		}
	})
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	eventService := taskevents.New(eventRepo)
	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 1), eventService, projector, nil)

	if _, err := service.Reject(ctx, approvalRecord.ID, "方案不完善"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	snapshot, err := snapshotRepo.GetBySessionID(ctx, session.ID)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if snapshot == nil || snapshot.LatestTaskStatus == nil || *snapshot.LatestTaskStatus != models.StatusFailed {
		t.Fatalf("expected snapshot latest_task_status %q, got %#v", models.StatusFailed, snapshot)
	}
	if snapshot.LatestTaskSourceMessageID == nil || *snapshot.LatestTaskSourceMessageID != suggestionMessageID {
		t.Fatalf("expected snapshot source message id %q, got %#v", suggestionMessageID, snapshot.LatestTaskSourceMessageID)
	}
}

// TestTaskStatusNotifierApprovalServiceSyncsTerminalStatuses 验证`taskStatusNotifierApprovalService`在写入或副作用路径下的行为，防止同类回归。
func TestTaskStatusNotifierApprovalServiceSyncsTerminalStatuses(t *testing.T) {
	projector := &recordingApprovalProjector{}
	notifier := &recordingApprovalNotifier{}
	service := NewService(nil, nil, nil, nil, nil, nil, projector, notifier)
	task := &postgres.Task{ID: "task-terminal-1"}

	service.syncTaskStatusSideEffects(context.Background(), task, models.StatusFailed)
	if len(projector.statuses) != 1 || projector.statuses[0] != models.StatusFailed {
		t.Fatalf("expected projector to record %q once, got %#v", models.StatusFailed, projector.statuses)
	}
	if len(notifier.statuses) != 1 || notifier.statuses[0] != models.StatusFailed {
		t.Fatalf("expected notifier to record %q once, got %#v", models.StatusFailed, notifier.statuses)
	}

	projector.statuses = nil
	notifier.statuses = nil
	service.syncTaskStatusSideEffects(context.Background(), task, models.StatusExecuting)
	if len(projector.statuses) != 1 || projector.statuses[0] != models.StatusExecuting {
		t.Fatalf("expected projector to record %q once, got %#v", models.StatusExecuting, projector.statuses)
	}
	if len(notifier.statuses) != 0 {
		t.Fatalf("expected notifier to ignore non-terminal status, got %#v", notifier.statuses)
	}
}

// TestApproveCopiesBaseVersionIDToJob 验证`approve`在写入或副作用路径下的行为，防止同类回归。
func TestApproveCopiesBaseVersionIDToJob(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "审批服务 base version 测试-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "批准时复制 base version 到 job")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 1), nil, nil, nil)
	if _, err := service.Approve(ctx, approvalRecord.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	matchedJob, err := jobRepo.GetByApprovalID(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get job by approval id: %v", err)
	}
	if matchedJob == nil {
		t.Fatalf("expected to find pending job for approval %q", approvalRecord.ID)
	}
	if matchedJob.BaseVersionID == nil || *matchedJob.BaseVersionID != version.ID {
		t.Fatalf("expected pending job base_version_id=%q, got %#v", version.ID, matchedJob.BaseVersionID)
	}
}

// TestApproveRejectsLegacyPendingApprovalWithoutBaseVersion 验证`approve`在非法输入或失败路径下的行为，防止同类回归。
func TestApproveRejectsLegacyPendingApprovalWithoutBaseVersion(t *testing.T) {
	pool := newApprovalTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	ctx := approvalTestContext(t)

	resource, err := resourceRepo.Create(ctx, "审批服务 legacy approval 测试-"+approvalUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "legacy approval 缺少 base_version_id 时必须拒绝")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	clearApprovalBaseVersionID(t, ctx, pool, approvalRecord.ID)

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 1), nil, nil, nil)
	if _, err := service.Approve(ctx, approvalRecord.ID); err == nil {
		t.Fatal("expected approve to fail when pending approval is missing base_version_id")
	} else if !strings.Contains(err.Error(), "base_version_id") {
		t.Fatalf("expected error to mention base_version_id, got %v", err)
	}

	jobRecord, err := jobRepo.GetByApprovalID(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get job by approval id: %v", err)
	}
	if jobRecord != nil {
		t.Fatalf("expected no pending jobs for approval %q, got %#v", approvalRecord.ID, jobRecord)
	}
}

// TestApproveAlreadyDecidedReturnsError 验证`approveAlreadyDecided`在返回值分支下的行为，防止同类回归。
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
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "请只批准一次")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil, nil, nil)
	if _, err := service.Approve(ctx, approvalRecord.ID); err != nil {
		t.Fatalf("first approve: %v", err)
	}

	if _, err := service.Approve(ctx, approvalRecord.ID); err == nil {
		t.Fatal("expected second approve to fail")
	}
}

// TestApproveConcurrentOnlyOneSucceeds 验证`approveConcurrentOnlyOne`在成功路径下的行为，防止同类回归。
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
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "并发批准只能成功一次")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil, nil, nil)
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

	createdJob, err := jobRepo.GetByApprovalID(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get job by approval id: %v", err)
	}
	if createdJob == nil {
		t.Fatal("expected job after concurrent approve, got nil")
	}
	if createdJob.TaskID != task.ID {
		t.Fatalf("expected job task id %q, got %q", task.ID, createdJob.TaskID)
	}
	if createdJob.ApprovalID != approvalRecord.ID {
		t.Fatalf("expected job approval id %q, got %q", approvalRecord.ID, createdJob.ApprovalID)
	}
	if createdJob.Status != "pending" {
		t.Fatalf("expected job status %q, got %q", "pending", createdJob.Status)
	}
}

// TestApproveAndRejectConcurrentOnlyOneSucceeds 验证`approveAndRejectConcurrentOnlyOne`在成功路径下的行为，防止同类回归。
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
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "批准和拒绝只能成功一个")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, make(chan struct{}, 2), nil, nil, nil)
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

	createdJob, err := jobRepo.GetByApprovalID(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get job by approval id: %v", err)
	}

	switch updatedApproval.Status {
	case "approved":
		if updatedTask.Status != models.StatusExecuting {
			t.Fatalf("expected approved task to be executing, got %q", updatedTask.Status)
		}
		if createdJob == nil {
			t.Fatal("expected job after approve wins, got nil")
		}
		if createdJob.TaskID != task.ID {
			t.Fatalf("expected job task id %q, got %q", task.ID, createdJob.TaskID)
		}
		if createdJob.ApprovalID != approvalRecord.ID {
			t.Fatalf("expected job approval id %q, got %q", approvalRecord.ID, createdJob.ApprovalID)
		}
		if createdJob.Status != "pending" {
			t.Fatalf("expected job status %q, got %q", "pending", createdJob.Status)
		}
	case "rejected":
		if updatedTask.Status != models.StatusFailed {
			t.Fatalf("expected rejected task to be failed, got %q", updatedTask.Status)
		}
		if createdJob != nil {
			t.Fatalf("expected no job after reject wins, got %#v", createdJob)
		}
	default:
		t.Fatalf("unexpected approval status after concurrent approve/reject: %q", updatedApproval.Status)
	}
}

// TestGetApprovalReturnsRecord 验证`getApproval`在返回值分支下的行为，防止同类回归。
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
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "读取审批详情")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, nil, nil, nil, nil)

	found, err := service.GetApproval(ctx, approvalRecord.ID)
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if found.ID != approvalRecord.ID {
		t.Fatalf("expected approval id %q, got %q", approvalRecord.ID, found.ID)
	}
}

// TestGetJobReturnsRecord 验证`getJob`在返回值分支下的行为，防止同类回归。
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
		if err := postgrescleanup.CleanupResourceTree(ctx, pool, resource.ID); err != nil {
			t.Fatalf("cleanup approval resource %q: %v", resource.ID, err)
		}
	})
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "读取执行作业详情")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	service := NewService(pool, approvalRepo, jobRepo, taskRepo, nil, nil, nil, nil)

	found, err := service.GetJob(ctx, jobRecord.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if found.ID != jobRecord.ID {
		t.Fatalf("expected job id %q, got %q", jobRecord.ID, found.ID)
	}
}

// newApprovalTestPool 创建测试用隔离数据库连接池，统一初始化与清理约束。
func newApprovalTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	ctx := approvalTestContext(t)
	cfg := appconfig.Load()
	return postgrestest.NewIsolatedPool(t, ctx, cfg.DatabaseURL, "approval_service", postgres.NewPool, postgres.RunMigrations)
}

// clearApprovalBaseVersionID 为测试场景清理 `审批Base版本ID`，避免不同用例之间互相污染。
func clearApprovalBaseVersionID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, approvalID string) {
	t.Helper()

	if _, err := pool.Exec(ctx, `
		UPDATE approvals
		SET base_version_id = NULL
		WHERE id = $1
	`, approvalID); err != nil {
		t.Fatalf("clear approval base_version_id: %v", err)
	}
}

// recordingApprovalProjector 作为审批投影器的记录型测试替身，用于断言调用副作用。
type recordingApprovalProjector struct {
	statuses []string
}

// ProjectTaskStatusChanged 实现测试替身需要的 `ProjectTaskStatusChanged` 接口方法，为用例分支提供可控返回。
func (r *recordingApprovalProjector) ProjectTaskStatusChanged(_ context.Context, _ *string, _ string, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}

// recordingApprovalNotifier 作为审批Notifier的记录型测试替身，用于断言调用副作用。
type recordingApprovalNotifier struct {
	statuses []string
}

// Notify 实现测试替身需要的 `Notify` 接口方法，为用例分支提供可控返回。
func (r *recordingApprovalNotifier) Notify(_ context.Context, _ *postgres.Task, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}

// approvalTestContext 构造测试上下文，统一附带当前用例需要的取消和超时能力。
func approvalTestContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// approvalUniqueSuffix 生成测试数据使用的唯一后缀，避免并发或重复运行时发生命名冲突。
func approvalUniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// seedApprovalAssistantTask 为测试场景补齐 `审批助手任务` 所需数据，减少重复造数。
func seedApprovalAssistantTask(
	t *testing.T,
	ctx context.Context,
	assistantRepo *postgres.AssistantRepo,
	snapshotRepo *postgres.SessionContextSnapshotRepo,
	taskRepo *postgres.TaskRepo,
	resourceID string,
	instruction string,
) (*postgres.AssistantSession, *postgres.Task, string) {
	t.Helper()

	session, _, err := assistantRepo.CreateSessionWithMessages(ctx, "approval-snapshot-"+approvalUniqueSuffix(), nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := snapshotRepo.CreateEmpty(ctx, session.ID); err != nil {
		t.Fatalf("create empty snapshot: %v", err)
	}

	payload := fmt.Sprintf(`{"title":"建议创建任务","instruction":"%s","can_create":true,"action_label":"确认创建任务","resource_id":"%s","resource_label":"测试资源","status_message":"资源已明确，可以创建任务。"}`, instruction, resourceID)
	messages, err := assistantRepo.AppendMessages(ctx, session.ID, []postgres.AssistantMessageInput{{
		Role:    "assistant",
		Kind:    "task_suggestion",
		Payload: []byte(payload),
	}})
	if err != nil {
		t.Fatalf("append suggestion message: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one suggestion message, got %d", len(messages))
	}

	task, created, err := taskRepo.CreateFromAssistantSuggestion(ctx, resourceID, instruction, messages[0].ID)
	if err != nil {
		t.Fatalf("create assistant task: %v", err)
	}
	if !created {
		t.Fatal("expected assistant task to be newly created")
	}

	if err := snapshotRepo.UpsertLatestTask(ctx, postgres.UpsertLatestTaskParams{
		SessionID:       session.ID,
		TaskID:          task.ID,
		Status:          task.Status,
		SourceMessageID: &messages[0].ID,
	}); err != nil {
		t.Fatalf("seed latest task snapshot: %v", err)
	}

	return session, task, messages[0].ID
}
