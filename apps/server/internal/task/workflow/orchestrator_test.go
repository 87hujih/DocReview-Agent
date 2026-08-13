package workflow

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/agent/planner"
	"agent_project/apps/server/internal/assistant"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	"agent_project/apps/server/internal/task/models"
	"agent_project/apps/server/internal/testsupport/postgrestest"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestOrchestratorRecordsCoreTaskEvents 验证`orchestrator`在写入或副作用路径下的行为，防止同类回归。
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
		0, // 说明： use default contextMaxRunes
		nil,
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

// TestOrchestratorMarksTaskFailedWhenExecutionContextExpires 验证`orchestrator`在流程控制路径下的行为，防止同类回归。
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

// TestOrchestratorProjectsSnapshotStatusTransitions 验证`orchestrator`在写入或副作用路径下的行为，防止同类回归。
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
			nil,
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
			nil,
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
			failingPlannerAgent{err: fmt.Errorf("处理失败：planner boom")},
			fakeReviewerAgent{},
			fakeEditorAgent{},
			fakeRetrieverService{},
			eventService,
			0,
			projector,
			nil,
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

// TestTaskStatusNotifierOrchestratorSyncsTerminalStatuses 验证`taskStatusNotifierOrchestrator`在写入或副作用路径下的行为，防止同类回归。
func TestTaskStatusNotifierOrchestratorSyncsTerminalStatuses(t *testing.T) {
	task := &postgres.Task{ID: "task-terminal-1"}
	projector := &recordingWorkflowProjector{}
	notifier := &recordingWorkflowNotifier{}
	orchestrator := New(
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		0,
		projector,
		notifier,
	)

	orchestrator.syncTaskStatusSideEffects(context.Background(), task, models.StatusCompleted)
	if len(projector.statuses) != 1 || projector.statuses[0] != models.StatusCompleted {
		t.Fatalf("expected projector to record %q once, got %#v", models.StatusCompleted, projector.statuses)
	}
	if len(notifier.statuses) != 1 || notifier.statuses[0] != models.StatusCompleted {
		t.Fatalf("expected notifier to record %q once, got %#v", models.StatusCompleted, notifier.statuses)
	}

	projector.statuses = nil
	notifier.statuses = nil
	orchestrator.syncTaskStatusSideEffects(context.Background(), task, models.StatusAwaitingApproval)
	if len(projector.statuses) != 1 || projector.statuses[0] != models.StatusAwaitingApproval {
		t.Fatalf("expected projector to record %q once, got %#v", models.StatusAwaitingApproval, projector.statuses)
	}
	if len(notifier.statuses) != 0 {
		t.Fatalf("expected notifier to ignore non-terminal status, got %#v", notifier.statuses)
	}
}

// fakePlannerAgent 作为PlannerAgent的测试替身，用于在用例里提供可控的依赖行为。
type fakePlannerAgent struct{}

// Plan 实现测试替身需要的 `Plan` 接口方法，为用例分支提供可控返回。
func (fakePlannerAgent) Plan(context.Context, string, string, string) (*planner.PlanResult, error) {
	return &planner.PlanResult{
		Intent:        "审阅考勤条款",
		SearchQueries: []string{"考勤"},
		FocusSections: []string{"考勤"},
	}, nil
}

// blockingPlannerAgent 作为PlannerAgent的阻塞型测试替身，用于覆盖并发与超时路径。
type blockingPlannerAgent struct{}

