package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AssistantSession 表示一个可持久化的助手会话。
type AssistantSession struct {
	ID            string
	Title         string
	LastMessageAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AssistantMessage 表示会话内的一条结构化消息。
type AssistantMessage struct {
	ID         string
	SessionID  string
	Role       string
	Kind       string
	SequenceNo int
	Payload    []byte
	CreatedAt  time.Time
}

// AssistantMessageInput 表示待插入的一条会话消息。
type AssistantMessageInput struct {
	Role    string
	Kind    string
	Payload []byte
}

// AssistantRepo 封装助手会话与消息的 PostgreSQL 访问。
type AssistantRepo struct {
	pool *pgxpool.Pool
}

// NewAssistantRepo 使用连接池创建助手仓储。
func NewAssistantRepo(pool *pgxpool.Pool) *AssistantRepo {
	return &AssistantRepo{pool: pool}
}

// ListSessions 按最近消息时间倒序返回会话列表。
func (r *AssistantRepo) ListSessions(ctx context.Context) ([]AssistantSession, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, title, last_message_at, created_at, updated_at
		FROM assistant_sessions
		ORDER BY last_message_at DESC, id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []AssistantSession
	for rows.Next() {
		session, err := scanAssistantSession(rows)
		if err != nil {
			return nil, err
		}

		sessions = append(sessions, session)
	}

	return sessions, rows.Err()
}

// GetSessionByID 在会话不存在时返回 nil。
func (r *AssistantRepo) GetSessionByID(ctx context.Context, id string) (*AssistantSession, error) {
	session, err := scanAssistantSession(r.pool.QueryRow(ctx, `
		SELECT id, title, last_message_at, created_at, updated_at
		FROM assistant_sessions
		WHERE id = $1
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &session, nil
}

// ListMessages 返回指定会话的完整消息流。
func (r *AssistantRepo) ListMessages(ctx context.Context, sessionID string) ([]AssistantMessage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, session_id, role, kind, sequence_no, payload, created_at
		FROM assistant_messages
		WHERE session_id = $1
		ORDER BY sequence_no ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []AssistantMessage
	for rows.Next() {
		message, err := scanAssistantMessage(rows)
		if err != nil {
			return nil, err
		}

		messages = append(messages, message)
	}

	return messages, rows.Err()
}

// ListMessagesAfterSequence 返回指定序号之后的消息窗口。
func (r *AssistantRepo) ListMessagesAfterSequence(ctx context.Context, sessionID string, afterSequenceNo int) ([]AssistantMessage, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, session_id, role, kind, sequence_no, payload, created_at
		FROM assistant_messages
		WHERE session_id = $1
		  AND sequence_no > $2
		ORDER BY sequence_no ASC, id ASC
	`, sessionID, afterSequenceNo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []AssistantMessage
	for rows.Next() {
		message, err := scanAssistantMessage(rows)
		if err != nil {
			return nil, err
		}

		messages = append(messages, message)
	}

	return messages, rows.Err()
}

// GetMessageByID 在消息不存在时返回 nil。
func (r *AssistantRepo) GetMessageByID(ctx context.Context, id string) (*AssistantMessage, error) {
	message, err := scanAssistantMessage(r.pool.QueryRow(ctx, `
		SELECT id, session_id, role, kind, sequence_no, payload, created_at
		FROM assistant_messages
		WHERE id = $1
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &message, nil
}

// CreateSessionWithMessages 创建会话并一次性写入首批消息。
func (r *AssistantRepo) CreateSessionWithMessages(ctx context.Context, title string, inputs []AssistantMessageInput) (*AssistantSession, []AssistantMessage, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	session, err := scanAssistantSession(tx.QueryRow(ctx, `
		INSERT INTO assistant_sessions (title)
		VALUES ($1)
		RETURNING id, title, last_message_at, created_at, updated_at
	`, title))
	if err != nil {
		return nil, nil, err
	}

	messages, lastMessageAt, err := insertAssistantMessages(ctx, tx, session.ID, 0, inputs)
	if err != nil {
		return nil, nil, err
	}

	if !lastMessageAt.IsZero() {
		session, err = updateAssistantSessionTimestamp(ctx, tx, session.ID, lastMessageAt)
		if err != nil {
			return nil, nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}

	return &session, messages, nil
}

// AppendMessages 向已有会话按顺序追加消息。
func (r *AssistantRepo) AppendMessages(ctx context.Context, sessionID string, inputs []AssistantMessageInput) ([]AssistantMessage, error) {
	if len(inputs) == 0 {
		return []AssistantMessage{}, nil
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	messages, err := appendAssistantMessagesTx(ctx, tx, sessionID, inputs)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return messages, nil
}

// DeleteSession 删除会话及其级联消息。
func (r *AssistantRepo) DeleteSession(ctx context.Context, id string) (bool, error) {
	result, err := r.pool.Exec(ctx, `
		DELETE FROM assistant_sessions
		WHERE id = $1
	`, id)
	if err != nil {
		return false, err
	}

	return result.RowsAffected() > 0, nil
}

// insertAssistantMessages 批量写入助手消息记录，并保持会话内顺序字段连续。
func insertAssistantMessages(
	ctx context.Context,
	tx pgx.Tx,
	sessionID string,
	baseSequence int,
	inputs []AssistantMessageInput,
) ([]AssistantMessage, time.Time, error) {
	messages := make([]AssistantMessage, 0, len(inputs))
	var lastMessageAt time.Time

	for index, input := range inputs {
		message, err := scanAssistantMessage(tx.QueryRow(ctx, `
			INSERT INTO assistant_messages (session_id, role, kind, sequence_no, payload)
			VALUES ($1, $2, $3, $4, $5::jsonb)
			RETURNING id, session_id, role, kind, sequence_no, payload, created_at
		`, sessionID, input.Role, input.Kind, baseSequence+index+1, string(input.Payload)))
		if err != nil {
			return nil, time.Time{}, err
		}

		messages = append(messages, message)
		lastMessageAt = message.CreatedAt
	}

	return messages, lastMessageAt, nil
}

// appendAssistantMessagesTx 追加 `助手消息事务`，保持消息和副作用写入顺序一致。
func appendAssistantMessagesTx(
	ctx context.Context,
	tx pgx.Tx,
	sessionID string,
	inputs []AssistantMessageInput,
) ([]AssistantMessage, error) {
	if len(inputs) == 0 {
		return []AssistantMessage{}, nil
	}

	var lockedSessionID string
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM assistant_sessions
		WHERE id = $1
		FOR UPDATE
	`, sessionID).Scan(&lockedSessionID); err != nil {
		return nil, err
	}

	var currentMaxSequence int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(sequence_no), 0)
		FROM assistant_messages
		WHERE session_id = $1
	`, sessionID).Scan(&currentMaxSequence); err != nil {
		return nil, err
	}

	messages, lastMessageAt, err := insertAssistantMessages(ctx, tx, sessionID, currentMaxSequence, inputs)
	if err != nil {
		return nil, err
	}

	if !lastMessageAt.IsZero() {
		if _, err := updateAssistantSessionTimestamp(ctx, tx, sessionID, lastMessageAt); err != nil {
			return nil, err
		}
	}

	return messages, nil
}

// updateAssistantSessionTimestamp 刷新助手会话的最近活动时间，确保列表排序反映最新交互。
func updateAssistantSessionTimestamp(ctx context.Context, tx pgx.Tx, sessionID string, timestamp time.Time) (AssistantSession, error) {
	return scanAssistantSession(tx.QueryRow(ctx, `
		UPDATE assistant_sessions
		SET last_message_at = $2,
		    updated_at = $2
		WHERE id = $1
		RETURNING id, title, last_message_at, created_at, updated_at
	`, sessionID, timestamp))
}

// scanAssistantSession 把当前数据库行扫描成 `助手会话`，统一查询结果到领域结构的映射。
func scanAssistantSession(row pgx.Row) (AssistantSession, error) {
	var session AssistantSession

	err := row.Scan(
		&session.ID,
		&session.Title,
		&session.LastMessageAt,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		return AssistantSession{}, err
	}

	return session, nil
}

// scanAssistantMessage 把当前数据库行扫描成 `助手消息`，统一查询结果到领域结构的映射。
func scanAssistantMessage(row pgx.Row) (AssistantMessage, error) {
	var message AssistantMessage

	err := row.Scan(
		&message.ID,
		&message.SessionID,
		&message.Role,
		&message.Kind,
		&message.SequenceNo,
		&message.Payload,
		&message.CreatedAt,
	)
	if err != nil {
		return AssistantMessage{}, err
	}

	return message, nil
}
