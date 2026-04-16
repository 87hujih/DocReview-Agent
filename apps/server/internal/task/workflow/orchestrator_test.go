package workflow

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/agent/planner"
	"agent_project/apps/server/internal/assistant"
	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/models"
	"agent_project/apps/server/internal/testsupport/postgrestest"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOrchestratorRecordsCoreTaskEvents(t *testing.T) {
	pool := newWorkflowTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	eventService := taskevents.New(eventRepo)

	ctx := workflowTestContext(t)
	resource, err := resourceRepo.Create(ctx, "编排事件测试-"+workflowUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		workflowCleanupResource(t, pool, resource.ID)
	})

	if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 考勤\n原始条款", "original"); err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "请审阅考勤条款")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	orchestrator := New(
		taskRepo,
		resourceRepo,
		approvalRepo,
		fakePlannerAgent{},
		fakeReviewerAgent{},
		fakeEditorAgent{},
		fakeRetrieverService{},
		eventService,
		0, // use default contextMaxRunes
		nil,
	)

	orchestrator.Orchestrate(ctx, task)

	events, err := eventRepo.ListByTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	eventTypes := make([]string, 0, len(events))
	for _, event := range events {
		eventTypes = append(eventTypes, event.EventType)
	}

	for _, expected := range []string{"task.status_changed", "step.started", "artifact.created", "approval.created"} {
		if !slices.Contains(eventTypes, expected) {
			t.Fatalf("expected event %q in %v", expected, eventTypes)
		}
	}
}

