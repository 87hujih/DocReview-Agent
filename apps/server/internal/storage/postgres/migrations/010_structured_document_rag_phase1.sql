CREATE TABLE IF NOT EXISTS resource_version_structures (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id        UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version_id         UUID        NOT NULL REFERENCES resource_versions(id) ON DELETE CASCADE UNIQUE,
    source_format      TEXT        NOT NULL DEFAULT '',
    parser_name        TEXT        NOT NULL DEFAULT '',
    parser_version     TEXT        NOT NULL DEFAULT '',
    document_json      JSONB       NOT NULL,
    quality_flags_json JSONB       NOT NULL DEFAULT '[]'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS resource_sections (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id  UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version_id   UUID        NOT NULL REFERENCES resource_versions(id) ON DELETE CASCADE,
    section_key  TEXT        NOT NULL,
    section_index INT        NOT NULL DEFAULT 0,
    section_type TEXT        NOT NULL DEFAULT 'unknown',
    title        TEXT        NOT NULL DEFAULT '',
    summary      TEXT        NOT NULL DEFAULT '',
    content      TEXT        NOT NULL,
    page_start   INT         NOT NULL DEFAULT 0,
    page_end     INT         NOT NULL DEFAULT 0,
    metadata_json JSONB      NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE(version_id, section_key)
);

CREATE INDEX IF NOT EXISTS idx_resource_sections_version_type_order
ON resource_sections (version_id, section_type, section_index, id);

ALTER TABLE resource_chunks
    ADD COLUMN IF NOT EXISTS section_id UUID REFERENCES resource_sections(id) ON DELETE SET NULL;

ALTER TABLE resource_chunks
    ADD COLUMN IF NOT EXISTS section_type TEXT NOT NULL DEFAULT 'whole_document';

ALTER TABLE resource_chunks
    ADD COLUMN IF NOT EXISTS chunk_role TEXT NOT NULL DEFAULT 'section_body';

ALTER TABLE resource_chunks
    ADD COLUMN IF NOT EXISTS window_group_id TEXT NOT NULL DEFAULT '';

ALTER TABLE resource_chunks
    ADD COLUMN IF NOT EXISTS page_start INT NOT NULL DEFAULT 0;

ALTER TABLE resource_chunks
    ADD COLUMN IF NOT EXISTS page_end INT NOT NULL DEFAULT 0;

ALTER TABLE resource_chunks
    ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_resource_chunks_version_type_role_index
ON resource_chunks (version_id, section_type, chunk_role, chunk_index);
