package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Approval 表示任务进入人工审批前生成的审批记录。
type Approval struct {
	ID           string
	TaskID       string
	Status       string
	RejectReason *string
	DecidedAt    *time.Time
	CreatedAt    time.Time
}

// ApprovalRepo 封装审批表的最小访问能力。
type ApprovalRepo struct {
	pool *pgxpool.Pool
}

// NewApprovalRepo 使用连接池创建审批仓储。
func NewApprovalRepo(pool *pgxpool.Pool) *ApprovalRepo {
	return &ApprovalRepo{pool: pool}
}

// Create 为任务创建一条 pending 审批记录。
func (r *ApprovalRepo) Create(ctx context.Context, taskID string) (*Approval, error) {
	approval, err := scanApproval(r.pool.QueryRow(ctx, `
		INSERT INTO approvals (task_id)
		VALUES ($1)
		RETURNING id, task_id, status, reject_reason, decided_at, created_at
	`, taskID))
	if err != nil {
		return nil, err
	}

	return &approval, nil
}

func scanApproval(row pgx.Row) (Approval, error) {
	var approval Approval

	err := row.Scan(
		&approval.ID,
		&approval.TaskID,
		&approval.Status,
		&approval.RejectReason,
		&approval.DecidedAt,
		&approval.CreatedAt,
	)
	if err != nil {
		return Approval{}, err
	}

	return approval, nil
}
