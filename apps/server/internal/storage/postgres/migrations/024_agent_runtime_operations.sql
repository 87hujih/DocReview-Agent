-- 阶段 J：仅追加操作员幂等恢复动作的审计事实。
-- 运行时状态继续保存在现有的 Run、Step 和 Outbox 表中。

CREATE TABLE IF NOT EXISTS agent_runtime_operator_actions (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces(id),
    request_id    TEXT        NOT NULL,
    action_type   TEXT        NOT NULL,
    target_type   TEXT        NOT NULL,
    target_id     TEXT        NOT NULL,
    operator_id   TEXT        NOT NULL,
    reason        TEXT        NOT NULL,
    input_json    JSONB       NOT NULL DEFAULT '{}'::jsonb,
    result_json   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    status        TEXT        NOT NULL DEFAULT 'completed',
    requested_at  TIMESTAMPTZ NOT NULL,
    completed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (workspace_id, request_id),
    CHECK (length(btrim(request_id)) > 0),
    CHECK (action_type IN ('cancel_run', 'retry_run', 'replay_dead_letter')),
    CHECK (target_type IN ('run', 'outbox_event')),
    CHECK (length(btrim(target_id)) > 0),
    CHECK (length(btrim(operator_id)) > 0),
    CHECK (length(btrim(reason)) > 0),
    CHECK (jsonb_typeof(input_json) = 'object'),
    CHECK (jsonb_typeof(result_json) = 'object'),
    CHECK (status = 'completed'),
    CHECK (completed_at >= requested_at)
);

CREATE INDEX IF NOT EXISTS idx_agent_runtime_operator_actions_target
ON agent_runtime_operator_actions (workspace_id, target_type, target_id, requested_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_agent_runtime_operator_actions_operator
ON agent_runtime_operator_actions (workspace_id, operator_id, requested_at DESC, id DESC);
