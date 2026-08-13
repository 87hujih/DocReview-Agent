package postgres

import (
	"regexp"
	"strings"
	"testing"
)

// TestToolRuntimeMigrationAddsRecoverableCallLease 验证对应场景下的正常路径与失败路径。
func TestToolRuntimeMigrationAddsRecoverableCallLease(t *testing.T) {
	contents, err := migrationsFS.ReadFile("migrations/019_tool_runtime_controls.sql")
	if err != nil {
		t.Fatalf("read tool runtime migration: %v", err)
	}
	sql := string(contents)
	for _, fragment := range []string{
		"ALTER TABLE tool_calls",
		"ADD COLUMN IF NOT EXISTS claimed_by TEXT",
		"ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ",
		"ADD COLUMN IF NOT EXISTS lease_generation BIGINT",
		"ADD COLUMN IF NOT EXISTS attempt_count INTEGER",
		"idx_tool_calls_expired_lease",
		"CREATE TABLE IF NOT EXISTS agent_artifacts",
		"UNIQUE (workspace_id, idempotency_key)",
		"jsonb_typeof(content_json) = 'object'",
		"jsonb_typeof(provenance_json) = 'array'",
		"CREATE TABLE IF NOT EXISTS agent_tool_approvals",
		"resources_hash TEXT NOT NULL",
		"idx_agent_tool_approvals_pending",
		"CREATE TABLE IF NOT EXISTS agent_tool_rate_limit_buckets",
		"PRIMARY KEY (workspace_id, principal_type, principal_id, tool_name, tool_version, bucket_start)",
		"idx_agent_tool_rate_limit_bucket_start",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("expected tool runtime migration fragment %q", fragment)
		}
	}
	destructive := regexp.MustCompile(`(?mi)^\s*(UPDATE\s|DELETE\s+FROM\s|DROP\s|TRUNCATE\s|ALTER\s+TABLE[^;]*SET\s+NOT\s+NULL)`)
	if match := destructive.FindString(sql); match != "" {
		t.Fatalf("tool runtime migration must remain expand-only; found %q", strings.TrimSpace(match))
	}
}
