package agentops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/agent/operations"

	"github.com/jackc/pgx/v5"
)

const cancelSQL = `
WITH request_lock AS (
    SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':' || $2, 0))
), existing AS (
    SELECT action.* FROM agent_runtime_operator_actions AS action, request_lock
    WHERE action.workspace_id = $1 AND action.request_id = $2
    FOR UPDATE
), target_run AS (
    SELECT run.id, run.status AS previous_status
    FROM agent_runs AS run, request_lock
    WHERE run.workspace_id = $1 AND run.id = $4::uuid
      AND run.status IN ('queued', 'running', 'waiting_input', 'waiting_approval')
      AND NOT EXISTS (SELECT 1 FROM existing)
    FOR UPDATE OF run
), changed_run AS (
    UPDATE agent_runs AS run
    SET cancel_requested_at = COALESCE(run.cancel_requested_at, $6),
        status = CASE WHEN run.status IN ('waiting_input', 'waiting_approval') THEN 'queued' ELSE run.status END,
        version = run.version + 1, updated_at = $6
    FROM target_run AS target
    WHERE run.id = target.id
    RETURNING run.id, target.previous_status, run.status
), awakened AS (
    UPDATE agent_steps AS step
    SET status = 'queued', next_retry_at = $6, completed_at = NULL,
        claimed_by = NULL, lease_expires_at = NULL, heartbeat_at = NULL, updated_at = $6
    FROM changed_run AS run
    WHERE step.run_id = run.id AND step.status IN ('waiting_input', 'waiting_approval')
    RETURNING step.id
), result AS (
    SELECT jsonb_build_object(
        'run_id', run.id::text, 'previous_status', run.previous_status,
        'status', run.status, 'awakened_steps', (SELECT count(*) FROM awakened)
    ) AS result_json
    FROM changed_run AS run
), inserted AS (
    INSERT INTO agent_runtime_operator_actions (
        workspace_id, request_id, action_type, target_type, target_id,
        operator_id, reason, input_json, result_json, requested_at, completed_at
    )
    SELECT $1, $2, 'cancel_run', 'run', $4, $3, $5,
           jsonb_build_object('run_id', $4), result.result_json, $6, $6
    FROM result
    RETURNING *
)
SELECT id::text, action_type, target_id, status, result_json, operator_id, reason, false
FROM inserted
UNION ALL
SELECT id::text, action_type, target_id, status, result_json, operator_id, reason, true
FROM existing`

const retrySQL = `
WITH request_lock AS (
    SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':' || $2, 0))
), existing AS (
    SELECT action.* FROM agent_runtime_operator_actions AS action, request_lock
    WHERE action.workspace_id = $1 AND action.request_id = $2
    FOR UPDATE
), target_run AS (
    SELECT run.id
    FROM agent_runs AS run, request_lock
    WHERE run.workspace_id = $1 AND run.id = $4::uuid AND run.status = 'failed'
      AND run.cancel_requested_at IS NULL
      AND NOT EXISTS (SELECT 1 FROM existing)
    FOR UPDATE OF run
), target_step AS (
    SELECT step.id, step.step_key, step.attempt_count, step.max_attempts,
           COALESCE(step.error_json ->> 'category', '') AS error_category
    FROM agent_steps AS step
    JOIN target_run AS run ON run.id = step.run_id
    WHERE step.status = 'failed'
      AND COALESCE(step.error_json ->> 'category', '') IN ('rate_limited', 'timeout', 'retryable_upstream', 'lease_expired')
    ORDER BY step.created_at DESC, step.id DESC
    LIMIT 1
    FOR UPDATE OF step
), changed_step AS (
    UPDATE agent_steps AS step
    SET status = 'queued', next_retry_at = $6, completed_at = NULL,
        claimed_by = NULL, lease_expires_at = NULL, heartbeat_at = NULL,
        max_attempts = GREATEST(max_attempts, attempt_count + 1), updated_at = $6
    FROM target_step AS target
    WHERE step.id = target.id
    RETURNING step.id, step.run_id, step.step_key, step.attempt_count, step.max_attempts, target.error_category
), changed_run AS (
    UPDATE agent_runs AS run
    SET status = 'queued', current_step = step.step_key, version = run.version + 1, updated_at = $6
    FROM changed_step AS step
    WHERE run.id = step.run_id AND run.status = 'failed'
    RETURNING run.id
), result AS (
    SELECT jsonb_build_object(
        'run_id', run.id::text, 'step_id', step.id::text, 'step_key', step.step_key,
        'attempt_count', step.attempt_count, 'max_attempts', step.max_attempts,
        'error_category', step.error_category, 'status', 'queued'
    ) AS result_json
    FROM changed_run AS run JOIN changed_step AS step ON step.run_id = run.id
), inserted AS (
    INSERT INTO agent_runtime_operator_actions (
        workspace_id, request_id, action_type, target_type, target_id,
        operator_id, reason, input_json, result_json, requested_at, completed_at
    )
    SELECT $1, $2, 'retry_run', 'run', $4, $3, $5,
           jsonb_build_object('run_id', $4), result.result_json, $6, $6
    FROM result
    RETURNING *
)
SELECT id::text, action_type, target_id, status, result_json, operator_id, reason, false
FROM inserted
UNION ALL
SELECT id::text, action_type, target_id, status, result_json, operator_id, reason, true
FROM existing`

