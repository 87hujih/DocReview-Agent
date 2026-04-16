package job

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	executoragent "agent_project/apps/server/internal/agent/executor"
	"agent_project/apps/server/internal/assistant"
	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/models"
	"agent_project/apps/server/internal/testsupport/postgrescleanup"
	"agent_project/apps/server/internal/testsupport/postgrestest"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWorkerProcessesJob(t *testing.T) {
	pool := newJobTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	ctx := jobTestContext(t)

	resource, err := resourceRepo.Create(ctx, "Worker 集成测试-"+jobUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupJobResource(t, pool, resource.ID)
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, strings.Join([]string{
		"# 文档标题",
		"",
		"## 第一章",
		"原始第一章内容",
		"",
		"## 第二章",
		"原始第二章内容",
		"",
	}, "\n"), "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "执行审批后的修订")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	if _, err := taskRepo.AddArtifact(ctx, task.ID, "diff_preview", []byte(`{"sections":[{"section_title":"第一章","original":"原始第一章内容","revised":"修订后的第一章内容","reason":"补充说明","citation_ids":["cite_1"]}]}`)); err != nil {
		t.Fatalf("add diff preview artifact: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if err := approvalRepo.UpdateStatus(ctx, approvalRecord.ID, "approved", nil); err != nil {
		t.Fatalf("update approval to approved: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	versionIndexer := indexer.NewService(resourceRepo, jobEmbedder{})
	exec := executoragent.New(taskRepo, resourceRepo, versionIndexer)
	eventService := taskevents.New(eventRepo)
	worker := New(jobRepo, exec, taskRepo, 1, eventService, nil, nil)

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	worker.Start(workerCtx, 1)

	worker.JobCh() <- struct{}{}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		currentJob, err := jobRepo.GetByID(ctx, jobRecord.ID)
		if err != nil {
			t.Fatalf("get current job: %v", err)
		}
		if currentJob != nil && currentJob.Status == "done" {
			currentTask, err := taskRepo.GetByID(ctx, task.ID)
			if err != nil {
				t.Fatalf("get current task: %v", err)
			}
			if currentTask == nil {
				t.Fatal("expected current task, got nil")
			}
			if currentTask.Status != models.StatusCompleted {
				t.Fatalf("expected task status %q, got %q", models.StatusCompleted, currentTask.Status)
			}

			currentVersion, err := resourceRepo.GetCurrentVersion(ctx, resource.ID)
			if err != nil {
				t.Fatalf("get current version: %v", err)
			}
			if currentVersion == nil {
				t.Fatal("expected current version, got nil")
			}
			if currentVersion.VersionNumber != version.VersionNumber+1 {
				t.Fatalf("expected version number %d, got %d", version.VersionNumber+1, currentVersion.VersionNumber)
			}
			if !strings.Contains(currentVersion.Content, "修订后的第一章内容") {
				t.Fatalf("expected revised content in version, got %q", currentVersion.Content)
			}

			events, err := eventRepo.ListByTask(ctx, task.ID)
			if err != nil {
				t.Fatalf("list task events: %v", err)
			}
			if len(events) != 3 {
				t.Fatalf("expected 3 task events, got %d", len(events))
			}
			if events[0].EventType != "job.claimed" {
				t.Fatalf("expected first event type %q, got %q", "job.claimed", events[0].EventType)
			}
			if events[1].EventType != "job.completed" {
				t.Fatalf("expected second event type %q, got %q", "job.completed", events[1].EventType)
			}
			if events[2].EventType != "task.status_changed" {
				t.Fatalf("expected third event type %q, got %q", "task.status_changed", events[2].EventType)
			}
			return
		}

		if currentJob != nil && currentJob.Status == "failed" {
			t.Fatalf("expected job to finish successfully, got failed with error %#v", currentJob.ErrorMessage)
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("timed out waiting for worker to process job")
}

func TestWorkerMarksTaskFailedWhenExecutionFails(t *testing.T) {
	pool := newJobTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	ctx := jobTestContext(t)

	resource, err := resourceRepo.Create(ctx, "Worker 失败集成测试-"+jobUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupJobResource(t, pool, resource.ID)
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, strings.Join([]string{
		"# 文档标题",
		"",
		"## 第一章",
		"原始第一章内容",
		"",
	}, "\n"), "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "执行审批后的修订失败路径")
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
	if err := approvalRepo.UpdateStatus(ctx, approvalRecord.ID, "approved", nil); err != nil {
		t.Fatalf("update approval to approved: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	versionIndexer := indexer.NewService(resourceRepo, jobEmbedder{})
	exec := executoragent.New(taskRepo, resourceRepo, versionIndexer)
	eventService := taskevents.New(eventRepo)
	worker := New(jobRepo, exec, taskRepo, 1, eventService, nil, nil)

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	worker.Start(workerCtx, 1)

	worker.JobCh() <- struct{}{}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		currentJob, err := jobRepo.GetByID(ctx, jobRecord.ID)
		if err != nil {
			t.Fatalf("get current job: %v", err)
		}
		if currentJob != nil && currentJob.Status == "failed" {
			if currentJob.ErrorMessage == nil || !strings.Contains(*currentJob.ErrorMessage, "未找到 diff 预览产物") {
				t.Fatalf("expected job error to mention missing diff preview, got %#v", currentJob.ErrorMessage)
			}

			currentTask, err := taskRepo.GetByID(ctx, task.ID)
			if err != nil {
				t.Fatalf("get current task: %v", err)
			}
			if currentTask == nil {
				t.Fatal("expected current task, got nil")
			}
			if currentTask.Status != models.StatusFailed {
				t.Fatalf("expected task status %q, got %q", models.StatusFailed, currentTask.Status)
			}
			if currentTask.ErrorMessage == nil || !strings.Contains(*currentTask.ErrorMessage, "未找到 diff 预览产物") {
				t.Fatalf("expected task error to mention missing diff preview, got %#v", currentTask.ErrorMessage)
			}

			events, err := eventRepo.ListByTask(ctx, task.ID)
			if err != nil {
				t.Fatalf("list task events: %v", err)
			}
			if len(events) != 3 {
				t.Fatalf("expected 3 task events, got %d", len(events))
			}
			if events[0].EventType != "job.claimed" {
				t.Fatalf("expected first event type %q, got %q", "job.claimed", events[0].EventType)
			}
			if events[1].EventType != "job.failed" {
				t.Fatalf("expected second event type %q, got %q", "job.failed", events[1].EventType)
			}
			if events[2].EventType != "task.status_changed" {
				t.Fatalf("expected third event type %q, got %q", "task.status_changed", events[2].EventType)
			}
			return
		}

		if currentJob != nil && currentJob.Status == "done" {
			t.Fatalf("expected job to fail, got done with new version %#v", currentJob.NewVersionID)
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("timed out waiting for worker to process failed job")
}

func TestWorkerProcessesJobProjectsCompletedSnapshot(t *testing.T) {
	pool := newJobTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	assistantRepo := postgres.NewAssistantRepo(pool)
	snapshotRepo := postgres.NewSessionContextSnapshotRepo(pool)
	projector := assistant.NewSessionContextProjector(snapshotRepo)
	ctx := jobTestContext(t)

	resource, err := resourceRepo.Create(ctx, "Worker 快照成功-"+jobUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupJobResource(t, pool, resource.ID)
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, strings.Join([]string{
		"# 文档标题",
		"",
		"## 第一章",
		"原始第一章内容",
		"",
		"## 第二章",
		"原始第二章内容",
		"",
	}, "\n"), "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	session, task, suggestionMessageID := seedJobAssistantTask(t, ctx, assistantRepo, snapshotRepo, taskRepo, resource.ID, "执行审批后的修订")
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup session: %v", err)
		}
	})
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	if _, err := taskRepo.AddArtifact(ctx, task.ID, "diff_preview", []byte(`{"sections":[{"section_title":"第一章","original":"原始第一章内容","revised":"修订后的第一章内容","reason":"补充说明","citation_ids":["cite_1"]}]}`)); err != nil {
		t.Fatalf("add diff preview artifact: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if err := approvalRepo.UpdateStatus(ctx, approvalRecord.ID, "approved", nil); err != nil {
		t.Fatalf("update approval to approved: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	versionIndexer := indexer.NewService(resourceRepo, jobEmbedder{})
	exec := executoragent.New(taskRepo, resourceRepo, versionIndexer)
	eventService := taskevents.New(eventRepo)
	worker := New(jobRepo, exec, taskRepo, 1, eventService, projector, nil)

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	worker.Start(workerCtx, 1)
	worker.JobCh() <- struct{}{}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		currentJob, err := jobRepo.GetByID(ctx, jobRecord.ID)
		if err != nil {
			t.Fatalf("get current job: %v", err)
		}
		if currentJob != nil && currentJob.Status == "done" {
			snapshot, err := snapshotRepo.GetBySessionID(ctx, session.ID)
			if err != nil {
				t.Fatalf("get snapshot: %v", err)
			}
			if snapshot == nil || snapshot.LatestTaskStatus == nil || *snapshot.LatestTaskStatus != models.StatusCompleted {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if snapshot.LatestTaskSourceMessageID == nil || *snapshot.LatestTaskSourceMessageID != suggestionMessageID {
				t.Fatalf("expected snapshot source message id %q, got %#v", suggestionMessageID, snapshot.LatestTaskSourceMessageID)
			}
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("timed out waiting for worker to project completed snapshot")
}

func TestWorkerMarksTaskFailedProjectsFailedSnapshot(t *testing.T) {
	pool := newJobTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	assistantRepo := postgres.NewAssistantRepo(pool)
	snapshotRepo := postgres.NewSessionContextSnapshotRepo(pool)
	projector := assistant.NewSessionContextProjector(snapshotRepo)
	ctx := jobTestContext(t)

	resource, err := resourceRepo.Create(ctx, "Worker 快照失败-"+jobUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupJobResource(t, pool, resource.ID)
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, strings.Join([]string{
		"# 文档标题",
		"",
		"## 第一章",
		"原始第一章内容",
		"",
	}, "\n"), "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	session, task, suggestionMessageID := seedJobAssistantTask(t, ctx, assistantRepo, snapshotRepo, taskRepo, resource.ID, "执行审批后的修订失败路径")
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
	if err := approvalRepo.UpdateStatus(ctx, approvalRecord.ID, "approved", nil); err != nil {
		t.Fatalf("update approval to approved: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	versionIndexer := indexer.NewService(resourceRepo, jobEmbedder{})
	exec := executoragent.New(taskRepo, resourceRepo, versionIndexer)
	eventService := taskevents.New(eventRepo)
	worker := New(jobRepo, exec, taskRepo, 1, eventService, projector, nil)

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	worker.Start(workerCtx, 1)
	worker.JobCh() <- struct{}{}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		currentJob, err := jobRepo.GetByID(ctx, jobRecord.ID)
		if err != nil {
			t.Fatalf("get current job: %v", err)
		}
		if currentJob != nil && currentJob.Status == "failed" {
			snapshot, err := snapshotRepo.GetBySessionID(ctx, session.ID)
			if err != nil {
				t.Fatalf("get snapshot: %v", err)
			}
			if snapshot == nil || snapshot.LatestTaskStatus == nil || *snapshot.LatestTaskStatus != models.StatusFailed {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			if snapshot.LatestTaskSourceMessageID == nil || *snapshot.LatestTaskSourceMessageID != suggestionMessageID {
				t.Fatalf("expected snapshot source message id %q, got %#v", suggestionMessageID, snapshot.LatestTaskSourceMessageID)
			}
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("timed out waiting for worker to project failed snapshot")
}

func TestTaskStatusNotifierWorkerSyncsTerminalStatuses(t *testing.T) {
	projector := &recordingJobProjector{}
	notifier := &recordingJobNotifier{}
	worker := New(nil, nil, nil, 1, nil, projector, notifier)
	task := &postgres.Task{ID: "task-terminal-1"}

	worker.syncTaskStatusSideEffects(context.Background(), task, models.StatusCompleted)
	if len(projector.statuses) != 1 || projector.statuses[0] != models.StatusCompleted {
		t.Fatalf("expected projector to record %q once, got %#v", models.StatusCompleted, projector.statuses)
	}
	if len(notifier.statuses) != 1 || notifier.statuses[0] != models.StatusCompleted {
		t.Fatalf("expected notifier to record %q once, got %#v", models.StatusCompleted, notifier.statuses)
	}

	projector.statuses = nil
	notifier.statuses = nil
	worker.syncTaskStatusSideEffects(context.Background(), task, models.StatusExecuting)
	if len(projector.statuses) != 1 || projector.statuses[0] != models.StatusExecuting {
		t.Fatalf("expected projector to record %q once, got %#v", models.StatusExecuting, projector.statuses)
	}
	if len(notifier.statuses) != 0 {
		t.Fatalf("expected notifier to ignore non-terminal status, got %#v", notifier.statuses)
	}
}

func TestWorkerFailsLegacyJobWithoutBaseVersion(t *testing.T) {
	pool := newJobTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	ctx := jobTestContext(t)

	resource, err := resourceRepo.Create(ctx, "Worker legacy job 测试-"+jobUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupJobResource(t, pool, resource.ID)
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, strings.Join([]string{
		"# 文档标题",
		"",
		"## 第一章",
		"原始第一章内容",
		"",
	}, "\n"), "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "legacy job 缺少 base_version_id")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusAwaitingApproval, nil); err != nil {
		t.Fatalf("update task to awaiting approval: %v", err)
	}

	if _, err := taskRepo.AddArtifact(ctx, task.ID, "diff_preview", []byte(`{"sections":[{"section_title":"第一章","original":"原始第一章内容","revised":"修订后的第一章内容","reason":"补充说明","citation_ids":["cite_1"]}]}`)); err != nil {
		t.Fatalf("add diff preview artifact: %v", err)
	}

	approvalRecord, err := approvalRepo.Create(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if err := approvalRepo.UpdateStatus(ctx, approvalRecord.ID, "approved", nil); err != nil {
		t.Fatalf("update approval to approved: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID, version.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE execution_jobs
		SET base_version_id = NULL
		WHERE id = $1
	`, jobRecord.ID); err != nil {
		t.Fatalf("clear job base_version_id: %v", err)
	}

	versionIndexer := indexer.NewService(resourceRepo, jobEmbedder{})
	exec := executoragent.New(taskRepo, resourceRepo, versionIndexer)
	eventService := taskevents.New(eventRepo)
	worker := New(jobRepo, exec, taskRepo, 1, eventService, nil, nil)

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	worker.Start(workerCtx, 1)

	worker.JobCh() <- struct{}{}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		currentJob, err := jobRepo.GetByID(ctx, jobRecord.ID)
		if err != nil {
			t.Fatalf("get current job: %v", err)
		}
		if currentJob != nil && currentJob.Status == "failed" {
			if currentJob.ErrorMessage == nil || !strings.Contains(*currentJob.ErrorMessage, "base_version_id") {
				t.Fatalf("expected job error to mention base_version_id, got %#v", currentJob.ErrorMessage)
			}
			return
		}

		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("timed out waiting for worker to fail legacy job")
}

func newJobTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	ctx := jobTestContext(t)
	cfg := appconfig.Load()
	return postgrestest.NewIsolatedPool(t, ctx, cfg.DatabaseURL, "job_worker", postgres.NewPool, postgres.RunMigrations)
}

func cleanupJobResource(t *testing.T, pool *pgxpool.Pool, resourceID string) {
	t.Helper()

	ctx := jobTestContext(t)
	if err := postgrescleanup.CleanupResourceTree(ctx, pool, resourceID); err != nil {
		t.Fatalf("cleanup resource tree for resource %q: %v", resourceID, err)
	}
}

func jobTestContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func jobUniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func seedJobAssistantTask(
	t *testing.T,
	ctx context.Context,
	assistantRepo *postgres.AssistantRepo,
	snapshotRepo *postgres.SessionContextSnapshotRepo,
	taskRepo *postgres.TaskRepo,
	resourceID string,
	instruction string,
) (*postgres.AssistantSession, *postgres.Task, string) {
	t.Helper()

	session, _, err := assistantRepo.CreateSessionWithMessages(ctx, "job-snapshot-"+jobUniqueSuffix(), nil)
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

type jobEmbedder struct{}

func (jobEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for index := range texts {
		vector := make([]float32, 1024)
		vector[index%len(vector)] = 1
		vectors = append(vectors, vector)
	}

	return vectors, nil
}

type recordingJobProjector struct {
	statuses []string
}

func (r *recordingJobProjector) ProjectTaskStatusChanged(_ context.Context, _ *string, _ string, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}

type recordingJobNotifier struct {
	statuses []string
}

func (r *recordingJobNotifier) Notify(_ context.Context, _ *postgres.Task, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}
