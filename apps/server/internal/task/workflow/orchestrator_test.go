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
				SectionTitle: "考勤",
				Original:     "原始条款",
				Revised:      "明确后的考勤条款",
				Reason:       "减少歧义",
				CitationIDs:  []string{"cite_1"},
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

func workflowCleanupResource(t *testing.T, pool *pgxpool.Pool, resourceID string) {
	t.Helper()

	ctx := workflowTestContext(t)
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