const replayDeadLetterSQL = `
WITH request_lock AS (
    SELECT pg_advisory_xact_lock(hashtextextended($1::text || ':' || $2, 0))
), existing AS (
    SELECT action.* FROM agent_runtime_operator_actions AS action, request_lock
    WHERE action.workspace_id = $1 AND action.request_id = $2
    FOR UPDATE
), target_event AS (
    SELECT event.id, event.event_type, event.attempt_count
    FROM outbox_events AS event
    JOIN agent_runs AS run ON run.id::text = event.payload_json ->> 'run_id'
    CROSS JOIN request_lock
    WHERE run.workspace_id = $1 AND event.id = $4::uuid AND event.status = 'dead_letter'
      AND event.event_type IN ('agent.step.outcome_committed', 'agent.tool_approval.rejected')
      AND NOT EXISTS (SELECT 1 FROM existing)
    FOR UPDATE OF event
), changed_event AS (
    UPDATE outbox_events AS event
    SET status = 'pending', attempt_count = 0, next_attempt_at = $6,
        claimed_by = NULL, lease_expires_at = NULL, error_json = NULL, published_at = NULL
    FROM target_event AS target
    WHERE event.id = $4::uuid AND event.id = target.id
    RETURNING event.id, event.event_type, target.attempt_count AS previous_attempt_count
), result AS (
    SELECT jsonb_build_object(
        'event_id', event.id::text, 'event_type', event.event_type,
        'previous_attempt_count', event.previous_attempt_count,
        'status', 'pending', 'idempotency_identity_preserved', true
    ) AS result_json
    FROM changed_event AS event
), inserted AS (
    INSERT INTO agent_runtime_operator_actions (
        workspace_id, request_id, action_type, target_type, target_id,
        operator_id, reason, input_json, result_json, requested_at, completed_at
    )
    SELECT $1, $2, 'replay_dead_letter', 'outbox_event', $4, $3, $5,
           jsonb_build_object('event_id', $4), result.result_json, $6, $6
    FROM result
    RETURNING *
)
SELECT id::text, action_type, target_id, status, result_json, operator_id, reason, false
FROM inserted
UNION ALL
SELECT id::text, action_type, target_id, status, result_json, operator_id, reason, true
FROM existing`

// Cancel 执行该函数负责的核心处理逻辑。
func (repository *Repository) Cancel(ctx context.Context, request operations.ActionRequest) (operations.ActionResult, error) {
	return repository.executeAction(ctx, request, "cancel_run", "run", cancelSQL)
}

// Retry 执行该函数负责的核心处理逻辑。
func (repository *Repository) Retry(ctx context.Context, request operations.ActionRequest) (operations.ActionResult, error) {
	return repository.executeAction(ctx, request, "retry_run", "run", retrySQL)
}

// ReplayDeadLetter 执行该函数负责的核心处理逻辑。
func (repository *Repository) ReplayDeadLetter(ctx context.Context, request operations.ActionRequest) (operations.ActionResult, error) {
	return repository.executeAction(ctx, request, "replay_dead_letter", "outbox_event", replayDeadLetterSQL)
}

// executeAction 执行该函数负责的核心处理逻辑。
func (repository *Repository) executeAction(ctx context.Context, request operations.ActionRequest, action, targetType, query string) (operations.ActionResult, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.RequestID = strings.TrimSpace(request.RequestID)
	request.OperatorID = strings.TrimSpace(request.OperatorID)
	request.Reason = strings.TrimSpace(request.Reason)
	request.RunID = strings.TrimSpace(request.RunID)
	request.EventID = strings.TrimSpace(request.EventID)
	targetID := request.RunID
	if targetType == "outbox_event" {
		targetID = request.EventID
	}
	if request.WorkspaceID == "" || request.RequestID == "" || request.OperatorID == "" || request.Reason == "" || targetID == "" || request.RequestedAt.IsZero() {
		return operations.ActionResult{}, fmt.Errorf("complete audited 操作员动作 fields 不能为空")
	}
	if repository == nil || repository.pool == nil {
		return operations.ActionResult{}, fmt.Errorf("operations 数据库不能为空")
	}
	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return operations.ActionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var result operations.ActionResult
	var storedOperator, storedReason string
	err = tx.QueryRow(ctx, query,
		request.WorkspaceID, request.RequestID, request.OperatorID, targetID, request.Reason, request.RequestedAt,
	).Scan(&result.ActionID, &result.Action, &result.TargetID, &result.Status, &result.ResultJSON,
		&storedOperator, &storedReason, &result.Replayed)
	if errors.Is(err, pgx.ErrNoRows) {
		return operations.ActionResult{}, ErrNotFound
	}
	if err != nil {
		return operations.ActionResult{}, err
	}
	if result.Action != action || result.TargetID != targetID || storedOperator != request.OperatorID || storedReason != request.Reason {
		return operations.ActionResult{}, ErrActionConflict
	}
	if !json.Valid(result.ResultJSON) {
		return operations.ActionResult{}, fmt.Errorf("操作员动作结果无效 JSON")
	}
	if err := tx.Commit(ctx); err != nil {
		return operations.ActionResult{}, err
	}
	return result, nil
}
