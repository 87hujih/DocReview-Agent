CREATE TABLE IF NOT EXISTS agent_observations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step_id UUID NOT NULL REFERENCES agent_steps(id) ON DELETE CASCADE,
    observation_key TEXT NOT NULL CHECK (length(btrim(observation_key)) > 0),
    kind TEXT NOT NULL CHECK (length(btrim(kind)) > 0),
    action TEXT NOT NULL CHECK (length(btrim(action)) > 0),
    tool_call_id UUID REFERENCES tool_calls(id) ON DELETE SET NULL,
    payload_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(payload_json) = 'object'),
    content_hash TEXT NOT NULL CHECK (length(btrim(content_hash)) > 0),
    novel BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, observation_key)
);

CREATE INDEX IF NOT EXISTS idx_agent_observations_run_created
ON agent_observations (run_id, created_at, id);

CREATE INDEX IF NOT EXISTS idx_agent_observations_run_hash
ON agent_observations (run_id, content_hash, created_at, id);

CREATE TABLE IF NOT EXISTS agent_shadow_comparisons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    legacy_task_id UUID REFERENCES tasks(id) ON DELETE SET NULL,
    legacy_output_hash TEXT,
    typed_output_hash TEXT,
    status TEXT NOT NULL
        CHECK (status IN ('matched', 'diverged', 'unavailable')),
    details_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(details_json) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_shadow_comparisons_status_created
ON agent_shadow_comparisons (status, created_at, id);