// Plan 实现测试替身需要的 `Plan` 接口方法，为用例分支提供可控返回。
func (blockingPlannerAgent) Plan(ctx context.Context, _ string, _ string, _ string) (*planner.PlanResult, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// failingPlannerAgent 作为PlannerAgent的失败型测试替身，用于覆盖错误分支。
type failingPlannerAgent struct {
	err error
}

// Plan 实现测试替身需要的 `Plan` 接口方法，为用例分支提供可控返回。
func (a failingPlannerAgent) Plan(context.Context, string, string, string) (*planner.PlanResult, error) {
	return nil, a.err
}

// fakeReviewerAgent 作为ReviewerAgent的测试替身，用于在用例里提供可控的依赖行为。
type fakeReviewerAgent struct{}

// Review 实现测试替身需要的 `Review` 接口方法，为用例分支提供可控返回。
func (fakeReviewerAgent) Review(context.Context, string, []citation.Citation, string) (string, error) {
	return "考勤条款需要补充边界。", nil
}

// fakeEditorAgent 作为EditorAgent的测试替身，用于在用例里提供可控的依赖行为。
type fakeEditorAgent struct{}

// Edit 实现测试替身需要的 `Edit` 接口方法，为用例分支提供可控返回。
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

// fakeNoChangeEditorAgent 作为NoChangeEditorAgent的测试替身，用于在用例里提供可控的依赖行为。
type fakeNoChangeEditorAgent struct{}

// Edit 实现测试替身需要的 `Edit` 接口方法，为用例分支提供可控返回。
func (fakeNoChangeEditorAgent) Edit(context.Context, string, string, []citation.Citation) (*editor.DiffPreview, error) {
	return &editor.DiffPreview{NoChange: true}, nil
}

// fakeRetrieverService 作为检索器服务的测试替身，用于在用例里提供可控的依赖行为。
type fakeRetrieverService struct{}

// SearchByResource 实现测试替身需要的 `SearchByResource` 接口方法，为用例分支提供可控返回。
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

// recordingWorkflowProjector 作为工作流投影器的记录型测试替身，用于断言调用副作用。
type recordingWorkflowProjector struct {
	statuses []string
}

// ProjectTaskStatusChanged 实现测试替身需要的 `ProjectTaskStatusChanged` 接口方法，为用例分支提供可控返回。
func (r *recordingWorkflowProjector) ProjectTaskStatusChanged(_ context.Context, _ *string, _ string, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}

// recordingWorkflowNotifier 作为工作流Notifier的记录型测试替身，用于断言调用副作用。
type recordingWorkflowNotifier struct {
	statuses []string
}

// Notify 实现测试替身需要的 `Notify` 接口方法，为用例分支提供可控返回。
func (r *recordingWorkflowNotifier) Notify(_ context.Context, _ *postgres.Task, status string) error {
	r.statuses = append(r.statuses, status)
	return nil
}

// newWorkflowTestPool 创建测试用隔离数据库连接池，统一初始化与清理约束。
func newWorkflowTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := workflowTestContext(t)
	return postgrestest.NewIsolatedPool(t, ctx, "task_workflow", postgres.NewPool, postgres.RunMigrations)
}

// workflowCleanupResource 为测试场景处理 `工作流Cleanup资源` 的辅助步骤，减少重复搭建逻辑。
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

// workflowTestContext 构造测试上下文，统一附带当前用例需要的取消和超时能力。
func workflowTestContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// workflowUniqueSuffix 生成测试数据使用的唯一后缀，避免并发或重复运行时发生命名冲突。
func workflowUniqueSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// seedWorkflowAssistantTask 为测试场景补齐 `工作流助手任务` 所需数据，减少重复造数。
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

// TestValidateDiffPreviewRejectsNil 验证`validateDiffPreview`在非法输入或失败路径下的行为，防止同类回归。
func TestValidateDiffPreviewRejectsNil(t *testing.T) {
	if err := validateDiffPreview(nil, nil); err == nil {
		t.Fatal("预览为 nil 时期望返回错误")
	}
}

// TestValidateDiffPreviewRejectsEmptySections 验证`validateDiffPreview`在非法输入或失败路径下的行为，防止同类回归。
func TestValidateDiffPreviewRejectsEmptySections(t *testing.T) {
	preview := &editor.DiffPreview{Sections: nil}
	if err := validateDiffPreview(preview, nil); err == nil {
		t.Fatal("sections 为空时期望返回错误")
	}
}

