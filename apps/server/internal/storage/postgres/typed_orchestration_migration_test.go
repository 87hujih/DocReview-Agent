package postgres

import (
	"regexp"
	"strings"
	"testing"
)

// TestTypedOrchestrationMigrationAddsDurableObservationsAndShadowComparisons 验证对应场景下的正常路径与失败路径。
func TestTypedOrchestrationMigrationAddsDurableObservationsAndShadowComparisons(t *testing.T) {
	contents, err := migrationsFS.ReadFile("migrations/020_typed_orchestration.sql")
	if err != nil {
		t.Fatalf("read typed orchestration migration: %v", err)
	}
	sql := string(contents)
	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS agent_observations",
		"UNIQUE (run_id, observation_key)",
		"payload_json JSONB NOT NULL",
		"content_hash TEXT NOT NULL",
		"CREATE TABLE IF NOT EXISTS agent_shadow_comparisons",
		"CHECK (status IN ('matched', 'diverged', 'unavailable'))",
		"UNIQUE (run_id)",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("expected typed orchestration migration fragment %q", fragment)
		}
	}
	destructive := regexp.MustCompile(`(?mi)^\s*(UPDATE\s|DELETE\s+FROM\s|DROP\s|TRUNCATE\s|ALTER\s+TABLE[^;]*SET\s+NOT\s+NULL)`)
	if match := destructive.FindString(sql); match != "" {
		t.Fatalf("typed orchestration migration must remain expand-only; found %q", strings.TrimSpace(match))
	}
}
