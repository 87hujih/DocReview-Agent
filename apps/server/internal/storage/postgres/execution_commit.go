package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/task/models"

	"github.com/jackc/pgx/v5"
)

// CommitPreparedExecutionParams 描述一次 prepared execution 提交所需的输入。
type CommitPreparedExecutionParams struct {
	JobID         string
	BaseVersionID string
	NewContent    string
	Chunks        []ResourceChunkInput
}

// CommitPreparedExecutionResult 描述事务型提交后的最终结果。
type CommitPreparedExecutionResult struct {
	JobStatus      string
	NewVersionID   *string
	FailureMessage *string
}

// CommitPreparedExecution 在单事务里提交 prepared execution；若发现当前版本已漂移，则 fail closed。
func (r *JobRepo) CommitPreparedExecution(
	ctx context.Context,
	params CommitPreparedExecutionParams,
	hook ExecutionJobFinalizeHook,
) (*CommitPreparedExecutionResult, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	job, err := getExecutionJobByIDForUpdateTx(ctx, tx, params.JobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf("执行作业不存在")
	}
	if job.Status != "running" {
		return nil, fmt.Errorf("执行作业状态不是 running（当前：%s）", job.Status)
	}

	task, err := GetTaskByIDForUpdateTx(ctx, tx, job.TaskID)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, fmt.Errorf("任务不存在")
	}
	resource, err := lockResourceForExecutionTx(ctx, tx, task.ResourceID)
	if err != nil {
		return nil, err
	}
	if resource == nil {
		return nil, fmt.Errorf("资源不存在")
	}

	currentVersion, err := getCurrentVersionForUpdateTx(ctx, tx, task.ResourceID)
	if err != nil {
		return nil, err
	}
	if currentVersion == nil {
		return nil, fmt.Errorf("资源当前版本不存在")
	}

	fromTaskStatus := task.Status
	if currentVersion.ID != strings.TrimSpace(params.BaseVersionID) {
		return finalizePreparedExecutionConflict(ctx, tx, task, job, fromTaskStatus, params.BaseVersionID, currentVersion.ID, hook)
	}

	if err := models.Transition(task.Status, models.StatusCompleted); err != nil {
		return nil, err
	}

	newVersion, err := scanResourceVersion(tx.QueryRow(ctx, `
		INSERT INTO resource_versions (resource_id, version_number, content, source)
		VALUES ($1, $2, $3, $4)
		RETURNING id, resource_id, version_number, content, source, created_at
	`, task.ResourceID, currentVersion.VersionNumber+1, params.NewContent, "agent_edit"))
	if err != nil {
		return nil, err
	}

	for _, chunk := range params.Chunks {
		if err := createChunkTx(ctx, tx, &ResourceChunk{
			ResourceID:   task.ResourceID,
			VersionID:    newVersion.ID,
			ChunkIndex:   chunk.ChunkIndex,
			SectionTitle: chunk.SectionTitle,
			Content:      chunk.Content,
			Embedding:    chunk.Embedding,
		}); err != nil {
			return nil, err
		}
	}

	if err := UpdateExecutionJobStatusTx(ctx, tx, job.ID, "done", nil, &newVersion.ID); err != nil {
		return nil, err
	}
	if err := UpdateTaskStatusTx(ctx, tx, task.ID, models.StatusCompleted, nil); err != nil {
		return nil, err
	}

	job.Status = "done"
	job.ErrorMessage = nil
	job.NewVersionID = &newVersion.ID
	task.Status = models.StatusCompleted
	task.ErrorMessage = nil

	if hook != nil {
		if err := hook(ctx, tx, task, job, fromTaskStatus); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &CommitPreparedExecutionResult{
		JobStatus:    "done",
		NewVersionID: &newVersion.ID,
	}, nil
}

// finalizePreparedExecutionConflict 收敛 `Prepared执行Conflict` 的最终结果，统一尾部状态和错误处理。
func finalizePreparedExecutionConflict(
	ctx context.Context,
	tx pgx.Tx,
	task *Task,
	job *ExecutionJob,
	fromTaskStatus string,
	expectedBaseVersionID string,
	currentVersionID string,
	hook ExecutionJobFinalizeHook,
) (*CommitPreparedExecutionResult, error) {
	if err := models.Transition(task.Status, models.StatusFailed); err != nil {
		return nil, err
	}

	errorMessage := fmt.Sprintf(
		"base version 已漂移：expected %s, got %s",
		strings.TrimSpace(expectedBaseVersionID),
		strings.TrimSpace(currentVersionID),
	)
	if err := UpdateExecutionJobStatusTx(ctx, tx, job.ID, "failed", &errorMessage, nil); err != nil {
		return nil, err
	}
	if err := UpdateTaskStatusTx(ctx, tx, task.ID, models.StatusFailed, &errorMessage); err != nil {
		return nil, err
	}

	job.Status = "failed"
	job.ErrorMessage = &errorMessage
	job.NewVersionID = nil
	task.Status = models.StatusFailed
	task.ErrorMessage = &errorMessage

	if hook != nil {
		if err := hook(ctx, tx, task, job, fromTaskStatus); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &CommitPreparedExecutionResult{
		JobStatus:      "failed",
		FailureMessage: &errorMessage,
	}, nil
}

// lockResourceForExecutionTx 在事务内锁定 `执行事务的资源`，避免并发更新产生竞争。
func lockResourceForExecutionTx(ctx context.Context, tx pgx.Tx, resourceID string) (*Resource, error) {
	resource, err := scanResource(tx.QueryRow(ctx, `
		SELECT id, title, source_type, source_ref, created_at, updated_at
		FROM resources
		WHERE id = $1
		FOR UPDATE
	`, resourceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &resource, nil
}

// getCurrentVersionForUpdateTx 在事务内读取资源当前版本并加锁，确保提交执行结果时看到一致的基线版本。
func getCurrentVersionForUpdateTx(ctx context.Context, tx pgx.Tx, resourceID string) (*ResourceVersion, error) {
	version, err := scanResourceVersion(tx.QueryRow(ctx, `
		SELECT id, resource_id, version_number, content, source, created_at
		FROM resource_versions
		WHERE resource_id = $1
		ORDER BY version_number DESC
		LIMIT 1
		FOR UPDATE
	`, resourceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &version, nil
}
