CREATE TABLE IF NOT EXISTS assistant_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title           TEXT        NOT NULL,
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS assistant_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id  UUID        NOT NULL REFERENCES assistant_sessions(id) ON DELETE CASCADE,
    role        TEXT        NOT NULL,
    kind        TEXT        NOT NULL,
    sequence_no INT         NOT NULL,
    payload     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE(session_id, sequence_no)
);

CREATE INDEX IF NOT EXISTS idx_assistant_sessions_last_message_at
ON assistant_sessions (last_message_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_assistant_messages_session_sequence
ON assistant_messages (session_id, sequence_no ASC, id ASC);
