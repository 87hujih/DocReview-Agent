-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- ============================================================
-- resources：文档资源
-- ============================================================
CREATE TABLE IF NOT EXISTS resources (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title       TEXT        NOT NULL,
    source_type TEXT        NOT NULL DEFAULT 'upload',
    source_ref  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- resource_versions：文档版本
-- ============================================================
CREATE TABLE IF NOT EXISTS resource_versions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id    UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version_number INT         NOT NULL DEFAULT 1,
    content        TEXT        NOT NULL,
    source         TEXT        NOT NULL DEFAULT 'original',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE(resource_id, version_number)
);

-- ============================================================
-- resource_chunks：文档切片（含向量）
-- ============================================================
CREATE TABLE IF NOT EXISTS resource_chunks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id   UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version_id    UUID        NOT NULL REFERENCES resource_versions(id) ON DELETE CASCADE,
    chunk_index   INT         NOT NULL,
    section_title TEXT        NOT NULL DEFAULT '',
    content       TEXT        NOT NULL,
    embedding     vector(1024),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- tasks：任务
-- ============================================================
CREATE TABLE IF NOT EXISTS tasks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id   UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    instruction   TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'pending',
    error_message TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- task_steps：任务步骤
-- ============================================================
CREATE TABLE IF NOT EXISTS task_steps (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    step_name     TEXT        NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'pending',
    error_message TEXT,
    started_at    TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- task_artifacts：任务产物
-- ============================================================
CREATE TABLE IF NOT EXISTS task_artifacts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    artifact_type TEXT        NOT NULL,
    content       JSONB       NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- approvals：审批
-- ============================================================
CREATE TABLE IF NOT EXISTS approvals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       UUID        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE UNIQUE,
    status        TEXT        NOT NULL DEFAULT 'pending',
    reject_reason TEXT,
    decided_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ============================================================
-- execution_jobs：执行作业
-- ============================================================
CREATE TABLE IF NOT EXISTS execution_jobs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id        UUID        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    approval_id    UUID        NOT NULL REFERENCES approvals(id) ON DELETE CASCADE,
    status         TEXT        NOT NULL DEFAULT 'pending',
    error_message  TEXT,
    new_version_id UUID        REFERENCES resource_versions(id),
    started_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
