CREATE TABLE IF NOT EXISTS resource_version_structures (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id        UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version_id         UUID        NOT NULL REFERENCES resource_versions(id) ON DELETE CASCADE,
    source_format      TEXT        NOT NULL,
    parser_name        TEXT        NOT NULL,
    parser_version     TEXT,
    document_json      JSONB       NOT NULL,
    quality_flags_json JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (version_id)
);

CREATE INDEX IF NOT EXISTS idx_resource_version_structures_resource_version
ON resource_version_structures (resource_id, version_id);

CREATE TABLE IF NOT EXISTS resource_sections (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id           UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version_id            UUID        NOT NULL REFERENCES resource_versions(id) ON DELETE CASCADE,
    section_key           TEXT        NOT NULL,
    section_type          TEXT        NOT NULL,
    section_order         INT         NOT NULL,
    title                 TEXT        NOT NULL DEFAULT '',
    canonical_entity_name TEXT,
    aliases_json          JSONB       NOT NULL DEFAULT '[]'::jsonb,
    summary               TEXT        NOT NULL DEFAULT '',
    content               TEXT        NOT NULL DEFAULT '',
    page_start            INT,
    page_end              INT,
    metadata_json         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 兼容历史库里已存在旧版 resource_sections 表但缺少新列的情况。
ALTER TABLE resource_sections
    ADD COLUMN IF NOT EXISTS section_order INT,
    ADD COLUMN IF NOT EXISTS title TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS canonical_entity_name TEXT,
    ADD COLUMN IF NOT EXISTS aliases_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS content TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS page_start INT,
    ADD COLUMN IF NOT EXISTS page_end INT,
    ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE resource_sections
SET title = COALESCE(title, ''),
    aliases_json = COALESCE(aliases_json, '[]'::jsonb),
    summary = COALESCE(summary, ''),
    content = COALESCE(content, ''),
    metadata_json = COALESCE(metadata_json, '{}'::jsonb),
    created_at = COALESCE(created_at, now())
WHERE title IS NULL
   OR aliases_json IS NULL
   OR summary IS NULL
   OR content IS NULL
   OR metadata_json IS NULL
   OR created_at IS NULL;

WITH ordered_sections AS (
    SELECT id,
           ROW_NUMBER() OVER (
               PARTITION BY version_id
               ORDER BY page_start NULLS LAST, page_end NULLS LAST, created_at ASC, id ASC
           ) - 1 AS normalized_order
    FROM resource_sections
    WHERE section_order IS NULL
)
UPDATE resource_sections AS sections
SET section_order = ordered_sections.normalized_order
FROM ordered_sections
WHERE sections.id = ordered_sections.id;

ALTER TABLE resource_sections
    ALTER COLUMN title SET DEFAULT '',
    ALTER COLUMN aliases_json SET DEFAULT '[]'::jsonb,
    ALTER COLUMN summary SET DEFAULT '',
    ALTER COLUMN content SET DEFAULT '',
    ALTER COLUMN metadata_json SET DEFAULT '{}'::jsonb,
    ALTER COLUMN created_at SET DEFAULT now();

ALTER TABLE resource_sections
    ALTER COLUMN section_order SET NOT NULL,
    ALTER COLUMN title SET NOT NULL,
    ALTER COLUMN aliases_json SET NOT NULL,
    ALTER COLUMN summary SET NOT NULL,
    ALTER COLUMN content SET NOT NULL,
    ALTER COLUMN metadata_json SET NOT NULL,
    ALTER COLUMN created_at SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_resource_sections_version_order
ON resource_sections (version_id, section_order);

CREATE INDEX IF NOT EXISTS idx_resource_sections_version_type_order
ON resource_sections (version_id, section_type, section_order);

ALTER TABLE resource_chunks
    ADD COLUMN IF NOT EXISTS section_id UUID REFERENCES resource_sections(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS section_type TEXT,
    ADD COLUMN IF NOT EXISTS chunk_role TEXT,
    ADD COLUMN IF NOT EXISTS window_group_id TEXT,
    ADD COLUMN IF NOT EXISTS order_in_section INT,
    ADD COLUMN IF NOT EXISTS page_start INT,
    ADD COLUMN IF NOT EXISTS page_end INT,
    ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_resource_chunks_version_section_order
ON resource_chunks (version_id, section_id, order_in_section);

CREATE INDEX IF NOT EXISTS idx_resource_chunks_version_window_order
ON resource_chunks (version_id, window_group_id, order_in_section)
WHERE window_group_id IS NOT NULL;

ALTER TABLE session_context_snapshots
    ADD COLUMN IF NOT EXISTS active_section_id UUID,
    ADD COLUMN IF NOT EXISTS active_section_type TEXT,
    ADD COLUMN IF NOT EXISTS active_entity_name TEXT,
    ADD COLUMN IF NOT EXISTS last_citation_windows_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS last_enumerated_entities_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS ordinal_reference_frame_json JSONB NOT NULL DEFAULT '[]'::jsonb;