// TestValidateDiffPreviewRejectsEmptySectionTitle 验证`validateDiffPreview`在非法输入或失败路径下的行为，防止同类回归。
func TestValidateDiffPreviewRejectsEmptySectionTitle(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	preview := &editor.DiffPreview{Sections: []editor.DiffSection{
		{SectionTitle: "", SectionOccurrence: 1, Original: "a", Revised: "b", Reason: "r", CitationIDs: []string{"cite_1"}},
	}}
	if err := validateDiffPreview(preview, citations); err == nil {
		t.Fatal("section_title 为空时期望返回错误")
	}
}

// TestValidateDiffPreviewRejectsEmptyOriginal 验证`validateDiffPreview`在非法输入或失败路径下的行为，防止同类回归。
func TestValidateDiffPreviewRejectsEmptyOriginal(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	preview := &editor.DiffPreview{Sections: []editor.DiffSection{
		{SectionTitle: "t", SectionOccurrence: 1, Original: "", Revised: "b", Reason: "r", CitationIDs: []string{"cite_1"}},
	}}
	if err := validateDiffPreview(preview, citations); err == nil {
		t.Fatal("original 为空时期望返回错误")
	}
}

// TestValidateDiffPreviewRejectsIdenticalOriginalRevised 验证`validateDiffPreview`在非法输入或失败路径下的行为，防止同类回归。
func TestValidateDiffPreviewRejectsIdenticalOriginalRevised(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	preview := &editor.DiffPreview{Sections: []editor.DiffSection{
		{SectionTitle: "t", SectionOccurrence: 1, Original: "same", Revised: "same", Reason: "r", CitationIDs: []string{"cite_1"}},
	}}
	if err := validateDiffPreview(preview, citations); err == nil {
		t.Fatal("original 与 revised 相同时期望返回错误")
	}
}

// TestValidateDiffPreviewRejectsSectionsOverLimit 验证`validateDiffPreview`在非法输入或失败路径下的行为，防止同类回归。
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

// TestValidateDiffPreviewRejectsFieldOverRuneLimit 验证`validateDiffPreview`在非法输入或失败路径下的行为，防止同类回归。
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

// TestValidateDiffPreviewPassesNoChange 验证`validateDiffPreview`在合法输入或兼容路径下的行为，防止同类回归。
func TestValidateDiffPreviewPassesNoChange(t *testing.T) {
	preview := &editor.DiffPreview{NoChange: true}
	if err := validateDiffPreview(preview, nil); err != nil {
		t.Fatalf("no-change 预览应通过校验：%v", err)
	}
}

// TestValidateDiffPreviewRejectsMissingSectionOccurrence 验证`validateDiffPreview`在非法输入或失败路径下的行为，防止同类回归。
func TestValidateDiffPreviewRejectsMissingSectionOccurrence(t *testing.T) {
	citations := []citation.Citation{{CitationID: "cite_1"}}
	preview := &editor.DiffPreview{Sections: []editor.DiffSection{
		{SectionTitle: "考勤", SectionOccurrence: 0, Original: "原文", Revised: "修订后", Reason: "补充定义", CitationIDs: []string{"cite_1"}},
	}}
	if err := validateDiffPreview(preview, citations); err == nil {
		t.Fatal("section_occurrence 缺失时期望返回错误")
	}
}

// TestValidateDiffPreviewPassesValid 验证`validateDiffPreview`在合法输入或兼容路径下的行为，防止同类回归。
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

// TestDraftingToCompletedAllowed 验证`draftingToCompletedAllowed`在特定边界条件下的行为，防止同类回归。
func TestDraftingToCompletedAllowed(t *testing.T) {
	if err := models.Transition(models.StatusDrafting, models.StatusCompleted); err != nil {
		t.Fatalf("无需修改分支应允许 drafting -> completed：%v", err)
	}
}

// TestDraftingToCompletedNotReachableFromAwaitingApproval 验证`draftingToCompletedNotReachableFromAwaitingApproval`在特定边界条件下的行为，防止同类回归。
func TestDraftingToCompletedNotReachableFromAwaitingApproval(t *testing.T) {
	if err := models.Transition(models.StatusAwaitingApproval, models.StatusCompleted); err == nil {
		t.Fatal("awaiting_approval 不应直接转换为 completed")
	}
}
