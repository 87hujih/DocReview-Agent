package agentprojection

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/agent/projection"
	"agent_project/apps/server/internal/storage/postgres/outbox"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const loadStepSnapshotSQL = `
SELECT run.turn_id, run.id, step.step_type,
       COALESCE(step.output_json, '{}'::jsonb), step.error_json
FROM agent_runs AS run
JOIN agent_steps AS step ON step.run_id = run.id
WHERE run.id = $1 AND step.id = $2
  AND run.runtime_mode = 'durable'
  AND run.turn_id IS NOT NULL`

const loadRejectedApprovalSnapshotSQL = `
SELECT run.turn_id, run.id, run.status, step.step_type,
       COALESCE(step.output_json, '{}'::jsonb), step.error_json
FROM agent_runs AS run
JOIN agent_tool_approvals AS approval ON approval.run_id = run.id
JOIN agent_steps AS step ON step.id = approval.step_id AND step.run_id = run.id
WHERE run.id = $1 AND approval.id = $2 AND approval.status = 'rejected'
  AND run.runtime_mode = 'durable'
  AND run.turn_id IS NOT NULL`

type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 校验依赖并创建对应实例。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// 加载 按作用域读取并返回所需数据。
func (repository *Repository) Load(ctx context.Context, event outbox.Event) (projection.RuntimeSnapshot, error) {
	if _, err := uuid.Parse(strings.TrimSpace(event.ID)); err != nil {
		return projection.RuntimeSnapshot{}, fmt.Errorf("projection event ID must be a UUID")
	}
	var payload struct {
		RunID      string `json:"run_id"`
		StepID     string `json:"step_id"`
		ApprovalID string `json:"approval_id"`
		RunStatus  string `json:"run_status"`
	}
	if err := json.Unmarshal(event.PayloadJSON, &payload); err != nil {
		return projection.RuntimeSnapshot{}, fmt.Errorf("投影事件负载无效")
	}
	if _, err := uuid.Parse(strings.TrimSpace(payload.RunID)); err != nil {
		return projection.RuntimeSnapshot{}, fmt.Errorf("投影事件 run_id 必须为一个 UUID")
	}
	query := ""
	targetID := ""
	// 根据当前状态或类型选择对应的处理分支。
	switch event.EventType {
	case "agent.step.outcome_committed":
		query = loadStepSnapshotSQL
		targetID = strings.TrimSpace(payload.StepID)
	case "agent.tool_approval.rejected":
		query = loadRejectedApprovalSnapshotSQL
		targetID = strings.TrimSpace(payload.ApprovalID)
	default:
		return projection.RuntimeSnapshot{}, fmt.Errorf("不支持的运行时投影事件 %q", event.EventType)
	}
	if _, err := uuid.Parse(targetID); err != nil {
		return projection.RuntimeSnapshot{}, fmt.Errorf("投影事件目标 ID 必须为一个 UUID")
	}
	if repository == nil || repository.pool == nil {
		return projection.RuntimeSnapshot{}, fmt.Errorf("运行时投影数据库不能为空")
	}
	var snapshot projection.RuntimeSnapshot
	var err error
	row := repository.pool.QueryRow(ctx, query, payload.RunID, targetID)
	if event.EventType == "agent.step.outcome_committed" {
		snapshot.RunStatus = strings.TrimSpace(payload.RunStatus)
		if !validRunStatus(snapshot.RunStatus) {
			return projection.RuntimeSnapshot{}, fmt.Errorf("投影事件运行_status 无效")
		}
		err = row.Scan(&snapshot.TurnID, &snapshot.RunID, &snapshot.StepType, &snapshot.OutputJSON, &snapshot.ErrorJSON)
	} else {
		err = row.Scan(&snapshot.TurnID, &snapshot.RunID, &snapshot.RunStatus, &snapshot.StepType, &snapshot.OutputJSON, &snapshot.ErrorJSON)
	}
	if err == pgx.ErrNoRows {
		return projection.RuntimeSnapshot{}, fmt.Errorf("可信的持久化的运行时投影快照未找到")
	}
	return snapshot, err
}

// validRunStatus 执行该函数负责的核心处理逻辑。
func validRunStatus(status string) bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch status {
	case "queued", "running", "waiting_input", "waiting_approval", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

// Exists 执行该函数负责的核心处理逻辑。
func (repository *Repository) Exists(ctx context.Context, eventID, projectionName string) (bool, error) {
	if err := validateReceipt(eventID, projectionName, "sha256:"+strings.Repeat("0", 64)); err != nil {
		return false, err
	}
	if repository == nil || repository.pool == nil {
		return false, fmt.Errorf("投影回执数据库不能为空")
	}
	var exists bool
	err := repository.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM outbox_projection_receipts
			WHERE event_id = $1 AND projection_name = $2
		)
	`, eventID, strings.TrimSpace(projectionName)).Scan(&exists)
	return exists, err
}

// 记录 按领域约束持久化数据。
func (repository *Repository) Record(ctx context.Context, eventID, projectionName, payloadHash string) error {
	if err := validateReceipt(eventID, projectionName, payloadHash); err != nil {
		return err
	}
	if repository == nil || repository.pool == nil {
		return fmt.Errorf("投影回执数据库不能为空")
	}
	tag, err := repository.pool.Exec(ctx, `
		INSERT INTO outbox_projection_receipts (event_id, projection_name, payload_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (event_id, projection_name) DO NOTHING
	`, eventID, strings.TrimSpace(projectionName), payloadHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var persisted string
	if err := repository.pool.QueryRow(ctx, `
		SELECT payload_hash FROM outbox_projection_receipts
		WHERE event_id = $1 AND projection_name = $2
	`, eventID, strings.TrimSpace(projectionName)).Scan(&persisted); err != nil {
		return err
	}
	if persisted != payloadHash {
		return fmt.Errorf("投影回执 idempotency conflict")
	}
	return nil
}

// validateReceipt 校验输入及领域约束。
func validateReceipt(eventID, projectionName, payloadHash string) error {
	if _, err := uuid.Parse(strings.TrimSpace(eventID)); err != nil || strings.TrimSpace(projectionName) == "" {
		return fmt.Errorf("projection receipt event and name are invalid")
	}
	if len(payloadHash) != 71 || !strings.HasPrefix(payloadHash, "sha256:") {
		return fmt.Errorf("投影回执负载哈希无效")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(payloadHash, "sha256:")); err != nil {
		return fmt.Errorf("投影回执负载哈希无效")
	}
	return nil
}

var _ projection.RuntimeSnapshotReader = (*Repository)(nil)
var _ projection.ReceiptStore = (*Repository)(nil)
