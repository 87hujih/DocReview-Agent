package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"

	"agent_project/apps/server/internal/task/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// TestCommitPreparedExecutionWritesVersionChunksJobTaskAndEventsAtomically 验证`commitPreparedExecution`在写入或副作用路径下的行为，防止同类回归。
func TestCommitPreparedExecutionWritesVersionChunksJobTaskAndEventsAtomically(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	eventRepo := NewTaskEventRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "原子执行提交测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })

	baseVersion, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "# 文档标题\n\n## 第一章\n原始正文\n", "original")
	if err != nil {
		t.Fatalf("create base version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "提交准备好的执行结果")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, baseVersion.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE approvals
		SET status = 'approved', decided_at = now(), base_version_id = $2
		WHERE id = $1
	`, approvalRecord.ID, baseVersion.ID); err != nil {
		t.Fatalf("update approval base version: %v", err)
	}

	job, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, baseVersion.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE execution_jobs
		SET status = 'running', started_at = now(), base_version_id = $2
		WHERE id = $1
	`, job.ID, baseVersion.ID); err != nil {
		t.Fatalf("update job to running: %v", err)
	}

	result, err := jobRepo.CommitPreparedExecution(ctx, CommitPreparedExecutionParams{
		JobID:         job.ID,
		BaseVersionID: baseVersion.ID,
		NewContent:    "# 文档标题\n\n## 第一章\n修订后的正文\n",
		Chunks: []ResourceChunkInput{
			{
				ChunkIndex:   0,
				SectionTitle: "第一章",
				Content:      "修订后的正文",
				Embedding:    testVector(1),
			},
		},
	}, func(ctx context.Context, tx pgx.Tx, task *Task, job *ExecutionJob, fromTaskStatus string) error {
		return recordExecutionCommitEvents(ctx, tx, eventRepo, task, job, fromTaskStatus)
	})
	if err != nil {
		t.Fatalf("commit prepared execution: %v", err)
	}

	if result.JobStatus != "done" {
		t.Fatalf("expected job status done, got %#v", result)
	}
	if result.NewVersionID == nil || strings.TrimSpace(*result.NewVersionID) == "" {
		t.Fatalf("expected new version id, got %#v", result.NewVersionID)
	}

	storedJob, err := jobRepo.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if storedJob == nil || storedJob.Status != "done" {
		t.Fatalf("expected stored job status done, got %#v", storedJob)
	}

	storedTask, err := taskRepo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if storedTask == nil || storedTask.Status != models.StatusCompleted {
		t.Fatalf("expected stored task status %q, got %#v", models.StatusCompleted, storedTask)
	}

	currentVersion, err := resourceRepo.GetCurrentVersion(ctx, resource.ID)
	if err != nil {
		t.Fatalf("get current version: %v", err)
	}
	if currentVersion == nil || currentVersion.ID != *result.NewVersionID {
		t.Fatalf("expected current version %q, got %#v", *result.NewVersionID, currentVersion)
	}

	chunkCount, err := resourceRepo.CountChunksByVersion(ctx, *result.NewVersionID)
	if err != nil {
		t.Fatalf("count chunks by version: %v", err)
	}
	if chunkCount != 1 {
		t.Fatalf("expected 1 chunk for committed version, got %d", chunkCount)
	}

	events, err := eventRepo.ListByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list task events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 task events, got %d", len(events))
	}
}

// TestCommitPreparedExecutionFailsClosedWhenCurrentVersionDrifts 验证`commitPreparedExecutionFailsClosedWhenCurrentVersionDrifts`在特定边界条件下的行为，防止同类回归。
func TestCommitPreparedExecutionFailsClosedWhenCurrentVersionDrifts(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	eventRepo := NewTaskEventRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "执行提交漂移测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })

	baseVersion, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "# 文档标题\n\n## 第一章\n原始正文\n", "original")
	if err != nil {
		t.Fatalf("create base version: %v", err)
	}
	if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 2, "# 文档标题\n\n## 第一章\n当前正文\n", "agent_edit"); err != nil {
		t.Fatalf("create current version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "漂移时必须 fail closed")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, baseVersion.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE approvals
		SET status = 'approved', decided_at = now(), base_version_id = $2
		WHERE id = $1
	`, approvalRecord.ID, baseVersion.ID); err != nil {
		t.Fatalf("update approval base version: %v", err)
	}

	job, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, baseVersion.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE execution_jobs
		SET status = 'running', started_at = now(), base_version_id = $2
		WHERE id = $1
	`, job.ID, baseVersion.ID); err != nil {
		t.Fatalf("update job to running: %v", err)
	}

	result, err := jobRepo.CommitPreparedExecution(ctx, CommitPreparedExecutionParams{
		JobID:         job.ID,
		BaseVersionID: baseVersion.ID,
		NewContent:    "# 文档标题\n\n## 第一章\n修订后的正文\n",
		Chunks: []ResourceChunkInput{
			{
				ChunkIndex:   0,
				SectionTitle: "第一章",
				Content:      "修订后的正文",
				Embedding:    testVector(2),
			},
		},
	}, func(ctx context.Context, tx pgx.Tx, task *Task, job *ExecutionJob, fromTaskStatus string) error {
		return recordExecutionCommitEvents(ctx, tx, eventRepo, task, job, fromTaskStatus)
	})
	if err != nil {
		t.Fatalf("commit prepared execution drift result: %v", err)
	}

	if result.JobStatus != "failed" {
		t.Fatalf("expected drift to produce failed result, got %#v", result)
	}
	if result.NewVersionID != nil {
		t.Fatalf("expected no new version id on drift, got %#v", result.NewVersionID)
	}
	if result.FailureMessage == nil || !strings.Contains(*result.FailureMessage, "base version") {
		t.Fatalf("expected drift failure message, got %#v", result.FailureMessage)
	}

	currentVersion, err := resourceRepo.GetCurrentVersion(ctx, resource.ID)
	if err != nil {
		t.Fatalf("get current version: %v", err)
	}
	if currentVersion == nil || currentVersion.VersionNumber != 2 {
		t.Fatalf("expected current version number 2 after drift failure, got %#v", currentVersion)
	}

	storedTask, err := taskRepo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if storedTask == nil || storedTask.Status != models.StatusFailed {
		t.Fatalf("expected task status %q, got %#v", models.StatusFailed, storedTask)
	}
}

