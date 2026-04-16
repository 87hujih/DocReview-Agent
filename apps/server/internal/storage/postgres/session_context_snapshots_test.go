package postgres

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSessionContextSnapshotRepoCreateEmptyAndGetBySessionID(t *testing.T) {
	pool := newTestPool(t)
	assistantRepo := NewAssistantRepo(pool)
	snapshotRepo := NewSessionContextSnapshotRepo(pool)
	ctx := testContext(t)

	session, _, err := assistantRepo.CreateSessionWithMessages(ctx, "快照空会话", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup session: %v", err)
		}
	})

	got, err := snapshotRepo.CreateEmpty(ctx, session.ID)
	if err != nil {
		t.Fatalf("create empty snapshot: %v", err)
	}
	assertEmptySessionContextSnapshot(t, got, session.ID)

	gotAgain, err := snapshotRepo.CreateEmpty(ctx, session.ID)
	if err != nil {
		t.Fatalf("create empty snapshot idempotently: %v", err)
	}
	assertEmptySessionContextSnapshot(t, gotAgain, session.ID)

	loaded, err := snapshotRepo.GetBySessionID(ctx, session.ID)
	if err != nil {
		t.Fatalf("get snapshot by session id: %v", err)
	}
	assertEmptySessionContextSnapshot(t, loaded, session.ID)
}

func TestSessionContextSnapshotRepoUpsertActiveResourceAndPendingTaskSuggestion(t *testing.T) {
	pool := newTestPool(t)
	assistantRepo := NewAssistantRepo(pool)
	resourceRepo := NewResourceRepo(pool)
	snapshotRepo := NewSessionContextSnapshotRepo(pool)
	ctx := testContext(t)

	session, _, err := assistantRepo.CreateSessionWithMessages(ctx, "快照活跃资源", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup session: %v", err)
		}
	})

	firstResource, err := resourceRepo.Create(ctx, "学生守则 v1", "upload")
	if err != nil {
		t.Fatalf("create first resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, firstResource.ID)
	})

	secondResource, err := resourceRepo.Create(ctx, "学生守则 v2", "upload")
	if err != nil {
		t.Fatalf("create second resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, secondResource.ID)
	})

	firstSessionFileMessage := appendAssistantMessage(t, ctx, assistantRepo, session.ID, "assistant", "session_file", map[string]any{
		"file_name":       "students-v1.md",
		"resource_id":     firstResource.ID,
		"resource_title":  firstResource.Title,
		"source_type":     firstResource.SourceType,
		"status":          "ready",
		"resource_label":  firstResource.Title,
		"status_message":  "资源已入库",
		"original_name":   "students-v1.md",
		"resource_source": firstResource.SourceType,
	})
	if err := snapshotRepo.UpsertActiveResource(ctx, UpsertActiveResourceParams{
		SessionID:       session.ID,
		ResourceID:      firstResource.ID,
		ResourceTitle:   firstResource.Title,
		ResourceSource:  firstResource.SourceType,
		SourceMessageID: &firstSessionFileMessage.ID,
	}); err != nil {
		t.Fatalf("upsert first active resource: %v", err)
	}

	secondSessionFileMessage := appendAssistantMessage(t, ctx, assistantRepo, session.ID, "assistant", "session_file", map[string]any{
		"file_name":       "students-v2.md",
		"resource_id":     secondResource.ID,
		"resource_title":  secondResource.Title,
		"source_type":     secondResource.SourceType,
		"status":          "ready",
		"resource_label":  secondResource.Title,
		"status_message":  "资源已切换",
		"original_name":   "students-v2.md",
		"resource_source": secondResource.SourceType,
	})
	if err := snapshotRepo.UpsertActiveResource(ctx, UpsertActiveResourceParams{
		SessionID:       session.ID,
		ResourceID:      secondResource.ID,
		ResourceTitle:   secondResource.Title,
		ResourceSource:  secondResource.SourceType,
		SourceMessageID: &secondSessionFileMessage.ID,
	}); err != nil {
		t.Fatalf("upsert second active resource: %v", err)
	}

	firstSuggestion := appendAssistantMessage(t, ctx, assistantRepo, session.ID, "assistant", "task_suggestion", map[string]any{
		"title":          "建议创建任务",
		"instruction":    "先整理第一版资源",
		"can_create":     true,
		"action_label":   "确认创建任务",
		"resource_id":    secondResource.ID,
		"resource_label": secondResource.Title,
		"status_message": "资源已明确，可以创建任务。",
	})
	if err := snapshotRepo.UpsertPendingTaskSuggestion(ctx, UpsertPendingTaskSuggestionParams{
		SessionID: session.ID,
		MessageID: firstSuggestion.ID,
		Instruction: "先整理第一版资源",
	}); err != nil {
		t.Fatalf("upsert first pending suggestion: %v", err)
	}

	secondSuggestion := appendAssistantMessage(t, ctx, assistantRepo, session.ID, "assistant", "task_suggestion", map[string]any{
		"title":          "建议创建任务",
		"instruction":    "改成整理第二版资源",
		"can_create":     true,
		"action_label":   "确认创建任务",
		"resource_id":    secondResource.ID,
		"resource_label": secondResource.Title,
		"status_message": "资源已切换到第二版。",
	})
	if err := snapshotRepo.UpsertPendingTaskSuggestion(ctx, UpsertPendingTaskSuggestionParams{
		SessionID:    session.ID,
		MessageID:    secondSuggestion.ID,
		Instruction:  "改成整理第二版资源",
	}); err != nil {
		t.Fatalf("upsert second pending suggestion: %v", err)
	}

	loaded, err := snapshotRepo.GetBySessionID(ctx, session.ID)
	if err != nil {
		t.Fatalf("get snapshot after upserts: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected snapshot after upserts")
	}
	if loaded.ActiveResourceID == nil || *loaded.ActiveResourceID != secondResource.ID {
		t.Fatalf("expected active_resource_id %q, got %#v", secondResource.ID, loaded.ActiveResourceID)
	}
	if loaded.ActiveResourceTitle == nil || *loaded.ActiveResourceTitle != secondResource.Title {
		t.Fatalf("expected active_resource_title %q, got %#v", secondResource.Title, loaded.ActiveResourceTitle)
	}
	if loaded.ActiveResourceSourceType == nil || *loaded.ActiveResourceSourceType != secondResource.SourceType {
		t.Fatalf("expected active_resource_source_type %q, got %#v", secondResource.SourceType, loaded.ActiveResourceSourceType)
	}
	if loaded.ActiveResourceSourceMessageID == nil || *loaded.ActiveResourceSourceMessageID != secondSessionFileMessage.ID {
		t.Fatalf("expected active_resource_source_message_id %q, got %#v", secondSessionFileMessage.ID, loaded.ActiveResourceSourceMessageID)
	}
	if loaded.PendingTaskSuggestionMessageID == nil || *loaded.PendingTaskSuggestionMessageID != secondSuggestion.ID {
		t.Fatalf("expected pending_task_suggestion_message_id %q, got %#v", secondSuggestion.ID, loaded.PendingTaskSuggestionMessageID)
	}
	if loaded.PendingTaskInstruction == nil || *loaded.PendingTaskInstruction != "改成整理第二版资源" {
		t.Fatalf("expected pending_task_instruction to be overwritten, got %#v", loaded.PendingTaskInstruction)
	}
}

