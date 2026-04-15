ALTER TABLE approvals
ADD COLUMN IF NOT EXISTS base_version_id UUID REFERENCES resource_versions(id);

ALTER TABLE execution_jobs
ADD COLUMN IF NOT EXISTS base_version_id UUID REFERENCES resource_versions(id);

CREATE INDEX IF NOT EXISTS idx_approvals_base_version_id
    ON approvals(base_version_id);

CREATE INDEX IF NOT EXISTS idx_execution_jobs_base_version_id
    ON execution_jobs(base_version_id);
