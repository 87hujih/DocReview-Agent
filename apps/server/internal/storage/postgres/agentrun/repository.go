package agentrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agentruntime "agent_project/apps/server/internal/agent/runtime"
	"agent_project/apps/server/internal/storage/postgres/outbox"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrLeaseLost           = errors.New("agent 步骤租约为没有 longer owned")
	ErrIdempotencyConflict = errors.New("idempotency 键 conflicts 包含 different 输入")
	ErrRunConflict         = errors.New("agent 运行状态或版本 conflict")
)

type Repository struct {
	pool *pgxpool.Pool
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// NewRepository 校验依赖并创建对应实例。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

type Run struct {
	ID                string
	OrganizationID    *string
	WorkspaceID       *string
	SessionID         *string
	TaskID            *string
	RequestID         *string
	TraceID           *string
	Status            agentruntime.RunStatus
	Objective         string
	CurrentStep       *string
	MaxSteps          int
	MaxToolCalls      int
	TokenBudget       *int64
	CostBudget        *float64
	DeadlineAt        *time.Time
	CancelRequestedAt *time.Time
	StateJSON         json.RawMessage
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type Step struct {
	ID              string
	RunID           string
	StepKey         string
	StepType        string
	Status          agentruntime.StepStatus
	InputJSON       json.RawMessage
	OutputJSON      json.RawMessage
	ErrorJSON       json.RawMessage
	ClaimedBy       *string
	LeaseExpiresAt  *time.Time
	HeartbeatAt     *time.Time
	LeaseGeneration int64
	AttemptCount    int
	MaxAttempts     int
	NextRetryAt     *time.Time
	StartedAt       *time.Time
	CompletedAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CreateRunParams struct {
	OrganizationID *string
	WorkspaceID    *string
	SessionID      *string
	TaskID         *string
	RequestID      *string
	TraceID        *string
	Objective      string
	MaxSteps       int
	MaxToolCalls   int
	TokenBudget    *int64
	CostBudget     *float64
	DeadlineAt     *time.Time
	StateJSON      json.RawMessage
}

type CreateStepParams struct {
	RunID       string
	StepKey     string
	StepType    string
	InputJSON   json.RawMessage
	MaxAttempts int
}

type HeartbeatParams struct {
	StepID          string
	WorkerID        string
	LeaseGeneration int64
	Now             time.Time
	LeaseDuration   time.Duration
}

type CompleteStepParams struct {
	StepID          string
	WorkerID        string
	LeaseGeneration int64
	OutputJSON      json.RawMessage
	CompletedAt     time.Time
}

type RetryStepParams struct {
	StepID          string
	WorkerID        string
	LeaseGeneration int64
	ErrorJSON       json.RawMessage
	NextRetryAt     time.Time
	Now             time.Time
}

type TransitionRunParams struct {
	RunID           string
	From            agentruntime.RunStatus
	To              agentruntime.RunStatus
	ExpectedVersion int64
	CurrentStep     *string
	At              time.Time
}

type ResumeWaitingParams struct {
	RunID              string
	StepID             string
	ExpectedRunStatus  agentruntime.RunStatus
	ExpectedStepStatus agentruntime.StepStatus
	At                 time.Time
}

const runColumns = `
	id, organization_id, workspace_id, session_id, task_id, request_id, trace_id,
	status, objective, current_step, max_steps, max_tool_calls, token_budget,
	cost_budget, deadline_at, cancel_requested_at, state_json, version, created_at, updated_at`

const stepColumns = `
	id, run_id, step_key, step_type, status, input_json, output_json, error_json,
	claimed_by, lease_expires_at, heartbeat_at, lease_generation, attempt_count,
	max_attempts, next_retry_at, started_at, completed_at, created_at, updated_at`

const claimStepSQL = `
WITH candidate AS (
	SELECT step.id AS candidate_step_id
	FROM agent_steps AS step
	JOIN agent_runs AS run ON run.id = step.run_id
	WHERE step.status = 'queued'
	  AND (step.next_retry_at IS NULL OR step.next_retry_at <= $1)
	  AND step.attempt_count < step.max_attempts
	  AND run.status IN ('queued', 'running')
	  AND run.runtime_mode = 'durable'
	  AND run.principal_type IS NOT NULL
	  AND run.principal_id IS NOT NULL
	  AND run.trust_source IS NOT NULL
	  AND length(btrim(run.trust_source)) > 0
	  AND run.workspace_id IS NOT NULL
	  AND NOT EXISTS (
		SELECT 1 FROM agent_steps AS active
		WHERE active.run_id = step.run_id AND active.status = 'running'
	  )
	ORDER BY (run.cancel_requested_at IS NOT NULL OR (run.deadline_at IS NOT NULL AND run.deadline_at <= $1)) DESC,
		step.next_retry_at NULLS FIRST, step.created_at, step.id
	FOR UPDATE OF step, run SKIP LOCKED
	LIMIT 1
)
UPDATE agent_steps AS step
SET status = 'running',
	claimed_by = $2,
	lease_expires_at = $3,
	heartbeat_at = $1,
	lease_generation = step.lease_generation + 1,
	attempt_count = step.attempt_count + 1,
	next_retry_at = NULL,
	error_json = NULL,
	started_at = COALESCE(step.started_at, $1),
	updated_at = $1
FROM candidate
WHERE step.id = candidate.candidate_step_id
RETURNING ` + stepColumns

const heartbeatStepSQL = `
	UPDATE agent_steps
	SET heartbeat_at = $4, lease_expires_at = $5, updated_at = $4
	WHERE id = $1 AND status = 'running' AND claimed_by = $2 AND lease_generation = $3
	  AND lease_expires_at > $4`

const completeStepSQL = `
	UPDATE agent_steps
	SET status = 'succeeded', output_json = $4, error_json = NULL,
		claimed_by = NULL, lease_expires_at = NULL, heartbeat_at = NULL,
		completed_at = $5, updated_at = $5
	WHERE id = $1 AND status = 'running' AND claimed_by = $2 AND lease_generation = $3
	  AND lease_expires_at > $5`

const retryStepSQL = `
	UPDATE agent_steps
	SET status = 'queued', error_json = $4, claimed_by = NULL,
		lease_expires_at = NULL, heartbeat_at = NULL, next_retry_at = $5, updated_at = $6
	WHERE id = $1 AND status = 'running' AND claimed_by = $2 AND lease_generation = $3
	  AND lease_expires_at > $6
	  AND attempt_count < max_attempts`

const recoverExpiredStepsSQL = `
	WITH expired AS (
		UPDATE agent_steps
		SET status = CASE WHEN attempt_count < max_attempts THEN 'queued' ELSE 'failed' END,
			claimed_by = NULL,
			lease_expires_at = NULL,
			heartbeat_at = NULL,
			next_retry_at = CASE WHEN attempt_count < max_attempts THEN $1 ELSE NULL END,
			completed_at = CASE WHEN attempt_count < max_attempts THEN NULL ELSE $1 END,
			error_json = jsonb_build_object('category', 'lease_expired', 'retryable', attempt_count < max_attempts),
			updated_at = $1
		WHERE status = 'running' AND lease_expires_at <= $1
		RETURNING id, run_id, status, attempt_count
	), close_attempts AS (
		UPDATE agent_attempts AS attempt
		SET error_category = 'lease_expired',
			finish_reason = COALESCE(attempt.finish_reason, 'worker_lease_expired'),
			completed_at = $1
		FROM expired
		WHERE attempt.step_id = expired.id
		  AND attempt.attempt_number = expired.attempt_count
		  AND attempt.completed_at IS NULL
		RETURNING attempt.id
	), reset_runs AS (
		UPDATE agent_runs AS run
		SET status = CASE
				WHEN EXISTS (SELECT 1 FROM expired WHERE expired.run_id = run.id AND expired.status = 'queued') THEN 'queued'
				ELSE 'failed'
			END,
			current_step = NULL,
			updated_at = $1,
			version = version + 1
		WHERE run.id IN (SELECT expired.run_id FROM expired)
		  AND run.status = 'running'
		RETURNING run.id
	)
	SELECT
		COUNT(*) FILTER (WHERE status = 'queued'),
		COUNT(*) FILTER (WHERE status = 'failed')
	FROM expired`

const requestCancelSQL = `
	WITH target AS (
		UPDATE agent_runs
		SET cancel_requested_at = COALESCE(cancel_requested_at, $2),
			status = CASE WHEN status IN ('waiting_input', 'waiting_approval') THEN 'queued' ELSE status END,
			updated_at = $2,
			version = version + 1
		WHERE id = $1 AND status IN ('queued', 'running', 'waiting_input', 'waiting_approval')
		  AND cancel_requested_at IS NULL
		RETURNING id
	), existing AS (
		SELECT id FROM agent_runs
		WHERE id = $1 AND status IN ('queued', 'running', 'waiting_input', 'waiting_approval')
		  AND cancel_requested_at IS NOT NULL
	), awakened AS (
		UPDATE agent_steps
		SET status = 'queued', next_retry_at = $2, completed_at = NULL,
			claimed_by = NULL, lease_expires_at = NULL, heartbeat_at = NULL,
			updated_at = $2
		WHERE run_id IN (SELECT id FROM target)
		  AND status IN ('waiting_input', 'waiting_approval')
		RETURNING id
	)
	SELECT EXISTS (SELECT 1 FROM target) OR EXISTS (SELECT 1 FROM existing), COUNT(*) FROM awakened`

// CreateOrGetRun 按领域约束持久化数据。
func (r *Repository) CreateOrGetRun(ctx context.Context, params CreateRunParams) (*Run, bool, error) {
	params, err := prepareCreateRunParams(params)
	if err != nil {
		return nil, false, err
	}
	return createOrGetRun(ctx, r.pool, params)
}

// prepareCreateRunParams 执行该函数负责的核心处理逻辑。
func prepareCreateRunParams(params CreateRunParams) (CreateRunParams, error) {
	params.Objective = strings.TrimSpace(params.Objective)
	if params.Objective == "" {
		return CreateRunParams{}, fmt.Errorf("objective is required")
	}
	if params.MaxSteps == 0 {
		params.MaxSteps = 64
	}
	if params.MaxSteps < 1 {
		return CreateRunParams{}, fmt.Errorf("max_steps 必须为正数")
	}
	if params.MaxToolCalls == 0 {
		params.MaxToolCalls = 32
	}
	if params.MaxToolCalls < 0 {
		return CreateRunParams{}, fmt.Errorf("max_工具_calls 必须 not 为负数")
	}
	if params.TokenBudget != nil && *params.TokenBudget <= 0 {
		return CreateRunParams{}, fmt.Errorf("token_budget 必须为正数")
	}
	if params.CostBudget != nil && *params.CostBudget < 0 {
		return CreateRunParams{}, fmt.Errorf("cost_budget 必须 not 为负数")
	}
	stateJSON, err := normalizeJSONObject(params.StateJSON, "state_json")
	if err != nil {
		return CreateRunParams{}, err
	}
	params.StateJSON = stateJSON
	params.RequestID = trimOptional(params.RequestID)
	return params, nil
}

// createOrGetRun 执行该函数负责的核心处理逻辑。
func createOrGetRun(ctx context.Context, query rowQuerier, params CreateRunParams) (*Run, bool, error) {
	run, err := scanRun(query.QueryRow(ctx, `
		INSERT INTO agent_runs (
			organization_id, workspace_id, session_id, task_id, request_id, trace_id,
			objective, max_steps, max_tool_calls, token_budget, cost_budget, deadline_at, state_json
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT DO NOTHING
		RETURNING `+runColumns,
		params.OrganizationID, params.WorkspaceID, params.SessionID, params.TaskID,
		params.RequestID, trimOptional(params.TraceID), params.Objective, params.MaxSteps,
		params.MaxToolCalls, params.TokenBudget, params.CostBudget, params.DeadlineAt, params.StateJSON,
	))
	if err == nil {
		return &run, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	if params.RequestID == nil {
		return nil, false, fmt.Errorf("创建 agent 运行返回了没有 row 不包含一个 idempotency 键")
	}

	run, err = scanRun(query.QueryRow(ctx, `
		SELECT `+runColumns+`
		FROM agent_runs
		WHERE workspace_id IS NOT DISTINCT FROM $1 AND request_id = $2
	`, params.WorkspaceID, *params.RequestID))
	if err != nil {
		return nil, false, err
	}
	if !sameRunRequest(run, params) {
		return nil, false, ErrIdempotencyConflict
	}
	return &run, false, nil
}

// CreateOrGetRunWithInitialStep creates the 持久化的 运行 和 its first 类型化的
// 步骤 位于 一个 事务. This prevents 一个 处理流程 crash 来自 leaving 一个
// idempotent 运行 不包含 runnable work.
func (r *Repository) CreateOrGetRunWithInitialStep(ctx context.Context, runParams CreateRunParams, stepParams CreateStepParams) (*Run, *Step, bool, error) {
	preparedRun, err := prepareCreateRunParams(runParams)
	if err != nil {
		return nil, nil, false, err
	}
	stepParams.RunID = "initial-run"
	preparedStep, err := prepareCreateStepParams(stepParams)
	if err != nil {
		return nil, nil, false, err
	}

	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	run, runCreated, err := createOrGetRun(ctx, tx, preparedRun)
	if err != nil {
		return nil, nil, false, err
	}
	preparedStep.RunID = run.ID
	step, stepCreated, err := createOrGetStep(ctx, tx, preparedStep)
	if err != nil {
		return nil, nil, false, err
	}
	eventKey := "agent-run-created:" + run.ID
	payloadJSON, err := json.Marshal(map[string]any{
		"run_id": run.ID, "initial_step_id": step.ID, "initial_step_type": step.StepType,
	})
	if err != nil {
		return nil, nil, false, err
	}
	if _, _, err := outbox.NewRepository(r.pool).Enqueue(ctx, tx, outbox.EnqueueParams{
		AggregateType: "agent_run", AggregateID: run.ID, EventType: "agent.run.created",
		IdempotencyKey: &eventKey, PayloadJSON: payloadJSON,
	}); err != nil {
		return nil, nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, false, err
	}
	return run, step, runCreated || stepCreated, nil
}

// GetRun 按作用域读取并返回所需数据。
func (r *Repository) GetRun(ctx context.Context, runID string) (*Run, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	run, err := scanRun(r.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM agent_runs WHERE id = $1`, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// TransitionRun 执行该函数负责的核心处理逻辑。
func (r *Repository) TransitionRun(ctx context.Context, params TransitionRunParams) error {
	params.RunID = strings.TrimSpace(params.RunID)
	if params.RunID == "" || params.ExpectedVersion <= 0 || params.At.IsZero() {
		return fmt.Errorf("run_id、expected_version、和 transition time 不能为空")
	}
	if !agentruntime.CanTransitionRun(params.From, params.To) {
		return fmt.Errorf("无效的运行 transition %s -> %s", params.From, params.To)
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE agent_runs
		SET status = $3, current_step = $5, updated_at = $6, version = version + 1
		WHERE id = $1 AND status = $2 AND version = $4
	`, params.RunID, params.From, params.To, params.ExpectedVersion, trimOptional(params.CurrentStep), params.At)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrRunConflict
	}
	return nil
}

// ResumeWaitingStep atomically makes 一个 persisted 等待 步骤 runnable 之后
// the corresponding user-输入 或 审批 fact 具有 been durably recorded.
func (r *Repository) ResumeWaitingStep(ctx context.Context, params ResumeWaitingParams) error {
	params.RunID = strings.TrimSpace(params.RunID)
	params.StepID = strings.TrimSpace(params.StepID)
	if params.RunID == "" || params.StepID == "" || params.At.IsZero() {
		return fmt.Errorf("run_id、step_id、和 resume time 不能为空")
	}
	matchingWaitingStates :=
		(params.ExpectedRunStatus == agentruntime.RunStatusWaitingInput && params.ExpectedStepStatus == agentruntime.StepStatusWaitingInput) ||
			(params.ExpectedRunStatus == agentruntime.RunStatusWaitingApproval && params.ExpectedStepStatus == agentruntime.StepStatusWaitingApproval)
	if !matchingWaitingStates {
		return fmt.Errorf("resume requires matching waiting states")
	}

	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE agent_steps
		SET status = 'queued', next_retry_at = $4, completed_at = NULL,
			claimed_by = NULL, lease_expires_at = NULL, heartbeat_at = NULL,
			updated_at = $4
		WHERE id = $1 AND run_id = $2 AND status = $3
	`, params.StepID, params.RunID, params.ExpectedStepStatus, params.At)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrRunConflict
	}

	tag, err = tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'queued', current_step = NULL, updated_at = $4, version = version + 1
		WHERE id = $1 AND status = $2
		  AND EXISTS (SELECT 1 FROM agent_steps WHERE id = $3 AND run_id = agent_runs.id AND status = 'queued')
	`, params.RunID, params.ExpectedRunStatus, params.StepID, params.At)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrRunConflict
	}
	return tx.Commit(ctx)
}

// CreateOrGetStep 按领域约束持久化数据。
func (r *Repository) CreateOrGetStep(ctx context.Context, params CreateStepParams) (*Step, bool, error) {
	params, err := prepareCreateStepParams(params)
	if err != nil {
		return nil, false, err
	}
	return createOrGetStep(ctx, r.pool, params)
}

// prepareCreateStepParams 执行该函数负责的核心处理逻辑。
func prepareCreateStepParams(params CreateStepParams) (CreateStepParams, error) {
	params.RunID = strings.TrimSpace(params.RunID)
	params.StepKey = strings.TrimSpace(params.StepKey)
	params.StepType = strings.TrimSpace(params.StepType)
	if params.RunID == "" || params.StepKey == "" || params.StepType == "" {
		return CreateStepParams{}, fmt.Errorf("run_id、step_键、和 step_type 不能为空")
	}
	inputJSON, err := normalizeJSONObject(params.InputJSON, "input_json")
	if err != nil {
		return CreateStepParams{}, err
	}
	if params.MaxAttempts == 0 {
		params.MaxAttempts = 5
	}
	if params.MaxAttempts < 1 {
		return CreateStepParams{}, fmt.Errorf("max_attempts 必须为正数")
	}
	params.InputJSON = inputJSON
	return params, nil
}

// createOrGetStep 执行该函数负责的核心处理逻辑。
func createOrGetStep(ctx context.Context, query rowQuerier, params CreateStepParams) (*Step, bool, error) {
	step, err := scanStep(query.QueryRow(ctx, `
		INSERT INTO agent_steps (run_id, step_key, step_type, input_json, max_attempts)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (run_id, step_key) DO NOTHING
		RETURNING `+stepColumns,
		params.RunID, params.StepKey, params.StepType, params.InputJSON, params.MaxAttempts,
	))
	if err == nil {
		return &step, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	step, err = scanStep(query.QueryRow(ctx, `
		SELECT `+stepColumns+` FROM agent_steps WHERE run_id = $1 AND step_key = $2
	`, params.RunID, params.StepKey))
	if err != nil {
		return nil, false, err
	}
	if step.StepType != params.StepType || step.MaxAttempts != params.MaxAttempts || !jsonEqual(step.InputJSON, params.InputJSON) {
		return nil, false, ErrIdempotencyConflict
	}
	return &step, false, nil
}

// ClaimNextStep 执行该函数负责的核心处理逻辑。
func (r *Repository) ClaimNextStep(ctx context.Context, now time.Time, workerID string, leaseDuration time.Duration) (*Step, error) {
	workerID = strings.TrimSpace(workerID)
	if now.IsZero() || workerID == "" || leaseDuration <= 0 {
		return nil, fmt.Errorf("now、工作进程_id、和正数租约时长不能为空")
	}

	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	step, err := scanStep(tx.QueryRow(ctx, claimStepSQL, now, workerID, now.Add(leaseDuration)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = CASE WHEN status = 'queued' THEN 'running' ELSE status END,
			current_step = $2,
			updated_at = $3,
			version = version + 1
		WHERE id = $1 AND status IN ('queued', 'running')
	`, step.RunID, step.StepKey, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &step, nil
}

// HeartbeatStep 执行该函数负责的核心处理逻辑。
func (r *Repository) HeartbeatStep(ctx context.Context, params HeartbeatParams) error {
	params.StepID = strings.TrimSpace(params.StepID)
	params.WorkerID = strings.TrimSpace(params.WorkerID)
	if params.StepID == "" || params.WorkerID == "" {
		return fmt.Errorf("step_id 和工作进程_id 不能为空")
	}
	if params.LeaseGeneration <= 0 {
		return fmt.Errorf("lease_generation must be positive")
	}
	if params.Now.IsZero() || params.LeaseDuration <= 0 {
		return fmt.Errorf("now 和正数租约时长不能为空")
	}

	tag, err := r.pool.Exec(ctx, heartbeatStepSQL, params.StepID, params.WorkerID, params.LeaseGeneration, params.Now, params.Now.Add(params.LeaseDuration))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// CompleteStep 执行该函数负责的核心处理逻辑。
func (r *Repository) CompleteStep(ctx context.Context, params CompleteStepParams) error {
	outputJSON, err := normalizeJSONObject(params.OutputJSON, "output_json")
	if err != nil {
		return err
	}
	if strings.TrimSpace(params.StepID) == "" || strings.TrimSpace(params.WorkerID) == "" || params.LeaseGeneration <= 0 || params.CompletedAt.IsZero() {
		return fmt.Errorf("有效的步骤租约和 completed_at 不能为空")
	}
	tag, err := r.pool.Exec(ctx, completeStepSQL, params.StepID, params.WorkerID, params.LeaseGeneration, outputJSON, params.CompletedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// ReleaseStepForRetry 执行该函数负责的核心处理逻辑。
func (r *Repository) ReleaseStepForRetry(ctx context.Context, params RetryStepParams) error {
	errorJSON, err := normalizeJSONObject(params.ErrorJSON, "error_json")
	if err != nil {
		return err
	}
	if strings.TrimSpace(params.StepID) == "" || strings.TrimSpace(params.WorkerID) == "" || params.LeaseGeneration <= 0 || params.Now.IsZero() || params.NextRetryAt.Before(params.Now) {
		return fmt.Errorf("有效的步骤租约、now、和 next_retry_at 不能为空")
	}
	tag, err := r.pool.Exec(ctx, retryStepSQL, params.StepID, params.WorkerID, params.LeaseGeneration, errorJSON, params.NextRetryAt, params.Now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// RequestCancel 执行该函数负责的核心处理逻辑。
func (r *Repository) RequestCancel(ctx context.Context, runID string, requestedAt time.Time) (bool, error) {
	if strings.TrimSpace(runID) == "" || requestedAt.IsZero() {
		return false, fmt.Errorf("run_id 和 requested_at 不能为空")
	}
	var requested bool
	var awakened int64
	err := r.pool.QueryRow(ctx, requestCancelSQL, runID, requestedAt).Scan(&requested, &awakened)
	return requested, err
}

type RecoveryResult struct {
	Requeued int64
	Failed   int64
}

// RecoverExpiredSteps 执行该函数负责的核心处理逻辑。
func (r *Repository) RecoverExpiredSteps(ctx context.Context, now time.Time) (RecoveryResult, error) {
	if now.IsZero() {
		return RecoveryResult{}, fmt.Errorf("now 不能为空")
	}
	var result RecoveryResult
	err := r.pool.QueryRow(ctx, recoverExpiredStepsSQL, now).Scan(&result.Requeued, &result.Failed)
	return result, err
}

// scanRun 执行该函数负责的核心处理逻辑。
func scanRun(row pgx.Row) (Run, error) {
	var value Run
	err := row.Scan(
		&value.ID, &value.OrganizationID, &value.WorkspaceID, &value.SessionID, &value.TaskID,
		&value.RequestID, &value.TraceID, &value.Status, &value.Objective, &value.CurrentStep,
		&value.MaxSteps, &value.MaxToolCalls, &value.TokenBudget, &value.CostBudget,
		&value.DeadlineAt, &value.CancelRequestedAt, &value.StateJSON, &value.Version,
		&value.CreatedAt, &value.UpdatedAt,
	)
	return value, err
}

// scanStep 执行该函数负责的核心处理逻辑。
func scanStep(row pgx.Row) (Step, error) {
	var value Step
	err := row.Scan(
		&value.ID, &value.RunID, &value.StepKey, &value.StepType, &value.Status,
		&value.InputJSON, &value.OutputJSON, &value.ErrorJSON, &value.ClaimedBy,
		&value.LeaseExpiresAt, &value.HeartbeatAt, &value.LeaseGeneration,
		&value.AttemptCount, &value.MaxAttempts, &value.NextRetryAt, &value.StartedAt,
		&value.CompletedAt, &value.CreatedAt, &value.UpdatedAt,
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
	if err != nil {
		return nil, fmt.Errorf("处理失败：normalize %s：%w", field, err)
	}
	return normalized, nil
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

// sameRunRequest 执行该函数负责的核心处理逻辑。
func sameRunRequest(run Run, params CreateRunParams) bool {
	return run.Objective == params.Objective &&
		equalStringPointer(run.OrganizationID, params.OrganizationID) &&
		equalStringPointer(run.WorkspaceID, params.WorkspaceID) &&
		equalStringPointer(run.SessionID, params.SessionID) &&
		equalStringPointer(run.TaskID, params.TaskID) &&
		equalStringPointer(run.TraceID, params.TraceID) &&
		run.MaxSteps == params.MaxSteps &&
		run.MaxToolCalls == params.MaxToolCalls &&
		equalInt64Pointer(run.TokenBudget, params.TokenBudget) &&
		equalFloat64Pointer(run.CostBudget, params.CostBudget) &&
		equalTimePointer(run.DeadlineAt, params.DeadlineAt) &&
		jsonEqual(run.StateJSON, params.StateJSON)
}

// equalStringPointer 执行该函数负责的核心处理逻辑。
func equalStringPointer(left *string, right *string) bool {
	if left == nil && right == nil {
		return true
	}
	return left != nil && right != nil && strings.TrimSpace(*left) == strings.TrimSpace(*right)
}

// equalInt64Pointer 执行该函数负责的核心处理逻辑。
func equalInt64Pointer(left *int64, right *int64) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

// equalFloat64Pointer 执行该函数负责的核心处理逻辑。
func equalFloat64Pointer(left *float64, right *float64) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

// equalTimePointer 执行该函数负责的核心处理逻辑。
func equalTimePointer(left *time.Time, right *time.Time) bool {
	return (left == nil && right == nil) ||
		(left != nil && right != nil && left.Truncate(time.Microsecond).Equal(right.Truncate(time.Microsecond)))
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
