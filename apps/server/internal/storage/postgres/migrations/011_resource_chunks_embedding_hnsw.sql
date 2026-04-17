-- PostgreSQL 仍然是向量真源；这里仅为 resource_chunks.embedding 增加 HNSW 加速索引。
-- 当前大量查询仍带 version_id 过滤；若后续 planner 收益不明显，再单独评估 current-only projection 或向量迁出。

CREATE INDEX IF NOT EXISTS idx_resource_chunks_embedding_hnsw
ON resource_chunks
USING hnsw (embedding vector_cosine_ops);
