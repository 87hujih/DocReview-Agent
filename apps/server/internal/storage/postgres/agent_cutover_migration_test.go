package postgres

import (
	"regexp"
	"strings"
	"testing"
)

// TestAgentCutoverMigrationExpandsTrustedScopeProjectionAndComparisonFacts 验证对应场景下的正常路径与失败路径。
func TestAgentCutoverMigrationExpandsTrustedScopeProjectionAndComparisonFacts(t *testing.T) {
	contents, err := migrationsFS.ReadFile("migrations/023_agent_runtime_vertical_cutover.sql")
	if err != nil {
		t.Fatalf("read Phase I migration: %v", err)
	}
	sql := string(contents)
	for _, fragment := range []string{
		"ALTER TABLE agent_turns",
		"ALTER TABLE agent_runs",
		"ADD COLUMN IF NOT EXISTS resource_id UUID",
		"ADD COLUMN IF NOT EXISTS principal_type TEXT",
		"ADD COLUMN IF NOT EXISTS principal_id UUID",
		"ADD COLUMN IF NOT EXISTS trust_source TEXT",
		"ADD COLUMN IF NOT EXISTS runtime_mode TEXT",
		"CREATE TABLE IF NOT EXISTS agent_turn_public_projections",
		"CREATE TABLE IF NOT EXISTS outbox_projection_receipts",
		"CREATE TABLE IF NOT EXISTS agent_cutover_comparisons",
		"UNIQUE (workspace_id, request_id, comparison_kind)",
		"CHECK (status IN ('matched', 'diverged', 'unavailable'))",
		"idx_agent_runs_runtime_drain",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("expected Phase I migration fragment %q", fragment)
		}
	}

	destructive := regexp.MustCompile(`(?mi)^\s*(UPDATE\s|DELETE\s+FROM\s|DROP\s|TRUNCATE\s|ALTER\s+TABLE[^;]*SET\s+NOT\s+NULL)`)
	if match := destructive.FindString(sql); match != "" {
		t.Fatalf("Phase I migration must remain append/expand-only; found %q", strings.TrimSpace(match))
	}
}
