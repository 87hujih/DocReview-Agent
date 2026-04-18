package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"agent_project/apps/server/internal/storage/postgres"
)

// TestAssistantTaskStatusNotifierDeduplicatesCompletedStatus 验证`assistantTaskStatusNotifier`在状态保持路径下的行为，防止同类回归。
func TestAssistantTaskStatusNotifierDeduplicatesCompletedStatus(t *testing.T) {
	sourceMessageID := "source-1"
	lookupRepo := &fakeTaskStatusSourceMessageRepo{
		messages: map[string]postgres.AssistantMessage{
			sourceMessageID: {
				ID:        sourceMessageID,
				SessionID: "session-1",
			},
		},
	}
	notificationRepo := newFakeTaskStatusNotificationRepo()
	notifier := NewAssistantTaskStatusNotifier(lookupRepo, notificationRepo)
	task := &postgres.Task{
		ID:              "task-1",
		Instruction:     "请修订第二章",
		ResourceID:      "resource-1",
		SourceMessageID: &sourceMessageID,
		Status:          "completed",
	}

	if err := notifier.Notify(context.Background(), task, "completed"); err != nil {
		t.Fatalf("notify completed task status: %v", err)
	}
	if err := notifier.Notify(context.Background(), task, "completed"); err != nil {
		t.Fatalf("notify duplicated completed task status: %v", err)
	}

	if len(notificationRepo.appends) != 1 {
		t.Fatalf("expected exactly 1 appended terminal message, got %d", len(notificationRepo.appends))
	}

	payload := decodeAssistantTaskStatusPayload(t, notificationRepo.appends[0].MessageInput.Payload)
	if payload.Title != "任务已完成" {
		t.Fatalf("expected completed title %q, got %q", "任务已完成", payload.Title)
	}
	if payload.DetailURL != "/tasks/task-1?session=session-1" {
		t.Fatalf("expected detail url %q, got %q", "/tasks/task-1?session=session-1", payload.DetailURL)
	}
	if payload.ResultURL != "/resources/resource-1?session=session-1" {
		t.Fatalf("expected result url %q, got %q", "/resources/resource-1?session=session-1", payload.ResultURL)
	}
}

// TestAssistantTaskStatusNotifierTreatsCompletedAndFailedAsDifferentKeys 验证`assistantTaskStatusNotifier`在流程控制路径下的行为，防止同类回归。
func TestAssistantTaskStatusNotifierTreatsCompletedAndFailedAsDifferentKeys(t *testing.T) {
	sourceMessageID := "source-1"
	lookupRepo := &fakeTaskStatusSourceMessageRepo{
		messages: map[string]postgres.AssistantMessage{
			sourceMessageID: {
				ID:        sourceMessageID,
				SessionID: "session-1",
			},
		},
	}
	notificationRepo := newFakeTaskStatusNotificationRepo()
	notifier := NewAssistantTaskStatusNotifier(lookupRepo, notificationRepo)
	task := &postgres.Task{
		ID:              "task-1",
		Instruction:     "请修订第二章",
		ResourceID:      "resource-1",
		SourceMessageID: &sourceMessageID,
	}

	if err := notifier.Notify(context.Background(), task, "completed"); err != nil {
		t.Fatalf("notify completed task status: %v", err)
	}
	if err := notifier.Notify(context.Background(), task, "failed"); err != nil {
		t.Fatalf("notify failed task status: %v", err)
	}

	if len(notificationRepo.appends) != 2 {
		t.Fatalf("expected completed and failed to append 2 messages, got %d", len(notificationRepo.appends))
	}

	completedPayload := decodeAssistantTaskStatusPayload(t, notificationRepo.appends[0].MessageInput.Payload)
	if completedPayload.ResultURL == "" {
		t.Fatal("expected completed payload to include result_url")
	}

	failedPayload := decodeAssistantTaskStatusPayload(t, notificationRepo.appends[1].MessageInput.Payload)
	if failedPayload.Title != "任务执行失败" {
		t.Fatalf("expected failed title %q, got %q", "任务执行失败", failedPayload.Title)
	}
	if failedPayload.ResultURL != "" {
		t.Fatalf("expected failed payload to omit result_url, got %q", failedPayload.ResultURL)
	}
}

