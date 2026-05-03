CREATE TABLE IF NOT EXISTS assistant_runtime_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES assistant_sessions(id) ON DELETE CASCADE,
    message_id UUID REFERENCES assistant_messages(id) ON DELETE SET NULL,
    source TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_assistant_runtime_events_session_created_at
ON assistant_runtime_events (session_id, created_at, id);

CREATE INDEX IF NOT EXISTS idx_assistant_runtime_events_message_id
ON assistant_runtime_events (message_id)
WHERE message_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS assistant_runtime_samples (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES assistant_sessions(id) ON DELETE CASCADE,
    decision_message_id UUID UNIQUE REFERENCES assistant_messages(id) ON DELETE SET NULL,
    request_kind TEXT NOT NULL DEFAULT '',
    response_mode TEXT NOT NULL DEFAULT '',
    planner_used BOOLEAN NOT NULL DEFAULT false,
    verifier_used BOOLEAN NOT NULL DEFAULT false,
    clarification_asked BOOLEAN NOT NULL DEFAULT false,
    clarification_outcome TEXT NOT NULL DEFAULT '',
    task_suggestion_created BOOLEAN NOT NULL DEFAULT false,
    task_suggestion_confirmed BOOLEAN NOT NULL DEFAULT false,
    task_suggestion_ignored BOOLEAN NOT NULL DEFAULT false,
    user_corrected BOOLEAN NOT NULL DEFAULT false,
    promoted_to_workflow BOOLEAN NOT NULL DEFAULT false,
    final_outcome TEXT NOT NULL DEFAULT '',
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_assistant_runtime_samples_session_created_at
ON assistant_runtime_samples (session_id, created_at, id);

CREATE INDEX IF NOT EXISTS idx_assistant_runtime_samples_final_outcome
ON assistant_runtime_samples (final_outcome);