func TestOrchestratorMarksTaskFailedWhenExecutionContextExpires(t *testing.T) {
	pool := newWorkflowTestPool(t)
	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	eventService := taskevents.New(eventRepo)

	ctx := workflowTestContext(t)
	resource, err := resourceRepo.Create(ctx, "编排超时失败测试-"+workflowUniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		workflowCleanupResource(t, pool, resource.ID)
	})

	if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 项目介绍\n原始内容", "original"); err != nil {
		t.Fatalf("create version: %v", err)
	}
	task, err := taskRepo.Create(ctx, resource.ID, "请把标题改成项目介绍")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	orchestrator := New(
		taskRepo,
		resourceRepo,
		approvalRepo,
		blockingPlannerAgent{},
		fakeReviewerAgent{},
		fakeEditorAgent{},
		fakeRetrieverService{},
		eventService,
		0,
		nil,
	)

	runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	orchestrator.Orchestrate(runCtx, task)

	verifyCtx := workflowTestContext(t)
	storedTask, err := taskRepo.GetByID(verifyCtx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if storedTask == nil {
		t.Fatal("expected stored task")
	}
	if storedTask.Status != models.StatusFailed {
		t.Fatalf("expected task status %q, got %q", models.StatusFailed, storedTask.Status)
	}
	if storedTask.ErrorMessage == nil || !strings.Contains(*storedTask.ErrorMessage, context.DeadlineExceeded.Error()) {
		t.Fatalf("expected task error to contain %q, got %v", context.DeadlineExceeded.Error(), storedTask.ErrorMessage)
	}

	steps, err := taskRepo.GetSteps(verifyCtx, task.ID)
	if err != nil {
		t.Fatalf("get steps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Status != "failed" {
		t.Fatalf("expected planner step failed, got %q", steps[0].Status)
	}
	if steps[0].ErrorMessage == nil || !strings.Contains(*steps[0].ErrorMessage, context.DeadlineExceeded.Error()) {
		t.Fatalf("expected step error to contain %q, got %v", context.DeadlineExceeded.Error(), steps[0].ErrorMessage)
	}

	events, err := eventRepo.ListByTask(verifyCtx, task.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	eventTypes := make([]string, 0, len(events))
	for _, event := range events {
		eventTypes = append(eventTypes, event.EventType)
	}
	for _, expected := range []string{"step.failed", "task.status_changed"} {
		if !slices.Contains(eventTypes, expected) {
			t.Fatalf("expected event %q in %v", expected, eventTypes)
		}
	}
}

func TestOrchestratorProjectsSnapshotStatusTransitions(t *testing.T) {
	t.Run("awaiting approval", func(t *testing.T) {
		pool := newWorkflowTestPool(t)
		ctx := workflowTestContext(t)
		resourceRepo := postgres.NewResourceRepo(pool)
		taskRepo := postgres.NewTaskRepo(pool)
		approvalRepo := postgres.NewApprovalRepo(pool)
		eventRepo := postgres.NewTaskEventRepo(pool)
		assistantRepo := postgres.NewAssistantRepo(pool)
		snapshotRepo := postgres.NewSessionContextSnapshotRepo(pool)
		projector := assistant.NewSessionContextProjector(snapshotRepo)
		eventService := taskevents.New(eventRepo)

		resource, err := resourceRepo.Create(ctx, "快照等待审批-"+workflowUniqueSuffix(), "upload")
		if err != nil {
			t.Fatalf("create resource: %v", err)
		}
		t.Cleanup(func() {
			workflowCleanupResource(t, pool, resource.ID)
		})
		if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 考勤\n原始条款", "original"); err != nil {
			t.Fatalf("create version: %v", err)
		}

		session, task, suggestionMessageID := seedWorkflowAssistantTask(t, ctx, assistantRepo, snapshotRepo, taskRepo, resource.ID, "请审阅考勤条款")
		t.Cleanup(func() {
			if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
				t.Fatalf("cleanup session: %v", err)
			}
		})

		orchestrator := New(
			taskRepo,
			resourceRepo,
			approvalRepo,
			fakePlannerAgent{},
			fakeReviewerAgent{},
			fakeEditorAgent{},
			fakeRetrieverService{},
			eventService,
			0,
			projector,
		)

		orchestrator.Orchestrate(ctx, task)

		snapshot, err := snapshotRepo.GetBySessionID(ctx, session.ID)
		if err != nil {
			t.Fatalf("get snapshot: %v", err)
		}
		if snapshot == nil || snapshot.LatestTaskStatus == nil || *snapshot.LatestTaskStatus != models.StatusAwaitingApproval {
			t.Fatalf("expected snapshot latest_task_status %q, got %#v", models.StatusAwaitingApproval, snapshot)
		}
		if snapshot.LatestTaskSourceMessageID == nil || *snapshot.LatestTaskSourceMessageID != suggestionMessageID {
			t.Fatalf("expected snapshot source message id %q, got %#v", suggestionMessageID, snapshot.LatestTaskSourceMessageID)
		}
	})

	t.Run("completed no-change", func(t *testing.T) {
		pool := newWorkflowTestPool(t)
		ctx := workflowTestContext(t)
		resourceRepo := postgres.NewResourceRepo(pool)
		taskRepo := postgres.NewTaskRepo(pool)
		approvalRepo := postgres.NewApprovalRepo(pool)
		eventRepo := postgres.NewTaskEventRepo(pool)
		assistantRepo := postgres.NewAssistantRepo(pool)
		snapshotRepo := postgres.NewSessionContextSnapshotRepo(pool)
		projector := assistant.NewSessionContextProjector(snapshotRepo)
		eventService := taskevents.New(eventRepo)

		resource, err := resourceRepo.Create(ctx, "快照无需修改-"+workflowUniqueSuffix(), "upload")
		if err != nil {
			t.Fatalf("create resource: %v", err)
		}
		t.Cleanup(func() {
			workflowCleanupResource(t, pool, resource.ID)
		})
		if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 考勤\n原始条款", "original"); err != nil {
			t.Fatalf("create version: %v", err)
		}

		session, task, _ := seedWorkflowAssistantTask(t, ctx, assistantRepo, snapshotRepo, taskRepo, resource.ID, "请确认这份文档无需修改")
		t.Cleanup(func() {
			if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
				t.Fatalf("cleanup session: %v", err)
			}
		})

		orchestrator := New(
			taskRepo,
			resourceRepo,
			approvalRepo,
			fakePlannerAgent{},
			fakeReviewerAgent{},
			fakeNoChangeEditorAgent{},
			fakeRetrieverService{},
			eventService,
			0,
			projector,
		)

		orchestrator.Orchestrate(ctx, task)

		snapshot, err := snapshotRepo.GetBySessionID(ctx, session.ID)
		if err != nil {
			t.Fatalf("get snapshot: %v", err)
		}
		if snapshot == nil || snapshot.LatestTaskStatus == nil || *snapshot.LatestTaskStatus != models.StatusCompleted {
			t.Fatalf("expected snapshot latest_task_status %q, got %#v", models.StatusCompleted, snapshot)
		}
	})

	t.Run("failed", func(t *testing.T) {
		pool := newWorkflowTestPool(t)
		ctx := workflowTestContext(t)
		resourceRepo := postgres.NewResourceRepo(pool)
		taskRepo := postgres.NewTaskRepo(pool)
		approvalRepo := postgres.NewApprovalRepo(pool)
		eventRepo := postgres.NewTaskEventRepo(pool)
		assistantRepo := postgres.NewAssistantRepo(pool)
		snapshotRepo := postgres.NewSessionContextSnapshotRepo(pool)
		projector := assistant.NewSessionContextProjector(snapshotRepo)
		eventService := taskevents.New(eventRepo)

		resource, err := resourceRepo.Create(ctx, "快照失败-"+workflowUniqueSuffix(), "upload")
		if err != nil {
			t.Fatalf("create resource: %v", err)
		}
		t.Cleanup(func() {
			workflowCleanupResource(t, pool, resource.ID)
		})
		if _, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 考勤\n原始条款", "original"); err != nil {
			t.Fatalf("create version: %v", err)
		}

		session, task, _ := seedWorkflowAssistantTask(t, ctx, assistantRepo, snapshotRepo, taskRepo, resource.ID, "请触发失败路径")
		t.Cleanup(func() {
			if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
				t.Fatalf("cleanup session: %v", err)
			}
		})

		orchestrator := New(
			taskRepo,
			resourceRepo,
			approvalRepo,
			failingPlannerAgent{err: fmt.Errorf("planner boom")},
			fakeReviewerAgent{},
			fakeEditorAgent{},
			fakeRetrieverService{},
			eventService,
			0,
			projector,
		)

		orchestrator.Orchestrate(ctx, task)

		snapshot, err := snapshotRepo.GetBySessionID(ctx, session.ID)
		if err != nil {
			t.Fatalf("get snapshot: %v", err)
		}
		if snapshot == nil || snapshot.LatestTaskStatus == nil || *snapshot.LatestTaskStatus != models.StatusFailed {
			t.Fatalf("expected snapshot latest_task_status %q, got %#v", models.StatusFailed, snapshot)
		}
	})
}