func TestSessionContextSnapshotRepoUpsertLatestTaskAndClearPendingTaskSuggestion(t *testing.T) {
	pool := newTestPool(t)
	assistantRepo := NewAssistantRepo(pool)
	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	snapshotRepo := NewSessionContextSnapshotRepo(pool)
	ctx := testContext(t)

	session, _, err := assistantRepo.CreateSessionWithMessages(ctx, "快照任务状态", nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup session: %v", err)
		}
	})

	resource, err := resourceRepo.Create(ctx, "任务快照资源", "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	suggestionMessage := appendAssistantMessage(t, ctx, assistantRepo, session.ID, "assistant", "task_suggestion", map[string]any{
		"title":          "建议创建任务",
		"instruction":    "请整理任务快照",
		"can_create":     true,
		"action_label":   "确认创建任务",
		"resource_id":    resource.ID,
		"resource_label": resource.Title,
		"status_message": "资源已明确，可以创建任务。",
	})
	if err := snapshotRepo.UpsertPendingTaskSuggestion(ctx, UpsertPendingTaskSuggestionParams{
		SessionID:   session.ID,
		MessageID:   suggestionMessage.ID,
		Instruction: "请整理任务快照",
	}); err != nil {
		t.Fatalf("seed pending suggestion: %v", err)
	}

	task, created, err := taskRepo.CreateFromAssistantSuggestion(ctx, resource.ID, "请整理任务快照", suggestionMessage.ID)
	if err != nil {
		t.Fatalf("create task from suggestion: %v", err)
	}
	if !created {
		t.Fatal("expected first task creation to report created=true")
	}

	if err := snapshotRepo.UpsertLatestTask(ctx, UpsertLatestTaskParams{
		SessionID:       session.ID,
		TaskID:          task.ID,
		Status:          task.Status,
		SourceMessageID: &suggestionMessage.ID,
	}); err != nil {
		t.Fatalf("upsert latest task: %v", err)
	}

	expectedConstraints := []map[string]string{
		{"label": "输出语言", "value": "中文"},
	}
	if err := snapshotRepo.UpdateConfirmedConstraints(ctx, session.ID, expectedConstraints); err != nil {
		t.Fatalf("update confirmed constraints: %v", err)
	}

	rollingSummary := "当前已确认中文输出，等待进入执行阶段。"
	if err := snapshotRepo.UpdateRollingSummary(ctx, session.ID, &rollingSummary, 6); err != nil {
		t.Fatalf("update rolling summary: %v", err)
	}

	if err := snapshotRepo.ClearPendingTaskSuggestion(ctx, session.ID); err != nil {
		t.Fatalf("clear pending suggestion: %v", err)
	}

	loaded, err := snapshotRepo.GetBySessionID(ctx, session.ID)
	if err != nil {
		t.Fatalf("get snapshot after latest task upsert: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected snapshot after latest task upsert")
	}
	if loaded.PendingTaskSuggestionMessageID != nil {
		t.Fatalf("expected pending_task_suggestion_message_id to be cleared, got %#v", loaded.PendingTaskSuggestionMessageID)
	}
	if loaded.PendingTaskInstruction != nil {
		t.Fatalf("expected pending_task_instruction to be cleared, got %#v", loaded.PendingTaskInstruction)
	}
	if loaded.LatestTaskID == nil || *loaded.LatestTaskID != task.ID {
		t.Fatalf("expected latest_task_id %q, got %#v", task.ID, loaded.LatestTaskID)
	}
	if loaded.LatestTaskStatus == nil || *loaded.LatestTaskStatus != task.Status {
		t.Fatalf("expected latest_task_status %q, got %#v", task.Status, loaded.LatestTaskStatus)
	}
	if loaded.LatestTaskSourceMessageID == nil || *loaded.LatestTaskSourceMessageID != suggestionMessage.ID {
		t.Fatalf("expected latest_task_source_message_id %q, got %#v", suggestionMessage.ID, loaded.LatestTaskSourceMessageID)
	}

	var gotConstraints []map[string]string
	if err := json.Unmarshal(loaded.ConfirmedConstraintsJSON, &gotConstraints); err != nil {
		t.Fatalf("unmarshal confirmed_constraints_json: %v", err)
	}
	if len(gotConstraints) != len(expectedConstraints) {
		t.Fatalf("expected %d confirmed constraints, got %d", len(expectedConstraints), len(gotConstraints))
	}
	if gotConstraints[0]["label"] != expectedConstraints[0]["label"] || gotConstraints[0]["value"] != expectedConstraints[0]["value"] {
		t.Fatalf("expected confirmed constraint %#v, got %#v", expectedConstraints[0], gotConstraints[0])
	}
	if loaded.RollingSummary == nil || *loaded.RollingSummary != rollingSummary {
		t.Fatalf("expected rolling_summary %q, got %#v", rollingSummary, loaded.RollingSummary)
	}
	if loaded.SummaryBaseSequenceNo != 6 {
		t.Fatalf("expected summary_base_sequence_no %d, got %d", 6, loaded.SummaryBaseSequenceNo)
	}
}

