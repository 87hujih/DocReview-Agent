-- 阶段 H：仅追加 EvidenceSet 检索配置档和严格的 ANN 元数据。
-- 现有分块仍可由旧链路读取；只有明确标记为就绪的数据行才必须满足生产语义检索配置档。

ALTER TABLE resource_chunks
    ADD COLUMN IF NOT EXISTS embedding_model TEXT,
    ADD COLUMN IF NOT EXISTS embedding_dimensions INTEGER,
    ADD COLUMN IF NOT EXISTS retrieval_index_version TEXT;

ALTER TABLE resource_chunks
    ADD CONSTRAINT chk_resource_chunks_embedding_dimensions
    CHECK (embedding_dimensions IS NULL OR embedding_dimensions = 1024)
    NOT VALID;

ALTER TABLE resource_chunks
    ADD CONSTRAINT chk_resource_chunks_embedding_vector_dimensions
    CHECK (embedding IS NULL OR embedding_dimensions IS NULL OR vector_dims(embedding) = embedding_dimensions)
    NOT VALID;

ALTER TABLE resource_chunks
    ADD CONSTRAINT chk_resource_chunks_ready_embedding_profile
    CHECK (
        embedding_status <> 'ready'
        OR (
            embedding IS NOT NULL
            AND NULLIF(BTRIM(embedding_profile), '') IS NOT NULL
            AND NULLIF(BTRIM(embedding_model), '') IS NOT NULL
            AND embedding_dimensions = 1024
            AND vector_dims(embedding) = embedding_dimensions
            AND NULLIF(BTRIM(retrieval_index_version), '') IS NOT NULL
        )
    )
    NOT VALID;

CREATE TABLE IF NOT EXISTS retrieval_profiles (
    profile_version          TEXT        PRIMARY KEY,
    schema_version           TEXT        NOT NULL,
    fusion_algorithm         TEXT        NOT NULL,
    lexical_weight           DOUBLE PRECISION NOT NULL,
    vector_weight            DOUBLE PRECISION NOT NULL,
    rrf_constant             DOUBLE PRECISION,
    minimum_fused_score      DOUBLE PRECISION NOT NULL,
    rerank_enabled           BOOLEAN     NOT NULL,
    rerank_profile_version   TEXT        NOT NULL,
    rerank_model             TEXT,
    embedding_profile        TEXT        NOT NULL,
    embedding_model          TEXT        NOT NULL,
    embedding_dimensions     INTEGER     NOT NULL,
    embedding_vector_type    TEXT        NOT NULL,
    lexical_index_version    TEXT        NOT NULL,
    semantic_index_version   TEXT        NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),

    CHECK (profile_version <> ''),
    CHECK (schema_version <> ''),
    CHECK (fusion_algorithm IN ('weighted_sum', 'reciprocal_rank_fusion')),
    CHECK (lexical_weight >= 0 AND vector_weight >= 0 AND lexical_weight + vector_weight > 0),
    CHECK (rrf_constant IS NULL OR rrf_constant > 0),
    CHECK (minimum_fused_score >= 0 AND minimum_fused_score <= 1),
    CHECK (NOT rerank_enabled OR (NULLIF(BTRIM(rerank_model), '') IS NOT NULL)),
    CHECK (embedding_dimensions = 1024),
    CHECK (embedding_vector_type = 'vector(1024)')
);

CREATE INDEX IF NOT EXISTS idx_resource_chunks_retrieval_scope
ON resource_chunks (
    resource_id,
    version_id,
    embedding_status,
    embedding_profile,
    embedding_model,
    embedding_dimensions,
    retrieval_index_version
);

CREATE INDEX IF NOT EXISTS idx_resource_chunks_embedding_hnsw_v1
ON resource_chunks
USING hnsw (embedding vector_cosine_ops)
WITH (m = 16, ef_construction = 64)
WHERE embedding IS NOT NULL
  AND embedding_status = 'ready'
  AND embedding_dimensions = 1024;