type fakePlannerAgent struct{}

func (fakePlannerAgent) Plan(context.Context, string, string, string) (*planner.PlanResult, error) {
	return &planner.PlanResult{
		Intent:        "审阅考勤条款",
		SearchQueries: []string{"考勤"},
		FocusSections: []string{"考勤"},
	}, nil
}

type blockingPlannerAgent struct{}

func (blockingPlannerAgent) Plan(ctx context.Context, _ string, _ string, _ string) (*planner.PlanResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type failingPlannerAgent struct {
	err error
}

func (a failingPlannerAgent) Plan(context.Context, string, string, string) (*planner.PlanResult, error) {
	return nil, a.err
}

type fakeReviewerAgent struct{}

func (fakeReviewerAgent) Review(context.Context, string, []citation.Citation, string) (string, error) {
	return "考勤条款需要补充边界。", nil
}

type fakeEditorAgent struct{}

func (fakeEditorAgent) Edit(context.Context, string, string, []citation.Citation) (*editor.DiffPreview, error) {
	return &editor.DiffPreview{
		Sections: []editor.DiffSection{
			{
				SectionTitle:      "考勤",
				SectionOccurrence: 1,
				Original:          "原始条款",
				Revised:           "明确后的考勤条款",
				Reason:            "减少歧义",
				CitationIDs:       []string{"cite_1"},
			},
		},
	}, nil
}

type fakeNoChangeEditorAgent struct{}

func (fakeNoChangeEditorAgent) Edit(context.Context, string, string, []citation.Citation) (*editor.DiffPreview, error) {
	return &editor.DiffPreview{NoChange: true}, nil
}

type fakeRetrieverService struct{}

func (fakeRetrieverService) SearchByResource(context.Context, string, string, int) ([]citation.Citation, error) {
	return []citation.Citation{
		{
			CitationID:   "ignored",
			ResourceID:   "resource-1",
			SectionTitle: "考勤",
			Snippet:      "考勤条款原文",
		},
	}, nil
}

func newWorkflowTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	if strings.TrimSpace(os.Getenv("DATABASE_URL")) == "" {
		t.Skip("database not available")
	}

	ctx := workflowTestContext(t)
	cfg := appconfig.Load()
	return postgrestest.NewIsolatedPool(t, ctx, cfg.DatabaseURL, "task_workflow", postgres.NewPool, postgres.RunMigrations)
}

