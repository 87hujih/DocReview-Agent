ALTER TABLE tool_calls
ADD COLUMN IF NOT EXISTS claimed_by TEXT;

ALTER TABLE tool_calls
ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;

ALTER TABLE tool_calls
ADD COLUMN IF NOT EXISTS lease_generation BIGINT NOT NULL DEFAULT 0
    CHECK (lease_generation >= 0);

ALTER TABLE tool_calls
ADD COLUMN IF NOT EXISTS attempt_count INTEGER NOT NULL DEFAULT 0
    CHECK (attempt_count >= 0);

CREATE INDEX IF NOT EXISTS idx_tool_calls_expired_lease
ON tool_calls (lease_expires_at, id)
WHERE status = 'running';

CREATE TABLE IF NOT EXISTS agent_artifacts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    run_id UUID REFERENCES agent_runs(id) ON DELETE SET NULL,
    step_id UUID REFERENCES agent_steps(id) ON DELETE SET NULL,
    idempotency_key TEXT NOT NULL CHECK (length(btrim(idempotency_key)) > 0),
    data_classification TEXT NOT NULL
        CHECK (data_classification IN ('public', 'internal', 'confidential', 'restricted')),
    content_json JSONB NOT NULL
        CHECK (jsonb_typeof(content_json) = 'object'),
    content_hash TEXT NOT NULL CHECK (length(btrim(content_hash)) > 0),
    token_count INTEGER NOT NULL CHECK (token_count >= 0),
    provenance_json JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(provenance_json) = 'array'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (workspace_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_agent_artifacts_workspace_created
ON agent_artifacts (workspace_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_agent_artifacts_run_step
ON agent_artifacts (run_id, step_id, created_at, id)
WHERE run_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS agent_tool_approvals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_id UUID NOT NULL REFERENCES agent_steps(id) ON DELETE CASCADE,
    tool_name TEXT NOT NULL CHECK (length(btrim(tool_name)) > 0),
    tool_version TEXT NOT NULL CHECK (length(btrim(tool_version)) > 0),
    idempotency_key TEXT NOT NULL CHECK (length(btrim(idempotency_key)) > 0),
    resources_json JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(resources_json) = 'array'),
    resources_hash TEXT NOT NULL CHECK (length(btrim(resources_hash)) > 0),
    payload_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(payload_json) = 'object'),
    reason TEXT NOT NULL CHECK (length(btrim(reason)) > 0),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    requested_by_type TEXT NOT NULL CHECK (length(btrim(requested_by_type)) > 0),
    requested_by_id TEXT NOT NULL CHECK (length(btrim(requested_by_id)) > 0),
    decided_by_type TEXT,
    decided_by_id TEXT,
    decision_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at TIMESTAMPTZ,
    UNIQUE (workspace_id, run_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_agent_tool_approvals_pending
ON agent_tool_approvals (workspace_id, created_at, id)
WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS agent_tool_rate_limit_buckets (
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    principal_type TEXT NOT NULL CHECK (length(btrim(principal_type)) > 0),
    principal_id TEXT NOT NULL CHECK (length(btrim(principal_id)) > 0),
    tool_name TEXT NOT NULL CHECK (length(btrim(tool_name)) > 0),
    tool_version TEXT NOT NULL CHECK (length(btrim(tool_version)) > 0),
    bucket_start TIMESTAMPTZ NOT NULL,
    call_count INTEGER NOT NULL CHECK (call_count > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, principal_type, principal_id, tool_name, tool_version, bucket_start)
);

CREATE INDEX IF NOT EXISTS idx_agent_tool_rate_limit_bucket_start
ON agent_tool_rate_limit_buckets (bucket_start);
