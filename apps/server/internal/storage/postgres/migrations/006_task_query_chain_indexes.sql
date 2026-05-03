CREATE INDEX IF NOT EXISTS idx_tasks_created_at_id
ON tasks (created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_task_steps_task_created_id
ON task_steps (task_id, created_at, id);

CREATE INDEX IF NOT EXISTS idx_task_artifacts_task_created_id
ON task_artifacts (task_id, created_at, id);
