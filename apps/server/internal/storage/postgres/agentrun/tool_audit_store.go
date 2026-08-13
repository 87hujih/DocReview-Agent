package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agenttools "agent_project/apps/server/internal/agent/tools"

	"github.com/jackc/pgx/v5"
)

var ErrToolCallLeaseLost = errors.New("工具调用租约为没有 longer owned")

type ToolAuditStore struct {
	repo          *Repository
	workerID      string
	leaseDuration time.Duration
}

var _ agenttools.AuditStore = (*ToolAuditStore)(nil)

// NewToolAuditStore 校验依赖并创建对应实例。
func NewToolAuditStore(repo *Repository, workerID string, leaseDuration time.Duration) (*ToolAuditStore, error) {
	workerID = strings.TrimSpace(workerID)
	if repo == nil || workerID == "" || leaseDuration <= 0 {
		return nil, fmt.Errorf("工具 audit repository、工作进程_id、和租约时长不能为空")
	}
	return &ToolAuditStore{repo: repo, workerID: workerID, leaseDuration: leaseDuration}, nil
}

type toolAuditRow struct {
	ID              string
	StepID          string
	ToolName        string
	ToolVersion     string
	InputJSON       json.RawMessage
	OutputJSON      json.RawMessage
	Status          string
	ErrorJSON       json.RawMessage
	ErrorCategory   *string
	ClaimedBy       *string
	LeaseExpiresAt  *time.Time
	LeaseGeneration int64
}

const toolAuditColumns = `
	id, step_id, tool_name, tool_version, input_json, output_json, status,
	error_json, error_category, claimed_by, lease_expires_at, lease_generation`

// Begin 执行该函数负责的核心处理逻辑。
func (s *ToolAuditStore) Begin(ctx context.Context, start agenttools.AuditStart) (agenttools.AuditRecord, error) {
	call := start.Call
	call.RunID = strings.TrimSpace(call.RunID)
	call.StepID = strings.TrimSpace(call.StepID)
	call.ToolName = strings.TrimSpace(call.ToolName)
	call.ToolVersion = strings.TrimSpace(call.ToolVersion)
	if call.RunID == "" || call.StepID == "" || call.ToolName == "" || call.ToolVersion == "" {
		return agenttools.AuditRecord{}, fmt.Errorf("run_id、step_id、工具_name、和工具_version 不能为空")
	}
	if start.Descriptor.Name != call.ToolName || start.Descriptor.Version != call.ToolVersion {
		return agenttools.AuditRecord{}, fmt.Errorf("工具 audit descript或不匹配 call 标识")
	}
	if start.StartedAt.IsZero() {
		return agenttools.AuditRecord{}, fmt.Errorf("工具 audit 启动ed_at 不能为空")
	}
	inputJSON := normalizeToolAuditInput(call.Input)
	if s.repo.pool == nil {
		return agenttools.AuditRecord{}, fmt.Errorf("工具 audit 数据库不能为空")
	}

	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := s.repo.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return agenttools.AuditRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	leaseDuration := toolAuditLeaseDuration(s.leaseDuration, start.Descriptor)
	leaseExpiresAt := start.StartedAt.Add(leaseDuration)
	idempotencyKey := trimOptionalString(call.IdempotencyKey)
	row, err := scanToolAuditRow(tx.QueryRow(ctx, `
		INSERT INTO tool_calls (
			run_id, step_id, tool_name, tool_version, input_json, status, idempotency_key,
			started_at, claimed_by, lease_expires_at, lease_generation, attempt_count
		)
		VALUES ($1, $2, $3, $4, $5, 'running', $6, $7, $8, $9, 1, 1)
		ON CONFLICT DO NOTHING
		RETURNING `+toolAuditColumns,
		call.RunID, call.StepID, call.ToolName, call.ToolVersion, inputJSON,
		idempotencyKey, start.StartedAt, s.workerID, leaseExpiresAt,
	))
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return agenttools.AuditRecord{}, err
		}
		return row.auditRecord(true)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return agenttools.AuditRecord{}, err
	}
	if idempotencyKey == nil {
		return agenttools.AuditRecord{}, fmt.Errorf("工具调用 insert conflicted 不包含 idempotency 键")
	}

	row, err = scanToolAuditRow(tx.QueryRow(ctx, `
		SELECT `+toolAuditColumns+`
		FROM tool_calls
		WHERE run_id = $1 AND idempotency_key = $2
		FOR UPDATE
	`, call.RunID, *idempotencyKey))
	if err != nil {
		return agenttools.AuditRecord{}, err
	}
	if row.StepID != call.StepID || row.ToolName != call.ToolName || row.ToolVersion != call.ToolVersion || !jsonEqual(row.InputJSON, inputJSON) {
		return agenttools.AuditRecord{}, ErrIdempotencyConflict
	}

	claimable := row.Status == "pending" || (row.Status == "running" && (row.LeaseExpiresAt == nil || !row.LeaseExpiresAt.After(start.StartedAt)))
	if claimable {
		row, err = scanToolAuditRow(tx.QueryRow(ctx, `
			UPDATE tool_calls
			SET status = 'running', claimed_by = $2, lease_expires_at = $3,
				lease_generation = lease_generation + 1, attempt_count = attempt_count + 1,
				started_at = COALESCE(started_at, $4)
			WHERE id = $1
			RETURNING `+toolAuditColumns,
			row.ID, s.workerID, leaseExpiresAt, start.StartedAt,
		))
		if err != nil {
			return agenttools.AuditRecord{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return agenttools.AuditRecord{}, err
		}
		return row.auditRecord(true)
	}
	if err := tx.Commit(ctx); err != nil {
		return agenttools.AuditRecord{}, err
	}
	return row.auditRecord(false)
}

