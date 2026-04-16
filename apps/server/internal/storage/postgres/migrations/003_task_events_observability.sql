CREATE TABLE IF NOT EXISTS task_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id    UUID        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    run_id     TEXT,
    step_name  TEXT        NOT NULL DEFAULT '',
    source     TEXT        NOT NULL,
    level      TEXT        NOT NULL,
    event_type TEXT        NOT NULL,
    message    TEXT        NOT NULL,
    payload    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_task_events_task_created_at
ON task_events (task_id, created_at, id);

CREATE INDEX IF NOT EXISTS idx_task_events_run_created_at
ON task_events (run_id, created_at, id);
