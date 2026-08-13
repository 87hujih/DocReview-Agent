package agentrun

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentruntime "agent_project/apps/server/internal/agent/runtime"
	"agent_project/apps/server/internal/storage/postgres/outbox"

	"github.com/jackc/pgx/v5"
)

type EngineStore struct {
	repo   *Repository
	outbox *outbox.Repository
}

// NewEngineStore 校验依赖并创建对应实例。
func NewEngineStore(repo *Repository) *EngineStore {
	return &EngineStore{repo: repo, outbox: outbox.NewRepository(repo.pool)}
}

// RecoverExpired 执行该函数负责的核心处理逻辑。
func (s *EngineStore) RecoverExpired(ctx context.Context, now time.Time) error {
	_, err := s.repo.RecoverExpiredSteps(ctx, now)
	return err
}

// 声明 执行该函数负责的核心处理逻辑。
func (s *EngineStore) Claim(ctx context.Context, request agentruntime.ClaimRequest) (*agentruntime.WorkItem, error) {
	step, err := s.repo.ClaimNextStep(ctx, request.Now, request.WorkerID, request.LeaseDuration)
	if err != nil || step == nil {
		return nil, err
	}
	run, err := s.repo.GetRun(ctx, step.RunID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, fmt.Errorf("claimed 步骤引用缺少运行 %s", step.RunID)
	}
	var stepCount int
	var toolCallCount int
	var tokensUsed int64
	var costUsed float64
	if err := s.repo.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM agent_steps WHERE run_id = $1),
			(SELECT COUNT(*) FROM tool_calls WHERE run_id = $1),
			COALESCE((SELECT SUM(COALESCE(input_tokens, 0) + COALESCE(output_tokens, 0))
				FROM agent_attempts JOIN agent_steps ON agent_steps.id = agent_attempts.step_id
				WHERE agent_steps.run_id = $1), 0),
			COALESCE((SELECT SUM(COALESCE(cost, 0))
				FROM agent_attempts JOIN agent_steps ON agent_steps.id = agent_attempts.step_id
				WHERE agent_steps.run_id = $1), 0)
	`, run.ID).Scan(&stepCount, &toolCallCount, &tokensUsed, &costUsed); err != nil {
		return nil, err
	}
	return &agentruntime.WorkItem{
		RunID: run.ID, RunVersion: run.Version, RunDeadlineAt: run.DeadlineAt,
		CancelRequestedAt: run.CancelRequestedAt, StepID: step.ID, StepKey: step.StepKey,
		StepType: step.StepType, InputJSON: step.InputJSON, AttemptNumber: step.AttemptCount,
		MaxAttempts: step.MaxAttempts, LeaseGeneration: step.LeaseGeneration, ClaimedBy: request.WorkerID,
		StepStartedAt: step.StartedAt,
		MaxSteps:      run.MaxSteps, StepCount: stepCount, MaxToolCalls: run.MaxToolCalls,
		ToolCallCount: toolCallCount, TokenBudget: run.TokenBudget, TokensUsed: tokensUsed,
		CostBudget: run.CostBudget, CostUsed: costUsed,
	}, nil
}

// 心跳 执行该函数负责的核心处理逻辑。
func (s *EngineStore) Heartbeat(ctx context.Context, input agentruntime.LeaseHeartbeat) error {
	return s.repo.HeartbeatStep(ctx, HeartbeatParams{
		StepID: input.StepID, WorkerID: input.WorkerID, LeaseGeneration: input.LeaseGeneration,
		Now: input.Now, LeaseDuration: input.LeaseDuration,
	})
}

// StartAttempt 执行该函数负责的核心处理逻辑。
func (s *EngineStore) StartAttempt(ctx context.Context, input agentruntime.AttemptStart) (string, error) {
	attempt, err := s.repo.CreateAttempt(ctx, CreateAttemptParams{
		StepID: input.StepID, AttemptNumber: input.AttemptNumber,
		TraceID: &input.TraceID, StartedAt: input.StartedAt,
	})
	if err != nil {
		return "", err
	}
	return attempt.ID, nil
}

// FinishAttempt 执行该函数负责的核心处理逻辑。
func (s *EngineStore) FinishAttempt(ctx context.Context, input agentruntime.AttemptFinish) error {
	return s.repo.FinishAttempt(ctx, FinishAttemptParams{
		AttemptID: input.AttemptID, Provider: input.Provider, Model: input.Model, PromptVersion: input.PromptVersion,
		Temperature: input.Temperature, ContextManifestID: input.ContextManifestID,
		InputTokens: input.InputTokens, OutputTokens: input.OutputTokens,
		Cost: input.Cost, LatencyMS: input.LatencyMS, RetryCount: input.RetryCount, FinishReason: &input.FinishReason,
		ErrorCategory: input.ErrorCategory, CompletedAt: input.CompletedAt,
	})
}

// CommitOutcome 执行该函数负责的核心处理逻辑。
func (s *EngineStore) CommitOutcome(ctx context.Context, input agentruntime.OutcomeCommit) error {
	outputJSON, err := normalizeJSONObject(input.OutputJSON, "output_json")
	if err != nil {
		return err
	}
	var errorJSON any
	if input.Error != nil {
		encoded, err := json.Marshal(input.Error)
		if err != nil {
			return err
		}
		errorJSON = encoded
	}

	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := s.repo.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	completedAt := any(nil)
	if input.StepStatus == agentruntime.StepStatusSucceeded || input.StepStatus == agentruntime.StepStatusFailed || input.StepStatus == agentruntime.StepStatusCancelled {
		completedAt = input.CommittedAt
	}
	tag, err := tx.Exec(ctx, `
		UPDATE agent_steps
		SET status = $4, output_json = $5, error_json = $6,
			claimed_by = NULL, lease_expires_at = NULL, heartbeat_at = NULL,
			completed_at = $7, updated_at = $8
		WHERE id = $1 AND status = 'running' AND claimed_by = $2
		  AND lease_generation = $3 AND lease_expires_at > $8
	`, input.Work.StepID, s.workerID(input), input.Work.LeaseGeneration, input.StepStatus,
		outputJSON, errorJSON, completedAt, input.CommittedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		committed, err := s.outcomeAlreadyCommitted(ctx, tx, input)
		if err != nil {
			return err
		}
		if !committed {
			return ErrLeaseLost
		}
		return tx.Commit(ctx)
	}

	for _, observation := range input.Observations {
		if err := insertObservation(ctx, tx, input.Work, observation, input.CommittedAt); err != nil {
			return err
		}
	}

	for _, next := range input.NextSteps {
		stepInput, err := normalizeJSONObject(next.InputJSON, "next_step.input_json")
		if err != nil {
			return err
		}
		maxAttempts := next.MaxAttempts
		if maxAttempts == 0 {
			maxAttempts = 5
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_steps (run_id, step_key, step_type, input_json, max_attempts)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (run_id, step_key) DO NOTHING
		`, input.Work.RunID, next.StepKey, next.StepType, stepInput, maxAttempts); err != nil {
			return err
		}
	}

	var currentStep any
	if len(input.NextSteps) > 0 {
		currentStep = input.NextSteps[0].StepKey
	} else if input.RunStatus == agentruntime.RunStatusWaitingInput || input.RunStatus == agentruntime.RunStatusWaitingApproval {
		currentStep = input.Work.StepKey
	}
	tag, err = tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = $2, current_step = $3, updated_at = $4, version = version + 1
		WHERE id = $1 AND status = 'running' AND version = $5
	`, input.Work.RunID, input.RunStatus, currentStep, input.CommittedAt, input.Work.RunVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrRunConflict
	}

	if err := s.enqueueOutcome(ctx, tx, input); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ScheduleRetry 执行该函数负责的核心处理逻辑。
func (s *EngineStore) ScheduleRetry(ctx context.Context, input agentruntime.RetryCommit) error {
	errorJSON, err := json.Marshal(input.Error)
	if err != nil {
		return err
	}
	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := s.repo.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, retryStepSQL, input.Work.StepID, s.workerIDFromWork(input.Work), input.Work.LeaseGeneration, errorJSON, input.NextRetryAt, input.CommittedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		committed, err := s.retryAlreadyCommitted(ctx, tx, input)
		if err != nil {
			return err
		}
		if !committed {
			return ErrLeaseLost
		}
		return tx.Commit(ctx)
	}
	tag, err = tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'queued', current_step = $2, updated_at = $3, version = version + 1
		WHERE id = $1 AND status = 'running' AND version = $4
	`, input.Work.RunID, input.Work.StepKey, input.CommittedAt, input.Work.RunVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrRunConflict
	}
	key, payload, err := retryEvent(input)
	if err != nil {
		return err
	}
	if _, _, err := s.outbox.Enqueue(ctx, tx, outbox.EnqueueParams{
		AggregateType: "agent_run", AggregateID: input.Work.RunID, EventType: "agent.step.retry_scheduled",
		IdempotencyKey: &key, PayloadJSON: payload,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// retryAlreadyCommitted 执行该函数负责的核心处理逻辑。
func (s *EngineStore) retryAlreadyCommitted(ctx context.Context, tx pgx.Tx, input agentruntime.RetryCommit) (bool, error) {
	key, expectedPayload, err := retryEvent(input)
	if err != nil {
		return false, err
	}
	var eventType string
	var payload json.RawMessage
	err = tx.QueryRow(ctx, `
		SELECT event_type, payload_json
		FROM outbox_events
		WHERE aggregate_type = 'agent_run' AND aggregate_id = $1 AND idempotency_key = $2
	`, input.Work.RunID, key).Scan(&eventType, &payload)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return eventType == "agent.step.retry_scheduled" && jsonEqual(payload, expectedPayload), nil
}

// retryEvent 执行该函数负责的核心处理逻辑。
func retryEvent(input agentruntime.RetryCommit) (string, json.RawMessage, error) {
	key := fmt.Sprintf("step-retry:%s:%d", input.Work.StepID, input.Work.LeaseGeneration)
	payload, err := json.Marshal(map[string]any{
		"run_id": input.Work.RunID, "step_id": input.Work.StepID,
		"next_retry_at": input.NextRetryAt, "error": input.Error,
	})
	return key, payload, err
}

// enqueueOutcome 执行该函数负责的核心处理逻辑。
func (s *EngineStore) enqueueOutcome(ctx context.Context, tx pgx.Tx, input agentruntime.OutcomeCommit) error {
	key, payload, err := outcomeEvent(input)
	if err != nil {
		return err
	}
	_, _, err = s.outbox.Enqueue(ctx, tx, outbox.EnqueueParams{
		AggregateType: "agent_run", AggregateID: input.Work.RunID,
		EventType: "agent.step.outcome_committed", IdempotencyKey: &key, PayloadJSON: payload,
	})
	return err
}

// outcomeAlreadyCommitted 执行该函数负责的核心处理逻辑。
func (s *EngineStore) outcomeAlreadyCommitted(ctx context.Context, tx pgx.Tx, input agentruntime.OutcomeCommit) (bool, error) {
	key, expectedPayload, err := outcomeEvent(input)
	if err != nil {
		return false, err
	}
	var eventType string
	var payload json.RawMessage
	err = tx.QueryRow(ctx, `
		SELECT event_type, payload_json
		FROM outbox_events
		WHERE aggregate_type = 'agent_run' AND aggregate_id = $1 AND idempotency_key = $2
	`, input.Work.RunID, key).Scan(&eventType, &payload)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return eventType == "agent.step.outcome_committed" && jsonEqual(payload, expectedPayload), nil
}

// outcomeEvent 执行该函数负责的核心处理逻辑。
func outcomeEvent(input agentruntime.OutcomeCommit) (string, json.RawMessage, error) {
	key := fmt.Sprintf("step-outcome:%s:%d", input.Work.StepID, input.Work.LeaseGeneration)
	commitHash, err := outcomeCommitHash(input)
	if err != nil {
		return "", nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"run_id": input.Work.RunID, "step_id": input.Work.StepID,
		"step_status": input.StepStatus, "run_status": input.RunStatus, "commit_hash": commitHash,
	})
	return key, payload, err
}

// outcomeCommitHash 执行该函数负责的核心处理逻辑。
func outcomeCommitHash(input agentruntime.OutcomeCommit) (string, error) {
	outputJSON, err := normalizeJSONObject(input.OutputJSON, "output_json")
	if err != nil {
		return "", err
	}
	type nextStep struct {
		StepKey     string          `json:"step_key"`
		StepType    string          `json:"step_type"`
		InputJSON   json.RawMessage `json:"input_json"`
		MaxAttempts int             `json:"max_attempts"`
	}
	type observation struct {
		ObservationKey string          `json:"observation_key"`
		Kind           string          `json:"kind"`
		Action         string          `json:"action"`
		ToolCallID     string          `json:"tool_call_id,omitempty"`
		PayloadJSON    json.RawMessage `json:"payload_json"`
		ContentHash    string          `json:"content_hash"`
		Novel          bool            `json:"novel"`
	}
	nextSteps := make([]nextStep, 0, len(input.NextSteps))
	for _, next := range input.NextSteps {
		nextInput, err := normalizeJSONObject(next.InputJSON, "next_step.input_json")
		if err != nil {
			return "", err
		}
		maxAttempts := next.MaxAttempts
		if maxAttempts == 0 {
			maxAttempts = 5
		}
		nextSteps = append(nextSteps, nextStep{
			StepKey: strings.TrimSpace(next.StepKey), StepType: strings.TrimSpace(next.StepType),
			InputJSON: nextInput, MaxAttempts: maxAttempts,
		})
	}
	observations := make([]observation, 0, len(input.Observations))
	for _, item := range input.Observations {
		payload, err := normalizeJSONObject(item.PayloadJSON, "observation.payload_json")
		if err != nil {
			return "", err
		}
		observations = append(observations, observation{
			ObservationKey: strings.TrimSpace(item.ObservationKey), Kind: strings.TrimSpace(item.Kind),
			Action: strings.TrimSpace(item.Action), ToolCallID: strings.TrimSpace(item.ToolCallID),
			PayloadJSON: payload, ContentHash: strings.TrimSpace(item.ContentHash), Novel: item.Novel,
		})
	}
	envelope, err := json.Marshal(struct {
		StepStatus   agentruntime.StepStatus      `json:"step_status"`
		RunStatus    agentruntime.RunStatus       `json:"run_status"`
		OutputJSON   json.RawMessage              `json:"output_json"`
		Error        *agentruntime.ExecutionError `json:"error,omitempty"`
		NextSteps    []nextStep                   `json:"next_steps"`
		Observations []observation                `json:"observations"`
	}{
		StepStatus: input.StepStatus, RunStatus: input.RunStatus, OutputJSON: outputJSON,
		Error: input.Error, NextSteps: nextSteps, Observations: observations,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(envelope)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// insertObservation 执行该函数负责的核心处理逻辑。
func insertObservation(ctx context.Context, tx pgx.Tx, work agentruntime.WorkItem, item agentruntime.ObservationSpec, createdAt time.Time) error {
	item.ObservationKey = strings.TrimSpace(item.ObservationKey)
	item.Kind = strings.TrimSpace(item.Kind)
	item.Action = strings.TrimSpace(item.Action)
	item.ToolCallID = strings.TrimSpace(item.ToolCallID)
	item.ContentHash = strings.TrimSpace(item.ContentHash)
	payload, err := normalizeJSONObject(item.PayloadJSON, "observation.payload_json")
	if err != nil {
		return err
	}
	if item.ObservationKey == "" || item.Kind == "" || item.Action == "" || !strings.HasPrefix(item.ContentHash, "sha256:") || createdAt.IsZero() {
		return fmt.Errorf("观察结果标识、内容哈希、和 created_at 不能为空")
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO agent_observations (
			run_id, step_id, observation_key, kind, action, tool_call_id,
			payload_json, content_hash, novel, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (run_id, observation_key) DO NOTHING
	`, work.RunID, work.StepID, item.ObservationKey, item.Kind, item.Action,
		trimOptionalString(item.ToolCallID), payload, item.ContentHash, item.Novel, createdAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var kind, action, toolCallID, contentHash string
	var persistedPayload json.RawMessage
	var novel bool
	err = tx.QueryRow(ctx, `
		SELECT kind, action, COALESCE(tool_call_id::text, ''), payload_json, content_hash, novel
		FROM agent_observations
		WHERE run_id = $1 AND observation_key = $2
	`, work.RunID, item.ObservationKey).Scan(&kind, &action, &toolCallID, &persistedPayload, &contentHash, &novel)
	if err != nil {
		return err
	}
	if kind != item.Kind || action != item.Action || toolCallID != item.ToolCallID || contentHash != item.ContentHash || novel != item.Novel || !jsonEqual(persistedPayload, payload) {
		return ErrIdempotencyConflict
	}
	return nil
}

// workerID 执行该函数负责的核心处理逻辑。
func (s *EngineStore) workerID(input agentruntime.OutcomeCommit) string {
	return s.workerIDFromWork(input.Work)
}

// workerIDFromWork 执行该函数负责的核心处理逻辑。
func (s *EngineStore) workerIDFromWork(work agentruntime.WorkItem) string {
	// ClaimedBy 为 intentionally not exposed 用于 executors; EngineStore uses the 工作进程
	// 标识 captured 位于 WorkItem 由 声明.
	return work.ClaimedBy
}

var _ agentruntime.Store = (*EngineStore)(nil)
