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
