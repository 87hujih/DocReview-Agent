package outbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrLeaseLost = errors.New("outbox 事件租约为没有 longer owned")

type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 校验依赖并创建对应实例。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

type Event struct {
	ID              string
	AggregateType   string
	AggregateID     string
	EventType       string
	IdempotencyKey  *string
	PayloadJSON     json.RawMessage
	Status          string
	AttemptCount    int
	NextAttemptAt   *time.Time
	ClaimedBy       *string
	LeaseExpiresAt  *time.Time
	LeaseGeneration int64
	ErrorJSON       json.RawMessage
	CreatedAt       time.Time
	PublishedAt     *time.Time
}

type EnqueueParams struct {
	AggregateType  string
	AggregateID    string
	EventType      string
	IdempotencyKey *string
	PayloadJSON    json.RawMessage
	NextAttemptAt  *time.Time
}

type ClaimParams struct {
	Now           time.Time
	WorkerID      string
	LeaseDuration time.Duration
	Limit         int
	EventTypes    []string
}

type PublishParams struct {
	EventID         string
	WorkerID        string
	LeaseGeneration int64
	PublishedAt     time.Time
}

type RetryParams struct {
	EventID         string
	WorkerID        string
	LeaseGeneration int64
	ErrorJSON       json.RawMessage
	NextAttemptAt   time.Time
	Now             time.Time
	DeadLetter      bool
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

const eventColumns = `
	id, aggregate_type, aggregate_id, event_type, idempotency_key, payload_json,
	status, attempt_count, next_attempt_at, claimed_by, lease_expires_at,
	lease_generation, error_json, created_at, published_at`

const claimEventsSQL = `
WITH candidates AS (
	SELECT event.id AS candidate_event_id
	FROM outbox_events AS event
	WHERE event.status = 'pending'
	  AND (event.next_attempt_at IS NULL OR event.next_attempt_at <= $1)
	  AND ($5::text[] IS NULL OR event.event_type = ANY($5::text[]))
	ORDER BY event.next_attempt_at NULLS FIRST, event.created_at, event.id
	FOR UPDATE OF event SKIP LOCKED
	LIMIT $4
)
UPDATE outbox_events AS event
SET status = 'publishing',
	claimed_by = $2,
	lease_expires_at = $3,
	lease_generation = event.lease_generation + 1,
	attempt_count = event.attempt_count + 1,
	next_attempt_at = NULL
FROM candidates
WHERE event.id = candidates.candidate_event_id
RETURNING ` + eventColumns

const markPublishedSQL = `
	UPDATE outbox_events
	SET status = 'published', published_at = $4, error_json = NULL,
		claimed_by = NULL, lease_expires_at = NULL
	WHERE id = $1 AND status = 'publishing' AND claimed_by = $2 AND lease_generation = $3
	  AND lease_expires_at > $4`

const scheduleRetrySQL = `
	UPDATE outbox_events
	SET status = $4, error_json = $5, next_attempt_at = $6,
		claimed_by = NULL, lease_expires_at = NULL
	WHERE id = $1 AND status = 'publishing' AND claimed_by = $2 AND lease_generation = $3
	  AND lease_expires_at > $7`

// Enqueue 执行该函数负责的核心处理逻辑。
func (r *Repository) Enqueue(ctx context.Context, tx pgx.Tx, params EnqueueParams) (*Event, bool, error) {
	params.AggregateType = strings.TrimSpace(params.AggregateType)
	params.AggregateID = strings.TrimSpace(params.AggregateID)
	params.EventType = strings.TrimSpace(params.EventType)
	if params.AggregateType == "" || params.AggregateID == "" || params.EventType == "" {
		return nil, false, fmt.Errorf("aggregate_type、aggregate_id、和 event_type 不能为空")
	}
	payloadJSON, err := normalizeJSONObject(params.PayloadJSON, "payload_json")
	if err != nil {
		return nil, false, err
	}
	params.IdempotencyKey = trimOptional(params.IdempotencyKey)
	if params.IdempotencyKey == nil {
		return nil, false, fmt.Errorf("idempotency_key is required")
	}

	var query rowQuerier = r.pool
	if tx != nil {
		query = tx
	}
	event, err := scanEvent(query.QueryRow(ctx, `
		INSERT INTO outbox_events (
			aggregate_type, aggregate_id, event_type, idempotency_key, payload_json, next_attempt_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING
		RETURNING `+eventColumns,
		params.AggregateType, params.AggregateID, params.EventType,
		params.IdempotencyKey, payloadJSON, params.NextAttemptAt,
	))
	if err == nil {
		return &event, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	event, err = scanEvent(query.QueryRow(ctx, `
		SELECT `+eventColumns+`
		FROM outbox_events
		WHERE aggregate_type = $1 AND aggregate_id = $2 AND idempotency_key = $3
	`, params.AggregateType, params.AggregateID, *params.IdempotencyKey))
	if err != nil {
		return nil, false, err
	}
	if event.EventType != params.EventType || !jsonEqual(event.PayloadJSON, payloadJSON) {
		return nil, false, fmt.Errorf("outbox idempotency 键 conflicts 包含 different 事件内容")
	}
	return &event, false, nil
}

// 声明 执行该函数负责的核心处理逻辑。
func (r *Repository) Claim(ctx context.Context, params ClaimParams) ([]Event, error) {
	params.WorkerID = strings.TrimSpace(params.WorkerID)
	if params.Now.IsZero() || params.WorkerID == "" || params.LeaseDuration <= 0 {
		return nil, fmt.Errorf("now、工作进程_id、和正数租约时长不能为空")
	}
	if params.Limit <= 0 || params.Limit > 1000 {
		return nil, fmt.Errorf("上限必须为介于 1 和 1000")
	}
	eventTypes := make([]string, 0, len(params.EventTypes))
	seenTypes := make(map[string]struct{}, len(params.EventTypes))
	for _, eventType := range params.EventTypes {
		eventType = strings.TrimSpace(eventType)
		if eventType == "" {
			return nil, fmt.Errorf("outbox claim event type is required")
		}
		if _, exists := seenTypes[eventType]; !exists {
			seenTypes[eventType] = struct{}{}
			eventTypes = append(eventTypes, eventType)
		}
	}
	if len(eventTypes) > 100 {
		return nil, fmt.Errorf("处理失败：outbox 声明事件类型 filter 为 too large")
	}
	var eventTypesArg any
	if len(eventTypes) > 0 {
		eventTypesArg = eventTypes
	}

	rows, err := r.pool.Query(ctx, claimEventsSQL, params.Now, params.WorkerID, params.Now.Add(params.LeaseDuration), params.Limit, eventTypesArg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]Event, 0, params.Limit)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// MarkPublished 执行该函数负责的核心处理逻辑。
func (r *Repository) MarkPublished(ctx context.Context, params PublishParams) error {
	if strings.TrimSpace(params.EventID) == "" || strings.TrimSpace(params.WorkerID) == "" || params.LeaseGeneration <= 0 || params.PublishedAt.IsZero() {
		return fmt.Errorf("有效的事件租约和 published_at 不能为空")
	}
	tag, err := r.pool.Exec(ctx, markPublishedSQL, params.EventID, params.WorkerID, params.LeaseGeneration, params.PublishedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// ScheduleRetry 执行该函数负责的核心处理逻辑。
func (r *Repository) ScheduleRetry(ctx context.Context, params RetryParams) error {
	errorJSON, err := normalizeJSONObject(params.ErrorJSON, "error_json")
	if err != nil {
		return err
	}
	if strings.TrimSpace(params.EventID) == "" || strings.TrimSpace(params.WorkerID) == "" || params.LeaseGeneration <= 0 || params.Now.IsZero() {
		return fmt.Errorf("有效的事件租约和 now 不能为空")
	}
	status := "pending"
	if params.DeadLetter {
		status = "dead_letter"
	} else if params.NextAttemptAt.Before(params.Now) {
		return fmt.Errorf("next_attempt_at 必须 not 为之前 now")
	}
	tag, err := r.pool.Exec(ctx, scheduleRetrySQL, params.EventID, params.WorkerID, params.LeaseGeneration, status, errorJSON, nullableRetryTime(status, params.NextAttemptAt), params.Now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// RecoverExpiredLeases 执行该函数负责的核心处理逻辑。
func (r *Repository) RecoverExpiredLeases(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		return 0, fmt.Errorf("now 不能为空")
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE outbox_events
		SET status = 'pending', claimed_by = NULL, lease_expires_at = NULL,
			next_attempt_at = $1,
			error_json = jsonb_build_object('category', 'lease_expired', 'retryable', true)
		WHERE status = 'publishing' AND lease_expires_at <= $1
	`, now)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// scanEvent 执行该函数负责的核心处理逻辑。
func scanEvent(row pgx.Row) (Event, error) {
	var value Event
	err := row.Scan(
		&value.ID, &value.AggregateType, &value.AggregateID, &value.EventType,
		&value.IdempotencyKey, &value.PayloadJSON, &value.Status, &value.AttemptCount,
		&value.NextAttemptAt, &value.ClaimedBy, &value.LeaseExpiresAt,
		&value.LeaseGeneration, &value.ErrorJSON, &value.CreatedAt, &value.PublishedAt,
	)
	return value, err
}

// normalizeJSONObject 执行该函数负责的核心处理逻辑。
func normalizeJSONObject(value json.RawMessage, field string) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("处理失败：%s 必须为有效的 JSON", field)
	}
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%s 必须是 JSON 对象", field)
	}
	normalized, err := json.Marshal(object)
	return normalized, err
}

// jsonEqual 执行该函数负责的核心处理逻辑。
func jsonEqual(left json.RawMessage, right json.RawMessage) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	leftJSON, _ := json.Marshal(leftValue)
	rightJSON, _ := json.Marshal(rightValue)
	return bytes.Equal(leftJSON, rightJSON)
}

// trimOptional 执行该函数负责的核心处理逻辑。
func trimOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// nullableRetryTime 执行该函数负责的核心处理逻辑。
func nullableRetryTime(status string, value time.Time) any {
	if status == "dead_letter" {
		return nil
	}
	return value
}
