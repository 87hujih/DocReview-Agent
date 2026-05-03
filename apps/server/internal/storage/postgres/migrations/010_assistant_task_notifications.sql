CREATE TABLE IF NOT EXISTS assistant_task_notifications (
    task_id     UUID        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    status      TEXT        NOT NULL,
    session_id  UUID        NOT NULL REFERENCES assistant_sessions(id) ON DELETE CASCADE,
    message_id  UUID        REFERENCES assistant_messages(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, status)
);
