ALTER TABLE session_context_snapshots
    ADD COLUMN IF NOT EXISTS pending_clarification_json JSONB,
    ADD COLUMN IF NOT EXISTS advisory_context_json JSONB,
    ADD COLUMN IF NOT EXISTS pending_proposal_json JSONB,
    ADD COLUMN IF NOT EXISTS authorization_state_json JSONB,
    ADD COLUMN IF NOT EXISTS execution_state_json JSONB;

UPDATE session_context_snapshots
SET pending_clarification_json = COALESCE(pending_clarification_json, '{}'::jsonb),
    advisory_context_json = COALESCE(advisory_context_json, '{}'::jsonb),
    pending_proposal_json = COALESCE(pending_proposal_json, '{}'::jsonb),
    authorization_state_json = COALESCE(authorization_state_json, '{}'::jsonb),
    execution_state_json = COALESCE(execution_state_json, '{}'::jsonb)
WHERE pending_clarification_json IS NULL
   OR advisory_context_json IS NULL
   OR pending_proposal_json IS NULL
   OR authorization_state_json IS NULL
   OR execution_state_json IS NULL;

ALTER TABLE session_context_snapshots
    ALTER COLUMN pending_clarification_json SET DEFAULT '{}'::jsonb,
    ALTER COLUMN pending_clarification_json SET NOT NULL,
    ALTER COLUMN advisory_context_json SET DEFAULT '{}'::jsonb,
    ALTER COLUMN advisory_context_json SET NOT NULL,
    ALTER COLUMN pending_proposal_json SET DEFAULT '{}'::jsonb,
    ALTER COLUMN pending_proposal_json SET NOT NULL,
    ALTER COLUMN authorization_state_json SET DEFAULT '{}'::jsonb,
    ALTER COLUMN authorization_state_json SET NOT NULL,
    ALTER COLUMN execution_state_json SET DEFAULT '{}'::jsonb,
    ALTER COLUMN execution_state_json SET NOT NULL;
