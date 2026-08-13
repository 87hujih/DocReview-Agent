package agentops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/agent/operations"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const runListSQL = `
SELECT run.id::text, run.workspace_id::text, COALESCE(run.resource_id::text, ''),
       COALESCE(run.session_id::text, ''), COALESCE(run.request_id, ''), run.status,
       run.objective, COALESCE(run.current_step, ''),
       (SELECT COUNT(*) FROM agent_steps AS step WHERE step.run_id = run.id)::integer,
       (SELECT COUNT(*) FROM agent_steps AS step WHERE step.run_id = run.id AND step.status = 'succeeded')::integer,
       (SELECT COUNT(*) FROM agent_steps AS step WHERE step.run_id = run.id AND step.status = 'failed')::integer,
       COALESCE((
           SELECT approval.id::text FROM agent_tool_approvals AS approval
           WHERE approval.workspace_id = $1 AND approval.run_id = run.id AND approval.status = 'pending'
           ORDER BY approval.created_at, approval.id LIMIT 1
       ), ''),
       run.created_at, run.updated_at
FROM agent_runs AS run
WHERE run.workspace_id = $1
  AND ($2 = '' OR run.status = $2)
  AND (NULLIF($3, '') IS NULL OR run.resource_id = NULLIF($3, '')::uuid)
ORDER BY run.updated_at DESC, run.id DESC
LIMIT $4`

const approvalSelectColumns = `
approval.id::text, approval.workspace_id::text, approval.run_id::text, approval.step_id::text,
COALESCE(run.resource_id::text, ''), COALESCE(run.session_id::text, ''), run.objective,
approval.tool_name, approval.tool_version, approval.reason, approval.status,
approval.resources_json, approval.payload_json, COALESCE(approval.decision_reason, ''),
approval.created_at, approval.decided_at`

const approvalListSQL = `
SELECT ` + approvalSelectColumns + `
FROM agent_tool_approvals AS approval
JOIN agent_runs AS run ON run.id = approval.run_id AND run.workspace_id = approval.workspace_id
WHERE approval.workspace_id = $1
  AND ($2 = '' OR approval.status = $2)
ORDER BY approval.created_at DESC, approval.id DESC
LIMIT $3`

const approvalDetailSQL = `
SELECT ` + approvalSelectColumns + `
FROM agent_tool_approvals AS approval
JOIN agent_runs AS run ON run.id = approval.run_id AND run.workspace_id = approval.workspace_id
WHERE approval.workspace_id = $1 AND approval.id = $2`

func (repository *Repository) ListRuns(ctx context.Context, request operations.RunListRequest) ([]operations.RunSummary, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.Status = strings.TrimSpace(request.Status)
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	if request.WorkspaceID == "" || request.Limit < 1 || request.Limit > 100 {
		return nil, fmt.Errorf("workspace_id 和 1..100 limit 不能为空")
	}
	if request.Status != "" && !validRunStatus(request.Status) {
		return nil, fmt.Errorf("run status 无效")
	}
	if request.ResourceID != "" {
		if _, err := uuid.Parse(request.ResourceID); err != nil {
			return nil, fmt.Errorf("resource_id 无效")
		}
	}
	if repository == nil || repository.pool == nil {
		return nil, fmt.Errorf("operations 数据库不能为空")
	}
	rows, err := repository.pool.Query(ctx, runListSQL, request.WorkspaceID, request.Status, request.ResourceID, request.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]operations.RunSummary, 0)
	for rows.Next() {
		var item operations.RunSummary
		if err := rows.Scan(
			&item.ID, &item.WorkspaceID, &item.ResourceID, &item.SessionID, &item.RequestID,
			&item.Status, &item.Objective, &item.CurrentStep, &item.StepCount,
			&item.CompletedStepCount, &item.FailedStepCount, &item.PendingApprovalID,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (repository *Repository) ListApprovals(ctx context.Context, request operations.ApprovalListRequest) ([]operations.ApprovalSummary, error) {
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.Status = strings.TrimSpace(request.Status)
	if request.WorkspaceID == "" || request.Limit < 1 || request.Limit > 100 {
		return nil, fmt.Errorf("workspace_id 和 1..100 limit 不能为空")
	}
	if request.Status != "" && !validApprovalStatus(request.Status) {
		return nil, fmt.Errorf("approval status 无效")
	}
	if repository == nil || repository.pool == nil {
		return nil, fmt.Errorf("operations 数据库不能为空")
	}
	rows, err := repository.pool.Query(ctx, approvalListSQL, request.WorkspaceID, request.Status, request.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]operations.ApprovalSummary, 0)
	for rows.Next() {
		item, err := scanApprovalSummary(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (repository *Repository) GetApproval(ctx context.Context, workspaceID, approvalID string) (operations.ApprovalSummary, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	approvalID = strings.TrimSpace(approvalID)
	if workspaceID == "" || approvalID == "" {
		return operations.ApprovalSummary{}, fmt.Errorf("workspace_id 和 approval_id 不能为空")
	}
	if repository == nil || repository.pool == nil {
		return operations.ApprovalSummary{}, fmt.Errorf("operations 数据库不能为空")
	}
	item, err := scanApprovalSummary(repository.pool.QueryRow(ctx, approvalDetailSQL, workspaceID, approvalID))
	if errors.Is(err, pgx.ErrNoRows) {
		return operations.ApprovalSummary{}, ErrNotFound
	}
	return item, err
}

type approvalScanner interface {
	Scan(dest ...any) error
}

func scanApprovalSummary(row approvalScanner) (operations.ApprovalSummary, error) {
	var item operations.ApprovalSummary
	err := row.Scan(
		&item.ID, &item.WorkspaceID, &item.RunID, &item.StepID, &item.ResourceID,
		&item.SessionID, &item.Objective, &item.ToolName, &item.ToolVersion, &item.Reason,
		&item.Status, &item.ResourcesJSON, &item.PayloadJSON, &item.DecisionReason,
		&item.CreatedAt, &item.DecidedAt,
	)
	return item, err
}

func validRunStatus(status string) bool {
	switch status {
	case "queued", "running", "waiting_input", "waiting_approval", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func validApprovalStatus(status string) bool {
	switch status {
	case "pending", "approved", "rejected", "cancelled":
		return true
	default:
		return false
	}
}
