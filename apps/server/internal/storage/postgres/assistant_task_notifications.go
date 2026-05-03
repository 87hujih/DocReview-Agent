package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AssistantTaskNotification 表示 assistant 任务终态回写的幂等记录。
type AssistantTaskNotification struct {
	TaskID    string
	Status    string
	SessionID string
	MessageID *string
	CreatedAt time.Time
}

// AppendTaskStatusMessageParams 描述原子写入终态消息所需参数。
type AppendTaskStatusMessageParams struct {
	MessageInput AssistantMessageInput
	SessionID    string
	Status       string
	TaskID       string
}

// AssistantTaskNotificationRepo 封装任务终态通知表的最小持久化能力。
type AssistantTaskNotificationRepo struct {
	pool *pgxpool.Pool
}

// NewAssistantTaskNotificationRepo 使用连接池创建任务终态通知仓储。
func NewAssistantTaskNotificationRepo(pool *pgxpool.Pool) *AssistantTaskNotificationRepo {
	return &AssistantTaskNotificationRepo{pool: pool}
}

// Claim 为指定 task/status 抢占一条通知记录；已存在时返回现有记录。
func (r *AssistantTaskNotificationRepo) Claim(
	ctx context.Context,
	taskID string,
	status string,
	sessionID string,
) (*AssistantTaskNotification, bool, error) {
	record, err := scanAssistantTaskNotification(r.pool.QueryRow(ctx, `
		INSERT INTO assistant_task_notifications (task_id, status, session_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (task_id, status) DO NOTHING
		RETURNING task_id, status, session_id, message_id, created_at
	`, strings.TrimSpace(taskID), strings.TrimSpace(status), strings.TrimSpace(sessionID)))
	if err == nil {
		return &record, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	existing, getErr := r.GetByTaskStatus(ctx, taskID, status)
	return existing, false, getErr
}

// GetByTaskStatus 按 task/status 读取通知记录；不存在时返回 nil。
func (r *AssistantTaskNotificationRepo) GetByTaskStatus(
	ctx context.Context,
	taskID string,
	status string,
) (*AssistantTaskNotification, error) {
	record, err := scanAssistantTaskNotification(r.pool.QueryRow(ctx, `
		SELECT task_id, status, session_id, message_id, created_at
		FROM assistant_task_notifications
		WHERE task_id = $1 AND status = $2
	`, strings.TrimSpace(taskID), strings.TrimSpace(status)))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &record, nil
}

// UpdateMessageID 回填终态消息对应的 assistant message id。
func (r *AssistantTaskNotificationRepo) UpdateMessageID(
	ctx context.Context,
	taskID string,
	status string,
	messageID string,
) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE assistant_task_notifications
		SET message_id = $3
		WHERE task_id = $1 AND status = $2
	`, strings.TrimSpace(taskID), strings.TrimSpace(status), strings.TrimSpace(messageID))
	return err
}

// AppendTaskStatusMessage 在同一事务里完成 claim、消息写入和 message_id 回填。
func (r *AssistantTaskNotificationRepo) AppendTaskStatusMessage(
	ctx context.Context,
	params AppendTaskStatusMessageParams,
) (*AssistantMessage, bool, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, false, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	notification, created, err := claimAssistantTaskNotificationTx(
		ctx,
		tx,
		strings.TrimSpace(params.TaskID),
		strings.TrimSpace(params.Status),
		strings.TrimSpace(params.SessionID),
	)
	if err != nil {
		return nil, false, err
	}
	if notification == nil {
		return nil, false, nil
	}
	if !created && notification.MessageID != nil {
		return nil, false, nil
	}

	sessionID := notification.SessionID
	if strings.TrimSpace(sessionID) == "" {
		sessionID = strings.TrimSpace(params.SessionID)
	}

	messages, err := appendAssistantMessagesTx(ctx, tx, sessionID, []AssistantMessageInput{params.MessageInput})
	if err != nil {
		return nil, false, err
	}
	if len(messages) == 0 {
		return nil, false, nil
	}

	if err := updateAssistantTaskNotificationMessageIDTx(
		ctx,
		tx,
		strings.TrimSpace(params.TaskID),
		strings.TrimSpace(params.Status),
		messages[0].ID,
	); err != nil {
		return nil, false, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}

	return &messages[0], true, nil
}

// claimAssistantTaskNotificationTx 在事务内认领待发送的助手任务通知，避免多 worker 重复投递。
func claimAssistantTaskNotificationTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	status string,
	sessionID string,
) (*AssistantTaskNotification, bool, error) {
	record, err := scanAssistantTaskNotification(tx.QueryRow(ctx, `
		INSERT INTO assistant_task_notifications (task_id, status, session_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (task_id, status) DO NOTHING
		RETURNING task_id, status, session_id, message_id, created_at
	`, taskID, status, sessionID))
	if err == nil {
		return &record, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	locked, lockErr := scanAssistantTaskNotification(tx.QueryRow(ctx, `
		SELECT task_id, status, session_id, message_id, created_at
		FROM assistant_task_notifications
		WHERE task_id = $1 AND status = $2
		FOR UPDATE
	`, taskID, status))
	if lockErr != nil {
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return nil, false, nil
		}

		return nil, false, lockErr
	}

	return &locked, false, nil
}

// updateAssistantTaskNotificationMessageIDTx 在事务内回写助手任务通知对应的消息 ID，建立通知记录与消息的关联。
func updateAssistantTaskNotificationMessageIDTx(
	ctx context.Context,
	tx pgx.Tx,
	taskID string,
	status string,
	messageID string,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE assistant_task_notifications
		SET message_id = $3
		WHERE task_id = $1 AND status = $2
	`, taskID, status, messageID)
	return err
}

// scanAssistantTaskNotification 把当前数据库行扫描成 `助手任务通知`，统一查询结果到领域结构的映射。
func scanAssistantTaskNotification(row pgx.Row) (AssistantTaskNotification, error) {
	var notification AssistantTaskNotification

	err := row.Scan(
		&notification.TaskID,
		&notification.Status,
		&notification.SessionID,
		&notification.MessageID,
		&notification.CreatedAt,
	)
	if err != nil {
		return AssistantTaskNotification{}, err
	}

	return notification, nil
}
