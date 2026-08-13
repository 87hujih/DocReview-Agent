package agentpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/orchestration"
	"agent_project/apps/server/internal/agent/policy"
	agenttools "agent_project/apps/server/internal/agent/tools"
	"agent_project/apps/server/internal/agent/tools/builtin"
	"agent_project/apps/server/internal/storage/postgres/outbox"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Resolver struct{ pool *pgxpool.Pool }

// NewResolver 校验依赖并创建对应实例。
func NewResolver(pool *pgxpool.Pool) *Resolver { return &Resolver{pool: pool} }

var (
	_ policy.PermissionResolver = (*Resolver)(nil)
	_ policy.ResourceResolver   = (*Resolver)(nil)
	_ policy.ApprovalVerifier   = (*Resolver)(nil)
)

// HasPermission 执行该函数负责的核心处理逻辑。
func (r *Resolver) HasPermission(ctx context.Context, principal agenttools.SecurityContext, permission string) (bool, error) {
	if !validPrincipal(principal) || strings.TrimSpace(permission) == "" || principal.PrincipalType != "user" {
		return false, nil
	}
	if r == nil || r.pool == nil {
		return false, fmt.Errorf("policy 数据库不能为空")
	}
	var role string
	err := r.pool.QueryRow(ctx, `
		SELECT membership.role
		FROM memberships AS membership
		JOIN users AS account ON account.id = membership.user_id
		JOIN workspaces AS workspace ON workspace.id = membership.workspace_id
		WHERE membership.workspace_id = $1 AND membership.user_id = $2
		  AND membership.status = 'active' AND account.status = 'active' AND workspace.status = 'active'
	`, principal.WorkspaceID, principal.PrincipalID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return roleAllows(role, permission), nil
}

// AuthorizeResource 执行该函数负责的核心处理逻辑。
func (r *Resolver) AuthorizeResource(ctx context.Context, principal agenttools.SecurityContext, resource agenttools.ResourceRef) (bool, error) {
	if resource.Type != "document" && resource.Type != "artifact" && resource.Type != "task" {
		return false, nil
	}
	if !validPrincipal(principal) || strings.TrimSpace(resource.ID) == "" ||
		(resource.Access != agenttools.AccessRead && resource.Access != agenttools.AccessWrite) {
		return false, nil
	}
	if resource.Type == "document" && strings.TrimSpace(principal.ResourceID) != "" &&
		strings.TrimSpace(principal.ResourceID) != strings.TrimSpace(resource.ID) {
		return false, nil
	}
	if r == nil || r.pool == nil {
		return false, fmt.Errorf("policy 数据库不能为空")
	}
	query := map[string]string{
		"document": `SELECT EXISTS (SELECT 1 FROM resources WHERE id = $1 AND workspace_id = $2)`,
		"artifact": `SELECT EXISTS (SELECT 1 FROM agent_artifacts WHERE id = $1 AND workspace_id = $2)`,
		"task":     `SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND workspace_id = $2)`,
	}[resource.Type]
	var allowed bool
	if err := r.pool.QueryRow(ctx, query, resource.ID, principal.WorkspaceID).Scan(&allowed); err != nil {
		return false, err
	}
	return allowed, nil
}

// VerifyApproval 执行该函数负责的核心处理逻辑。
func (r *Resolver) VerifyApproval(ctx context.Context, check agenttools.ApprovalCheck) (bool, error) {
	if !validPrincipal(check.Principal) || strings.TrimSpace(check.ApprovalID) == "" ||
		strings.TrimSpace(check.RunID) == "" || strings.TrimSpace(check.StepID) == "" ||
		strings.TrimSpace(check.ToolName) == "" || strings.TrimSpace(check.ToolVersion) == "" || strings.TrimSpace(check.IdempotencyKey) == "" {
		return false, nil
	}
	if r == nil || r.pool == nil {
		return false, fmt.Errorf("policy 数据库不能为空")
	}
	// 说明： check.StepID proves this 为 一个 concrete persisted 执行 call. The
	// 审批 row's step_id 为 溯源信息 用于 RequestApproval, not the later
	// CommitPatch 步骤. 目标 authority 为 bound 由 运行, 工具/版本,
	// idempotency 键, 工作区, 和 canonical resources below.
	var approved bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_tool_approvals
			WHERE id = $1 AND workspace_id = $2 AND status = 'approved'
			  AND run_id = $3
			  AND tool_name = $4 AND tool_version = $5 AND idempotency_key = $6
			  AND resources_hash = $7
		)
	`, check.ApprovalID, check.Principal.WorkspaceID, check.RunID, check.ToolName, check.ToolVersion,
		check.IdempotencyKey, agenttools.HashResources(check.Resources)).Scan(&approved)
	return approved, err
}

type ApprovalStore struct {
	pool   *pgxpool.Pool
	outbox *outbox.Repository
}

// NewApprovalStore 校验依赖并创建对应实例。
func NewApprovalStore(pool *pgxpool.Pool) *ApprovalStore {
	return &ApprovalStore{pool: pool, outbox: outbox.NewRepository(pool)}
}

var _ builtin.ApprovalBackend = (*ApprovalStore)(nil)

type approvalRecord struct {
	ID             string
	WorkspaceID    string
	RunID          string
	StepID         string
	ToolName       string
	ToolVersion    string
	IdempotencyKey string
	ResourcesJSON  json.RawMessage
	ResourcesHash  string
	PayloadJSON    json.RawMessage
	Reason         string
	Status         string
	CreatedAt      time.Time
}

type DecisionParams struct {
	ApprovalID string
	Security   agenttools.SecurityContext
	Status     string
	Reason     string
	DecidedAt  time.Time
}

const approvalColumns = `
	id, workspace_id, run_id, step_id, tool_name, tool_version, idempotency_key,
	resources_json, resources_hash, payload_json, reason, status, created_at`

// RequestApproval 执行该函数负责的核心处理逻辑。
func (s *ApprovalStore) RequestApproval(ctx context.Context, security agenttools.SecurityContext, input builtin.ApprovalInput, idempotencyKey string) (builtin.Approval, error) {
	input.RunID = strings.TrimSpace(input.RunID)
	input.StepID = strings.TrimSpace(input.StepID)
	input.ToolName = strings.TrimSpace(input.ToolName)
	input.ToolVersion = strings.TrimSpace(input.ToolVersion)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Reason = strings.TrimSpace(input.Reason)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if !validPrincipal(security) {
		return builtin.Approval{}, fmt.Errorf("trusted workspace and principal scope are required")
	}
	if input.RunID == "" || input.StepID == "" || input.ToolName == "" || input.ToolVersion == "" ||
		input.IdempotencyKey == "" || input.Reason == "" || idempotencyKey == "" {
		return builtin.Approval{}, fmt.Errorf("审批目标和 idempotency fields 不能为空")
	}
	payload, err := normalizeObject(input.Payload)
	if err != nil {
		return builtin.Approval{}, err
	}
	resources, err := json.Marshal(input.Resources)
	if err != nil {
		return builtin.Approval{}, err
	}
	for _, resource := range input.Resources {
		if strings.TrimSpace(resource.Type) == "" || strings.TrimSpace(resource.ID) == "" ||
			(resource.Access != agenttools.AccessRead && resource.Access != agenttools.AccessWrite) {
			return builtin.Approval{}, fmt.Errorf("审批资源s 无效")
		}
	}
	resourcesHash := agenttools.HashResources(input.Resources)
	if s == nil || s.pool == nil {
		return builtin.Approval{}, fmt.Errorf("审批数据库不能为空")
	}
	resolver := NewResolver(s.pool)
	for _, resource := range input.Resources {
		allowed, err := resolver.AuthorizeResource(ctx, security, resource)
		if err != nil {
			return builtin.Approval{}, fmt.Errorf("鉴权审批资源 %s/%s：%w", resource.Type, resource.ID, err)
		}
		if !allowed {
			return builtin.Approval{}, &agenttools.ToolError{Category: agenttools.ErrorPermissionDenied, Message: "审批资源作用域校验未通过"}
		}
	}
	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return builtin.Approval{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var targetInScope bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_runs AS run
			JOIN agent_steps AS step ON step.run_id = run.id
			WHERE run.id = $1 AND step.id = $2 AND run.workspace_id = $3
		)
	`, input.RunID, input.StepID, security.WorkspaceID).Scan(&targetInScope); err != nil {
		return builtin.Approval{}, err
	}
	if !targetInScope {
		return builtin.Approval{}, &agenttools.ToolError{Category: agenttools.ErrorPermissionDenied, Message: "审批运行或步骤作用域校验未通过"}
	}
	record, err := scanApproval(tx.QueryRow(ctx, `
		INSERT INTO agent_tool_approvals (
			workspace_id, run_id, step_id, tool_name, tool_version, idempotency_key,
			resources_json, resources_hash, payload_json, reason, requested_by_type, requested_by_id
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (workspace_id, run_id, idempotency_key) DO NOTHING
		RETURNING `+approvalColumns,
		security.WorkspaceID, input.RunID, input.StepID, input.ToolName, input.ToolVersion,
		input.IdempotencyKey, resources, resourcesHash, payload, input.Reason,
		security.PrincipalType, security.PrincipalID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		record, err = scanApproval(tx.QueryRow(ctx, `
			SELECT `+approvalColumns+`
			FROM agent_tool_approvals
			WHERE workspace_id = $1 AND run_id = $2 AND idempotency_key = $3
		`, security.WorkspaceID, input.RunID, input.IdempotencyKey))
	}
	if err != nil {
		return builtin.Approval{}, err
	}
	if !sameApproval(record, input, resources, resourcesHash, payload) {
		return builtin.Approval{}, &agenttools.ToolError{Category: agenttools.ErrorConflict, Message: "审批幂等性冲突"}
	}
	eventKey := "tool-approval-requested:" + record.ID
	eventPayload, _ := json.Marshal(map[string]any{"approval_id": record.ID, "run_id": record.RunID, "tool_name": record.ToolName})
	if _, _, err := s.outbox.Enqueue(ctx, tx, outbox.EnqueueParams{
		AggregateType: "agent_tool_approval", AggregateID: record.ID,
		EventType: "agent.tool_approval.requested", IdempotencyKey: &eventKey, PayloadJSON: eventPayload,
	}); err != nil {
		return builtin.Approval{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return builtin.Approval{}, err
	}
	return builtin.Approval{ID: record.ID, Status: record.Status}, nil
}

// DecideApproval records 一个 human/API-side 决策. It 为 deliberately not 一个
// 工具: 模型 输出 不能 call this method through ToolRuntime 和 therefore
// 处理失败： 不能 manufacture 一个 authorization fact.
func (s *ApprovalStore) DecideApproval(ctx context.Context, params DecisionParams) (builtin.Approval, error) {
	params.ApprovalID = strings.TrimSpace(params.ApprovalID)
	params.Status = strings.TrimSpace(params.Status)
	params.Reason = strings.TrimSpace(params.Reason)
	if !validPrincipal(params.Security) || params.Security.PrincipalType != "user" {
		return builtin.Approval{}, fmt.Errorf("可信的 user 工作区和主体作用域不能为空")
	}
	if params.ApprovalID == "" || (params.Status != "approved" && params.Status != "rejected") || params.Reason == "" || params.DecidedAt.IsZero() {
		return builtin.Approval{}, fmt.Errorf("审批_id、approved/rejected 状态、原因、和 decided_at 不能为空")
	}
	if s == nil || s.pool == nil {
		return builtin.Approval{}, fmt.Errorf("审批数据库不能为空")
	}
	// 开启事务，确保后续状态变更以原子方式提交。
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return builtin.Approval{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var allowed bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM memberships AS membership
			JOIN users AS account ON account.id = membership.user_id
			JOIN workspaces AS workspace ON workspace.id = membership.workspace_id
			WHERE membership.workspace_id = $1 AND membership.user_id = $2
			  AND membership.status = 'active' AND account.status = 'active' AND workspace.status = 'active'
			  AND membership.role IN ('owner', 'admin')
		)
	`, params.Security.WorkspaceID, params.Security.PrincipalID).Scan(&allowed); err != nil {
		return builtin.Approval{}, err
	}
	if !allowed {
		return builtin.Approval{}, &agenttools.ToolError{Category: agenttools.ErrorPermissionDenied, Message: "没有提交审批决策的权限"}
	}

	record, err := scanApproval(tx.QueryRow(ctx, `
		UPDATE agent_tool_approvals
		SET status = $3, decision_reason = $4,
			decided_by_type = $5, decided_by_id = $6, decided_at = $7
		WHERE id = $1 AND workspace_id = $2 AND status = 'pending'
		RETURNING `+approvalColumns,
		params.ApprovalID, params.Security.WorkspaceID, params.Status, params.Reason,
		params.Security.PrincipalType, params.Security.PrincipalID, params.DecidedAt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		var status, decidedByType, decidedByID, reason string
		err = tx.QueryRow(ctx, `
			SELECT status, COALESCE(decided_by_type, ''), COALESCE(decided_by_id, ''), COALESCE(decision_reason, '')
			FROM agent_tool_approvals
			WHERE id = $1 AND workspace_id = $2
		`, params.ApprovalID, params.Security.WorkspaceID).Scan(&status, &decidedByType, &decidedByID, &reason)
		if errors.Is(err, pgx.ErrNoRows) {
			return builtin.Approval{}, &agenttools.ToolError{Category: agenttools.ErrorNotFound, Message: "未找到审批记录"}
		}
		if err != nil {
			return builtin.Approval{}, err
		}
		if status != params.Status || decidedByType != params.Security.PrincipalType || decidedByID != params.Security.PrincipalID || reason != params.Reason {
			return builtin.Approval{}, &agenttools.ToolError{Category: agenttools.ErrorConflict, Message: "审批已存在不同的终态决策"}
		}
		return builtin.Approval{ID: params.ApprovalID, Status: status}, nil
	}
	if err != nil {
		return builtin.Approval{}, err
	}
	if err := transitionTypedApproval(ctx, tx, record, params.Status, params.DecidedAt); err != nil {
		return builtin.Approval{}, err
	}
	eventKey := "tool-approval-decided:" + record.ID
	eventPayload, _ := json.Marshal(map[string]any{
		"approval_id": record.ID, "run_id": record.RunID, "tool_name": record.ToolName, "status": record.Status,
	})
	if _, _, err := s.outbox.Enqueue(ctx, tx, outbox.EnqueueParams{
		AggregateType: "agent_tool_approval", AggregateID: record.ID,
		EventType: "agent.tool_approval." + record.Status, IdempotencyKey: &eventKey, PayloadJSON: eventPayload,
	}); err != nil {
		return builtin.Approval{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return builtin.Approval{}, err
	}
	return builtin.Approval{ID: record.ID, Status: record.Status}, nil
}

// transitionTypedApproval runs 位于 the same 事务 as the human
// authorization fact. 旧版/non-等待 审批 rows 为 left unchanged.
// 一个 类型化的 审批 can therefore never be committed 不包含 either its exact
// CommitPatch 续接信息 或 一个 deterministic rejected 终止 状态.
func transitionTypedApproval(ctx context.Context, tx pgx.Tx, record approvalRecord, status string, decidedAt time.Time) error {
	var stepStatus, runStatus string
	var outputJSON json.RawMessage
	err := tx.QueryRow(ctx, `
		SELECT step.status, run.status, step.output_json
		FROM agent_steps AS step
		JOIN agent_runs AS run ON run.id = step.run_id
		WHERE step.id = $1 AND step.run_id = $2
		FOR UPDATE OF step, run
	`, record.StepID, record.RunID).Scan(&stepStatus, &runStatus, &outputJSON)
	if err != nil {
		return err
	}
	if stepStatus != "waiting_approval" && runStatus != "waiting_approval" {
		return nil
	}
	if stepStatus != "waiting_approval" || runStatus != "waiting_approval" {
		return fmt.Errorf("审批等待步骤/运行状态不匹配")
	}

	if status == "rejected" {
		errorJSON, _ := json.Marshal(map[string]any{
			"category": "policy_blocked", "message": "external approval was rejected", "approval_id": record.ID,
		})
		stepTag, err := tx.Exec(ctx, `
			UPDATE agent_steps
			SET status = 'failed', error_json = $2, completed_at = $3, updated_at = $3
			WHERE id = $1 AND status = 'waiting_approval'
		`, record.StepID, errorJSON, decidedAt)
		if err != nil {
			return err
		}
		if stepTag.RowsAffected() != 1 {
			return fmt.Errorf("审批等待步骤 transition conflict")
		}
		runTag, err := tx.Exec(ctx, `
			UPDATE agent_runs
			SET status = 'failed', current_step = NULL, updated_at = $2, version = version + 1
			WHERE id = $1 AND status = 'waiting_approval'
		`, record.RunID, decidedAt)
		if err != nil {
			return err
		}
		if runTag.RowsAffected() != 1 {
			return fmt.Errorf("审批等待运行 transition conflict")
		}
		return nil
	}

	waiting, err := orchestration.DecodeApprovalWaitOutput(outputJSON)
	if err != nil {
		return fmt.Errorf("解析类型化的审批续接信息：%w", err)
	}
	state := waiting.Continuation.State
	if waiting.ApprovalID != record.ID || state.Patch == nil || !state.Patch.Valid ||
		state.Patch.TargetIdempotencyKey != record.IdempotencyKey || !jsonEqual(state.Patch.PatchInput, waiting.Continuation.NodeInput) {
		return fmt.Errorf("类型化的审批续接信息为 not bound 用于 the approved 补丁")
	}
	continuationJSON, err := json.Marshal(waiting.Continuation)
	if err != nil {
		return fmt.Errorf("编码 approved 续接信息：%w", err)
	}
	stepKey := "commit_patch:approval:" + record.ID
	if _, err := tx.Exec(ctx, `
		INSERT INTO agent_steps (run_id, step_key, step_type, input_json, max_attempts)
		VALUES ($1, $2, 'CommitPatch', $3, 5)
		ON CONFLICT (run_id, step_key) DO NOTHING
	`, record.RunID, stepKey, continuationJSON); err != nil {
		return err
	}
	var persistedType string
	var persistedInput json.RawMessage
	if err := tx.QueryRow(ctx, `
		SELECT step_type, input_json FROM agent_steps WHERE run_id = $1 AND step_key = $2
	`, record.RunID, stepKey).Scan(&persistedType, &persistedInput); err != nil {
		return err
	}
	if persistedType != "CommitPatch" || !jsonEqual(persistedInput, continuationJSON) {
		return fmt.Errorf("处理失败：approved 续接信息 idempotency conflict")
	}
	stepTag, err := tx.Exec(ctx, `
		UPDATE agent_steps
		SET status = 'succeeded', completed_at = $2, updated_at = $2
		WHERE id = $1 AND status = 'waiting_approval'
	`, record.StepID, decidedAt)
	if err != nil {
		return err
	}
	if stepTag.RowsAffected() != 1 {
		return fmt.Errorf("审批等待步骤 transition conflict")
	}
	runTag, err := tx.Exec(ctx, `
		UPDATE agent_runs
		SET status = 'queued', current_step = $2, updated_at = $3, version = version + 1
		WHERE id = $1 AND status = 'waiting_approval'
	`, record.RunID, stepKey, decidedAt)
	if err != nil {
		return err
	}
	if runTag.RowsAffected() != 1 {
		return fmt.Errorf("审批等待运行 transition conflict")
	}
	return nil
}

// scanApproval 执行该函数负责的核心处理逻辑。
func scanApproval(row pgx.Row) (approvalRecord, error) {
	var value approvalRecord
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.RunID, &value.StepID, &value.ToolName,
		&value.ToolVersion, &value.IdempotencyKey, &value.ResourcesJSON, &value.ResourcesHash,
		&value.PayloadJSON, &value.Reason, &value.Status, &value.CreatedAt,
	)
	return value, err
}

// sameApproval 执行该函数负责的核心处理逻辑。
func sameApproval(record approvalRecord, input builtin.ApprovalInput, resources json.RawMessage, resourcesHash string, payload json.RawMessage) bool {
	return record.StepID == input.StepID && record.ToolName == input.ToolName && record.ToolVersion == input.ToolVersion &&
		record.ResourcesHash == resourcesHash && record.Reason == input.Reason &&
		jsonEqual(record.ResourcesJSON, resources) && jsonEqual(record.PayloadJSON, payload)
}

// roleAllows 执行该函数负责的核心处理逻辑。
func roleAllows(role, permission string) bool {
	permissions := map[string]map[string]struct{}{
		"viewer": {
			"document.read": {}, "retrieval.search": {}, "web.search": {}, "artifact.read": {},
		},
		"editor": {
			"document.read": {}, "document.write": {}, "retrieval.search": {}, "web.search": {},
			"artifact.read": {}, "artifact.write": {}, "workflow.request_approval": {},
		},
		"admin": {
			"document.read": {}, "document.write": {}, "retrieval.search": {}, "web.search": {},
			"artifact.read": {}, "artifact.write": {}, "workflow.request_approval": {}, "workflow.decide_approval": {},
		},
		"owner": {
			"document.read": {}, "document.write": {}, "retrieval.search": {}, "web.search": {},
			"artifact.read": {}, "artifact.write": {}, "workflow.request_approval": {}, "workflow.decide_approval": {},
		},
	}
	_, allowed := permissions[role][permission]
	return allowed
}

// validPrincipal 执行该函数负责的核心处理逻辑。
func validPrincipal(principal agenttools.SecurityContext) bool {
	return strings.TrimSpace(principal.PrincipalType) != "" && strings.TrimSpace(principal.PrincipalID) != "" && strings.TrimSpace(principal.WorkspaceID) != ""
}

// normalizeObject 执行该函数负责的核心处理逻辑。
func normalizeObject(raw json.RawMessage) (json.RawMessage, error) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, fmt.Errorf("审批负载必须是 JSON 对象")
	}
	return json.Marshal(value)
}

// jsonEqual 执行该函数负责的核心处理逻辑。
func jsonEqual(left, right json.RawMessage) bool {
	leftValue, err := decodeJSONNumber(left)
	if err != nil {
		return false
	}
	rightValue, err := decodeJSONNumber(right)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func decodeJSONNumber(raw json.RawMessage) (any, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
