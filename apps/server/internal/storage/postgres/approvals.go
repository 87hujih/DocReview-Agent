package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agent_project/apps/server/internal/task/models"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Approval 表示任务进入人工审批前生成的审批记录。
type Approval struct {
	ID            string
	TaskID        string
	BaseVersionID *string
	Status        string
	RejectReason  *string
	DecidedAt     *time.Time
	CreatedAt     time.Time
}

// ExecutionJob 表示审批通过后进入执行阶段的异步作业。
type ExecutionJob struct {
	ID            string
	TaskID        string
	ApprovalID    string
	BaseVersionID *string
	Status        string
	ErrorMessage  *string
	NewVersionID  *string
	StartedAt     *time.Time
	CompletedAt   *time.Time
	CreatedAt     time.Time
}

// ApprovalRepo 封装审批表的最小访问能力。
type ApprovalRepo struct {
	pool *pgxpool.Pool
}

// JobRepo 封装 execution_jobs 表的访问能力。
type JobRepo struct {
	pool *pgxpool.Pool
}

// ExecutionJobFinalizeHook 允许调用方在作业和任务终态更新事务内补写事件。
type ExecutionJobFinalizeHook func(ctx context.Context, tx pgx.Tx, task *Task, job *ExecutionJob, fromTaskStatus string) error

// NewApprovalRepo 使用连接池创建审批仓储。
func NewApprovalRepo(pool *pgxpool.Pool) *ApprovalRepo {
	return &ApprovalRepo{pool: pool}
}

// NewJobRepo 使用连接池创建执行作业仓储。
func NewJobRepo(pool *pgxpool.Pool) *JobRepo {
	return &JobRepo{pool: pool}
}

// Create 为任务创建一条 pending 审批记录。
func (r *ApprovalRepo) Create(ctx context.Context, taskID string, baseVersionID string) (*Approval, error) {
	trimmedBaseVersionID := strings.TrimSpace(baseVersionID)
	if trimmedBaseVersionID == "" {
		return nil, fmt.Errorf("审批记录缺少 base_version_id")
	}

	approval, err := scanApproval(r.pool.QueryRow(ctx, `
		INSERT INTO approvals (task_id, base_version_id)
		VALUES ($1, $2)
		RETURNING id, task_id, base_version_id, status, reject_reason, decided_at, created_at
	`, taskID, trimmedBaseVersionID))
	if err != nil {
		return nil, err
	}

	return &approval, nil
}

