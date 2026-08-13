package agentturn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	turn "agent_project/apps/server/internal/agent/turn"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const createTurnSQL = `
INSERT INTO agent_turns (
	organization_id, workspace_id, resource_id, session_id, idempotency_scope,
	request_id, trace_id, principal_type, principal_id, trust_source, runtime_mode,
	input_json, input_hash
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (idempotency_scope, request_id) DO NOTHING
RETURNING id, session_id, request_id, status, created_at, updated_at`

// 处理失败： createFactsSQL 为 deliberately 一个 data-modifying CTE statement executed 位于
// the same 事务 as createTurnSQL. 一个 crash 不能 expose 一个 user 消息
// 不包含 its 持久化的 运行 和 initial 类型化的 步骤 (或 the inverse).
const createFactsSQL = `
WITH locked_session AS MATERIALIZED (
	SELECT id
	FROM assistant_sessions
	WHERE id = $2
	FOR UPDATE
),
created_session AS (
	INSERT INTO assistant_sessions (title, last_message_at, updated_at)
	SELECT $3, $8, $8
	WHERE $2::uuid IS NULL
	RETURNING id
),
selected_session AS MATERIALIZED (
	SELECT id FROM locked_session
	UNION ALL
	SELECT id FROM created_session
),
linked_turn AS (
	UPDATE agent_turns
	SET session_id = selected_session.id, updated_at = $8
	FROM selected_session
	WHERE agent_turns.id = $1
	RETURNING agent_turns.id, selected_session.id AS session_id
),
inserted_message AS (
	INSERT INTO assistant_messages (session_id, turn_id, role, kind, sequence_no, payload, created_at)
	SELECT linked_turn.session_id, linked_turn.id, 'user', 'text',
		COALESCE((SELECT MAX(sequence_no) FROM assistant_messages WHERE session_id = linked_turn.session_id), 0) + 1,
		jsonb_build_object('content', $4::jsonb ->> 'message'), $8
	FROM linked_turn
	RETURNING id, session_id
),
inserted_run AS (
	INSERT INTO agent_runs (
		organization_id, workspace_id, resource_id, session_id, request_id, trace_id, turn_id,
		principal_type, principal_id, trust_source, runtime_mode,
		status, objective, max_steps, max_tool_calls, state_json
	)
	SELECT $5, $6, $9::uuid, linked_turn.session_id, 'turn:' || $1::text, $7, $1,
		$10, $11, $12, $13,
		'queued', $4::jsonb ->> 'message', 64, 32,
		jsonb_build_object('turn_id', $1::text, 'resource_id', ($9::uuid)::text, 'runtime_mode', $13::text)
	FROM linked_turn
	RETURNING id, session_id
),
inserted_step AS (
	INSERT INTO agent_steps (run_id, step_key, step_type, input_json, max_attempts)
	SELECT inserted_run.id, 'understand_goal:1', 'UnderstandGoal',
		jsonb_build_object('message', $4::jsonb ->> 'message', 'resource_id', ($9::uuid)::text), 5
	FROM inserted_run
	RETURNING id, run_id
),
inserted_events AS (
	INSERT INTO agent_turn_events (turn_id, sequence_no, event_type, payload_json, created_at)
	SELECT $1, 1, 'turn.accepted', jsonb_build_object('turn_id', $1::text), $8
	UNION ALL
	SELECT $1, 2, 'run.queued', jsonb_build_object('run_id', inserted_step.run_id::text), $8
	FROM inserted_step
	RETURNING turn_id
),
inserted_outbox AS (
	INSERT INTO outbox_events (
		aggregate_type, aggregate_id, event_type, idempotency_key, payload_json, status, created_at
	)
	SELECT 'agent_turn', $1::text, 'agent.turn.accepted', 'turn-accepted:' || $1::text,
		jsonb_build_object('turn_id', $1::text, 'run_id', inserted_step.run_id::text), 'pending', $8
	FROM inserted_step
	RETURNING id
)
SELECT inserted_run.session_id, inserted_run.id
FROM inserted_run, inserted_outbox`

const selectTurnSQL = `
SELECT turn.id, turn.session_id, run.id, turn.request_id, turn.status, turn.created_at, turn.updated_at,
	turn.input_hash
FROM agent_turns AS turn
LEFT JOIN agent_runs AS run ON run.turn_id = turn.id
WHERE turn.idempotency_scope = $1 AND turn.request_id = $2`

