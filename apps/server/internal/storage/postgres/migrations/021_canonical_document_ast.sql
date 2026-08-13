-- 阶段 G：仅扩展规范文档 AST 和原子补丁提交事实。

ALTER TABLE resources
    ADD COLUMN IF NOT EXISTS metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE resource_versions
    ADD COLUMN IF NOT EXISTS canonical_schema_version TEXT,
    ADD COLUMN IF NOT EXISTS renderer_profile TEXT,
    ADD COLUMN IF NOT EXISTS embedding_profile TEXT;

CREATE TABLE IF NOT EXISTS canonical_documents (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id         UUID        NOT NULL REFERENCES workspaces(id),
    resource_id          UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version_id           UUID        NOT NULL REFERENCES resource_versions(id) ON DELETE CASCADE,
    document_id          TEXT        NOT NULL,
    root_node_id         TEXT        NOT NULL,
    schema_version       TEXT        NOT NULL,
    source_format        TEXT        NOT NULL,
    content_hash         TEXT        NOT NULL,
    ast_json             JSONB       NOT NULL,
    metadata_json        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    renderer_profile     TEXT        NOT NULL,
    chunk_profile        TEXT        NOT NULL,
    embedding_profile    TEXT        NOT NULL,
    projection_status    TEXT        NOT NULL DEFAULT 'pending',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (version_id),
    CHECK (schema_version <> ''),
    CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    CHECK (projection_status IN ('pending', 'ready', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_canonical_documents_workspace_resource_version
ON canonical_documents (workspace_id, resource_id, version_id);

CREATE TABLE IF NOT EXISTS document_nodes (
    workspace_id        UUID        NOT NULL REFERENCES workspaces(id),
    resource_id         UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version_id          UUID        NOT NULL REFERENCES canonical_documents(version_id) ON DELETE CASCADE,
    node_id             TEXT        NOT NULL,
    parent_node_id      TEXT,
    sibling_order       INT         NOT NULL,
    node_type           TEXT        NOT NULL,
    attributes_json     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    content             TEXT        NOT NULL DEFAULT '',
    source_location_json JSONB      NOT NULL DEFAULT '{}'::jsonb,
    page_mapping_json   JSONB       NOT NULL DEFAULT '[]'::jsonb,
    metadata_json       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    content_hash        TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (version_id, node_id),
    UNIQUE (version_id, parent_node_id, sibling_order),
    FOREIGN KEY (version_id, parent_node_id)
        REFERENCES document_nodes(version_id, node_id)
        DEFERRABLE INITIALLY DEFERRED,
    CHECK (sibling_order >= 0),
    CHECK (node_type <> ''),
    CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$')
);

CREATE INDEX IF NOT EXISTS idx_document_nodes_resource_version_parent_order
ON document_nodes (resource_id, version_id, parent_node_id, sibling_order);

CREATE INDEX IF NOT EXISTS idx_document_nodes_workspace_node
ON document_nodes (workspace_id, node_id);

ALTER TABLE canonical_documents
    ADD CONSTRAINT fk_canonical_documents_root_node
    FOREIGN KEY (version_id, root_node_id)
    REFERENCES document_nodes(version_id, node_id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE IF NOT EXISTS document_node_source_mappings (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES workspaces(id),
    resource_id   UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    version_id    UUID        NOT NULL,
    node_id       TEXT        NOT NULL,
    mapping_order INT         NOT NULL,
    source_json   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    page_number   INT,
    start_offset  INT         NOT NULL DEFAULT 0,
    end_offset    INT         NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (version_id, node_id, mapping_order),
    FOREIGN KEY (version_id, node_id)
        REFERENCES document_nodes(version_id, node_id) ON DELETE CASCADE,
    CHECK (mapping_order >= 0),
    CHECK (page_number IS NULL OR page_number > 0),
    CHECK (start_offset >= 0),
    CHECK (end_offset >= start_offset)
);

ALTER TABLE resource_sections
    ADD COLUMN IF NOT EXISTS canonical_node_id TEXT;

ALTER TABLE resource_chunks
    ADD COLUMN IF NOT EXISTS canonical_node_id TEXT,
    ADD COLUMN IF NOT EXISTS content_hash TEXT,
    ADD COLUMN IF NOT EXISTS chunk_profile TEXT,
    ADD COLUMN IF NOT EXISTS embedding_profile TEXT,
    ADD COLUMN IF NOT EXISTS embedding_status TEXT NOT NULL DEFAULT 'pending';

ALTER TABLE resource_sections
    ADD CONSTRAINT fk_resource_sections_canonical_node
    FOREIGN KEY (version_id, canonical_node_id)
    REFERENCES document_nodes(version_id, node_id)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE resource_chunks
    ADD CONSTRAINT fk_resource_chunks_canonical_node
    FOREIGN KEY (version_id, canonical_node_id)
    REFERENCES document_nodes(version_id, node_id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX IF NOT EXISTS idx_resource_sections_version_canonical_node
ON resource_sections (version_id, canonical_node_id)
WHERE canonical_node_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_resource_chunks_version_canonical_node
ON resource_chunks (version_id, canonical_node_id)
WHERE canonical_node_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS document_patch_commits (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id      UUID        NOT NULL REFERENCES workspaces(id),
    resource_id       UUID        NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    idempotency_key   TEXT        NOT NULL,
    patch_hash        TEXT        NOT NULL,
    patch_schema_version TEXT     NOT NULL,
    patch_json        JSONB       NOT NULL,
    base_version_id   UUID        NOT NULL REFERENCES resource_versions(id),
    new_version_id    UUID        NOT NULL REFERENCES resource_versions(id),
    outbox_event_id   UUID        NOT NULL REFERENCES outbox_events(id),
    actor_id          TEXT        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (workspace_id, idempotency_key),
    UNIQUE (new_version_id),
    UNIQUE (outbox_event_id),
    CHECK (idempotency_key <> ''),
    CHECK (patch_hash ~ '^sha256:[0-9a-f]{64}$')
);

CREATE INDEX IF NOT EXISTS idx_document_patch_commits_resource_created
ON document_patch_commits (workspace_id, resource_id, created_at DESC);