// TestCommitPreparedExecutionRollsBackOnChunkInsertFailure 验证`commitPreparedExecution`在回滚路径下的行为，防止同类回归。
func TestCommitPreparedExecutionRollsBackOnChunkInsertFailure(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "执行提交回滚测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })

	baseVersion, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "# 文档标题\n\n## 第一章\n原始正文\n", "original")
	if err != nil {
		t.Fatalf("create base version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "chunk 写入失败时必须回滚")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, baseVersion.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE approvals
		SET status = 'approved', decided_at = now(), base_version_id = $2
		WHERE id = $1
	`, approvalRecord.ID, baseVersion.ID); err != nil {
		t.Fatalf("update approval base version: %v", err)
	}

	job, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, baseVersion.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE execution_jobs
		SET status = 'running', started_at = now(), base_version_id = $2
		WHERE id = $1
	`, job.ID, baseVersion.ID); err != nil {
		t.Fatalf("update job to running: %v", err)
	}

	_, err = jobRepo.CommitPreparedExecution(ctx, CommitPreparedExecutionParams{
		JobID:         job.ID,
		BaseVersionID: baseVersion.ID,
		NewContent:    "# 文档标题\n\n## 第一章\n修订后的正文\n",
		Chunks: []ResourceChunkInput{
			{
				ChunkIndex:   0,
				SectionTitle: "第一章",
				Content:      "修订后的正文",
				Embedding:    pgvector.NewVector([]float32{1, 2}),
			},
		},
	}, nil)
	if err == nil {
		t.Fatal("expected commit prepared execution to fail when chunk insert fails")
	}

	currentVersion, err := resourceRepo.GetCurrentVersion(ctx, resource.ID)
	if err != nil {
		t.Fatalf("get current version: %v", err)
	}
	if currentVersion == nil || currentVersion.VersionNumber != 1 {
		t.Fatalf("expected current version number 1 after rollback, got %#v", currentVersion)
	}

	storedJob, err := jobRepo.GetByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if storedJob == nil || storedJob.Status != "running" {
		t.Fatalf("expected job to remain running after rollback, got %#v", storedJob)
	}

	storedTask, err := taskRepo.GetByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if storedTask == nil || storedTask.Status != models.StatusExecuting {
		t.Fatalf("expected task status %q after rollback, got %#v", models.StatusExecuting, storedTask)
	}
}

// TestCommitPreparedExecutionAllowsOnlyOneSuccessForSameResourceBaseVersion 验证`commitPreparedExecution`在合法输入或兼容路径下的行为，防止同类回归。
func TestCommitPreparedExecutionAllowsOnlyOneSuccessForSameResourceBaseVersion(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "执行提交并发测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() { cleanupResource(t, pool, resource.ID) })

	baseVersion, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "# 文档标题\n\n## 第一章\n原始正文\n", "original")
	if err != nil {
		t.Fatalf("create base version: %v", err)
	}

	firstJob := seedRunningExecutionJob(t, pool, resourceRepo, taskRepo, approvalRepo, jobRepo, ctx, resource.ID, baseVersion.ID, "并发提交任务 1")
	secondJob := seedRunningExecutionJob(t, pool, resourceRepo, taskRepo, approvalRepo, jobRepo, ctx, resource.ID, baseVersion.ID, "并发提交任务 2")

	startCh := make(chan struct{})
	resultCh := make(chan *CommitPreparedExecutionResult, 2)
	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	runCommit := func(job *ExecutionJob, seed float32) {
		defer wg.Done()
		<-startCh
		result, err := jobRepo.CommitPreparedExecution(ctx, CommitPreparedExecutionParams{
			JobID:         job.ID,
			BaseVersionID: baseVersion.ID,
			NewContent:    "# 文档标题\n\n## 第一章\n修订后的正文\n",
			Chunks: []ResourceChunkInput{
				{
					ChunkIndex:   0,
					SectionTitle: "第一章",
					Content:      "修订后的正文",
					Embedding:    testVector(seed),
				},
			},
		}, nil)
		errCh <- err
		resultCh <- result
	}

	wg.Add(2)
	go runCommit(firstJob, 3)
	go runCommit(secondJob, 4)
	close(startCh)
	wg.Wait()
	close(errCh)
	close(resultCh)

	var successCount, failureCount int
	for err := range errCh {
		if err != nil {
			t.Fatalf("unexpected concurrent commit error: %v", err)
		}
	}
	for result := range resultCh {
		switch result.JobStatus {
		case "done":
			successCount++
		case "failed":
			failureCount++
		default:
			t.Fatalf("unexpected concurrent commit result %#v", result)
		}
	}

	if successCount != 1 || failureCount != 1 {
		t.Fatalf("expected one success and one failure, got success=%d failure=%d", successCount, failureCount)
	}
}

