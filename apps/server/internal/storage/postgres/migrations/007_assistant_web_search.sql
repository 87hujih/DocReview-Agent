-- 007: 为 assistant_sessions 增加联网搜索开关字段。
-- 默认关闭，只能由用户显式切换。
ALTER TABLE assistant_sessions
    ADD COLUMN IF NOT EXISTS web_search_enabled BOOLEAN NOT NULL DEFAULT false;
