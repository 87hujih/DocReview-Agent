package postgres

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/task/models"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAssistantTaskNotificationsRepoClaimUpdateAndLookup 验证`assistantTaskNotificationsRepoClaimUpdateAndLookup`在特定边界条件下的行为，防止同类回归。
func TestAssistantTaskNotificationsRepoClaimUpdateAndLookup(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)
	notificationRepo := NewAssistantTaskNotificationRepo(pool)
	task, session, seedMessage := seedAssistantTaskNotificationContext(t, pool, ctx)

	notification, created, err := notificationRepo.Claim(ctx, task.ID, models.StatusCompleted, session.ID)
	if err != nil {
		t.Fatalf("claim notification: %v", err)
	}
	if !created {
		t.Fatal("expected first claim to create a notification row")
	}
	if notification == nil {
		t.Fatal("expected created notification row")
	}
	if notification.TaskID != task.ID {
		t.Fatalf("expected task id %q, got %q", task.ID, notification.TaskID)
	}
	if notification.Status != models.StatusCompleted {
		t.Fatalf("expected status %q, got %q", models.StatusCompleted, notification.Status)
	}
	if notification.SessionID != session.ID {
		t.Fatalf("expected session id %q, got %q", session.ID, notification.SessionID)
	}
	if notification.MessageID != nil {
		t.Fatalf("expected message_id to be nil before update, got %#v", notification.MessageID)
	}

	if err := notificationRepo.UpdateMessageID(ctx, task.ID, models.StatusCompleted, seedMessage.ID); err != nil {
		t.Fatalf("update notification message id: %v", err)
	}

	stored, err := notificationRepo.GetByTaskStatus(ctx, task.ID, models.StatusCompleted)
	if err != nil {
		t.Fatalf("get notification by task/status: %v", err)
	}
	if stored == nil {
		t.Fatal("expected stored notification row")
	}
	if stored.MessageID == nil || *stored.MessageID != seedMessage.ID {
		t.Fatalf("expected stored message id %q, got %#v", seedMessage.ID, stored.MessageID)
	}

	claimedAgain, createdAgain, err := notificationRepo.Claim(ctx, task.ID, models.StatusCompleted, session.ID)
	if err != nil {
		t.Fatalf("claim notification again: %v", err)
	}
	if createdAgain {
		t.Fatal("expected duplicate claim to reuse the existing notification row")
	}
	if claimedAgain == nil || claimedAgain.MessageID == nil || *claimedAgain.MessageID != seedMessage.ID {
		t.Fatalf("expected duplicate claim to return stored message id %q, got %#v", seedMessage.ID, claimedAgain)
	}
}