func appendAssistantMessage(
	t *testing.T,
	ctx context.Context,
	repo *AssistantRepo,
	sessionID string,
	role string,
	kind string,
	payload any,
) AssistantMessage {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s payload: %v", kind, err)
	}

	messages, err := repo.AppendMessages(ctx, sessionID, []AssistantMessageInput{{
		Role:    role,
		Kind:    kind,
		Payload: body,
	}})
	if err != nil {
		t.Fatalf("append %s message: %v", kind, err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected one appended %s message, got %d", kind, len(messages))
	}

	return messages[0]
}

func assertEmptySessionContextSnapshot(t *testing.T, snapshot *SessionContextSnapshotRecord, sessionID string) {
	t.Helper()

	if snapshot == nil {
		t.Fatal("expected snapshot, got nil")
	}
	if snapshot.SessionID != sessionID {
		t.Fatalf("expected session_id %q, got %q", sessionID, snapshot.SessionID)
	}
	if snapshot.ActiveResourceID != nil || snapshot.ActiveResourceTitle != nil || snapshot.ActiveResourceSourceType != nil {
		t.Fatalf("expected active resource fields to be empty, got %#v", snapshot)
	}
	if snapshot.PendingTaskSuggestionMessageID != nil || snapshot.PendingTaskInstruction != nil {
		t.Fatalf("expected pending suggestion fields to be empty, got %#v", snapshot)
	}
	if snapshot.LatestTaskID != nil || snapshot.LatestTaskStatus != nil {
		t.Fatalf("expected latest task fields to be empty, got %#v", snapshot)
	}
	if string(snapshot.ConfirmedConstraintsJSON) != "[]" {
		t.Fatalf("expected confirmed_constraints_json to default to [], got %s", string(snapshot.ConfirmedConstraintsJSON))
	}
	if snapshot.SummaryBaseSequenceNo != 0 {
		t.Fatalf("expected summary_base_sequence_no 0, got %d", snapshot.SummaryBaseSequenceNo)
	}
}
