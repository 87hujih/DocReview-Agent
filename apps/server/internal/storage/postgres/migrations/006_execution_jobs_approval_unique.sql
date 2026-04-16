CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_jobs_approval_id_unique
ON execution_jobs (approval_id);
