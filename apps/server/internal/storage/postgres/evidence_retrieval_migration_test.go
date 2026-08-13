package postgres

import (
	"regexp"
	"strings"
	"testing"
)

// TestEvidenceRetrievalMigrationIsExpandOnlyAndAddsStrictANNProfile 验证对应场景下的正常路径与失败路径。
func TestEvidenceRetrievalMigrationIsExpandOnlyAndAddsStrictANNProfile(t *testing.T) {
	contents, err := migrationsFS.ReadFile("migrations/022_evidence_retrieval.sql")
	if err != nil {
		t.Fatalf("read Phase H migration: %v", err)
	}
	sql := string(contents)
	for _, fragment := range []string{
		"ALTER TABLE resource_chunks",
		"ADD COLUMN IF NOT EXISTS embedding_model TEXT",
		"ADD COLUMN IF NOT EXISTS embedding_dimensions INTEGER",
		"ADD COLUMN IF NOT EXISTS retrieval_index_version TEXT",
		"vector_dims(embedding) = embedding_dimensions",
		"embedding_dimensions = 1024",
		"embedding_status <> 'ready'",
		"idx_resource_chunks_retrieval_scope",
		"USING hnsw (embedding vector_cosine_ops)",
		"embedding_status = 'ready'",
		"CREATE TABLE IF NOT EXISTS retrieval_profiles",
		"fusion_algorithm",
		"minimum_fused_score",
		"rerank_profile_version",
		"embedding_vector_type",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("Phase H migration missing %q", fragment)
		}
	}
	destructive := regexp.MustCompile(`(?mi)^\s*(UPDATE\s|DELETE\s+FROM\s|DROP\s|TRUNCATE\s|ALTER\s+TABLE[^;]*SET\s+NOT\s+NULL)`)
	if match := destructive.FindString(sql); match != "" {
		t.Fatalf("Phase H migration must remain expand-only; found %q", strings.TrimSpace(match))
	}
}