// TestAssistantTaskNotificationsRepoAppendTaskStatusMessageIsIdempotent 验证`assistantTaskNotificationsRepoAppendTaskStatusMessageIsIdempotent`在特定边界条件下的行为，防止同类回归。
func TestAssistantTaskNotificationsRepoAppendTaskStatusMessageIsIdempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := testContext(t)
	assistantRepo := NewAssistantRepo(pool)
	notificationRepo := NewAssistantTaskNotificationRepo(pool)
	task, session, _ := seedAssistantTaskNotificationContext(t, pool, ctx)

	firstMessage, created, err := notificationRepo.AppendTaskStatusMessage(ctx, AppendTaskStatusMessageParams{
		MessageInput: mustAssistantMessageInput(t, "assistant", "task_status", `{"task_id":"task-1","title":"任务已完成","instruction":"请修订第二章","status":"completed","status_message":"最终修订版本已写入资源库，可以查看结果或继续对话。","detail_url":"/tasks/task-1?session=session-1","resource_id":"resource-1","result_url":"/resources/resource-1?session=session-1"}`),
		SessionID:    session.ID,
		Status:       models.StatusCompleted,
		TaskID:       task.ID,
	})
	if err != nil {
		t.Fatalf("append first task status message: %v", err)
	}
	if !created {
		t.Fatal("expected first append to create a message")
	}
	if firstMessage == nil {
		t.Fatal("expected first append to return the persisted message")
	}
	if firstMessage.Kind != "task_status" {
		t.Fatalf("expected message kind %q, got %q", "task_status", firstMessage.Kind)
	}

	secondMessage, createdAgain, err := notificationRepo.AppendTaskStatusMessage(ctx, AppendTaskStatusMessageParams{
		MessageInput: mustAssistantMessageInput(t, "assistant", "task_status", `{"task_id":"task-1","title":"任务已完成","instruction":"请修订第二章","status":"completed","status_message":"最终修订版本已写入资源库，可以查看结果或继续对话。","detail_url":"/tasks/task-1?session=session-1","resource_id":"resource-1","result_url":"/resources/resource-1?session=session-1"}`),
		SessionID:    session.ID,
		Status:       models.StatusCompleted,
		TaskID:       task.ID,
	})
	if err != nil {
		t.Fatalf("append duplicated task status message: %v", err)
	}
	if createdAgain {
		t.Fatal("expected duplicated append to no-op")
	}
	if secondMessage != nil {
		t.Fatalf("expected duplicated append to return nil message, got %#v", secondMessage)
	}

	failedMessage, failedCreated, err := notificationRepo.AppendTaskStatusMessage(ctx, AppendTaskStatusMessageParams{
		MessageInput: mustAssistantMessageInput(t, "assistant", "task_status", `{"task_id":"task-1","title":"任务执行失败","instruction":"请修订第二章","status":"failed","status_message":"任务未能完成，请打开详情查看失败原因。","detail_url":"/tasks/task-1?session=session-1","resource_id":"resource-1"}`),
		SessionID:    session.ID,
		Status:       models.StatusFailed,
		TaskID:       task.ID,
	})
	if err != nil {
		t.Fatalf("append failed task status message: %v", err)
	}
	if !failedCreated {
		t.Fatal("expected different terminal status to create another notification")
	}
	if failedMessage == nil {
		t.Fatal("expected failed terminal status to persist a message")
	}

	allMessages, err := assistantRepo.ListMessages(ctx, session.ID)
	if err != nil {
		t.Fatalf("list assistant messages: %v", err)
	}
	if len(allMessages) != 3 {
		t.Fatalf("expected seed message plus 2 terminal messages, got %d", len(allMessages))
	}
	if allMessages[1].Kind != "task_status" || allMessages[2].Kind != "task_status" {
		t.Fatalf("expected appended messages to be task_status, got %#v", allMessages)
	}
}

// seedAssistantTaskNotificationContext 为测试场景补齐 `助手任务通知上下文` 所需数据，减少重复造数。
func seedAssistantTaskNotificationContext(
	t *testing.T,
	pool *pgxpool.Pool,
	ctx context.Context,
) (*Task, *AssistantSession, AssistantMessage) {
	t.Helper()

	resourceRepo := NewResourceRepo(pool)
	taskRepo := NewTaskRepo(pool)
	assistantRepo := NewAssistantRepo(pool)

	resource, err := resourceRepo.Create(ctx, "任务通知测试-"+uniqueSuffix(), "upload")
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	task, err := taskRepo.Create(ctx, resource.ID, "请修订第二章")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	session, messages, err := assistantRepo.CreateSessionWithMessages(ctx, "任务通知会话", []AssistantMessageInput{
		mustAssistantMessageInput(t, "assistant", "text", `{"content":"这是原会话消息"}`),
	})
	if err != nil {
		t.Fatalf("create assistant session: %v", err)
	}
	t.Cleanup(func() {
		if _, err := assistantRepo.DeleteSession(ctx, session.ID); err != nil {
			t.Fatalf("cleanup assistant session: %v", err)
		}
	})

	if len(messages) != 1 {
		t.Fatalf("expected exactly 1 seed message, got %d", len(messages))
	}

	return task, session, messages[0]
}
