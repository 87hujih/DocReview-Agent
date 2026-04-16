package assistant

import (
	"context"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

type taskStatusSourceMessageRepo interface {
	GetMessageByID(ctx context.Context, id string) (*postgres.AssistantMessage, error)
}

type taskStatusNotificationRepo interface {
	AppendTaskStatusMessage(
		ctx context.Context,
		params postgres.AppendTaskStatusMessageParams,
	) (*postgres.AssistantMessage, bool, error)
}

// AssistantTaskStatusNotifier 负责把 assistant-originated task 的终态回写到原会话。
type AssistantTaskStatusNotifier struct {
	messageRepo      taskStatusSourceMessageRepo
	notificationRepo taskStatusNotificationRepo
}

// NewAssistantTaskStatusNotifier 构造任务终态通知器。
func NewAssistantTaskStatusNotifier(
	messageRepo taskStatusSourceMessageRepo,
	notificationRepo taskStatusNotificationRepo,
) *AssistantTaskStatusNotifier {
	return &AssistantTaskStatusNotifier{
		messageRepo:      messageRepo,
		notificationRepo: notificationRepo,
	}
}

// Notify 根据任务终态向原会话追加一条 task_status 消息。
func (n *AssistantTaskStatusNotifier) Notify(ctx context.Context, task *postgres.Task, status string) error {
	if n == nil || task == nil || n.messageRepo == nil || n.notificationRepo == nil {
		return nil
	}

	normalizedStatus := strings.TrimSpace(status)
	if normalizedStatus == "" {
		normalizedStatus = strings.TrimSpace(task.Status)
	}
	if normalizedStatus != "completed" && normalizedStatus != "failed" {
		return nil
	}
	if task.SourceMessageID == nil || strings.TrimSpace(*task.SourceMessageID) == "" {
		return nil
	}

	sourceMessage, err := n.messageRepo.GetMessageByID(ctx, strings.TrimSpace(*task.SourceMessageID))
	if err != nil {
		return err
	}
	if sourceMessage == nil || strings.TrimSpace(sourceMessage.SessionID) == "" {
		return fmt.Errorf("source assistant message %q not found", strings.TrimSpace(*task.SourceMessageID))
	}

	payload := buildTaskStatusPayload(task, sourceMessage.SessionID, normalizedStatus)
	messageInput, err := buildMessageInput(RoleAssistant, KindTaskStatus, payload)
	if err != nil {
		return err
	}

	_, _, err = n.notificationRepo.AppendTaskStatusMessage(ctx, postgres.AppendTaskStatusMessageParams{
		MessageInput: messageInput,
		SessionID:    sourceMessage.SessionID,
		Status:       normalizedStatus,
		TaskID:       task.ID,
	})
	return err
}

func buildTaskStatusPayload(task *postgres.Task, sessionID string, status string) TaskStatusPayload {
	payload := TaskStatusPayload{
		DetailURL:     buildTaskDetailURL(task.ID, sessionID),
		Instruction:   strings.TrimSpace(task.Instruction),
		ResourceID:    strings.TrimSpace(task.ResourceID),
		Status:        status,
		StatusMessage: "任务未能完成，请打开详情查看失败原因。",
		TaskID:        task.ID,
		Title:         "任务执行失败",
	}

	if status == "completed" {
		payload.Title = "任务已完成"
		payload.StatusMessage = "最终修订版本已写入资源库，可以查看结果或继续对话。"
		if payload.ResourceID != "" {
			payload.ResultURL = buildResourceDetailURL(payload.ResourceID, sessionID)
		}
	}

	return payload
}
