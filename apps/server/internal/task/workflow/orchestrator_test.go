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

type fakePlannerAgent struct{}

func (fakePlannerAgent) Plan(context.Context, string, string, string) (*planner.PlanResult, error) {
	return &planner.PlanResult{
		Intent:        "审阅考勤条款",
		SearchQueries: []string{"考勤"},
		FocusSections: []string{"考勤"},
	}, nil
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
