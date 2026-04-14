package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TaskEvent 表示任务执行过程中的结构化事件。
type TaskEvent struct {
	ID        string
	TaskID    string
	RunID     *string
	StepName  string
	Source    string
	Level     string
	EventType string
	Message   string
	Payload   []byte
	CreatedAt time.Time
}

// TaskEventCreateParams 描述写入 task_events 所需的字段。
type TaskEventCreateParams struct {
	TaskID    string
	RunID     *string
	StepName  string
	Source    string
	Level     string
	EventType string
	Message   string
	Payload   []byte
	CreatedAt time.Time
}

// TaskEventRepo 封装 task_events 表的访问能力。
type TaskEventRepo struct {
	pool *pgxpool.Pool
}

// NewTaskEventRepo 使用连接池创建任务事件仓储。
func NewTaskEventRepo(pool *pgxpool.Pool) *TaskEventRepo {
	return &TaskEventRepo{pool: pool}
}

// Add 写入一条任务事件。
func (r *TaskEventRepo) Add(ctx context.Context, params TaskEventCreateParams) (*TaskEvent, error) {
	payload := params.Payload
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	createdAt := params.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	event, err := scanTaskEvent(r.pool.QueryRow(ctx, `
		INSERT INTO task_events (task_id, run_id, step_name, source, level, event_type, message, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9)
		RETURNING id, task_id, run_id, step_name, source, level, event_type, message, payload, created_at
	`, params.TaskID, params.RunID, params.StepName, params.Source, params.Level, params.EventType, params.Message, string(payload), createdAt))
	if err != nil {
		return nil, err
	}

	return &event, nil
}

// ListByTask 按创建时间正序返回任务事件。
func (r *TaskEventRepo) ListByTask(ctx context.Context, taskID string) ([]TaskEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, run_id, step_name, source, level, event_type, message, payload, created_at
		FROM task_events
		WHERE task_id = $1
		ORDER BY created_at ASC, id ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectTaskEvents(rows)
}

// ListByRunID 按创建时间正序返回同一 run_id 下的事件。
func (r *TaskEventRepo) ListByRunID(ctx context.Context, runID string) ([]TaskEvent, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, run_id, step_name, source, level, event_type, message, payload, created_at
		FROM task_events
		WHERE run_id = $1
		ORDER BY created_at ASC, id ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return collectTaskEvents(rows)
}

func collectTaskEvents(rows pgx.Rows) ([]TaskEvent, error) {
	events := make([]TaskEvent, 0)
	for rows.Next() {
		event, err := scanTaskEvent(rows)
		if err != nil {
			return nil, err
		}

		events = append(events, event)
	}

	return events, rows.Err()
}

func scanTaskEvent(row pgx.Row) (TaskEvent, error) {
	var event TaskEvent

	err := row.Scan(
		&event.ID,
		&event.TaskID,
		&event.RunID,
		&event.StepName,
		&event.Source,
		&event.Level,
		&event.EventType,
		&event.Message,
		&event.Payload,
		&event.CreatedAt,
	)
	if err != nil {
		return TaskEvent{}, err
	}

	return event, nil
}
