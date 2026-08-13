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

	"github.com/jackc/pgx/v5"
)

type Attempt struct {
	ID                string
	StepID            string
	AttemptNumber     int
	Provider          *string
	Model             *string
	PromptVersion     *string
	Temperature       *float64
	ContextManifestID *string
	TraceID           *string
	InputTokens       *int64
	OutputTokens      *int64
	Cost              *float64
	LatencyMS         *int64
	RetryCount        int
	FinishReason      *string
	ErrorCategory     *string
	StartedAt         time.Time
	CompletedAt       *time.Time
}

type CreateAttemptParams struct {
	StepID            string
	AttemptNumber     int
	Provider          *string
	Model             *string
	PromptVersion     *string
	Temperature       *float64
	ContextManifestID *string
	TraceID           *string
	StartedAt         time.Time
}

type FinishAttemptParams struct {
	AttemptID         string
	Provider          string
	Model             string
	PromptVersion     string
	Temperature       *float64
	ContextManifestID string
	InputTokens       int64
	OutputTokens      int64
	Cost              float64
	LatencyMS         int64
	RetryCount        int
	FinishReason      *string
	ErrorCategory     *agentruntime.ErrorCategory
	CompletedAt       time.Time
}

type ContextManifest struct {
	ID                   string
	RunID                string
	StepID               string
	TokenBudget          int64
	ReservedOutputTokens int64
	Tokenizer            string
	ItemsJSON            json.RawMessage
	TotalTokens          int64
	ContentHash          string
	CreatedAt            time.Time
}

type CreateContextManifestParams struct {
	RunID                string
	StepID               string
	TokenBudget          int64
	ReservedOutputTokens int64
	Tokenizer            string
	ItemsJSON            json.RawMessage
	TotalTokens          int64
	ContentHash          string
}

type ToolCall struct {
	ID             string
	RunID          string
	StepID         string
	ToolName       string
	ToolVersion    string
	InputJSON      json.RawMessage
	OutputJSON     json.RawMessage
	Status         string
	IdempotencyKey *string
	ErrorJSON      json.RawMessage
	ErrorCategory  *string
	LatencyMS      *int64
	StartedAt      *time.Time
	CompletedAt    *time.Time
	CreatedAt      time.Time
}

type CreateToolCallParams struct {
	RunID          string
	StepID         string
	ToolName       string
	ToolVersion    string
	InputJSON      json.RawMessage
	IdempotencyKey *string
}

type CompleteToolCallParams struct {
	ToolCallID    string
	Status        string
	OutputJSON    json.RawMessage
	ErrorJSON     json.RawMessage
	ErrorCategory *agentruntime.ErrorCategory
	LatencyMS     int64
	CompletedAt   time.Time
}

const attemptColumns = `
	id, step_id, attempt_number, provider, model, prompt_version, temperature,
	context_manifest_id, trace_id, input_tokens, output_tokens, cost, latency_ms,
	retry_count, finish_reason, error_category, started_at, completed_at`

const contextManifestColumns = `
	id, run_id, step_id, token_budget, reserved_output_tokens, tokenizer,
	items_json, total_tokens, content_hash, created_at`

const toolCallColumns = `
	id, run_id, step_id, tool_name, tool_version, input_json, output_json,
	status, idempotency_key, error_json, error_category, latency_ms,
	started_at, completed_at, created_at`

