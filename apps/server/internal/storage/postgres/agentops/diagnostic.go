package agentops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/operations"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound       = errors.New("工作区-作用域d 运行时目标未找到")
	ErrActionConflict = errors.New("操作员请求 conflicts 包含一个 different 动作")
)

type Repository struct{ pool *pgxpool.Pool }

// NewRepository 校验依赖并创建对应实例。
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

const diagnosticRunSQL = `
SELECT id::text, workspace_id::text, COALESCE(resource_id::text, ''), COALESCE(session_id::text, ''), COALESCE(request_id, ''),
       COALESCE(trace_id, ''), COALESCE(runtime_mode, ''), status, objective,
       COALESCE(current_step, ''), version, state_json, deadline_at, cancel_requested_at,
       created_at, updated_at
FROM agent_runs
WHERE id = $2 AND workspace_id = $1`

const diagnosticStepsSQL = `
SELECT step.id::text, step.run_id::text, step.step_key, step.step_type, step.status,
       step.input_json, COALESCE(step.output_json, '{}'::jsonb), COALESCE(step.error_json, '{}'::jsonb),
       COALESCE(step.claimed_by, ''), step.lease_expires_at, step.lease_generation,
       step.attempt_count, step.max_attempts, step.next_retry_at, step.created_at, step.updated_at
FROM agent_steps AS step
JOIN agent_runs AS run ON run.id = step.run_id
WHERE run.workspace_id = $1 AND run.id = $2
ORDER BY step.created_at, step.id`

const diagnosticAttemptsSQL = `
SELECT attempt.id::text, attempt.step_id::text, attempt.attempt_number,
       COALESCE(attempt.context_manifest_id::text, ''), COALESCE(attempt.trace_id, ''),
       COALESCE(attempt.provider, ''), COALESCE(attempt.model, ''),
       COALESCE(attempt.input_tokens, 0), COALESCE(attempt.output_tokens, 0),
       COALESCE(attempt.cost, 0)::double precision, COALESCE(attempt.latency_ms, 0),
       COALESCE(attempt.error_category, ''), attempt.started_at, attempt.completed_at
FROM agent_attempts AS attempt
JOIN agent_steps AS step ON step.id = attempt.step_id
JOIN agent_runs AS run ON run.id = step.run_id
WHERE run.workspace_id = $1 AND run.id = $2
ORDER BY step.created_at, attempt.attempt_number, attempt.id`

const diagnosticToolsSQL = `
SELECT call.id::text, call.run_id::text, call.step_id::text, call.tool_name, call.tool_version,
       call.status, COALESCE(call.idempotency_key, ''), call.input_json,
       COALESCE(call.output_json, '{}'::jsonb), COALESCE(call.error_json, '{}'::jsonb),
       COALESCE(call.error_category, ''), call.lease_generation,
       COALESCE(call.output_json #>> '{output,evidence_set,set_id}', call.output_json #>> '{output,summary,set_id}', ''),
       call.started_at, call.completed_at
FROM tool_calls AS call
JOIN agent_runs AS run ON run.id = call.run_id
WHERE run.workspace_id = $1 AND run.id = $2
ORDER BY call.created_at, call.id`

const diagnosticManifestsSQL = `
SELECT manifest.id::text, manifest.run_id::text, manifest.step_id::text,
       manifest.tokenizer, manifest.token_budget, manifest.reserved_output_tokens,
       manifest.total_tokens, manifest.content_hash, manifest.items_json, manifest.created_at
FROM context_manifests AS manifest
JOIN agent_runs AS run ON run.id = manifest.run_id
WHERE run.workspace_id = $1 AND run.id = $2
ORDER BY manifest.created_at, manifest.id`

const diagnosticApprovalsSQL = `
SELECT approval.id::text, approval.run_id::text, approval.step_id::text,
       approval.tool_name, approval.status, approval.created_at, approval.decided_at
FROM agent_tool_approvals AS approval
JOIN agent_runs AS run ON run.id = approval.run_id
WHERE approval.workspace_id = $1 AND run.workspace_id = $1 AND run.id = $2
ORDER BY approval.created_at, approval.id`

const diagnosticOutboxSQL = `
WITH scoped_run AS (
    SELECT id::text FROM agent_runs WHERE workspace_id = $1 AND id = $2
)
SELECT event.id::text, event.aggregate_type, event.aggregate_id, event.event_type,
       event.status, event.attempt_count, event.lease_generation, event.payload_json,
       COALESCE(event.error_json, '{}'::jsonb), event.created_at, event.published_at
FROM outbox_events AS event
WHERE EXISTS (
    SELECT 1 FROM scoped_run AS run
    WHERE (event.aggregate_type = 'agent_run' AND event.aggregate_id = run.id)
       OR event.payload_json ->> 'run_id' = run.id
       OR event.id::text IN (
           SELECT COALESCE(call.output_json #>> '{output,commit,outbox_id}', '')
           FROM tool_calls AS call WHERE call.run_id::text = run.id
       )
)
ORDER BY event.created_at, event.id`