func workflowCleanupResource(t *testing.T, pool *pgxpool.Pool, resourceID string) {
	t.Helper()

	ctx := workflowTestContext(t)
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
	if _, err := pool.Exec(ctx, `DELETE FROM resources WHERE id = $1`, resourceID); err != nil {
		t.Fatalf("cleanup resource %q: %v", resourceID, err)
	}
}

func workflowTestContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func workflowUniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func seedWorkflowAssistantTask(
	t *testing.T,
	ctx context.Context,
	assistantRepo *postgres.AssistantRepo,
	snapshotRepo *postgres.SessionContextSnapshotRepo,
	taskRepo *postgres.TaskRepo,
	resourceID string,
	instruction string,
) (*postgres.AssistantSession, *postgres.Task, string) {
	t.Helper()

	session, _, err := assistantRepo.CreateSessionWithMessages(ctx, "workflow-snapshot-"+workflowUniqueSuffix(), nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := snapshotRepo.CreateEmpty(ctx, session.ID); err != nil {
		t.Fatalf("create empty snapshot: %v", err)
	}

	suggestionPayload := fmt.Sprintf(`{"title":"建议创建任务","instruction":"%s","can_create":true,"action_label":"确认创建任务","resource_id":"%s","resource_label":"测试资源","status_message":"资源已明确，可以创建任务。"}`, instruction, resourceID)
	messages, err := assistantRepo.AppendMessages(ctx, session.ID, []postgres.AssistantMessageInput{{
		Role:    "assistant",
		Kind:    "task_suggestion",
		Payload: []byte(suggestionPayload),
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

// --- validateDiffPreview 单元测试（2.4）---

func TestValidateDiffPreviewRejectsNil(t *testing.T) {
	if err := validateDiffPreview(nil, nil); err == nil {
		t.Fatal("预览为 nil 时期望返回错误")
	}
}

func TestValidateDiffPreviewRejectsEmptySections(t *testing.T) {
	preview := &editor.DiffPreview{Sections: nil}
	if err := validateDiffPreview(preview, nil); err == nil {
		t.Fatal("sections 为空时期望返回错误")
	}
}

func TestValidateDiffPreviewRejectsEmptySectionTitle(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	preview := &editor.DiffPreview{Sections: []editor.DiffSection{
		{SectionTitle: "", SectionOccurrence: 1, Original: "a", Revised: "b", Reason: "r", CitationIDs: []string{"cite_1"}},
	}}
	if err := validateDiffPreview(preview, citations); err == nil {
		t.Fatal("section_title 为空时期望返回错误")
	}
}

func TestValidateDiffPreviewRejectsEmptyOriginal(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	preview := &editor.DiffPreview{Sections: []editor.DiffSection{
		{SectionTitle: "t", SectionOccurrence: 1, Original: "", Revised: "b", Reason: "r", CitationIDs: []string{"cite_1"}},
	}}
	if err := validateDiffPreview(preview, citations); err == nil {
		t.Fatal("original 为空时期望返回错误")
	}
}

func TestValidateDiffPreviewRejectsIdenticalOriginalRevised(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	preview := &editor.DiffPreview{Sections: []editor.DiffSection{
		{SectionTitle: "t", SectionOccurrence: 1, Original: "same", Revised: "same", Reason: "r", CitationIDs: []string{"cite_1"}},
	}}
	if err := validateDiffPreview(preview, citations); err == nil {
		t.Fatal("original 与 revised 相同时期望返回错误")
	}
}

func TestValidateDiffPreviewRejectsSectionsOverLimit(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	sections := make([]editor.DiffSection, 51)
	for i := range sections {
		sections[i] = editor.DiffSection{SectionTitle: "t", SectionOccurrence: 1, Original: "a", Revised: "b", Reason: "r", CitationIDs: []string{"cite_1"}}
	}
	preview := &editor.DiffPreview{Sections: sections}
	if err := validateDiffPreview(preview, citations); err == nil {
		t.Fatal("章节数超过 50 时期望返回错误")
	}
}

func TestValidateDiffPreviewRejectsFieldOverRuneLimit(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	longStr := strings.Repeat("x", 10001)
	preview := &editor.DiffPreview{Sections: []editor.DiffSection{
		{SectionTitle: longStr, SectionOccurrence: 1, Original: "a", Revised: "b", Reason: "r", CitationIDs: []string{"cite_1"}},
	}}
	if err := validateDiffPreview(preview, citations); err == nil {
		t.Fatal("section_title 超过字符长度上限时期望返回错误")
	}
}

func TestValidateDiffPreviewPassesNoChange(t *testing.T) {
	preview := &editor.DiffPreview{NoChange: true}
	if err := validateDiffPreview(preview, nil); err != nil {
		t.Fatalf("no-change 预览应通过校验：%v", err)
	}
}

func TestValidateDiffPreviewRejectsMissingSectionOccurrence(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	preview := &editor.DiffPreview{Sections: []editor.DiffSection{
		{SectionTitle: "考勤", SectionOccurrence: 0, Original: "原文", Revised: "修订后", Reason: "补充定义", CitationIDs: []string{"cite_1"}},
	}}
	if err := validateDiffPreview(preview, citations); err == nil {
		t.Fatal("section_occurrence 缺失时期望返回错误")
	}
}

func TestValidateDiffPreviewPassesValid(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	preview := &editor.DiffPreview{Sections: []editor.DiffSection{
		{SectionTitle: "考勤", SectionOccurrence: 1, Original: "原文", Revised: "修订后", Reason: "补充定义", CitationIDs: []string{"cite_1"}},
	}}
	if err := validateDiffPreview(preview, citations); err != nil {
		t.Fatalf("合法预览应通过校验：%v", err)
	}
}

// --- 状态机转换测试（2.5）---

func TestDraftingToCompletedAllowed(t *testing.T) {
	if err := models.Transition(models.StatusDrafting, models.StatusCompleted); err != nil {
		t.Fatalf("无需修改分支应允许 drafting -> completed：%v", err)
	}
}

func TestDraftingToCompletedNotReachableFromAwaitingApproval(t *testing.T) {
	if err := models.Transition(models.StatusAwaitingApproval, models.StatusCompleted); err == nil {
		t.Fatal("awaiting_approval 不应直接转换为 completed")
	}
}
