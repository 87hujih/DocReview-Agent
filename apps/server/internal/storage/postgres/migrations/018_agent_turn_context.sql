CREATE TABLE IF NOT EXISTS agent_turns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    workspace_id UUID REFERENCES workspaces(id) ON DELETE SET NULL,
    session_id UUID REFERENCES assistant_sessions(id) ON DELETE SET NULL,
    idempotency_scope TEXT NOT NULL CHECK (length(btrim(idempotency_scope)) > 0),
    request_id TEXT NOT NULL CHECK (length(btrim(request_id)) > 0),
    trace_id TEXT,
    status TEXT NOT NULL DEFAULT 'accepted'
        CHECK (status IN (
            'accepted', 'running', 'waiting_input', 'waiting_approval',
            'succeeded', 'failed', 'cancelled'
        )),
    input_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(input_json) = 'object'),
    input_hash TEXT NOT NULL CHECK (length(btrim(input_hash)) > 0),
    output_json JSONB
        CHECK (output_json IS NULL OR jsonb_typeof(output_json) = 'object'),
    error_json JSONB
        CHECK (error_json IS NULL OR jsonb_typeof(error_json) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    CHECK (completed_at IS NULL OR status IN ('succeeded', 'failed', 'cancelled')),
    UNIQUE (idempotency_scope, request_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_turns_workspace_request
ON agent_turns (workspace_id, request_id)
WHERE workspace_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_turns_session_request
ON agent_turns (session_id, request_id)
WHERE workspace_id IS NULL AND session_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_turns_global_request
ON agent_turns (request_id)
WHERE workspace_id IS NULL AND session_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_agent_turns_status_created
ON agent_turns (status, created_at, id)
WHERE status IN ('accepted', 'running', 'waiting_input', 'waiting_approval');

CREATE TABLE IF NOT EXISTS agent_turn_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    turn_id UUID NOT NULL REFERENCES agent_turns(id) ON DELETE CASCADE,
    sequence_no INTEGER NOT NULL CHECK (sequence_no > 0),
    event_type TEXT NOT NULL CHECK (length(btrim(event_type)) > 0),
    payload_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(payload_json) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (turn_id, sequence_no)
);

CREATE INDEX IF NOT EXISTS idx_agent_turn_events_turn_sequence
ON agent_turn_events (turn_id, sequence_no, id);

CREATE TABLE IF NOT EXISTS agent_turn_outcomes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    turn_id UUID NOT NULL REFERENCES agent_turns(id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK (length(btrim(idempotency_key)) > 0),
    outcome_hash TEXT NOT NULL CHECK (length(btrim(outcome_hash)) > 0),
    status TEXT NOT NULL
        CHECK (status IN (
            'running', 'waiting_input', 'waiting_approval',
            'succeeded', 'failed', 'cancelled'
        )),
    output_json JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(output_json) = 'object'),
    error_json JSONB
        CHECK (error_json IS NULL OR jsonb_typeof(error_json) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (turn_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_agent_turn_outcomes_turn_created
ON agent_turn_outcomes (turn_id, created_at, id);

ALTER TABLE assistant_messages
ADD COLUMN IF NOT EXISTS turn_id UUID REFERENCES agent_turns(id) ON DELETE SET NULL;

ALTER TABLE assistant_messages
ADD COLUMN IF NOT EXISTS outcome_id UUID REFERENCES agent_turn_outcomes(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_assistant_messages_turn
ON assistant_messages (turn_id, sequence_no, id)
WHERE turn_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_assistant_messages_outcome
ON assistant_messages (outcome_id, sequence_no, id)
WHERE outcome_id IS NOT NULL;

ALTER TABLE agent_runs
ADD COLUMN IF NOT EXISTS turn_id UUID REFERENCES agent_turns(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_runs_turn
ON agent_runs (turn_id)
WHERE turn_id IS NOT NULL;
