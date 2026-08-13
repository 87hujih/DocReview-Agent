package postgres

import (
	"regexp"
	"strings"
	"testing"
)

// TestDurableAgentRuntimeMigrationDefinesRequiredExpandOnlySchema 验证对应场景下的正常路径与失败路径。
func TestDurableAgentRuntimeMigrationDefinesRequiredExpandOnlySchema(t *testing.T) {
	contents, err := migrationsFS.ReadFile("migrations/017_durable_agent_runtime.sql")
	if err != nil {
		t.Fatalf("read durable runtime migration: %v", err)
	}
	sql := string(contents)

	for _, table := range []string{
		"agent_runs",
		"agent_steps",
		"agent_attempts",
		"tool_calls",
		"context_manifests",
		"outbox_events",
	} {
		if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Fatalf("expected migration to create %s", table)
		}
	}

	for _, fragment := range []string{
		"UNIQUE (run_id, step_key)",
		"UNIQUE (step_id, attempt_number)",
		"idx_agent_steps_claimable",
		"idx_agent_steps_expired_lease",
		"idx_tool_calls_run_idempotency",
		"idx_outbox_events_idempotency",
		"lease_generation BIGINT",
		"request_id TEXT",
		"items_json JSONB",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("expected durable runtime migration fragment %q", fragment)
		}
	}

	destructive := regexp.MustCompile(`(?mi)^\s*(UPDATE\s|DELETE\s+FROM\s|DROP\s|TRUNCATE\s|ALTER\s+TABLE[^;]*SET\s+NOT\s+NULL)`)
	if match := destructive.FindString(sql); match != "" {
		t.Fatalf("durable runtime migration must remain expand-only; found %q", strings.TrimSpace(match))
	}
}

// TestDurableAgentRuntimeMigrationConstrainsLifecycleAndJSONShapes 验证对应场景下的正常路径与失败路径。
func TestDurableAgentRuntimeMigrationConstrainsLifecycleAndJSONShapes(t *testing.T) {
	contents, err := migrationsFS.ReadFile("migrations/017_durable_agent_runtime.sql")
	if err != nil {
		t.Fatalf("read durable runtime migration: %v", err)
	}
	sql := string(contents)

	for _, status := range []string{"queued", "running", "waiting_input", "waiting_approval", "succeeded", "failed", "cancelled"} {
		if !strings.Contains(sql, "'"+status+"'") {
			t.Fatalf("expected runtime status %q to be constrained", status)
		}
	}
	for _, jsonColumn := range []string{"input_json", "output_json", "error_json", "items_json", "payload_json"} {
		if !strings.Contains(sql, "jsonb_typeof("+jsonColumn+")") {
			t.Fatalf("expected JSON shape check for %s", jsonColumn)
		}
	}
}
