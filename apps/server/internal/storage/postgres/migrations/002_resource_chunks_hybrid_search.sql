CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS idx_resource_chunks_search_text_trgm
ON resource_chunks
USING gin ((lower(coalesce(section_title, '') || ' ' || content)) gin_trgm_ops);