const createOutcomeSQL = `
INSERT INTO agent_turn_outcomes (
	turn_id, idempotency_key, outcome_hash, status, output_json, error_json
)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (turn_id, idempotency_key) DO NOTHING
RETURNING id, outcome_hash`

const createOutcomeFactsSQL = `
WITH locked_session AS MATERIALIZED (
	SELECT id
	FROM assistant_sessions
	WHERE id = $3
	FOR UPDATE
),
message_base AS MATERIALIZED (
	SELECT COALESCE(MAX(sequence_no), 0) AS sequence_no
	FROM assistant_messages
	WHERE session_id = $3
),
event_base AS MATERIALIZED (
	SELECT COALESCE(MAX(sequence_no), 0) AS sequence_no
	FROM agent_turn_events
	WHERE turn_id = $2
),
message_items AS MATERIALIZED (
	SELECT item.value AS message, item.ordinality::integer AS ordinality
	FROM jsonb_array_elements($4::jsonb) WITH ORDINALITY AS item(value, ordinality)
),
inserted_messages AS (
	INSERT INTO assistant_messages (
		session_id, turn_id, outcome_id, role, kind, sequence_no, payload, created_at
	)
	SELECT locked_session.id, $2, $1,
		message_items.message ->> 'role', message_items.message ->> 'kind',
		message_base.sequence_no + message_items.ordinality,
		message_items.message -> 'payload', $8
	FROM locked_session, message_base, message_items
	RETURNING id, role, kind, sequence_no, payload, created_at
),
updated_session AS (
	UPDATE assistant_sessions
	SET last_message_at = $8, updated_at = $8
	FROM locked_session
	WHERE assistant_sessions.id = locked_session.id
	RETURNING assistant_sessions.id
),
updated_turn AS (
	UPDATE agent_turns
	SET status = $5, output_json = $6, error_json = $7, updated_at = $8,
		started_at = COALESCE(started_at, $8),
		completed_at = CASE WHEN $5 IN ('succeeded', 'failed', 'cancelled') THEN $8 ELSE NULL END
	FROM updated_session
	WHERE agent_turns.id = $2
	RETURNING agent_turns.id, agent_turns.session_id, agent_turns.request_id,
		agent_turns.status, agent_turns.created_at, agent_turns.updated_at, agent_turns.workspace_id
),
inserted_events AS (
	INSERT INTO agent_turn_events (turn_id, sequence_no, event_type, payload_json, created_at)
	SELECT $2, event_base.sequence_no + ROW_NUMBER() OVER (ORDER BY inserted_messages.sequence_no, inserted_messages.id)::integer,
		'assistant.message', jsonb_build_object(
			'id', inserted_messages.id::text,
			'role', inserted_messages.role,
			'kind', inserted_messages.kind,
			'sequence_no', inserted_messages.sequence_no,
			'payload', inserted_messages.payload,
			'created_at', inserted_messages.created_at
		), $8
	FROM event_base, inserted_messages
	UNION ALL
	SELECT $2, event_base.sequence_no + jsonb_array_length($4::jsonb) + 1,
		'turn.' || $5, jsonb_build_object('turn_id', $2::text, 'status', $5), $8
	FROM event_base
	RETURNING id
),
upserted_public_projection AS (
	INSERT INTO agent_turn_public_projections (
		turn_id, workspace_id, status, dto_json, content_hash, last_event_sequence, created_at, updated_at
	)
	SELECT updated_turn.id, updated_turn.workspace_id, updated_turn.status,
		jsonb_build_object(
			'status', updated_turn.status,
			'output', $6::jsonb,
			'error', $7::jsonb,
			'messages', $4::jsonb
		),
		$9, event_base.sequence_no + jsonb_array_length($4::jsonb) + 1, $8, $8
	FROM updated_turn, event_base
	WHERE updated_turn.workspace_id IS NOT NULL
	  AND updated_turn.status IN ('waiting_input', 'waiting_approval', 'succeeded', 'failed', 'cancelled')
	ON CONFLICT (turn_id) DO UPDATE
	SET status = EXCLUDED.status,
		dto_json = EXCLUDED.dto_json,
		content_hash = EXCLUDED.content_hash,
		last_event_sequence = EXCLUDED.last_event_sequence,
		updated_at = EXCLUDED.updated_at
	WHERE agent_turn_public_projections.last_event_sequence <= EXCLUDED.last_event_sequence
	RETURNING turn_id
),
inserted_outbox AS (
	INSERT INTO outbox_events (
		aggregate_type, aggregate_id, event_type, idempotency_key, payload_json, status, created_at
	)
	SELECT 'agent_turn', $2::text, 'agent.turn.outcome_committed',
		'turn-outcome:' || $1::text,
		jsonb_build_object('turn_id', $2::text, 'outcome_id', $1::text, 'status', $5),
		'pending', $8
	FROM updated_turn
	RETURNING id
)
SELECT updated_turn.id, updated_turn.session_id, updated_turn.request_id,
	updated_turn.status, updated_turn.created_at, updated_turn.updated_at
FROM updated_turn, inserted_outbox`