// GetByID 按主键读取审批记录，不存在时返回 nil。
func (r *ApprovalRepo) GetByID(ctx context.Context, id string) (*Approval, error) {
	approval, err := scanApproval(r.pool.QueryRow(ctx, `
		SELECT id, task_id, base_version_id, status, reject_reason, decided_at, created_at
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

// GetByIDForUpdateTx 在事务内按主键读取审批记录并加行锁，不存在时返回 nil。
func GetApprovalByIDForUpdateTx(ctx context.Context, tx pgx.Tx, id string) (*Approval, error) {
	approval, err := scanApproval(tx.QueryRow(ctx, `
		SELECT id, task_id, base_version_id, status, reject_reason, decided_at, created_at
		FROM approvals
		WHERE id = $1
		FOR UPDATE
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
		SELECT id, task_id, base_version_id, status, reject_reason, decided_at, created_at
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
			SELECT id, task_id, base_version_id, status, reject_reason, decided_at, created_at
			FROM approvals
			WHERE status = $1
			ORDER BY created_at DESC
		`, statusFilter)
	} else {
		rows, err = r.pool.Query(ctx, `
			SELECT id, task_id, base_version_id, status, reject_reason, decided_at, created_at
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
func (r *JobRepo) Create(ctx context.Context, taskID string, approvalID string, baseVersionID string) (*ExecutionJob, error) {
	trimmedBaseVersionID := strings.TrimSpace(baseVersionID)
	if trimmedBaseVersionID == "" {
		return nil, fmt.Errorf("执行作业缺少 base_version_id")
	}

	job, err := scanExecutionJob(r.pool.QueryRow(ctx, `
		INSERT INTO execution_jobs (task_id, approval_id, base_version_id)
		VALUES ($1, $2, $3)
		RETURNING id, task_id, approval_id, base_version_id, status, error_message, new_version_id, started_at, completed_at, created_at
	`, taskID, approvalID, trimmedBaseVersionID))
	if err != nil {
		return nil, err
	}

	return &job, nil
}

// GetByID 按主键读取执行作业，不存在时返回 nil。
func (r *JobRepo) GetByID(ctx context.Context, id string) (*ExecutionJob, error) {
	job, err := scanExecutionJob(r.pool.QueryRow(ctx, `
		SELECT id, task_id, approval_id, base_version_id, status, error_message, new_version_id, started_at, completed_at, created_at
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

// GetByApprovalID 按 approval_id 读取执行作业，不存在时返回 nil。
func (r *JobRepo) GetByApprovalID(ctx context.Context, approvalID string) (*ExecutionJob, error) {
	job, err := scanExecutionJob(r.pool.QueryRow(ctx, `
		SELECT id, task_id, approval_id, base_version_id, status, error_message, new_version_id, started_at, completed_at, created_at
		FROM execution_jobs
		WHERE approval_id = $1
	`, approvalID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &job, nil
}

func getExecutionJobByIDForUpdateTx(ctx context.Context, tx pgx.Tx, id string) (*ExecutionJob, error) {
	job, err := scanExecutionJob(tx.QueryRow(ctx, `
		SELECT id, task_id, approval_id, base_version_id, status, error_message, new_version_id, started_at, completed_at, created_at
		FROM execution_jobs
		WHERE id = $1
		FOR UPDATE
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &job, nil
}

// GetTaskByIDForUpdateTx 在事务内按主键读取任务并加行锁，不存在时返回 nil。
func GetTaskByIDForUpdateTx(ctx context.Context, tx pgx.Tx, id string) (*Task, error) {
	task, err := scanTask(tx.QueryRow(ctx, `
		SELECT id, resource_id, instruction, source_message_id, status, error_message, created_at, updated_at
		FROM tasks
		WHERE id = $1
		FOR UPDATE
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &task, nil
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
		RETURNING id, task_id, approval_id, base_version_id, status, error_message, new_version_id, started_at, completed_at, created_at
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

// FinalizeSuccess 在同一事务内将 execution_job 和 task 推进到成功终态。
func (r *JobRepo) FinalizeSuccess(
	ctx context.Context,
	jobID string,
	newVersionID string,
	hook ExecutionJobFinalizeHook,
) error {
	return r.finalize(ctx, jobID, "done", nil, &newVersionID, models.StatusCompleted, hook)
}

// FinalizeFailure 在同一事务内将 execution_job 和 task 推进到失败终态。
func (r *JobRepo) FinalizeFailure(
	ctx context.Context,
	jobID string,
	errorMessage string,
	hook ExecutionJobFinalizeHook,
) error {
	return r.finalize(ctx, jobID, "failed", &errorMessage, nil, models.StatusFailed, hook)
}

func scanApproval(row pgx.Row) (Approval, error) {
	var approval Approval

	err := row.Scan(
		&approval.ID,
		&approval.TaskID,
		&approval.BaseVersionID,
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
		&job.BaseVersionID,
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

// UpdateExecutionJobStatusTx 在事务内更新执行作业状态。
func UpdateExecutionJobStatusTx(
	ctx context.Context,
	tx pgx.Tx,
	id string,
	status string,
	errorMessage *string,
	newVersionID *string,
) error {
	_, err := tx.Exec(ctx, `
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

// UpdateApprovalStatusTx 在事务内更新审批状态。
func UpdateApprovalStatusTx(ctx context.Context, tx pgx.Tx, id string, status string, rejectReason *string) error {
	_, err := tx.Exec(ctx, `
		UPDATE approvals
		SET status = $2,
		    reject_reason = $3,
		    decided_at = now()
		WHERE id = $1
	`, id, status, rejectReason)
	return err
}

// UpdateApprovalStatusTxReturning 在事务内更新审批状态，并返回更新后的审批记录。
func UpdateApprovalStatusTxReturning(
	ctx context.Context,
	tx pgx.Tx,
	id string,
	status string,
	rejectReason *string,
) (*Approval, error) {
	approval, err := scanApproval(tx.QueryRow(ctx, `
		UPDATE approvals
		SET status = $2,
		    reject_reason = $3,
		    decided_at = now()
		WHERE id = $1
		RETURNING id, task_id, base_version_id, status, reject_reason, decided_at, created_at
	`, id, status, rejectReason))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &approval, nil
}

// CreateJobTx 在事务内创建执行作业。
func CreateJobTx(ctx context.Context, tx pgx.Tx, taskID string, approvalID string, baseVersionID string) (*ExecutionJob, error) {
	if strings.TrimSpace(baseVersionID) == "" {
		return nil, fmt.Errorf("执行作业缺少 base_version_id")
	}

	job, err := scanExecutionJob(tx.QueryRow(ctx, `
		INSERT INTO execution_jobs (task_id, approval_id, base_version_id)
		VALUES ($1, $2, $3)
		RETURNING id, task_id, approval_id, base_version_id, status, error_message, new_version_id, started_at, completed_at, created_at
	`, taskID, approvalID, baseVersionID))
	if err != nil {
		return nil, err
	}

	return &job, nil
}

func (r *JobRepo) finalize(
	ctx context.Context,
	jobID string,
	jobStatus string,
	errorMessage *string,
	newVersionID *string,
	taskStatus string,
	hook ExecutionJobFinalizeHook,
) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	job, err := getExecutionJobByIDForUpdateTx(ctx, tx, jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return fmt.Errorf("执行作业不存在")
	}
	if job.Status != "running" {
		return fmt.Errorf("执行作业状态不是 running（当前：%s）", job.Status)
	}

	task, err := GetTaskByIDForUpdateTx(ctx, tx, job.TaskID)
	if err != nil {
		return err
	}
	if task == nil {
		return fmt.Errorf("任务不存在")
	}

	fromTaskStatus := task.Status
	if err := models.Transition(task.Status, taskStatus); err != nil {
		return err
	}
	if err := UpdateExecutionJobStatusTx(ctx, tx, job.ID, jobStatus, errorMessage, newVersionID); err != nil {
		return err
	}
	if err := UpdateTaskStatusTx(ctx, tx, task.ID, taskStatus, errorMessage); err != nil {
		return err
	}

	job.Status = jobStatus
	job.ErrorMessage = errorMessage
	job.NewVersionID = newVersionID
	task.Status = taskStatus
	task.ErrorMessage = errorMessage
	if hook != nil {
		if err := hook(ctx, tx, task, job, fromTaskStatus); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// UpdateTaskStatusTx 在事务内更新任务状态。
func UpdateTaskStatusTx(ctx context.Context, tx pgx.Tx, id string, status string, errorMessage *string) error {
	_, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status = $2, error_message = $3, updated_at = now()
		WHERE id = $1
	`, id, status, errorMessage)
	return err
}

// ListPending 返回所有 pending 状态的执行作业，按创建时间正序。
func (r *JobRepo) ListPending(ctx context.Context) ([]ExecutionJob, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, approval_id, base_version_id, status, error_message, new_version_id, started_at, completed_at, created_at
		FROM execution_jobs
		WHERE status = 'pending'
		ORDER BY created_at
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]ExecutionJob, 0)
	for rows.Next() {
		job, err := scanExecutionJob(rows)
		if err != nil {
			return nil, err
		}

		jobs = append(jobs, job)
	}

	return jobs, rows.Err()
}

// CreateForTaskAwaitingApproval 在同一事务内原子地创建审批记录并将任务状态从
// drafting 切换为 awaiting_approval。若任务当前状态不是 drafting，则返回错误并
// 回滚，不创建审批记录。
func (r *ApprovalRepo) CreateForTaskAwaitingApproval(ctx context.Context, taskID string, baseVersionID string) (*Approval, error) {
	if strings.TrimSpace(baseVersionID) == "" {
		return nil, fmt.Errorf("审批记录缺少 base_version_id")
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var currentStatus string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM tasks WHERE id = $1 FOR UPDATE
	`, taskID).Scan(&currentStatus); err != nil {
		return nil, fmt.Errorf("读取任务状态失败：%w", err)
	}

	if currentStatus != "drafting" {
		return nil, fmt.Errorf("任务状态不是 drafting（当前：%s），无法创建审批记录", currentStatus)
	}

	approval, err := scanApproval(tx.QueryRow(ctx, `
		INSERT INTO approvals (task_id, base_version_id)
		VALUES ($1, $2)
		RETURNING id, task_id, base_version_id, status, reject_reason, decided_at, created_at
	`, taskID, baseVersionID))
	if err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE tasks SET status = 'awaiting_approval', updated_at = now() WHERE id = $1
	`, taskID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &approval, nil
}
