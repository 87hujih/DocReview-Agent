package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AssistantRuntimeEvent 表示 assistant runtime 在持久化层中的一条结构化事件。
type AssistantRuntimeEvent struct {
	ID        string
	SessionID string
	MessageID *string
	Source    string
	EventType string
	Payload   []byte
	CreatedAt time.Time
}

// AssistantRuntimeEventCreateParams 描述写入 assistant runtime 事件所需的最小字段。
type AssistantRuntimeEventCreateParams struct {
	SessionID string
	MessageID *string
	Source    string
	EventType string
	Payload   []byte
	CreatedAt time.Time
}

// AssistantRuntimeEventRepo 封装 assistant_runtime_events 表的访问能力。
type AssistantRuntimeEventRepo struct {
	pool *pgxpool.Pool
}

var defaultAssistantRuntimeEventCreatedAtClock monotonicTimestampClock

// NewAssistantRuntimeEventRepo 使用连接池创建 assistant runtime 事件仓储。
func NewAssistantRuntimeEventRepo(pool *pgxpool.Pool) *AssistantRuntimeEventRepo {
	return &AssistantRuntimeEventRepo{pool: pool}
}

// Add 写入一条 assistant runtime 事件。
func (r *AssistantRuntimeEventRepo) Add(ctx context.Context, params AssistantRuntimeEventCreateParams) (*AssistantRuntimeEvent, error) {
	event, err := scanAssistantRuntimeEvent(r.pool.QueryRow(ctx, `
		INSERT INTO assistant_runtime_events (session_id, message_id, source, event_type, payload, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6)
		RETURNING id, session_id, message_id, source, event_type, payload, created_at
	`, assistantRuntimeEventInsertArgs(params)...))
	if err != nil {
		return nil, err
	}

	return &event, nil
}

// ListBySession 按创建时间正序返回同一会话下的 runtime 事件。
func (r *AssistantRuntimeEventRepo) ListBySession(ctx context.Context, sessionID string) ([]AssistantRuntimeEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, session_id, message_id, source, event_type, payload, created_at
		FROM assistant_runtime_events
		WHERE session_id = $1
		ORDER BY created_at ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]AssistantRuntimeEvent, 0)
	for rows.Next() {
		event, err := scanAssistantRuntimeEvent(rows)
		if err != nil {
			return nil, err
		}

		events = append(events, event)
	}

	return events, rows.Err()
}

// assistantRuntimeEventInsertArgs 统一 assistant runtime 事件写库时的默认值与时间精度。
func assistantRuntimeEventInsertArgs(params AssistantRuntimeEventCreateParams) []any {
	payload := params.Payload
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}

	createdAt := params.CreatedAt
	if createdAt.IsZero() {
		createdAt = defaultAssistantRuntimeEventCreatedAtClock.Next(time.Now().UTC())
	} else {
		createdAt = createdAt.UTC().Truncate(time.Microsecond)
	}

	return []any{
		params.SessionID,
		params.MessageID,
		params.Source,
		params.EventType,
		string(payload),
		createdAt,
	}
}

// scanAssistantRuntimeEvent 把当前数据库行扫描成 assistant runtime 事件。
func scanAssistantRuntimeEvent(row pgx.Row) (AssistantRuntimeEvent, error) {
	var event AssistantRuntimeEvent

	err := row.Scan(
		&event.ID,
		&event.SessionID,
		&event.MessageID,
		&event.Source,
		&event.EventType,
		&event.Payload,
		&event.CreatedAt,
	)
	if err != nil {
		return AssistantRuntimeEvent{}, err
	}

	return event, nil
}
