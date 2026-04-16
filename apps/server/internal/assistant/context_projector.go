package assistant

import (
	"context"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

type sessionContextSnapshotProjectorRepo interface {
	CreateEmpty(ctx context.Context, sessionID string) (*postgres.SessionContextSnapshotRecord, error)
	GetBySessionID(ctx context.Context, sessionID string) (*postgres.SessionContextSnapshotRecord, error)
	UpsertActiveResource(ctx context.Context, params postgres.UpsertActiveResourceParams) error
	UpsertPendingTaskSuggestion(ctx context.Context, params postgres.UpsertPendingTaskSuggestionParams) error
	UpsertLatestTask(ctx context.Context, params postgres.UpsertLatestTaskParams) error
	ClearPendingTaskSuggestion(ctx context.Context, sessionID string) error
}

type sessionContextSnapshotTaskStatusRepo interface {
	UpdateLatestTaskStatusBySourceMessageID(ctx context.Context, sourceMessageID string, status string) error
}

// SessionFileReadyProjection 描述 session_file ready 事件中与快照投影相关的字段。
type SessionFileReadyProjection struct {
	SessionID       string
	ResourceID      string
	ResourceTitle   string
	ResourceSource  string
	SourceMessageID string
}

// TaskSuggestionProjection 描述 task_suggestion 事件中与快照投影相关的字段。
type TaskSuggestionProjection struct {
	SessionID   string
	MessageID   string
	Instruction string
}

// TaskCreatedProjection 描述 task_created 事件中与快照投影相关的字段。
type TaskCreatedProjection struct {
	SessionID       string
	TaskID          string
	Status          string
	SourceMessageID string
}

// SessionContextProjector 负责把结构化事件折叠为会话上下文快照。
type SessionContextProjector struct {
	repo           sessionContextSnapshotProjectorRepo
	taskStatusRepo sessionContextSnapshotTaskStatusRepo
}

// NewSessionContextProjector 构造会话上下文投影器。
func NewSessionContextProjector(repo sessionContextSnapshotProjectorRepo) *SessionContextProjector {
	projector := &SessionContextProjector{repo: repo}
	if taskStatusRepo, ok := repo.(sessionContextSnapshotTaskStatusRepo); ok {
		projector.taskStatusRepo = taskStatusRepo
	}

	return projector
}

// InitSession 初始化一条空快照。
func (p *SessionContextProjector) InitSession(ctx context.Context, sessionID string) error {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return fmt.Errorf("session_id 不能为空")
	}

	_, err := p.repo.CreateEmpty(ctx, trimmedSessionID)
	return err
}

// ProjectSessionFileReady 把最近一次 ready 资源事件折叠为活跃资源。
func (p *SessionContextProjector) ProjectSessionFileReady(ctx context.Context, projection SessionFileReadyProjection) error {
	if strings.TrimSpace(projection.SessionID) == "" {
		return fmt.Errorf("session_id 不能为空")
	}
	if strings.TrimSpace(projection.ResourceID) == "" {
		return fmt.Errorf("resource_id 不能为空")
	}

	return p.repo.UpsertActiveResource(ctx, postgres.UpsertActiveResourceParams{
		SessionID:       strings.TrimSpace(projection.SessionID),
		ResourceID:      strings.TrimSpace(projection.ResourceID),
		ResourceTitle:   strings.TrimSpace(projection.ResourceTitle),
		ResourceSource:  strings.TrimSpace(projection.ResourceSource),
		SourceMessageID: optionalStringPointer(projection.SourceMessageID),
	})
}

// ProjectTaskSuggestionCreated 折叠最近一条待确认任务建议。
func (p *SessionContextProjector) ProjectTaskSuggestionCreated(ctx context.Context, projection TaskSuggestionProjection) error {
	if strings.TrimSpace(projection.SessionID) == "" {
		return fmt.Errorf("session_id 不能为空")
	}
	if strings.TrimSpace(projection.MessageID) == "" {
		return fmt.Errorf("message_id 不能为空")
	}

	return p.repo.UpsertPendingTaskSuggestion(ctx, postgres.UpsertPendingTaskSuggestionParams{
		SessionID:   strings.TrimSpace(projection.SessionID),
		MessageID:   strings.TrimSpace(projection.MessageID),
		Instruction: strings.TrimSpace(projection.Instruction),
	})
}

// ProjectTaskCreated 清理 pending suggestion，并写入最近已创建任务。
func (p *SessionContextProjector) ProjectTaskCreated(ctx context.Context, projection TaskCreatedProjection) error {
	trimmedSessionID := strings.TrimSpace(projection.SessionID)
	if trimmedSessionID == "" {
		return fmt.Errorf("session_id 不能为空")
	}
	if strings.TrimSpace(projection.TaskID) == "" {
		return fmt.Errorf("task_id 不能为空")
	}
	if strings.TrimSpace(projection.Status) == "" {
		return fmt.Errorf("task status 不能为空")
	}

	if err := p.repo.ClearPendingTaskSuggestion(ctx, trimmedSessionID); err != nil {
		return err
	}

	return p.repo.UpsertLatestTask(ctx, postgres.UpsertLatestTaskParams{
		SessionID:       trimmedSessionID,
		TaskID:          strings.TrimSpace(projection.TaskID),
		Status:          strings.TrimSpace(projection.Status),
		SourceMessageID: optionalStringPointer(projection.SourceMessageID),
	})
}

// ProjectTaskStatusChanged 根据 source_message_id 回写最近任务状态。
func (p *SessionContextProjector) ProjectTaskStatusChanged(ctx context.Context, sourceMessageID *string, taskID string, status string) error {
	_ = taskID

	if sourceMessageID == nil || strings.TrimSpace(*sourceMessageID) == "" {
		return nil
	}
	if strings.TrimSpace(status) == "" {
		return fmt.Errorf("task status 不能为空")
	}
	if p.taskStatusRepo == nil {
		return nil
	}

	return p.taskStatusRepo.UpdateLatestTaskStatusBySourceMessageID(ctx, strings.TrimSpace(*sourceMessageID), strings.TrimSpace(status))
}

func optionalStringPointer(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}