// toolAuditLeaseDuration 执行该函数负责的核心处理逻辑。
func toolAuditLeaseDuration(configured time.Duration, descriptor agenttools.Descriptor) time.Duration {
	attempts := descriptor.RetryPolicy.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}
	boundedExecution := descriptor.Timeout*time.Duration(attempts) +
		descriptor.RetryPolicy.MaxBackoff*time.Duration(attempts-1) + 5*time.Second
	if boundedExecution > configured {
		return boundedExecution
	}
	return configured
}

// normalizeToolAuditInput 执行该函数负责的核心处理逻辑。
func normalizeToolAuditInput(raw json.RawMessage) json.RawMessage {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err == nil && object != nil {
		normalized, _ := json.Marshal(object)
		return normalized
	}
	wrapped, _ := json.Marshal(map[string]any{
		"_tool_runtime_invalid_json": true,
		"raw":                        string(raw),
	})
	return wrapped
}

// Finish 执行该函数负责的核心处理逻辑。
func (s *ToolAuditStore) Finish(ctx context.Context, finish agenttools.AuditFinish) error {
	finish.ID = strings.TrimSpace(finish.ID)
	finish.ClaimedBy = strings.TrimSpace(finish.ClaimedBy)
	if finish.ID == "" || finish.ClaimedBy == "" || finish.LeaseGeneration <= 0 || finish.CompletedAt.IsZero() {
		return fmt.Errorf("tool call id, lease owner/generation, and completed_at are required")
	}
	if finish.ClaimedBy != s.workerID {
		return fmt.Errorf("工具调用租约 owner 不匹配 audit 工作进程")
	}
	if finish.Attempts < 0 || finish.LatencyMS < 0 {
		return fmt.Errorf("工具调用 attempts 和 latency 必须 not 为负数")
	}
	status := string(finish.Status)
	if status != "succeeded" && status != "failed" && status != "cancelled" {
		return fmt.Errorf("工具调用终止状态无效")
	}
	var outputJSON, errorJSON json.RawMessage
	var errorCategory any
	var err error
	if finish.Status == agenttools.AuditSucceeded {
		if finish.Result == nil {
			return fmt.Errorf("successful 工具调用结果不能为空")
		}
		outputJSON, err = json.Marshal(finish.Result)
	} else {
		if finish.Error == nil || !finish.Error.Category.Valid() {
			return fmt.Errorf("失败工具调用 classified 错误不能为空")
		}
		errorJSON, err = json.Marshal(finish.Error)
		errorCategory = string(finish.Error.Category)
	}
	if err != nil {
		return err
	}
	tag, err := s.repo.pool.Exec(ctx, `
		UPDATE tool_calls
		SET status = $4, output_json = $5, error_json = $6, error_category = $7,
			latency_ms = $8, completed_at = $9,
			attempt_count = attempt_count + GREATEST($10 - 1, 0),
			claimed_by = NULL, lease_expires_at = NULL
		WHERE id = $1 AND status = 'running' AND claimed_by = $2
		  AND lease_generation = $3 AND lease_expires_at > $9
	`, finish.ID, finish.ClaimedBy, finish.LeaseGeneration, status,
		nullableJSON(outputJSON), nullableJSON(errorJSON), errorCategory,
		finish.LatencyMS, finish.CompletedAt, finish.Attempts)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrToolCallLeaseLost
	}
	return nil
}

// scanToolAuditRow 执行该函数负责的核心处理逻辑。
func scanToolAuditRow(row pgx.Row) (toolAuditRow, error) {
	var value toolAuditRow
	err := row.Scan(
		&value.ID, &value.StepID, &value.ToolName, &value.ToolVersion, &value.InputJSON,
		&value.OutputJSON, &value.Status, &value.ErrorJSON, &value.ErrorCategory,
		&value.ClaimedBy, &value.LeaseExpiresAt, &value.LeaseGeneration,
	)
	return value, err
}

// auditRecord 执行该函数负责的核心处理逻辑。
func (row toolAuditRow) auditRecord(acquired bool) (agenttools.AuditRecord, error) {
	record := agenttools.AuditRecord{ID: row.ID, Acquired: acquired, Status: auditStatus(row.Status), LeaseGeneration: row.LeaseGeneration}
	if row.ClaimedBy != nil {
		record.ClaimedBy = *row.ClaimedBy
	}
	if row.Status == "succeeded" && len(row.OutputJSON) > 0 {
		var result agenttools.Result
		if err := json.Unmarshal(row.OutputJSON, &result); err != nil {
			return agenttools.AuditRecord{}, fmt.Errorf("解析持久化ed 工具结果：%w", err)
		}
		record.Result = &result
	}
	if (row.Status == "failed" || row.Status == "cancelled") && len(row.ErrorJSON) > 0 {
		var failure agenttools.ToolError
		if err := json.Unmarshal(row.ErrorJSON, &failure); err != nil {
			return agenttools.AuditRecord{}, fmt.Errorf("解析持久化ed 工具错误：%w", err)
		}
		record.Error = &failure
	}
	return record, nil
}

// auditStatus 执行该函数负责的核心处理逻辑。
func auditStatus(status string) agenttools.AuditStatus {
	// 根据当前状态或类型选择对应的处理分支。
	switch status {
	case "running":
		return agenttools.AuditRunning
	case "succeeded":
		return agenttools.AuditSucceeded
	case "failed":
		return agenttools.AuditFailed
	case "cancelled":
		return agenttools.AuditCancelled
	default:
		return agenttools.AuditPending
	}
}

// trimOptionalString 执行该函数负责的核心处理逻辑。
func trimOptionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