// CreateAttempt 按领域约束持久化数据。
func (r *Repository) CreateAttempt(ctx context.Context, params CreateAttemptParams) (*Attempt, error) {
	params.StepID = strings.TrimSpace(params.StepID)
	if params.StepID == "" {
		return nil, fmt.Errorf("step_id 不能为空")
	}
	if params.AttemptNumber <= 0 {
		return nil, fmt.Errorf("attempt_number 必须为正数")
	}
	if params.StartedAt.IsZero() {
		return nil, fmt.Errorf("启动ed_at 不能为空")
	}

	attempt, err := scanAttempt(r.pool.QueryRow(ctx, `
		INSERT INTO agent_attempts (
			step_id, attempt_number, provider, model, prompt_version, temperature,
			context_manifest_id, trace_id, started_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (step_id, attempt_number) DO NOTHING
		RETURNING `+attemptColumns,
		params.StepID, params.AttemptNumber, trimOptional(params.Provider), trimOptional(params.Model),
		trimOptional(params.PromptVersion), params.Temperature, params.ContextManifestID,
		trimOptional(params.TraceID), params.StartedAt,
	))
	if err == nil {
		return &attempt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	attempt, err = scanAttempt(r.pool.QueryRow(ctx, `
		SELECT `+attemptColumns+` FROM agent_attempts WHERE step_id = $1 AND attempt_number = $2
	`, params.StepID, params.AttemptNumber))
	if err != nil {
		return nil, err
	}
	return &attempt, nil
}

// FinishAttempt 执行该函数负责的核心处理逻辑。
func (r *Repository) FinishAttempt(ctx context.Context, params FinishAttemptParams) error {
	if strings.TrimSpace(params.AttemptID) == "" || params.CompletedAt.IsZero() {
		return fmt.Errorf("attempt_id 和 completed_at 不能为空")
	}
	if params.InputTokens < 0 || params.OutputTokens < 0 || params.Cost < 0 || params.LatencyMS < 0 || params.RetryCount < 0 {
		return fmt.Errorf("尝试 usage 值必须 not 为负数")
	}
	var errorCategory *string
	if params.ErrorCategory != nil {
		value := string(*params.ErrorCategory)
		errorCategory = &value
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE agent_attempts
		SET provider = COALESCE(NULLIF($2, ''), provider),
			model = COALESCE(NULLIF($3, ''), model),
			prompt_version = COALESCE(NULLIF($4, ''), prompt_version),
			temperature = COALESCE($5, temperature),
			context_manifest_id = COALESCE(NULLIF($6, '')::uuid, context_manifest_id),
			input_tokens = $7, output_tokens = $8, cost = $9, latency_ms = $10,
			retry_count = $11, finish_reason = $12, error_category = $13, completed_at = $14
		WHERE id = $1 AND completed_at IS NULL
	`, params.AttemptID, strings.TrimSpace(params.Provider), strings.TrimSpace(params.Model),
		strings.TrimSpace(params.PromptVersion), params.Temperature, strings.TrimSpace(params.ContextManifestID),
		params.InputTokens, params.OutputTokens, params.Cost,
		params.LatencyMS, params.RetryCount, trimOptional(params.FinishReason), errorCategory, params.CompletedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("尝试缺少或 al读取y completed")
	}
	return nil
}

// CreateContextManifest 按领域约束持久化数据。
func (r *Repository) CreateContextManifest(ctx context.Context, params CreateContextManifestParams) (*ContextManifest, error) {
	params.RunID = strings.TrimSpace(params.RunID)
	params.StepID = strings.TrimSpace(params.StepID)
	params.Tokenizer = strings.TrimSpace(params.Tokenizer)
	params.ContentHash = strings.TrimSpace(params.ContentHash)
	if params.RunID == "" || params.StepID == "" || params.Tokenizer == "" || params.ContentHash == "" {
		return nil, fmt.Errorf("run_id、step_id、分词器、和 content_hash 不能为空")
	}
	if params.TokenBudget <= 0 || params.ReservedOutputTokens < 0 || params.TotalTokens < 0 || params.TotalTokens+params.ReservedOutputTokens > params.TokenBudget {
		return nil, fmt.Errorf("上下文令牌预算无效")
	}
	itemsJSON, err := normalizeJSONArray(params.ItemsJSON, "items_json")
	if err != nil {
		return nil, err
	}

	manifest, err := scanContextManifest(r.pool.QueryRow(ctx, `
		INSERT INTO context_manifests (
			run_id, step_id, token_budget, reserved_output_tokens, tokenizer,
			items_json, total_tokens, content_hash
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+contextManifestColumns,
		params.RunID, params.StepID, params.TokenBudget, params.ReservedOutputTokens,
		params.Tokenizer, itemsJSON, params.TotalTokens, params.ContentHash,
	))
	if err != nil {
		return nil, err
	}
	return &manifest, nil
}

// GetContextManifest returns the immutable, ordered 上下文 记录 used 由 一个
// 模型 attempt. It never re-runs selection 或 reads current 资源 内容.
func (r *Repository) GetContextManifest(ctx context.Context, manifestID string) (*ContextManifest, error) {
	manifestID = strings.TrimSpace(manifestID)
	if manifestID == "" {
		return nil, fmt.Errorf("manifest_id 不能为空")
	}
	manifest, err := scanContextManifest(r.pool.QueryRow(ctx, `
		SELECT `+contextManifestColumns+`
		FROM context_manifests
		WHERE id = $1
	`, manifestID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &manifest, nil
}

// CreateOrGetToolCall 按领域约束持久化数据。
func (r *Repository) CreateOrGetToolCall(ctx context.Context, params CreateToolCallParams) (*ToolCall, bool, error) {
	params.RunID = strings.TrimSpace(params.RunID)
	params.StepID = strings.TrimSpace(params.StepID)
	params.ToolName = strings.TrimSpace(params.ToolName)
	params.ToolVersion = strings.TrimSpace(params.ToolVersion)
	if params.RunID == "" || params.StepID == "" || params.ToolName == "" || params.ToolVersion == "" {
		return nil, false, fmt.Errorf("run_id、step_id、工具_name、和工具_version 不能为空")
	}
	inputJSON, err := normalizeJSONObject(params.InputJSON, "input_json")
	if err != nil {
		return nil, false, err
	}
	params.IdempotencyKey = trimOptional(params.IdempotencyKey)

	call, err := scanToolCall(r.pool.QueryRow(ctx, `
		INSERT INTO tool_calls (run_id, step_id, tool_name, tool_version, input_json, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT DO NOTHING
		RETURNING `+toolCallColumns,
		params.RunID, params.StepID, params.ToolName, params.ToolVersion, inputJSON, params.IdempotencyKey,
	))
	if err == nil {
		return &call, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	if params.IdempotencyKey == nil {
		return nil, false, fmt.Errorf("创建工具调用返回了没有 row 不包含一个 idempotency 键")
	}
	call, err = scanToolCall(r.pool.QueryRow(ctx, `
		SELECT `+toolCallColumns+`
		FROM tool_calls WHERE run_id = $1 AND idempotency_key = $2
	`, params.RunID, *params.IdempotencyKey))
	if err != nil {
		return nil, false, err
	}
	if call.StepID != params.StepID || call.ToolName != params.ToolName || call.ToolVersion != params.ToolVersion || !jsonEqual(call.InputJSON, inputJSON) {
		return nil, false, ErrIdempotencyConflict
	}
	return &call, false, nil
}

// CompleteToolCall 执行该函数负责的核心处理逻辑。
func (r *Repository) CompleteToolCall(ctx context.Context, params CompleteToolCallParams) error {
	if strings.TrimSpace(params.ToolCallID) == "" || params.CompletedAt.IsZero() {
		return fmt.Errorf("工具_call_id 和 completed_at 不能为空")
	}
	if params.Status != "succeeded" && params.Status != "failed" && params.Status != "cancelled" {
		return fmt.Errorf("工具调用终止状态无效")
	}
	if params.LatencyMS < 0 {
		return fmt.Errorf("latency_ms 必须 not 为负数")
	}
	var outputJSON json.RawMessage
	var errorJSON json.RawMessage
	var err error
	if params.Status == "succeeded" {
		outputJSON, err = normalizeJSONObject(params.OutputJSON, "output_json")
	} else {
		errorJSON, err = normalizeJSONObject(params.ErrorJSON, "error_json")
	}
	if err != nil {
		return err
	}
	var errorCategory *string
	if params.ErrorCategory != nil {
		value := string(*params.ErrorCategory)
		errorCategory = &value
	}
	tag, err := r.pool.Exec(ctx, `
		UPDATE tool_calls
		SET status = $2, output_json = $3, error_json = $4, error_category = $5,
			latency_ms = $6, completed_at = $7
		WHERE id = $1 AND status IN ('pending', 'running')
	`, params.ToolCallID, params.Status, nullableJSON(outputJSON), nullableJSON(errorJSON),
		errorCategory, params.LatencyMS, params.CompletedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("工具调用缺少或 al读取y 终止")
	}
	return nil
}

// scanAttempt 执行该函数负责的核心处理逻辑。
func scanAttempt(row pgx.Row) (Attempt, error) {
	var value Attempt
	err := row.Scan(
		&value.ID, &value.StepID, &value.AttemptNumber, &value.Provider, &value.Model,
		&value.PromptVersion, &value.Temperature, &value.ContextManifestID, &value.TraceID,
		&value.InputTokens, &value.OutputTokens, &value.Cost, &value.LatencyMS,
		&value.RetryCount, &value.FinishReason, &value.ErrorCategory,
		&value.StartedAt, &value.CompletedAt,
	)
	return value, err
}

// scanContextManifest 执行该函数负责的核心处理逻辑。
func scanContextManifest(row pgx.Row) (ContextManifest, error) {
	var value ContextManifest
	err := row.Scan(
		&value.ID, &value.RunID, &value.StepID, &value.TokenBudget,
		&value.ReservedOutputTokens, &value.Tokenizer, &value.ItemsJSON,
		&value.TotalTokens, &value.ContentHash, &value.CreatedAt,
	)
	return value, err
}

// scanToolCall 执行该函数负责的核心处理逻辑。
func scanToolCall(row pgx.Row) (ToolCall, error) {
	var value ToolCall
	err := row.Scan(
		&value.ID, &value.RunID, &value.StepID, &value.ToolName, &value.ToolVersion,
		&value.InputJSON, &value.OutputJSON, &value.Status, &value.IdempotencyKey,
		&value.ErrorJSON, &value.ErrorCategory, &value.LatencyMS, &value.StartedAt,
		&value.CompletedAt, &value.CreatedAt,
	)
	return value, err
}

// normalizeJSONArray 执行该函数负责的核心处理逻辑。
func normalizeJSONArray(value json.RawMessage, field string) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		return json.RawMessage(`[]`), nil
	}
	if !json.Valid(value) {
		return nil, fmt.Errorf("处理失败：%s 必须为有效的 JSON", field)
	}
	var array []any
	if err := json.Unmarshal(value, &array); err != nil || array == nil {
		return nil, fmt.Errorf("%s 必须为一个 JSON 数组", field)
	}
	normalized, err := json.Marshal(array)
	return normalized, err
}

// nullableJSON 执行该函数负责的核心处理逻辑。
func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
