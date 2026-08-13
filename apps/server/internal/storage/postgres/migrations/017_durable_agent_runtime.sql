CREATE TABLE IF NOT EXISTS agent_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    workspace_id UUID REFERENCES workspaces(id) ON DELETE SET NULL,
    session_id UUID REFERENCES assistant_sessions(id) ON DELETE SET NULL,
    task_id UUID REFERENCES tasks(id) ON DELETE SET NULL,
    request_id TEXT,
    trace_id TEXT,
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'waiting_input', 'waiting_approval', 'succeeded', 'failed', 'cancelled')),
    objective TEXT NOT NULL CHECK (length(btrim(objective)) > 0),
    current_step TEXT,
    max_steps INTEGER NOT NULL DEFAULT 64 CHECK (max_steps > 0),
    max_tool_calls INTEGER NOT NULL DEFAULT 32 CHECK (max_tool_calls >= 0),
    token_budget BIGINT CHECK (token_budget IS NULL OR token_budget > 0),
    cost_budget NUMERIC(18, 6) CHECK (cost_budget IS NULL OR cost_budget >= 0),
    deadline_at TIMESTAMPTZ,
    cancel_requested_at TIMESTAMPTZ,
    state_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(state_json) = 'object'),
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (deadline_at IS NULL OR deadline_at > created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_runs_workspace_request
ON agent_runs (workspace_id, request_id)
WHERE workspace_id IS NOT NULL AND request_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_runs_legacy_request
ON agent_runs (request_id)
WHERE workspace_id IS NULL AND request_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agent_runs_status_deadline
ON agent_runs (status, deadline_at, created_at, id)
WHERE status IN ('queued', 'running', 'waiting_input', 'waiting_approval');

CREATE INDEX IF NOT EXISTS idx_agent_runs_session_created
ON agent_runs (session_id, created_at DESC, id DESC)
WHERE session_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS agent_steps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_key TEXT NOT NULL CHECK (length(btrim(step_key)) > 0),
    step_type TEXT NOT NULL CHECK (length(btrim(step_type)) > 0),
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'waiting_input', 'waiting_approval', 'succeeded', 'failed', 'cancelled')),
    input_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(input_json) = 'object'),
    output_json JSONB CHECK (output_json IS NULL OR jsonb_typeof(output_json) = 'object'),
    error_json JSONB CHECK (error_json IS NULL OR jsonb_typeof(error_json) = 'object'),
    claimed_by TEXT,
    lease_expires_at TIMESTAMPTZ,
    heartbeat_at TIMESTAMPTZ,
    lease_generation BIGINT NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    next_retry_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, step_key),
    CHECK ((status = 'running') = (claimed_by IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK (completed_at IS NULL OR status IN ('succeeded', 'failed', 'cancelled'))
);

CREATE INDEX IF NOT EXISTS idx_agent_steps_claimable
ON agent_steps (next_retry_at, created_at, id)
WHERE status = 'queued';

CREATE INDEX IF NOT EXISTS idx_agent_steps_expired_lease
ON agent_steps (lease_expires_at, id)
WHERE status = 'running';

CREATE INDEX IF NOT EXISTS idx_agent_steps_run_status
ON agent_steps (run_id, status, created_at, id);

CREATE TABLE IF NOT EXISTS context_manifests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_id UUID NOT NULL REFERENCES agent_steps(id) ON DELETE CASCADE,
    token_budget BIGINT NOT NULL CHECK (token_budget > 0),
    reserved_output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (reserved_output_tokens >= 0),
    tokenizer TEXT NOT NULL CHECK (length(btrim(tokenizer)) > 0),
    items_json JSONB NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(items_json) = 'array'),
    total_tokens BIGINT NOT NULL CHECK (total_tokens >= 0),
    content_hash TEXT NOT NULL CHECK (length(btrim(content_hash)) > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (total_tokens + reserved_output_tokens <= token_budget)
);

CREATE INDEX IF NOT EXISTS idx_context_manifests_run_step
ON context_manifests (run_id, step_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS agent_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    step_id UUID NOT NULL REFERENCES agent_steps(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    provider TEXT,
    model TEXT,
    prompt_version TEXT,
    temperature DOUBLE PRECISION,
    context_manifest_id UUID REFERENCES context_manifests(id) ON DELETE SET NULL,
    trace_id TEXT,
    input_tokens BIGINT CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens BIGINT CHECK (output_tokens IS NULL OR output_tokens >= 0),
    cost NUMERIC(18, 6) CHECK (cost IS NULL OR cost >= 0),
    latency_ms BIGINT CHECK (latency_ms IS NULL OR latency_ms >= 0),
    retry_count INTEGER NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    finish_reason TEXT,
    error_category TEXT CHECK (
        error_category IS NULL OR error_category IN (
            'invalid_input', 'permission_denied', 'not_found', 'conflict', 'rate_limited',
            'timeout', 'retryable_upstream', 'terminal_upstream', 'policy_blocked',
            'cancelled', 'lease_expired'
        )
    ),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    UNIQUE (step_id, attempt_number)
);

CREATE INDEX IF NOT EXISTS idx_agent_attempts_step_started
ON agent_attempts (step_id, started_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS tool_calls (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_id UUID NOT NULL REFERENCES agent_steps(id) ON DELETE CASCADE,
    tool_name TEXT NOT NULL CHECK (length(btrim(tool_name)) > 0),
    tool_version TEXT NOT NULL CHECK (length(btrim(tool_version)) > 0),
    input_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(input_json) = 'object'),
    output_json JSONB CHECK (output_json IS NULL OR jsonb_typeof(output_json) = 'object'),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'cancelled')),
    idempotency_key TEXT,
    error_json JSONB CHECK (error_json IS NULL OR jsonb_typeof(error_json) = 'object'),
    error_category TEXT CHECK (
        error_category IS NULL OR error_category IN (
            'invalid_input', 'permission_denied', 'not_found', 'conflict', 'rate_limited',
            'timeout', 'retryable_upstream', 'terminal_upstream', 'policy_blocked',
            'cancelled', 'lease_expired'
        )
    ),
    latency_ms BIGINT CHECK (latency_ms IS NULL OR latency_ms >= 0),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tool_calls_run_idempotency
ON tool_calls (run_id, idempotency_key)
WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_tool_calls_run_step_created
ON tool_calls (run_id, step_id, created_at, id);

CREATE TABLE IF NOT EXISTS outbox_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type TEXT NOT NULL CHECK (length(btrim(aggregate_type)) > 0),
    aggregate_id TEXT NOT NULL CHECK (length(btrim(aggregate_id)) > 0),
    event_type TEXT NOT NULL CHECK (length(btrim(event_type)) > 0),
    idempotency_key TEXT NOT NULL CHECK (length(btrim(idempotency_key)) > 0),
    payload_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(payload_json) = 'object'),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'publishing', 'published', 'dead_letter')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMPTZ,
    claimed_by TEXT,
    lease_expires_at TIMESTAMPTZ,
    lease_generation BIGINT NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    error_json JSONB CHECK (error_json IS NULL OR jsonb_typeof(error_json) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ,
    CHECK ((status = 'publishing') = (claimed_by IS NOT NULL AND lease_expires_at IS NOT NULL)),
    CHECK (published_at IS NULL OR status = 'published')
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_outbox_events_idempotency
ON outbox_events (aggregate_type, aggregate_id, idempotency_key);

CREATE INDEX IF NOT EXISTS idx_outbox_events_claimable
ON outbox_events (next_attempt_at, created_at, id)
WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_outbox_events_expired_lease
ON outbox_events (lease_expires_at, id)
WHERE status = 'publishing';
