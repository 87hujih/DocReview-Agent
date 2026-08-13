-- 阶段 I：仅追加或扩展可信切换、公开投影与对账事实；现有旧版数据行保持有效且不变。

ALTER TABLE agent_turns
    ADD COLUMN IF NOT EXISTS resource_id UUID REFERENCES resources(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS principal_type TEXT,
    ADD COLUMN IF NOT EXISTS principal_id UUID,
    ADD COLUMN IF NOT EXISTS trust_source TEXT,
    ADD COLUMN IF NOT EXISTS runtime_mode TEXT;

ALTER TABLE agent_turns
    ADD CONSTRAINT chk_agent_turns_runtime_mode
    CHECK (runtime_mode IS NULL OR runtime_mode IN ('legacy', 'shadow', 'durable'));

ALTER TABLE agent_runs
    ADD COLUMN IF NOT EXISTS resource_id UUID REFERENCES resources(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS principal_type TEXT,
    ADD COLUMN IF NOT EXISTS principal_id UUID,
    ADD COLUMN IF NOT EXISTS trust_source TEXT,
    ADD COLUMN IF NOT EXISTS runtime_mode TEXT;

ALTER TABLE agent_runs
    ADD CONSTRAINT chk_agent_runs_runtime_mode
    CHECK (runtime_mode IS NULL OR runtime_mode IN ('legacy', 'shadow', 'durable'));

CREATE INDEX IF NOT EXISTS idx_agent_turns_workspace_resource_created
ON agent_turns (workspace_id, resource_id, created_at DESC, id DESC)
WHERE workspace_id IS NOT NULL AND resource_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agent_runs_runtime_drain
ON agent_runs (runtime_mode, status, created_at, id)
WHERE runtime_mode = 'durable'
  AND status IN ('queued', 'running', 'waiting_input', 'waiting_approval');

CREATE TABLE IF NOT EXISTS agent_turn_public_projections (
    turn_id             UUID PRIMARY KEY REFERENCES agent_turns(id) ON DELETE CASCADE,
    workspace_id        UUID        NOT NULL REFERENCES workspaces(id),
    status              TEXT        NOT NULL,
    dto_json            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    content_hash        TEXT        NOT NULL,
    last_event_sequence INTEGER     NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (status IN ('waiting_input', 'waiting_approval', 'succeeded', 'failed', 'cancelled')),
    CHECK (jsonb_typeof(dto_json) = 'object'),
    CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CHECK (last_event_sequence > 0)
);

CREATE INDEX IF NOT EXISTS idx_agent_turn_public_projections_workspace_updated
ON agent_turn_public_projections (workspace_id, updated_at DESC, turn_id DESC);

CREATE TABLE IF NOT EXISTS outbox_projection_receipts (
    event_id         UUID        NOT NULL REFERENCES outbox_events(id) ON DELETE CASCADE,
    projection_name  TEXT        NOT NULL,
    payload_hash     TEXT        NOT NULL,
    completed_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (event_id, projection_name),
    CHECK (length(btrim(projection_name)) > 0),
    CHECK (payload_hash ~ '^sha256:[0-9a-f]{64}$')
);

CREATE TABLE IF NOT EXISTS agent_cutover_comparisons (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        UUID        NOT NULL REFERENCES workspaces(id),
    resource_id         UUID        NOT NULL REFERENCES resources(id),
    run_id              UUID        REFERENCES agent_runs(id) ON DELETE SET NULL,
    request_id          TEXT        NOT NULL,
    comparison_kind     TEXT        NOT NULL DEFAULT 'public_turn',
    status              TEXT        NOT NULL,
    legacy_result_hash  TEXT,
    typed_result_hash   TEXT,
    legacy_event_hash   TEXT,
    typed_event_hash    TEXT,
    legacy_dto_hash     TEXT,
    typed_dto_hash      TEXT,
    details_json        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (workspace_id, request_id, comparison_kind),
    CHECK (status IN ('matched', 'diverged', 'unavailable')),
    CHECK (jsonb_typeof(details_json) = 'object')
);

CREATE INDEX IF NOT EXISTS idx_agent_cutover_comparisons_status_created
ON agent_cutover_comparisons (status, created_at, id);