const allDiagnosticSQL = diagnosticRunSQL + diagnosticStepsSQL + diagnosticAttemptsSQL + diagnosticToolsSQL +
	diagnosticManifestsSQL + diagnosticApprovalsSQL + diagnosticOutboxSQL + metricsSQL

// Diagnose 执行该函数负责的核心处理逻辑。
func (repository *Repository) Diagnose(ctx context.Context, request operations.DiagnosticRequest) (operations.Diagnostic, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.RunID = strings.TrimSpace(request.RunID)
	if request.WorkspaceID == "" || request.RunID == "" {
		return operations.Diagnostic{}, fmt.Errorf("工作区_id 和 run_id 不能为空")
	}
	if repository == nil || repository.pool == nil {
		return operations.Diagnostic{}, fmt.Errorf("operations 数据库不能为空")
	}
	result := operations.Diagnostic{}
	err := repository.pool.QueryRow(ctx, diagnosticRunSQL, request.WorkspaceID, request.RunID).Scan(
		&result.Run.ID, &result.Run.WorkspaceID, &result.Run.ResourceID, &result.Run.SessionID, &result.Run.RequestID,
		&result.Run.TraceID, &result.Run.RuntimeMode, &result.Run.Status, &result.Run.Objective,
		&result.Run.CurrentStep, &result.Run.Version, &result.Run.StateJSON, &result.Run.DeadlineAt,
		&result.Run.CancelRequestedAt, &result.Run.CreatedAt, &result.Run.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return operations.Diagnostic{}, ErrNotFound
	}
	if err != nil {
		return operations.Diagnostic{}, err
	}
	if result.Steps, err = repository.listSteps(ctx, request); err != nil {
		return operations.Diagnostic{}, err
	}
	if result.Attempts, err = repository.listAttempts(ctx, request); err != nil {
		return operations.Diagnostic{}, err
	}
	if result.ToolCalls, err = repository.listTools(ctx, request); err != nil {
		return operations.Diagnostic{}, err
	}
	if result.ContextManifests, err = repository.listManifests(ctx, request); err != nil {
		return operations.Diagnostic{}, err
	}
	if result.Approvals, err = repository.listApprovals(ctx, request); err != nil {
		return operations.Diagnostic{}, err
	}
	if result.OutboxEvents, err = repository.listOutbox(ctx, request); err != nil {
		return operations.Diagnostic{}, err
	}
	result.TraceIndex = buildTraceIndex(result)
	result.Findings = diagnose(result, time.Now().UTC())
	return result, nil
}

