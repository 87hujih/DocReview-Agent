package postgres

import (
	"regexp"
	"strings"
	"testing"
)

// TestAgentTurnMigrationDefinesIdempotentTurnAndEventFacts 验证对应场景下的正常路径与失败路径。
func TestAgentTurnMigrationDefinesIdempotentTurnAndEventFacts(t *testing.T) {
	contents, err := migrationsFS.ReadFile("migrations/018_agent_turn_context.sql")
	if err != nil {
		t.Fatalf("read agent turn migration: %v", err)
	}
	sql := string(contents)

	for _, fragment := range []string{
		"CREATE TABLE IF NOT EXISTS agent_turns",
		"CREATE TABLE IF NOT EXISTS agent_turn_events",
		"CREATE TABLE IF NOT EXISTS agent_turn_outcomes",
		"request_id TEXT NOT NULL",
		"idempotency_scope TEXT NOT NULL",
		"input_hash TEXT NOT NULL",
		"UNIQUE (idempotency_scope, request_id)",
		"UNIQUE (turn_id, sequence_no)",
		"idx_agent_turns_workspace_request",
		"idx_agent_turns_session_request",
		"idx_agent_turns_global_request",
		"ADD COLUMN IF NOT EXISTS turn_id UUID",
		"idx_assistant_messages_turn",
		"idx_assistant_messages_outcome",
		"idx_agent_runs_turn",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("expected turn migration fragment %q", fragment)
		}
	}

	destructive := regexp.MustCompile(`(?mi)^\s*(UPDATE\s|DELETE\s+FROM\s|DROP\s|TRUNCATE\s|ALTER\s+TABLE[^;]*SET\s+NOT\s+NULL)`)
	if match := destructive.FindString(sql); match != "" {
		t.Fatalf("turn migration must remain expand-only; found %q", strings.TrimSpace(match))
	}
}

// TestAgentTurnMigrationConstrainsLifecycleAndJSONShapes 验证对应场景下的正常路径与失败路径。
func TestAgentTurnMigrationConstrainsLifecycleAndJSONShapes(t *testing.T) {
	contents, err := migrationsFS.ReadFile("migrations/018_agent_turn_context.sql")
	if err != nil {
		t.Fatalf("read agent turn migration: %v", err)
	}
	sql := string(contents)
	for _, status := range []string{"accepted", "running", "waiting_input", "waiting_approval", "succeeded", "failed", "cancelled"} {
		if !strings.Contains(sql, "'"+status+"'") {
			t.Fatalf("expected turn status %q", status)
		}
	}
	for _, column := range []string{"input_json", "output_json", "error_json", "payload_json"} {
		if !strings.Contains(sql, "jsonb_typeof("+column+")") {
			t.Fatalf("expected JSON object constraint for %s", column)
		}
	}
}
