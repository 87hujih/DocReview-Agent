package postgres_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCanonicalDocumentMigrationIsExpandOnlyAndTransactionComplete 验证对应场景下的正常路径与失败路径。
func TestCanonicalDocumentMigrationIsExpandOnlyAndTransactionComplete(t *testing.T) {
	path := filepath.Join("migrations", "021_canonical_document_ast.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(data))
	for _, required := range []string{
		"canonical_documents", "document_nodes", "document_node_source_mappings", "document_patch_commits",
		"schema_version", "content_hash", "root_node_id", "embedding_profile", "renderer_profile",
		"idempotency_key", "patch_hash", "base_version_id", "new_version_id", "outbox_event_id",
		"unique (workspace_id, idempotency_key)", "foreign key", "resource_sections", "resource_chunks",
	} {
		if !strings.Contains(sql, required) {
			t.Errorf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"drop table", "drop column", "truncate ", "delete from"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("migration is not expand-only: contains %q", forbidden)
		}
	}
}
