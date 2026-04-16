CREATE TABLE IF NOT EXISTS uploaded_files (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_id       UUID REFERENCES resources(id) ON DELETE SET NULL,
    session_id        UUID REFERENCES assistant_sessions(id) ON DELETE SET NULL,
    original_filename TEXT        NOT NULL,
    content_type      TEXT        NOT NULL DEFAULT 'application/octet-stream',
    size_bytes        BIGINT      NOT NULL,
    sha256            TEXT        NOT NULL,
    storage_key       TEXT        NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_uploaded_files_session_created
ON uploaded_files (session_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_uploaded_files_resource_created
ON uploaded_files (resource_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_uploaded_files_sha256
ON uploaded_files (sha256);