// listSteps 执行该函数负责的核心处理逻辑。
func (repository *Repository) listSteps(ctx context.Context, request operations.DiagnosticRequest) ([]operations.StepView, error) {
	rows, err := repository.pool.Query(ctx, diagnosticStepsSQL, request.WorkspaceID, request.RunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]operations.StepView, 0)
	for rows.Next() {
		var item operations.StepView
		if err := rows.Scan(&item.ID, &item.RunID, &item.StepKey, &item.StepType, &item.Status,
			&item.InputJSON, &item.OutputJSON, &item.ErrorJSON, &item.ClaimedBy, &item.LeaseExpiresAt,
			&item.LeaseGeneration, &item.AttemptCount, &item.MaxAttempts, &item.NextRetryAt,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// listAttempts 执行该函数负责的核心处理逻辑。
func (repository *Repository) listAttempts(ctx context.Context, request operations.DiagnosticRequest) ([]operations.AttemptView, error) {
	rows, err := repository.pool.Query(ctx, diagnosticAttemptsSQL, request.WorkspaceID, request.RunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]operations.AttemptView, 0)
	for rows.Next() {
		var item operations.AttemptView
		if err := rows.Scan(&item.ID, &item.StepID, &item.AttemptNumber, &item.ContextManifestID,
			&item.TraceID, &item.Provider, &item.Model, &item.InputTokens, &item.OutputTokens,
			&item.Cost, &item.LatencyMS, &item.ErrorCategory, &item.StartedAt, &item.CompletedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// listTools 执行该函数负责的核心处理逻辑。
func (repository *Repository) listTools(ctx context.Context, request operations.DiagnosticRequest) ([]operations.ToolCallView, error) {
	rows, err := repository.pool.Query(ctx, diagnosticToolsSQL, request.WorkspaceID, request.RunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]operations.ToolCallView, 0)
	for rows.Next() {
		var item operations.ToolCallView
		if err := rows.Scan(&item.ID, &item.RunID, &item.StepID, &item.ToolName, &item.ToolVersion,
			&item.Status, &item.IdempotencyKey, &item.InputJSON, &item.OutputJSON, &item.ErrorJSON,
			&item.ErrorCategory, &item.LeaseGeneration, &item.EvidenceSetID, &item.StartedAt,
			&item.CompletedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// listManifests 执行该函数负责的核心处理逻辑。
func (repository *Repository) listManifests(ctx context.Context, request operations.DiagnosticRequest) ([]operations.ContextManifestView, error) {
	rows, err := repository.pool.Query(ctx, diagnosticManifestsSQL, request.WorkspaceID, request.RunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]operations.ContextManifestView, 0)
	for rows.Next() {
		var item operations.ContextManifestView
		if err := rows.Scan(&item.ID, &item.RunID, &item.StepID, &item.Tokenizer, &item.TokenBudget,
			&item.ReservedOutputTokens, &item.TotalTokens, &item.ContentHash, &item.ItemsJSON,
			&item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// listApprovals 执行该函数负责的核心处理逻辑。
func (repository *Repository) listApprovals(ctx context.Context, request operations.DiagnosticRequest) ([]operations.ApprovalView, error) {
	rows, err := repository.pool.Query(ctx, diagnosticApprovalsSQL, request.WorkspaceID, request.RunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]operations.ApprovalView, 0)
	for rows.Next() {
		var item operations.ApprovalView
		if err := rows.Scan(&item.ID, &item.RunID, &item.StepID, &item.ToolName, &item.Status,
			&item.CreatedAt, &item.DecidedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// listOutbox 执行该函数负责的核心处理逻辑。
func (repository *Repository) listOutbox(ctx context.Context, request operations.DiagnosticRequest) ([]operations.OutboxView, error) {
	rows, err := repository.pool.Query(ctx, diagnosticOutboxSQL, request.WorkspaceID, request.RunID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]operations.OutboxView, 0)
	for rows.Next() {
		var item operations.OutboxView
		if err := rows.Scan(&item.ID, &item.AggregateType, &item.AggregateID, &item.EventType,
			&item.Status, &item.AttemptCount, &item.LeaseGeneration, &item.PayloadJSON,
			&item.ErrorJSON, &item.CreatedAt, &item.PublishedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// buildTraceIndex 执行该函数负责的核心处理逻辑。
func buildTraceIndex(diagnostic operations.Diagnostic) []operations.TraceLink {
	toolsByStep := make(map[string][]string)
	evidenceByStep := make(map[string][]string)
	manifestByStep := make(map[string]string)
	for _, manifest := range diagnostic.ContextManifests {
		manifestByStep[manifest.StepID] = manifest.ID
	}
	for _, call := range diagnostic.ToolCalls {
		toolsByStep[call.StepID] = append(toolsByStep[call.StepID], call.ID)
		if call.EvidenceSetID != "" {
			evidenceByStep[call.StepID] = append(evidenceByStep[call.StepID], call.EvidenceSetID)
		}
	}
	result := make([]operations.TraceLink, 0, len(diagnostic.Attempts)+len(diagnostic.Steps))
	seenSteps := make(map[string]struct{})
	for _, attempt := range diagnostic.Attempts {
		manifestID := attempt.ContextManifestID
		if manifestID == "" {
			manifestID = manifestByStep[attempt.StepID]
		}
		result = append(result, operations.TraceLink{
			RunID: diagnostic.Run.ID, StepID: attempt.StepID, AttemptID: attempt.ID,
			ToolCallIDs:       append([]string(nil), toolsByStep[attempt.StepID]...),
			ContextManifestID: manifestID,
			EvidenceSetIDs:    append([]string(nil), evidenceByStep[attempt.StepID]...),
		})
		seenSteps[attempt.StepID] = struct{}{}
	}
	for _, step := range diagnostic.Steps {
		if _, exists := seenSteps[step.ID]; exists {
			continue
		}
		result = append(result, operations.TraceLink{
			RunID: diagnostic.Run.ID, StepID: step.ID, ToolCallIDs: append([]string(nil), toolsByStep[step.ID]...),
			ContextManifestID: manifestByStep[step.ID], EvidenceSetIDs: append([]string(nil), evidenceByStep[step.ID]...),
		})
	}
	return result
}

// diagnose 执行该函数负责的核心处理逻辑。
func diagnose(diagnostic operations.Diagnostic, now time.Time) []operations.Finding {
	findings := make([]operations.Finding, 0)
	for _, step := range diagnostic.Steps {
		if step.Status == "running" && step.LeaseExpiresAt != nil && !step.LeaseExpiresAt.After(now) {
			findings = append(findings, operations.Finding{Severity: "critical", Code: "expired_step_lease", Message: "运行中步骤的租约已过期：" + step.ID})
		}
		if step.Status == "failed" && !emptyJSONObject(step.ErrorJSON) {
			findings = append(findings, operations.Finding{Severity: "error", Code: "failed_step", Message: "步骤出现终态错误：" + step.ID})
		}
	}
	for _, event := range diagnostic.OutboxEvents {
		if event.Status == "dead_letter" {
			findings = append(findings, operations.Finding{Severity: "critical", Code: "outbox_dead_letter", Message: "发件箱事件需要审核后重放：" + event.ID})
		}
	}
	if diagnostic.Run.Status == "waiting_approval" && len(diagnostic.Approvals) == 0 {
		findings = append(findings, operations.Finding{Severity: "critical", Code: "missing_approval_fact", Message: "运行正在等待审批，但缺少审批事实"})
	}
	return findings
}

// emptyJSONObject 执行该函数负责的核心处理逻辑。
func emptyJSONObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "{}" || trimmed == "null"
}

var _ operations.Store = (*Repository)(nil)
