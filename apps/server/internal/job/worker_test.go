package job

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	executoragent "agent_project/apps/server/internal/agent/executor"
	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/models"

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

	approvalRecord, err := approvalRepo.Create(ctx, task.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if err := approvalRepo.UpdateStatus(ctx, approvalRecord.ID, "approved", nil); err != nil {
		t.Fatalf("update approval to approved: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	versionIndexer := indexer.NewService(resourceRepo, jobEmbedder{})
	exec := executoragent.New(taskRepo, resourceRepo, versionIndexer)
	eventService := taskevents.New(eventRepo)
	worker := New(jobRepo, exec, taskRepo, 1, eventService)

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

	if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, strings.Join([]string{
		"# 文档标题",
		"",
		"## 第一章",
		"原始第一章内容",
		"",
	}, "\n"), "original"); err != nil {
		t.Fatalf("create version: %v", err)
	}

	task, err := taskRepo.Create(ctx, resource.ID, "执行审批后的修订失败路径")
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
	if err := approvalRepo.UpdateStatus(ctx, approvalRecord.ID, "approved", nil); err != nil {
		t.Fatalf("update approval to approved: %v", err)
	}
	if err := taskRepo.UpdateStatus(ctx, task.ID, models.StatusExecuting, nil); err != nil {
		t.Fatalf("update task to executing: %v", err)
	}

	jobRecord, err := jobRepo.Create(ctx, task.ID, approvalRecord.ID)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	versionIndexer := indexer.NewService(resourceRepo, jobEmbedder{})
	exec := executoragent.New(taskRepo, resourceRepo, versionIndexer)
	eventService := taskevents.New(eventRepo)
	worker := New(jobRepo, exec, taskRepo, 1, eventService)

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

func newJobTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	ctx := jobTestContext(t)
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

func cleanupJobResource(t *testing.T, pool *pgxpool.Pool, resourceID string) {
	t.Helper()

	ctx := jobTestContext(t)
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

func jobTestContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func jobUniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
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