const selectTurnByIDSQL = `
SELECT turn.id, turn.session_id, run.id, turn.request_id, turn.status, turn.created_at, turn.updated_at
FROM agent_turns AS turn
LEFT JOIN agent_runs AS run ON run.turn_id = turn.id
WHERE turn.id = $1`

type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 校验依赖并创建对应实例。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Accept 执行该函数负责的核心处理逻辑。
func (r *Repository) Accept(ctx context.Context, input turn.AcceptInput) (turn.Turn, bool, error) {
	prepared, scope, err := prepare(input)
	if err != nil {
		return turn.Turn{}, false, err
	}
	if r == nil || r.pool == nil {
		return turn.Turn{}, false, fmt.Errorf("agent 轮次数据库不能为空")
	}

	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return turn.Turn{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	record, created, err := insertOrGetTurn(ctx, tx, prepared, scope)
	if err != nil {
		return turn.Turn{}, false, err
	}
	if created {
		now := time.Now().UTC()
		var sessionID, runID string
		err = tx.QueryRow(ctx, createFactsSQL,
			record.ID, nullable(prepared.Request.SessionID), titleFor(prepared.Request.Message),
			prepared.InputJSON, nullable(prepared.Request.OrganizationID), nullable(prepared.Request.WorkspaceID),
			nullable(prepared.Request.TraceID), now, nullable(prepared.Request.ResourceID),
			nullable(prepared.Request.PrincipalType), nullable(prepared.Request.PrincipalID),
			nullable(prepared.Request.TrustSource), nullable(prepared.Request.RuntimeMode),
		).Scan(&sessionID, &runID)
		if errors.Is(err, pgx.ErrNoRows) {
			return turn.Turn{}, false, fmt.Errorf("处理失败：助手 session does not exist")
		}
		if err != nil {
			return turn.Turn{}, false, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE assistant_sessions
			SET last_message_at = $2, updated_at = $2
			WHERE id = $1
		`, sessionID, now); err != nil {
			return turn.Turn{}, false, err
		}
		record.SessionID = sessionID
		record.RunID = runID
		record.UpdatedAt = now
	}
	if err := tx.Commit(ctx); err != nil {
		return turn.Turn{}, false, err
	}
	return record.Turn, created, nil
}

// Commit 执行该函数负责的核心处理逻辑。
func (r *Repository) Commit(ctx context.Context, input turn.CommitInput) (turn.Turn, bool, error) {
	if err := validateCommit(input); err != nil {
		return turn.Turn{}, false, err
	}
	if r == nil || r.pool == nil {
		return turn.Turn{}, false, fmt.Errorf("agent 轮次数据库不能为空")
	}
	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return turn.Turn{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var outcomeID, storedHash string
	err = tx.QueryRow(ctx, createOutcomeSQL,
		input.Outcome.TurnID, input.Outcome.IdempotencyKey, input.OutcomeHash,
		input.Outcome.Status, input.Outcome.OutputJSON, nullableJSON(input.Outcome.ErrorJSON),
	).Scan(&outcomeID, &storedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			SELECT id, outcome_hash
			FROM agent_turn_outcomes
			WHERE turn_id = $1 AND idempotency_key = $2
		`, input.Outcome.TurnID, input.Outcome.IdempotencyKey).Scan(&outcomeID, &storedHash)
		if err != nil {
			return turn.Turn{}, false, err
		}
		if storedHash != input.OutcomeHash {
			return turn.Turn{}, false, turn.ErrIdempotencyConflict
		}
		record, err := scanTurn(tx.QueryRow(ctx, selectTurnByIDSQL, input.Outcome.TurnID))
		if err != nil {
			return turn.Turn{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return turn.Turn{}, false, err
		}
		return record, false, nil
	}
	if err != nil {
		return turn.Turn{}, false, err
	}

	var sessionID string
	var currentStatus turn.Status
	if err := tx.QueryRow(ctx, `
		SELECT session_id, status
		FROM agent_turns
		WHERE id = $1
		FOR UPDATE
	`, input.Outcome.TurnID).Scan(&sessionID, &currentStatus); err != nil {
		return turn.Turn{}, false, err
	}
	if !turn.CanTransition(currentStatus, input.Outcome.Status) {
		return turn.Turn{}, false, fmt.Errorf("无效的轮次 transition %s -> %s", currentStatus, input.Outcome.Status)
	}
	messagesJSON, err := json.Marshal(input.Outcome.Messages)
	if err != nil {
		return turn.Turn{}, false, err
	}
	now := time.Now().UTC()
	var record turn.Turn
	err = tx.QueryRow(ctx, createOutcomeFactsSQL,
		outcomeID, input.Outcome.TurnID, sessionID, messagesJSON, input.Outcome.Status,
		input.Outcome.OutputJSON, nullableJSON(input.Outcome.ErrorJSON), now, input.OutcomeHash,
	).Scan(&record.ID, &record.SessionID, &record.RequestID, &record.Status, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		return turn.Turn{}, false, err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM agent_runs WHERE turn_id = $1`, record.ID).Scan(&record.RunID); err != nil {
		return turn.Turn{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return turn.Turn{}, false, err
	}
	return record, true, nil
}

// 事件 执行该函数负责的核心处理逻辑。
func (r *Repository) Events(ctx context.Context, turnID string, afterSequence int) ([]turn.Event, error) {
	turnID = strings.TrimSpace(turnID)
	if _, err := uuid.Parse(turnID); err != nil || afterSequence < 0 {
		return nil, fmt.Errorf("有效的 turn_id 和 non-负数事件 curs或不能为空")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, turn_id, sequence_no, event_type, payload_json, created_at
		FROM agent_turn_events
		WHERE turn_id = $1 AND sequence_no > $2
		ORDER BY sequence_no, id
	`, turnID, afterSequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []turn.Event
	for rows.Next() {
		var event turn.Event
		if err := rows.Scan(&event.ID, &event.TurnID, &event.Sequence, &event.Type, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

type turnRecord struct {
	turn.Turn
	InputHash string
}

// insertOrGetTurn 执行该函数负责的核心处理逻辑。
func insertOrGetTurn(ctx context.Context, tx pgx.Tx, input turn.AcceptInput, scope string) (turnRecord, bool, error) {
	var record turnRecord
	var insertedSessionID *string
	err := tx.QueryRow(ctx, createTurnSQL,
		nullable(input.Request.OrganizationID), nullable(input.Request.WorkspaceID), nullable(input.Request.ResourceID),
		nullable(input.Request.SessionID), scope, input.Request.RequestID, nullable(input.Request.TraceID),
		nullable(input.Request.PrincipalType), nullable(input.Request.PrincipalID), nullable(input.Request.TrustSource),
		nullable(input.Request.RuntimeMode), input.InputJSON, input.InputHash,
	).Scan(&record.ID, &insertedSessionID, &record.RequestID, &record.Status, &record.CreatedAt, &record.UpdatedAt)
	if err == nil {
		if insertedSessionID != nil {
			record.SessionID = *insertedSessionID
		}
		record.InputHash = input.InputHash
		return record, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return turnRecord{}, false, err
	}

	var sessionID *string
	var runID *string
	err = tx.QueryRow(ctx, selectTurnSQL, scope, input.Request.RequestID).Scan(
		&record.ID, &sessionID, &runID, &record.RequestID, &record.Status,
		&record.CreatedAt, &record.UpdatedAt, &record.InputHash,
	)
	if err != nil {
		return turnRecord{}, false, err
	}
	if record.InputHash != input.InputHash {
		return turnRecord{}, false, turn.ErrIdempotencyConflict
	}
	if sessionID != nil {
		record.SessionID = *sessionID
	}
	if runID != nil {
		record.RunID = *runID
	}
	return record, false, nil
}

// scanTurn 执行该函数负责的核心处理逻辑。
func scanTurn(row pgx.Row) (turn.Turn, error) {
	var record turn.Turn
	var sessionID, runID *string
	err := row.Scan(&record.ID, &sessionID, &runID, &record.RequestID, &record.Status, &record.CreatedAt, &record.UpdatedAt)
	if sessionID != nil {
		record.SessionID = *sessionID
	}
	if runID != nil {
		record.RunID = *runID
	}
	return record, err
}

// prepare 执行该函数负责的核心处理逻辑。
func prepare(input turn.AcceptInput) (turn.AcceptInput, string, error) {
	input.Request.RequestID = strings.TrimSpace(input.Request.RequestID)
	input.Request.OrganizationID = strings.TrimSpace(input.Request.OrganizationID)
	input.Request.WorkspaceID = strings.TrimSpace(input.Request.WorkspaceID)
	input.Request.ResourceID = strings.TrimSpace(input.Request.ResourceID)
	input.Request.SessionID = strings.TrimSpace(input.Request.SessionID)
	input.Request.TraceID = strings.TrimSpace(input.Request.TraceID)
	input.Request.PrincipalType = strings.ToLower(strings.TrimSpace(input.Request.PrincipalType))
	input.Request.PrincipalID = strings.TrimSpace(input.Request.PrincipalID)
	input.Request.TrustSource = strings.TrimSpace(input.Request.TrustSource)
	input.Request.RuntimeMode = strings.ToLower(strings.TrimSpace(input.Request.RuntimeMode))
	if input.Request.RequestID == "" || strings.TrimSpace(input.Request.Message) == "" {
		return turn.AcceptInput{}, "", fmt.Errorf("request_id 和消息不能为空")
	}
	for name, value := range map[string]string{
		"organization_id": input.Request.OrganizationID,
		"workspace_id":    input.Request.WorkspaceID,
		"resource_id":     input.Request.ResourceID,
		"session_id":      input.Request.SessionID,
		"principal_id":    input.Request.PrincipalID,
	} {
		if value != "" {
			if _, err := uuid.Parse(value); err != nil {
				return turn.AcceptInput{}, "", fmt.Errorf("处理失败：%s 必须为一个 UUID", name)
			}
		}
	}
	if input.Request.RuntimeMode != "" && input.Request.RuntimeMode != "legacy" && input.Request.RuntimeMode != "shadow" && input.Request.RuntimeMode != "durable" {
		return turn.AcceptInput{}, "", fmt.Errorf("runtime_mode 无效")
	}
	if input.Request.PrincipalType != "" && input.Request.PrincipalType != "user" && input.Request.PrincipalType != "service" {
		return turn.AcceptInput{}, "", fmt.Errorf("principal_type 无效")
	}
	if input.Request.RuntimeMode == "durable" && (input.Request.WorkspaceID == "" || input.Request.ResourceID == "" ||
		input.Request.PrincipalType == "" || input.Request.PrincipalID == "" || input.Request.TrustSource == "") {
		return turn.AcceptInput{}, "", fmt.Errorf("durable request requires trusted principal, workspace, resource, and trust source")
	}
	var decoded map[string]any
	if len(input.InputJSON) == 0 || json.Unmarshal(input.InputJSON, &decoded) != nil || decoded == nil {
		return turn.AcceptInput{}, "", fmt.Errorf("输入_json 必须是 JSON 对象")
	}
	digest := sha256.Sum256(input.InputJSON)
	expectedHash := "sha256:" + hex.EncodeToString(digest[:])
	if input.InputHash != expectedHash {
		return turn.AcceptInput{}, "", fmt.Errorf("input_hash does not match input_json")
	}

	scope := "global"
	if input.Request.SessionID != "" {
		scope = "session:" + input.Request.SessionID
	}
	if input.Request.OrganizationID != "" {
		scope = "organization:" + input.Request.OrganizationID
	}
	if input.Request.WorkspaceID != "" {
		scope = "workspace:" + input.Request.WorkspaceID
	}
	return input, scope, nil
}

// validateCommit 校验输入及领域约束。
func validateCommit(input turn.CommitInput) error {
	if _, err := uuid.Parse(strings.TrimSpace(input.Outcome.TurnID)); err != nil {
		return fmt.Errorf("处理失败：turn_id 必须为一个 UUID")
	}
	prepared, err := turn.PrepareOutcome(input.Outcome)
	if err != nil {
		return err
	}
	if prepared.OutcomeHash != input.OutcomeHash {
		return fmt.Errorf("outcome_hash 不匹配 canonical 结果")
	}
	return nil
}

// nullable 执行该函数负责的核心处理逻辑。
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// nullableJSON 执行该函数负责的核心处理逻辑。
func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

// titleFor 执行该函数负责的核心处理逻辑。
func titleFor(message string) string {
	title := strings.TrimSpace(message)
	const maxRunes = 80
	if utf8.RuneCountInString(title) <= maxRunes {
		return title
	}
	runes := []rune(title)
	return strings.TrimSpace(string(runes[:maxRunes]))
}