// TestAssistantTaskStatusNotifierNoopsWithoutSourceMessageID 验证`assistantTaskStatusNotifier`在跳过或空操作路径下的行为，防止同类回归。
func TestAssistantTaskStatusNotifierNoopsWithoutSourceMessageID(t *testing.T) {
	lookupRepo := &fakeTaskStatusSourceMessageRepo{}
	notificationRepo := newFakeTaskStatusNotificationRepo()
	notifier := NewAssistantTaskStatusNotifier(lookupRepo, notificationRepo)

	if err := notifier.Notify(context.Background(), &postgres.Task{
		ID:          "task-1",
		Instruction: "请修订第二章",
		ResourceID:  "resource-1",
		Status:      "completed",
	}, "completed"); err != nil {
		t.Fatalf("notify task status without source message: %v", err)
	}

	if len(notificationRepo.appends) != 0 {
		t.Fatalf("expected no terminal messages without source_message_id, got %d", len(notificationRepo.appends))
	}
	if len(lookupRepo.lookups) != 0 {
		t.Fatalf("expected no source message lookup without source_message_id, got %d", len(lookupRepo.lookups))
	}
}

// fakeTaskStatusSourceMessageRepo 作为任务状态来源消息仓储的测试替身，用于在用例里提供可控的依赖行为。
type fakeTaskStatusSourceMessageRepo struct {
	err      error
	lookups  []string
	messages map[string]postgres.AssistantMessage
}

// GetMessageByID 实现测试替身需要的 `GetMessageByID` 接口方法，为用例分支提供可控返回。
func (r *fakeTaskStatusSourceMessageRepo) GetMessageByID(_ context.Context, id string) (*postgres.AssistantMessage, error) {
	r.lookups = append(r.lookups, id)
	if r.err != nil {
		return nil, r.err
	}

	message, ok := r.messages[id]
	if !ok {
		return nil, nil
	}

	cloned := message
	return &cloned, nil
}

// fakeTaskStatusNotificationRepo 作为任务状态通知仓储的测试替身，用于在用例里提供可控的依赖行为。
type fakeTaskStatusNotificationRepo struct {
	appends []postgres.AppendTaskStatusMessageParams
	err     error
	items   map[string]*postgres.AssistantMessage
}

// newFakeTaskStatusNotificationRepo 为测试场景处理 `newFake任务状态通知仓储` 的辅助步骤，减少重复搭建逻辑。
func newFakeTaskStatusNotificationRepo() *fakeTaskStatusNotificationRepo {
	return &fakeTaskStatusNotificationRepo{
		items: make(map[string]*postgres.AssistantMessage),
	}
}

// AppendTaskStatusMessage 实现测试替身需要的 `AppendTaskStatusMessage` 接口方法，为用例分支提供可控返回。
func (r *fakeTaskStatusNotificationRepo) AppendTaskStatusMessage(
	_ context.Context,
	params postgres.AppendTaskStatusMessageParams,
) (*postgres.AssistantMessage, bool, error) {
	if r.err != nil {
		return nil, false, r.err
	}

	key := fmt.Sprintf("%s|%s", params.TaskID, params.Status)
	if message, ok := r.items[key]; ok {
		return message, false, nil
	}

	r.appends = append(r.appends, params)
	message := &postgres.AssistantMessage{
		ID:         fmt.Sprintf("message-%d", len(r.appends)),
		SessionID:  params.SessionID,
		Role:       params.MessageInput.Role,
		Kind:       params.MessageInput.Kind,
		SequenceNo: len(r.appends),
		Payload:    append([]byte(nil), params.MessageInput.Payload...),
		CreatedAt:  time.Now(),
	}
	r.items[key] = message
	return message, true, nil
}

// decodeAssistantTaskStatusPayload 在测试里解码 `助手任务状态载荷`，便于直接断言结构化内容。
func decodeAssistantTaskStatusPayload(t *testing.T, payload []byte) TaskStatusPayload {
	t.Helper()

	var value TaskStatusPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatalf("unmarshal task status payload: %v", err)
	}

	return value
}
