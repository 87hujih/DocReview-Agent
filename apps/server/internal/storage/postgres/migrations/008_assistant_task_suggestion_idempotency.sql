ALTER TABLE tasks
ADD COLUMN IF NOT EXISTS source_message_id UUID REFERENCES assistant_messages(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_source_message_id_unique
ON tasks (source_message_id)
WHERE source_message_id IS NOT NULL;