// recordExecutionCommitEvents 为测试用例处理 `记录执行Commit事件`，减少样板搭建和断言前准备步骤。
func recordExecutionCommitEvents(
	ctx context.Context,
	tx pgx.Tx,
	eventRepo *TaskEventRepo,
	task *Task,
	job *ExecutionJob,
	fromTaskStatus string,
) error {
	eventType := "job.completed"
	message := "执行作业已完成"
	level := "info"
	payload := []byte(`{"approval_id":"` + job.ApprovalID + `","status":"` + job.Status + `"}`)
	if job.Status == "failed" {
		eventType = "job.failed"
		message = "执行作业执行失败"
		level = "error"
		payload = []byte(`{"approval_id":"` + job.ApprovalID + `","error_message":"` + derefString(job.ErrorMessage) + `"}`)
	}

	if _, err := eventRepo.AddTx(ctx, tx, TaskEventCreateParams{
		TaskID:    task.ID,
		RunID:     &job.ID,
		Source:    "job_worker",
		Level:     level,
		EventType: eventType,
		Message:   message,
		Payload:   payload,
	}); err != nil {
		return err
	}

	return addTaskStatusChangedEvent(ctx, tx, eventRepo, task, job, fromTaskStatus)
}

// addTaskStatusChangedEvent 为测试用例处理 `add任务状态Changed事件`，减少样板搭建和断言前准备步骤。
func addTaskStatusChangedEvent(
	ctx context.Context,
	tx pgx.Tx,
	eventRepo *TaskEventRepo,
	task *Task,
	job *ExecutionJob,
	fromTaskStatus string,
) error {
	payload := []byte(`{"from_status":"` + fromTaskStatus + `","to_status":"` + task.Status + `"}`)
	if job.ErrorMessage != nil {
		payload = []byte(`{"from_status":"` + fromTaskStatus + `","to_status":"` + task.Status + `","error_message":"` + *job.ErrorMessage + `"}`)
	}

	_, err := eventRepo.AddTx(ctx, tx, TaskEventCreateParams{
		TaskID:    task.ID,
		RunID:     &job.ID,
		Source:    "job_worker",
		Level:     mapTaskEventLevel(job.Status),
		EventType: "task.status_changed",
		Message:   "任务状态已更新",
		Payload:   payload,
	})
	return err
}

// mapTaskEventLevel 为测试用例处理 `map任务事件Level`，减少样板搭建和断言前准备步骤。
func mapTaskEventLevel(jobStatus string) string {
	if jobStatus == "failed" {
		return "error"
	}
	return "info"
}

// derefString 为测试场景处理 `derefString` 的辅助步骤，减少重复搭建逻辑。
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// seedRunningExecutionJob 为测试场景补齐 `Running执行作业` 所需数据，减少重复造数。
func seedRunningExecutionJob(
	t *testing.T,
	pool *pgxpool.Pool,
	resourceRepo *ResourceRepo,
	taskRepo *TaskRepo,
	approvalRepo *ApprovalRepo,
	jobRepo *JobRepo,
	ctx context.Context,
	resourceID string,
	baseVersionID string,
	instruction string,
) *ExecutionJob {
	t.Helper()

	task, err := taskRepo.Create(ctx, resourceID, instruction)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, baseVersionID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE approvals
		SET status = 'approved', decided_at = now(), base_version_id = $2
		WHERE id = $1
	`, approvalRecord.ID, baseVersionID); err != nil {
		t.Fatalf("update approval base version: %v", err)
	}

	job, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, baseVersionID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE execution_jobs
		SET status = 'running', started_at = now(), base_version_id = $2
		WHERE id = $1
	`, job.ID, baseVersionID); err != nil {
		t.Fatalf("update job to running: %v", err)
	}

	// 保证当前资源至少已有一个可对照的 current version。
	if _, err := resourceRepo.GetCurrentVersion(ctx, resourceID); err != nil {
		t.Fatalf("get current version: %v", err)
	}

	return job
}
