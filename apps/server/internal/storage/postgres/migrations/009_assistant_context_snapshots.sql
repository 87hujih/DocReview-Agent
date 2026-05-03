CREATE TABLE IF NOT EXISTS session_context_snapshots (
    session_id                          UUID PRIMARY KEY REFERENCES assistant_sessions(id) ON DELETE CASCADE,
    active_resource_id                  UUID        REFERENCES resources(id) ON DELETE SET NULL,
    active_resource_title               TEXT,
    active_resource_source_type         TEXT,
    active_resource_source_message_id   UUID        REFERENCES assistant_messages(id) ON DELETE SET NULL,
    pending_task_suggestion_message_id  UUID        REFERENCES assistant_messages(id) ON DELETE SET NULL,
    pending_task_instruction            TEXT,
    latest_task_id                      UUID        REFERENCES tasks(id) ON DELETE SET NULL,
    latest_task_status                  TEXT,
    latest_task_source_message_id       UUID        REFERENCES assistant_messages(id) ON DELETE SET NULL,
    confirmed_constraints_json          JSONB       NOT NULL DEFAULT '[]'::jsonb,
    rolling_summary                     TEXT,
    summary_base_sequence_no            INT         NOT NULL DEFAULT 0,
    created_at                          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_session_context_snapshots_latest_task_source_message_id
ON session_context_snapshots (latest_task_source_message_id)
WHERE latest_task_source_message_id IS NOT NULL;
