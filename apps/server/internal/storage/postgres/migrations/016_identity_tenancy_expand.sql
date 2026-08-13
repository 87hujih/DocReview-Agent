-- 阶段 1 仅扩展租户字段。现有数据暂不绑定作用域，后续需经单独审批后再执行双写与回填。
CREATE TABLE IF NOT EXISTS users (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    external_issuer  TEXT,
    external_subject TEXT,
    email            TEXT,
    display_name     TEXT        NOT NULL DEFAULT '',
    status           TEXT        NOT NULL DEFAULT 'active',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (status IN ('active', 'disabled')),
    CHECK (
        (external_issuer IS NULL AND external_subject IS NULL)
        OR (external_issuer IS NOT NULL AND external_subject IS NOT NULL)
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_external_identity
ON users (external_issuer, external_subject)
WHERE external_issuer IS NOT NULL AND external_subject IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_users_email
ON users (email)
WHERE email IS NOT NULL;

CREATE TABLE IF NOT EXISTS organizations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       TEXT        NOT NULL UNIQUE,
    name       TEXT        NOT NULL,
    status     TEXT        NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (status IN ('active', 'disabled'))
);

CREATE TABLE IF NOT EXISTS workspaces (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    slug            TEXT        NOT NULL,
    name            TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'active',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (organization_id, slug),
    CHECK (status IN ('active', 'disabled'))
);

CREATE INDEX IF NOT EXISTS idx_workspaces_organization
ON workspaces (organization_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS memberships (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    role         TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'active',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (workspace_id, user_id),
    CHECK (role IN ('owner', 'admin', 'editor', 'viewer')),
    CHECK (status IN ('active', 'disabled'))
);

CREATE INDEX IF NOT EXISTS idx_memberships_user
ON memberships (user_id, status, workspace_id);

CREATE TABLE IF NOT EXISTS principal_audit_events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID REFERENCES workspaces(id) ON DELETE RESTRICT,
    principal_type TEXT        NOT NULL,
    principal_id   TEXT        NOT NULL,
    action         TEXT        NOT NULL,
    resource_type  TEXT,
    resource_id    TEXT,
    decision       TEXT        NOT NULL,
    reason_code    TEXT,
    request_id     TEXT,
    metadata       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (decision IN ('observe', 'allow', 'deny', 'error'))
);

CREATE INDEX IF NOT EXISTS idx_principal_audit_workspace_created
ON principal_audit_events (workspace_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_principal_audit_principal_created
ON principal_audit_events (principal_type, principal_id, created_at DESC, id DESC);

ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS created_by_principal_type TEXT,
    ADD COLUMN IF NOT EXISTS created_by_principal_id TEXT;

ALTER TABLE tasks
    ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS requested_by_principal_type TEXT,
    ADD COLUMN IF NOT EXISTS requested_by_principal_id TEXT;

ALTER TABLE approvals
    ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS decided_by_principal_type TEXT,
    ADD COLUMN IF NOT EXISTS decided_by_principal_id TEXT;

ALTER TABLE execution_jobs
    ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS initiated_by_principal_type TEXT,
    ADD COLUMN IF NOT EXISTS initiated_by_principal_id TEXT;

ALTER TABLE assistant_sessions
    ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS created_by_principal_type TEXT,
    ADD COLUMN IF NOT EXISTS created_by_principal_id TEXT;

ALTER TABLE uploaded_files
    ADD COLUMN IF NOT EXISTS workspace_id UUID REFERENCES workspaces(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS created_by_principal_type TEXT,
    ADD COLUMN IF NOT EXISTS created_by_principal_id TEXT;

CREATE INDEX IF NOT EXISTS idx_resources_workspace_created
ON resources (workspace_id, created_at DESC, id DESC)
WHERE workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_tasks_workspace_created
ON tasks (workspace_id, created_at DESC, id DESC)
WHERE workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_approvals_workspace_created
ON approvals (workspace_id, created_at DESC, id DESC)
WHERE workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_execution_jobs_workspace_created
ON execution_jobs (workspace_id, created_at DESC, id DESC)
WHERE workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_assistant_sessions_workspace_activity
ON assistant_sessions (workspace_id, last_message_at DESC, id DESC)
WHERE workspace_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_uploaded_files_workspace_created
ON uploaded_files (workspace_id, created_at DESC, id DESC)
WHERE workspace_id IS NOT NULL;
