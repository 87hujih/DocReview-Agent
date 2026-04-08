package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Task 表示任务主记录。
type Task struct {
	ID           string
	ResourceID   string
	Instruction  string
	Status       string
	ErrorMessage *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TaskStep 表示 orchestrator 执行中的单个步骤记录。
type TaskStep struct {
	ID           string
	TaskID       string
	StepName     string
	Status       string
	ErrorMessage *string
	StartedAt    *time.Time
	CompletedAt  *time.Time
	CreatedAt    time.Time
}

// TaskArtifact 表示任务执行产物。
type TaskArtifact struct {
	ID           string
	TaskID       string
	ArtifactType string
	Content      []byte
	CreatedAt    time.Time
}

// TaskRepo 封装任务、步骤和产物三张表的访问。
type TaskRepo struct {
	pool *pgxpool.Pool
}

// NewTaskRepo 使用连接池创建任务仓储。
func NewTaskRepo(pool *pgxpool.Pool) *TaskRepo {
	return &TaskRepo{pool: pool}
}

// Create 插入一条 pending 任务记录。
func (r *TaskRepo) Create(ctx context.Context, resourceID string, instruction string) (*Task, error) {
	task, err := scanTask(r.pool.QueryRow(ctx, `
		INSERT INTO tasks (resource_id, instruction)
		VALUES ($1, $2)
		RETURNING id, resource_id, instruction, status, error_message, created_at, updated_at
	`, resourceID, instruction))
	if err != nil {
		return nil, err
	}

	return &task, nil
}

// GetByID 在任务不存在时返回 nil。
func (r *TaskRepo) GetByID(ctx context.Context, id string) (*Task, error) {
	task, err := scanTask(r.pool.QueryRow(ctx, `
		SELECT id, resource_id, instruction, status, error_message, created_at, updated_at
		FROM tasks
		WHERE id = $1
	`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	return &task, nil
}

// List 按创建时间倒序返回任务列表。
func (r *TaskRepo) List(ctx context.Context) ([]Task, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, resource_id, instruction, status, error_message, created_at, updated_at
		FROM tasks
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}

		tasks = append(tasks, task)
	}

	return tasks, rows.Err()
}

// UpdateStatus 更新任务状态、错误信息和更新时间。
func (r *TaskRepo) UpdateStatus(ctx context.Context, id string, status string, errorMessage *string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE tasks
		SET status = $2, error_message = $3, updated_at = now()
		WHERE id = $1
	`, id, status, errorMessage)
	return err
}

// AddStep 创建一条 running 状态的步骤记录，并写入 started_at。
func (r *TaskRepo) AddStep(ctx context.Context, taskID string, stepName string) (*TaskStep, error) {
	step, err := scanTaskStep(r.pool.QueryRow(ctx, `
		INSERT INTO task_steps (task_id, step_name, status, started_at)
		VALUES ($1, $2, 'running', now())
		RETURNING id, task_id, step_name, status, error_message, started_at, completed_at, created_at
	`, taskID, stepName))
	if err != nil {
		return nil, err
	}

	return &step, nil
}

// UpdateStep 更新步骤状态和错误信息；完成或失败时记录 completed_at。
func (r *TaskRepo) UpdateStep(ctx context.Context, stepID string, status string, errorMessage *string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE task_steps
		SET status = $2,
		    error_message = $3,
		    completed_at = CASE
		        WHEN $2 IN ('completed', 'failed') THEN now()
		        ELSE NULL
		    END
		WHERE id = $1
	`, stepID, status, errorMessage)
	return err
}

// GetSteps 按创建时间正序返回任务步骤。
func (r *TaskRepo) GetSteps(ctx context.Context, taskID string) ([]TaskStep, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, step_name, status, error_message, started_at, completed_at, created_at
		FROM task_steps
		WHERE task_id = $1
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []TaskStep
	for rows.Next() {
		step, err := scanTaskStep(rows)
		if err != nil {
			return nil, err
		}

		steps = append(steps, step)
	}

	return steps, rows.Err()
}

// AddArtifact 写入任务产物 JSONB。
func (r *TaskRepo) AddArtifact(ctx context.Context, taskID string, artifactType string, content []byte) (*TaskArtifact, error) {
	artifact, err := scanTaskArtifact(r.pool.QueryRow(ctx, `
		INSERT INTO task_artifacts (task_id, artifact_type, content)
		VALUES ($1, $2, $3::jsonb)
		RETURNING id, task_id, artifact_type, content, created_at
	`, taskID, artifactType, string(content)))
	if err != nil {
		return nil, err
	}

	return &artifact, nil
}

// GetArtifacts 按创建时间正序返回任务产物。
func (r *TaskRepo) GetArtifacts(ctx context.Context, taskID string) ([]TaskArtifact, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, task_id, artifact_type, content, created_at
		FROM task_artifacts
		WHERE task_id = $1
		ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []TaskArtifact
	for rows.Next() {
		artifact, err := scanTaskArtifact(rows)
		if err != nil {
			return nil, err
		}

		artifacts = append(artifacts, artifact)
	}

	return artifacts, rows.Err()
}

func scanTask(row pgx.Row) (Task, error) {
	var task Task

	err := row.Scan(
		&task.ID,
		&task.ResourceID,
		&task.Instruction,
		&task.Status,
		&task.ErrorMessage,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
	if err != nil {
		return Task{}, err
	}

	return task, nil
}

func scanTaskStep(row pgx.Row) (TaskStep, error) {
	var step TaskStep

	err := row.Scan(
		&step.ID,
		&step.TaskID,
		&step.StepName,
		&step.Status,
		&step.ErrorMessage,
		&step.StartedAt,
		&step.CompletedAt,
		&step.CreatedAt,
	)
	if err != nil {
		return TaskStep{}, err
	}

	return step, nil
}

func scanTaskArtifact(row pgx.Row) (TaskArtifact, error) {
	var artifact TaskArtifact

	err := row.Scan(
		&artifact.ID,
		&artifact.TaskID,
		&artifact.ArtifactType,
		&artifact.Content,
		&artifact.CreatedAt,
	)
	if err != nil {
		return TaskArtifact{}, err
	}

	return artifact, nil
}
