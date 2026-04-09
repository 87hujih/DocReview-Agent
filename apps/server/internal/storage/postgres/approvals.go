package postgres

import (
	"context"
	"errors"
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

// ExecutionJob 表示审批通过后进入执行阶段的异步作业。
type ExecutionJob struct {
	ID           string
	TaskID       string
	ApprovalID   string
	Status       string
	ErrorMessage *string
	NewVersionID *string
	StartedAt    *time.Time
	CompletedAt  *time.Time
	CreatedAt    time.Time
}

// ApprovalRepo 封装审批表的最小访问能力。
type ApprovalRepo struct {
	pool *pgxpool.Pool
}

// JobRepo 封装 execution_jobs 表的访问能力。
type JobRepo struct {
	pool *pgxpool.Pool
}

// NewApprovalRepo 使用连接池创建审批仓储。
func NewApprovalRepo(pool *pgxpool.Pool) *ApprovalRepo {
	return &ApprovalRepo{pool: pool}
}

// NewJobRepo 使用连接池创建执行作业仓储。
func NewJobRepo(pool *pgxpool.Pool) *JobRepo {
	return &JobRepo{pool: pool}
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

// GetByID 按主键读取审批记录，不存在时返回 nil。
func (r *ApprovalRepo) GetByID(ctx context.Context, id string) (*Approval, error) {
	approval, err := scanApproval(r.pool.QueryRow(ctx, `
		SELECT id, task_id, status, reject_reason, decided_at, created_at
		FROM approvals
		WHERE id = $1
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &approval, nil
}

// GetByTaskID 按任务 ID 读取审批记录，不存在时返回 nil。
func (r *ApprovalRepo) GetByTaskID(ctx context.Context, taskID string) (*Approval, error) {
	approval, err := scanApproval(r.pool.QueryRow(ctx, `
		SELECT id, task_id, status, reject_reason, decided_at, created_at
		FROM approvals
		WHERE task_id = $1
	`, taskID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &approval, nil
}

// List 按创建时间倒序返回审批记录，可按状态过滤。
func (r *ApprovalRepo) List(ctx context.Context, statusFilter string) ([]Approval, error) {
	var (
		rows pgx.Rows
		err  error
	)

	if statusFilter != "" {
		rows, err = r.pool.Query(ctx, `
			SELECT id, task_id, status, reject_reason, decided_at, created_at
			FROM approvals
			WHERE status = $1
			ORDER BY created_at DESC
		`, statusFilter)
	} else {
		rows, err = r.pool.Query(ctx, `
			SELECT id, task_id, status, reject_reason, decided_at, created_at
			FROM approvals
			ORDER BY created_at DESC
		`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var approvals []Approval
	for rows.Next() {
		approval, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}

		approvals = append(approvals, approval)
	}

	return approvals, rows.Err()
}

// UpdateStatus 更新审批状态，并记录决策时间。
func (r *ApprovalRepo) UpdateStatus(ctx context.Context, id string, status string, rejectReason *string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE approvals
		SET status = $2,
		    reject_reason = $3,
		    decided_at = now()
		WHERE id = $1
	`, id, status, rejectReason)
	return err
}

// Create 写入一条 pending execution job。
func (r *JobRepo) Create(ctx context.Context, taskID string, approvalID string) (*ExecutionJob, error) {
	job, err := scanExecutionJob(r.pool.QueryRow(ctx, `
		INSERT INTO execution_jobs (task_id, approval_id)
		VALUES ($1, $2)
		RETURNING id, task_id, approval_id, status, error_message, new_version_id, started_at, completed_at, created_at
	`, taskID, approvalID))
	if err != nil {
		return nil, err
	}

	return &job, nil
}

// GetByID 按主键读取执行作业，不存在时返回 nil。
func (r *JobRepo) GetByID(ctx context.Context, id string) (*ExecutionJob, error) {
	job, err := scanExecutionJob(r.pool.QueryRow(ctx, `
		SELECT id, task_id, approval_id, status, error_message, new_version_id, started_at, completed_at, created_at
		FROM execution_jobs
		WHERE id = $1
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &job, nil
}

// ClaimNext 抢占最早的 pending job 并切换为 running；没有待处理作业时返回 nil。
func (r *JobRepo) ClaimNext(ctx context.Context) (*ExecutionJob, error) {
	job, err := scanExecutionJob(r.pool.QueryRow(ctx, `
		UPDATE execution_jobs
		SET status = 'running', started_at = now()
		WHERE id = (
			SELECT id
			FROM execution_jobs
			WHERE status = 'pending'
			ORDER BY created_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, task_id, approval_id, status, error_message, new_version_id, started_at, completed_at, created_at
	`))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &job, nil
}

// UpdateStatus 更新执行作业状态；完成或失败时写入 completed_at。
func (r *JobRepo) UpdateStatus(ctx context.Context, id string, status string, errorMessage *string, newVersionID *string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE execution_jobs
		SET status = $2,
		    error_message = $3,
		    new_version_id = $4,
		    completed_at = CASE
		        WHEN $2 IN ('done', 'failed') THEN now()
		        ELSE NULL
		    END
		WHERE id = $1
	`, id, status, errorMessage, newVersionID)
	return err
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

func scanExecutionJob(row pgx.Row) (ExecutionJob, error) {
	var job ExecutionJob

	err := row.Scan(
		&job.ID,
		&job.TaskID,
		&job.ApprovalID,
		&job.Status,
		&job.ErrorMessage,
		&job.NewVersionID,
		&job.StartedAt,
		&job.CompletedAt,
		&job.CreatedAt,
	)
	if err != nil {
		return ExecutionJob{}, err
	}

	return job, nil
}
