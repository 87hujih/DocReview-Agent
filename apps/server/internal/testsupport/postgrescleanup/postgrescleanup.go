package postgrescleanup

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// CleanupResourceTree 在单事务里按固定顺序删除资源树，避免测试清理时出现锁顺序漂移。
func CleanupResourceTree(ctx context.Context, pool txBeginner, resourceID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := CleanupResourceTreeTx(ctx, tx, resourceID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// CleanupResourceTreeTx 在已有事务内删除资源关联的任务、审批、执行作业与资源自身。
func CleanupResourceTreeTx(ctx context.Context, tx pgx.Tx, resourceID string) error {
	if _, err := tx.Exec(ctx, `
		DELETE FROM execution_jobs
		WHERE task_id IN (SELECT id FROM tasks WHERE resource_id = $1)
		   OR new_version_id IN (SELECT id FROM resource_versions WHERE resource_id = $1)
	`, resourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM approvals
		WHERE task_id IN (SELECT id FROM tasks WHERE resource_id = $1)
	`, resourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM task_events
		WHERE task_id IN (SELECT id FROM tasks WHERE resource_id = $1)
	`, resourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM task_artifacts
		WHERE task_id IN (SELECT id FROM tasks WHERE resource_id = $1)
	`, resourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM task_steps
		WHERE task_id IN (SELECT id FROM tasks WHERE resource_id = $1)
	`, resourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM tasks WHERE resource_id = $1`, resourceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM resources WHERE id = $1`, resourceID); err != nil {
		return err
	}

	return nil
}
