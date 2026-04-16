package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestTaskRepoCRUD(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "任务仓储测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	firstTask, err := taskRepo.Create(ctx, resource.ID, "请修订第一章")
	if err != nil {
		t.Fatalf("create first task: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	secondTask, err := taskRepo.Create(ctx, resource.ID, "请修订第二章")
	if err != nil {
		t.Fatalf("create second task: %v", err)
	}

	gotTask, err := taskRepo.GetByID(ctx, firstTask.ID)
	if err != nil {
		t.Fatalf("get task by id: %v", err)
	}
	if gotTask == nil {
		t.Fatal("expected task, got nil")
	}
	if gotTask.Instruction != firstTask.Instruction {
		t.Fatalf("expected instruction %q, got %q", firstTask.Instruction, gotTask.Instruction)
	}
	if gotTask.Status != "pending" {
		t.Fatalf("expected initial status %q, got %q", "pending", gotTask.Status)
	}

	listedTasks, err := taskRepo.List(ctx)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	secondTaskIndex := slices.IndexFunc(listedTasks, func(task Task) bool {
		return task.ID == secondTask.ID
	})
	firstTaskIndex := slices.IndexFunc(listedTasks, func(task Task) bool {
		return task.ID == firstTask.ID
	})
	if secondTaskIndex == -1 || firstTaskIndex == -1 {
		t.Fatalf("expected task list to contain created tasks %q and %q, got %d tasks", secondTask.ID, firstTask.ID, len(listedTasks))
	}
	if secondTaskIndex > firstTaskIndex {
		t.Fatalf("expected newer task %q to appear before older task %q, got indexes [%d %d]", secondTask.ID, firstTask.ID, secondTaskIndex, firstTaskIndex)
	}

	errorMessage := "planner failed"
	if err := taskRepo.UpdateStatus(ctx, firstTask.ID, "failed", &errorMessage); err != nil {
		t.Fatalf("update task status: %v", err)
	}

	updatedTask, err := taskRepo.GetByID(ctx, firstTask.ID)
	if err != nil {
		t.Fatalf("get updated task: %v", err)
	}
	if updatedTask == nil {
		t.Fatal("expected updated task, got nil")
	}
	if updatedTask.Status != "failed" {
		t.Fatalf("expected updated status %q, got %q", "failed", updatedTask.Status)
	}
	if updatedTask.ErrorMessage == nil || *updatedTask.ErrorMessage != errorMessage {
		t.Fatalf("expected error message %q, got %#v", errorMessage, updatedTask.ErrorMessage)
	}
	if !updatedTask.UpdatedAt.After(firstTask.UpdatedAt) {
		t.Fatalf("expected updated_at to move forward, before=%v after=%v", firstTask.UpdatedAt, updatedTask.UpdatedAt)
	}

	step, err := taskRepo.AddStep(ctx, firstTask.ID, "planner")
	if err != nil {
		t.Fatalf("add step: %v", err)
	}
	if step.Status != "running" {
		t.Fatalf("expected step status %q, got %q", "running", step.Status)
	}
	if step.StartedAt == nil || step.StartedAt.IsZero() {
		t.Fatal("expected started_at to be set")
	}

	if err := taskRepo.UpdateStep(ctx, step.ID, "completed", nil); err != nil {
		t.Fatalf("update step: %v", err)
	}

	steps, err := taskRepo.GetSteps(ctx, firstTask.ID)
	if err != nil {
		t.Fatalf("get steps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Status != "completed" {
		t.Fatalf("expected step status %q, got %q", "completed", steps[0].Status)
	}
	if steps[0].CompletedAt == nil || steps[0].CompletedAt.IsZero() {
		t.Fatal("expected completed_at to be set")
	}

	citationsJSON := []byte(`[{"citation_id":"cite_1","resource_id":"` + resource.ID + `","section_title":"第一章","snippet":"原文"}]`)
	diffPreviewJSON := []byte(`{"sections":[{"section_title":"第一章","original":"原文","revised":"修订文","reason":"补充分级定义","citation_ids":["cite_1"]}]}`)

	firstArtifact, err := taskRepo.AddArtifact(ctx, firstTask.ID, "citations", citationsJSON)
	if err != nil {
		t.Fatalf("add first artifact: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	secondArtifact, err := taskRepo.AddArtifact(ctx, firstTask.ID, "diff_preview", diffPreviewJSON)
	if err != nil {
		t.Fatalf("add second artifact: %v", err)
	}

	artifacts, err := taskRepo.GetArtifacts(ctx, firstTask.ID)
	if err != nil {
		t.Fatalf("get artifacts: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(artifacts))
	}
	if artifacts[0].ID != firstArtifact.ID {
		t.Fatalf("expected first artifact id %q, got %q", firstArtifact.ID, artifacts[0].ID)
	}
	if artifacts[1].ID != secondArtifact.ID {
		t.Fatalf("expected second artifact id %q, got %q", secondArtifact.ID, artifacts[1].ID)
	}
	if !jsonEqual(artifacts[0].Content, citationsJSON) {
		t.Fatalf("expected citations artifact content %s, got %s", citationsJSON, artifacts[0].Content)
	}
	if !jsonEqual(artifacts[1].Content, diffPreviewJSON) {
		t.Fatalf("expected diff artifact content %s, got %s", diffPreviewJSON, artifacts[1].Content)
	}
}

func TestTaskRepoListOrdersByCreatedAtThenIDDescWhenTimestampsTie(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "任务列表排序稳定性测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	fixedCreatedAt := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
	firstTaskID := "00000000-0000-0000-0000-000000000001"
	secondTaskID := "00000000-0000-0000-0000-000000000002"
	cleanupFixedTaskIDs := func() {
		if _, cleanupErr := pool.Exec(testContext(t), `DELETE FROM tasks WHERE id = ANY($1)`, []string{firstTaskID, secondTaskID}); cleanupErr != nil {
			t.Fatalf("cleanup fixed task ids: %v", cleanupErr)
		}
	}
	cleanupFixedTaskIDs()
	t.Cleanup(cleanupFixedTaskIDs)
	if _, err := pool.Exec(ctx, `
		INSERT INTO tasks (id, resource_id, instruction, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'pending', $4, $4), ($5, $2, $6, 'pending', $4, $4)
	`, firstTaskID, resource.ID, "第一条任务", fixedCreatedAt, secondTaskID, "第二条任务"); err != nil {
		t.Fatalf("insert tasks with fixed ids: %v", err)
	}

	listedTasks, err := taskRepo.List(ctx)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(listedTasks) < 2 {
		t.Fatalf("expected at least 2 tasks, got %d", len(listedTasks))
	}

	wantOrder := []string{firstTaskID, secondTaskID}
	slices.SortFunc(wantOrder, func(left string, right string) int {
		switch {
		case left > right:
			return -1
		case left < right:
			return 1
		default:
			return 0
		}
	})

	if listedTasks[0].ID != wantOrder[0] || listedTasks[1].ID != wantOrder[1] {
		t.Fatalf("expected tasks ordered by id desc when created_at ties, got [%s %s], want [%s %s]", listedTasks[0].ID, listedTasks[1].ID, wantOrder[0], wantOrder[1])
	}
}

func TestTaskRepoCreateFromAssistantSuggestionReturnsExistingTaskOnDuplicate(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "助手建议幂等测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	assistantRepo := NewAssistantRepo(pool)
	session, messages, err := assistantRepo.CreateSessionWithMessages(ctx, "助手建议幂等测试", []AssistantMessageInput{
		{
			Role:    "assistant",
			Kind:    "task_suggestion",
			Payload: []byte(`{"instruction":"请修订第二章"}`),
		},
	})
	if err != nil {
		t.Fatalf("create assistant session with suggestion message: %v", err)
	}
	t.Cleanup(func() {
		if _, cleanupErr := pool.Exec(testContext(t), `DELETE FROM assistant_sessions WHERE id = $1`, session.ID); cleanupErr != nil {
			t.Fatalf("cleanup assistant session %q: %v", session.ID, cleanupErr)
		}
	})
	if len(messages) != 1 {
		t.Fatalf("expected 1 assistant suggestion message, got %d", len(messages))
	}
	sourceMessageID := messages[0].ID

	createFromSuggestionRepo, ok := any(taskRepo).(interface {
		CreateFromAssistantSuggestion(context.Context, string, string, string) (*Task, bool, error)
	})
	if !ok {
		t.Fatal("task repo does not implement assistant suggestion idempotency contract")
	}

	firstTask, firstCreated, err := createFromSuggestionRepo.CreateFromAssistantSuggestion(ctx, resource.ID, "请修订第二章", sourceMessageID)
	if err != nil {
		t.Fatalf("create task from assistant suggestion first time: %v", err)
	}
	if !firstCreated {
		t.Fatal("expected first create-from-suggestion call to create a new task")
	}

	secondTask, secondCreated, err := createFromSuggestionRepo.CreateFromAssistantSuggestion(ctx, resource.ID, "请修订第二章", sourceMessageID)
	if err != nil {
		t.Fatalf("create task from assistant suggestion second time: %v", err)
	}
	if secondCreated {
		t.Fatal("expected duplicate create-from-suggestion call to reuse existing task")
	}
	if secondTask == nil || firstTask == nil || secondTask.ID != firstTask.ID {
		t.Fatalf("expected duplicate create to return existing task %q, got first=%#v second=%#v", firstTask.ID, firstTask, secondTask)
	}

	rows, err := pool.Query(ctx, `
		SELECT id
		FROM tasks
		WHERE source_message_id = $1
	`, sourceMessageID)
	if err != nil {
		t.Fatalf("query tasks by source_message_id: %v", err)
	}
	defer rows.Close()

	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect task ids by source_message_id: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 task for duplicate suggestion, got %d (%v)", len(ids), ids)
	}
}

func TestTaskRepoGetStepsAndArtifactsOrderByCreatedAtThenIDAscWhenTimestampsTie(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "任务步骤与产物排序稳定性测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "验证步骤与产物排序")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	firstStep, err := taskRepo.AddStep(ctx, task.ID, "planner")
	if err != nil {
		t.Fatalf("add first step: %v", err)
	}
	secondStep, err := taskRepo.AddStep(ctx, task.ID, "reviewer")
	if err != nil {
		t.Fatalf("add second step: %v", err)
	}

	firstArtifact, err := taskRepo.AddArtifact(ctx, task.ID, "review_summary", []byte(`{"summary":"first"}`))
	if err != nil {
		t.Fatalf("add first artifact: %v", err)
	}
	secondArtifact, err := taskRepo.AddArtifact(ctx, task.ID, "diff_preview", []byte(`{"summary":"second"}`))
	if err != nil {
		t.Fatalf("add second artifact: %v", err)
	}

	fixedCreatedAt := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `UPDATE task_steps SET created_at = $2 WHERE id = ANY($1)`, []string{firstStep.ID, secondStep.ID}, fixedCreatedAt); err != nil {
		t.Fatalf("pin task_steps created_at: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE task_artifacts SET created_at = $2 WHERE id = ANY($1)`, []string{firstArtifact.ID, secondArtifact.ID}, fixedCreatedAt); err != nil {
		t.Fatalf("pin task_artifacts created_at: %v", err)
	}

	steps, err := taskRepo.GetSteps(ctx, task.ID)
	if err != nil {
		t.Fatalf("get steps: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}

	wantStepOrder := []string{firstStep.ID, secondStep.ID}
	slices.Sort(wantStepOrder)
	if steps[0].ID != wantStepOrder[0] || steps[1].ID != wantStepOrder[1] {
		t.Fatalf("expected steps ordered by id asc when created_at ties, got [%s %s], want [%s %s]", steps[0].ID, steps[1].ID, wantStepOrder[0], wantStepOrder[1])
	}

	artifacts, err := taskRepo.GetArtifacts(ctx, task.ID)
	if err != nil {
		t.Fatalf("get artifacts: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(artifacts))
	}

	wantArtifactOrder := []string{firstArtifact.ID, secondArtifact.ID}
	slices.Sort(wantArtifactOrder)
	if artifacts[0].ID != wantArtifactOrder[0] || artifacts[1].ID != wantArtifactOrder[1] {
		t.Fatalf("expected artifacts ordered by id asc when created_at ties, got [%s %s], want [%s %s]", artifacts[0].ID, artifacts[1].ID, wantArtifactOrder[0], wantArtifactOrder[1])
	}
}

func TestApprovalRepoCreate(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "审批仓储测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "请生成审批记录")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原始正文", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET status = 'drafting' WHERE id = $1`, task.ID); err != nil {
		t.Fatalf("set task to drafting: %v", err)
	}

	approval, err := approvalRepo.CreateForTaskAwaitingApproval(ctx, task.ID, version.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if approval.TaskID != task.ID {
		t.Fatalf("expected task id %q, got %q", task.ID, approval.TaskID)
	}
	if approval.Status != "pending" {
		t.Fatalf("expected approval status %q, got %q", "pending", approval.Status)
	}
	if approval.BaseVersionID == nil || *approval.BaseVersionID != version.ID {
		t.Fatalf("expected base version %q, got %#v", version.ID, approval.BaseVersionID)
	}
	if approval.RejectReason != nil {
		t.Fatalf("expected nil reject reason, got %#v", approval.RejectReason)
	}
	if approval.DecidedAt != nil {
		t.Fatalf("expected nil decided_at, got %#v", approval.DecidedAt)
	}
}

func TestApprovalRepoReadListAndUpdateStatus(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "审批扩展测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	firstTask, err := taskRepo.Create(ctx, resource.ID, "请审批第一份修订")
	if err != nil {
		t.Fatalf("create first task: %v", err)
	}
	firstVersion, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n第一份原始正文", "original")
	if err != nil {
		t.Fatalf("create first version: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET status = 'drafting' WHERE id = $1`, firstTask.ID); err != nil {
		t.Fatalf("set first task to drafting: %v", err)
	}

	firstApproval, err := approvalRepo.CreateForTaskAwaitingApproval(ctx, firstTask.ID, firstVersion.ID)
	if err != nil {
		t.Fatalf("create first approval: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	secondTask, err := taskRepo.Create(ctx, resource.ID, "请审批第二份修订")
	if err != nil {
		t.Fatalf("create second task: %v", err)
	}
	secondVersion, err := resourceRepo.CreateVersion(ctx, resource.ID, 2, "## 第一章\n第二份原始正文", "original")
	if err != nil {
		t.Fatalf("create second version: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET status = 'drafting' WHERE id = $1`, secondTask.ID); err != nil {
		t.Fatalf("set second task to drafting: %v", err)
	}

	secondApproval, err := approvalRepo.CreateForTaskAwaitingApproval(ctx, secondTask.ID, secondVersion.ID)
	if err != nil {
		t.Fatalf("create second approval: %v", err)
	}

	gotByID, err := approvalRepo.GetByID(ctx, firstApproval.ID)
	if err != nil {
		t.Fatalf("get approval by id: %v", err)
	}
	if gotByID == nil {
		t.Fatal("expected approval, got nil")
	}
	if gotByID.TaskID != firstTask.ID {
		t.Fatalf("expected task id %q, got %q", firstTask.ID, gotByID.TaskID)
	}

	gotByTaskID, err := approvalRepo.GetByTaskID(ctx, secondTask.ID)
	if err != nil {
		t.Fatalf("get approval by task id: %v", err)
	}
	if gotByTaskID == nil {
		t.Fatal("expected approval by task id, got nil")
	}
	if gotByTaskID.ID != secondApproval.ID {
		t.Fatalf("expected approval id %q, got %q", secondApproval.ID, gotByTaskID.ID)
	}

	rejectReason := "方案需要补充证据"
	if err := approvalRepo.UpdateStatus(ctx, firstApproval.ID, "rejected", &rejectReason); err != nil {
		t.Fatalf("update approval status: %v", err)
	}

	updatedApproval, err := approvalRepo.GetByID(ctx, firstApproval.ID)
	if err != nil {
		t.Fatalf("get updated approval: %v", err)
	}
	if updatedApproval == nil {
		t.Fatal("expected updated approval, got nil")
	}
	if updatedApproval.Status != "rejected" {
		t.Fatalf("expected status %q, got %q", "rejected", updatedApproval.Status)
	}
	if updatedApproval.RejectReason == nil || *updatedApproval.RejectReason != rejectReason {
		t.Fatalf("expected reject reason %q, got %#v", rejectReason, updatedApproval.RejectReason)
	}
	if updatedApproval.DecidedAt == nil || updatedApproval.DecidedAt.IsZero() {
		t.Fatal("expected decided_at to be set")
	}

	testApprovalIDs := map[string]struct{}{
		firstApproval.ID:  {},
		secondApproval.ID: {},
	}

	allApprovals, err := approvalRepo.List(ctx, "")
	if err != nil {
		t.Fatalf("list approvals: %v", err)
	}

	filteredApprovals := make([]Approval, 0, len(testApprovalIDs))
	for _, approval := range allApprovals {
		if _, ok := testApprovalIDs[approval.ID]; !ok {
			continue
		}

		filteredApprovals = append(filteredApprovals, approval)
	}
	if len(filteredApprovals) != 2 {
		t.Fatalf("expected 2 approvals in test set, got %d", len(filteredApprovals))
	}
	if filteredApprovals[0].ID != secondApproval.ID {
		t.Fatalf("expected latest approval %q first in test set, got %q", secondApproval.ID, filteredApprovals[0].ID)
	}

	rejectedApprovals, err := approvalRepo.List(ctx, "rejected")
	if err != nil {
		t.Fatalf("list rejected approvals: %v", err)
	}

	filteredRejectedApprovals := make([]Approval, 0, len(testApprovalIDs))
	for _, approval := range rejectedApprovals {
		if _, ok := testApprovalIDs[approval.ID]; !ok {
			continue
		}

		filteredRejectedApprovals = append(filteredRejectedApprovals, approval)
	}
	if len(filteredRejectedApprovals) != 1 {
		t.Fatalf("expected 1 rejected approval in test set, got %d", len(filteredRejectedApprovals))
	}
	if filteredRejectedApprovals[0].ID != firstApproval.ID {
		t.Fatalf("expected rejected approval id %q, got %q", firstApproval.ID, filteredRejectedApprovals[0].ID)
	}
}

func TestJobRepoCRUD(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	approvalRepo := NewApprovalRepo(pool)
	jobRepo := NewJobRepo(pool)
	ctx := testContext(t)

	resource, err := resourceRepo.Create(ctx, "执行作业测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	version, err := resourceRepo.CreateVersion(ctx, resource.ID, 1, "## 第一章\n原文内容", "original")
	if err != nil {
		t.Fatalf("create version: %v", err)
	}

	firstTask, err := taskRepo.Create(ctx, resource.ID, "执行第一份修订")
	if err != nil {
		t.Fatalf("create first task: %v", err)
	}
	firstApproval, err := approvalRepo.Create(ctx, firstTask.ID, version.ID)
	if err != nil {
		t.Fatalf("create first approval: %v", err)
	}
	firstJob, err := jobRepo.Create(ctx, firstTask.ID, firstApproval.ID, version.ID)
	if err != nil {
		t.Fatalf("create first job: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	secondTask, err := taskRepo.Create(ctx, resource.ID, "执行第二份修订")
	if err != nil {
		t.Fatalf("create second task: %v", err)
	}
	secondApproval, err := approvalRepo.Create(ctx, secondTask.ID, version.ID)
	if err != nil {
		t.Fatalf("create second approval: %v", err)
	}
	secondJob, err := jobRepo.Create(ctx, secondTask.ID, secondApproval.ID, version.ID)
	if err != nil {
		t.Fatalf("create second job: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE execution_jobs
		SET created_at = CASE
			WHEN id = $1 THEN TIMESTAMP '2000-01-01 00:00:00'
			WHEN id = $2 THEN TIMESTAMP '2000-01-01 00:00:01'
			ELSE created_at
		END
		WHERE id IN ($1, $2)
	`, firstJob.ID, secondJob.ID); err != nil {
		t.Fatalf("stabilize job ordering: %v", err)
	}

	gotJob, err := jobRepo.GetByID(ctx, firstJob.ID)
	if err != nil {
		t.Fatalf("get job by id: %v", err)
	}
	if gotJob == nil {
		t.Fatal("expected job, got nil")
	}
	if gotJob.TaskID != firstTask.ID {
		t.Fatalf("expected task id %q, got %q", firstTask.ID, gotJob.TaskID)
	}
	if gotJob.Status != "pending" {
		t.Fatalf("expected status %q, got %q", "pending", gotJob.Status)
	}

	claimedJob, err := jobRepo.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("claim next job: %v", err)
	}
	if claimedJob == nil {
		t.Fatal("expected claimed job, got nil")
	}
	if claimedJob.ID != firstJob.ID {
		t.Fatalf("expected earliest job %q, got %q", firstJob.ID, claimedJob.ID)
	}
	if claimedJob.Status != "running" {
		t.Fatalf("expected running status, got %q", claimedJob.Status)
	}
	if claimedJob.StartedAt == nil || claimedJob.StartedAt.IsZero() {
		t.Fatal("expected started_at to be set")
	}

	newVersionID := version.ID
	if err := jobRepo.UpdateStatus(ctx, claimedJob.ID, "done", nil, &newVersionID); err != nil {
		t.Fatalf("update claimed job status: %v", err)
	}

	updatedJob, err := jobRepo.GetByID(ctx, claimedJob.ID)
	if err != nil {
		t.Fatalf("get updated job: %v", err)
	}
	if updatedJob == nil {
		t.Fatal("expected updated job, got nil")
	}
	if updatedJob.Status != "done" {
		t.Fatalf("expected status %q, got %q", "done", updatedJob.Status)
	}
	if updatedJob.NewVersionID == nil || *updatedJob.NewVersionID != version.ID {
		t.Fatalf("expected new version id %q, got %#v", version.ID, updatedJob.NewVersionID)
	}
	if updatedJob.CompletedAt == nil || updatedJob.CompletedAt.IsZero() {
		t.Fatal("expected completed_at to be set")
	}

	secondClaimedJob, err := jobRepo.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("claim second job: %v", err)
	}
	if secondClaimedJob == nil {
		t.Fatal("expected second claimed job, got nil")
	}
	if secondClaimedJob.ID != secondJob.ID {
		t.Fatalf("expected second job %q, got %q", secondJob.ID, secondClaimedJob.ID)
	}

	errMsg := "executor failed"
	if err := jobRepo.UpdateStatus(ctx, secondClaimedJob.ID, "failed", &errMsg, nil); err != nil {
		t.Fatalf("update failed job status: %v", err)
	}

	noPendingJob, err := jobRepo.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("claim when no pending job: %v", err)
	}
	if noPendingJob != nil && (noPendingJob.ID == firstJob.ID || noPendingJob.ID == secondJob.ID) {
		t.Fatalf("expected test jobs to be consumed, got %#v", noPendingJob)
	}
}

func jsonEqual(left []byte, right []byte) bool {
	leftNormalized := normalizeJSON(left)
	rightNormalized := normalizeJSON(right)
	return bytes.Equal(leftNormalized, rightNormalized)
}

func normalizeJSON(input []byte) []byte {
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return nil
	}

	normalized, err := json.Marshal(value)
	if err != nil {
		return nil
	}

	return normalized
}
