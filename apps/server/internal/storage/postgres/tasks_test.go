package postgres

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
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
	if len(listedTasks) < 2 {
		t.Fatalf("expected at least 2 tasks, got %d", len(listedTasks))
	}
	if listedTasks[0].ID != secondTask.ID {
		t.Fatalf("expected latest task %q first, got %q", secondTask.ID, listedTasks[0].ID)
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

	approval, err := approvalRepo.Create(ctx, task.ID)
	if err != nil {
		t.Fatalf("create approval: %v", err)
	}
	if approval.TaskID != task.ID {
		t.Fatalf("expected task id %q, got %q", task.ID, approval.TaskID)
	}
	if approval.Status != "pending" {
		t.Fatalf("expected approval status %q, got %q", "pending", approval.Status)
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

	firstApproval, err := approvalRepo.Create(ctx, firstTask.ID)
	if err != nil {
		t.Fatalf("create first approval: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	secondTask, err := taskRepo.Create(ctx, resource.ID, "请审批第二份修订")
	if err != nil {
		t.Fatalf("create second task: %v", err)
	}

	secondApproval, err := approvalRepo.Create(ctx, secondTask.ID)
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
	firstApproval, err := approvalRepo.Create(ctx, firstTask.ID)
	if err != nil {
		t.Fatalf("create first approval: %v", err)
	}
	firstJob, err := jobRepo.Create(ctx, firstTask.ID, firstApproval.ID)
	if err != nil {
		t.Fatalf("create first job: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	secondTask, err := taskRepo.Create(ctx, resource.ID, "执行第二份修订")
	if err != nil {
		t.Fatalf("create second task: %v", err)
	}
	secondApproval, err := approvalRepo.Create(ctx, secondTask.ID)
	if err != nil {
		t.Fatalf("create second approval: %v", err)
	}
	secondJob, err := jobRepo.Create(ctx, secondTask.ID, secondApproval.ID)
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
