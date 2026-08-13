package agentops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/operations"
)

// TestDiagnosticAndMetricsQueriesAreWorkspaceScopedAndCoverRuntimeFacts 验证对应场景下的正常路径与失败路径。
func TestDiagnosticAndMetricsQueriesAreWorkspaceScopedAndCoverRuntimeFacts(t *testing.T) {
	for name, query := range map[string]string{
		"run": diagnosticRunSQL, "steps": diagnosticStepsSQL, "attempts": diagnosticAttemptsSQL,
		"tools": diagnosticToolsSQL, "manifests": diagnosticManifestsSQL, "approvals": diagnosticApprovalsSQL,
		"outbox": diagnosticOutboxSQL, "metrics": metricsSQL,
	} {
		lower := strings.ToLower(query)
		if !strings.Contains(lower, "workspace_id") {
			t.Fatalf("%s query is not workspace scoped: %s", name, query)
		}
	}
	for _, token := range []string{"agent_runs", "agent_steps", "agent_attempts", "tool_calls", "context_manifests", "outbox_events", "agent_tool_approvals", "agent_cutover_comparisons"} {
		if !strings.Contains(strings.ToLower(allDiagnosticSQL), token) {
			t.Fatalf("runtime diagnostics omit %s", token)
		}
	}
	for _, token := range []string{"output,summary,set_id", "output,summary,degradations", "output,summary,citations", "output,commit,outbox_id"} {
		if !strings.Contains(strings.ToLower(allDiagnosticSQL), token) {
			t.Fatalf("diagnostics do not cover artifact-bounded or commit output path %s", token)
		}
	}
}

func TestMetricsUseStructuredRetrievalProfileMismatchReason(t *testing.T) {
	lower := strings.ToLower(metricsSQL)
	if strings.Contains(lower, "ilike") || strings.Contains(lower, "error_json ->> 'message'") {
		t.Fatalf("profile mismatch metrics must not depend on localized error messages: %s", metricsSQL)
	}
	if !strings.Contains(lower, "error_json #>> '{details,reason_code}'") ||
		!strings.Contains(lower, "embedding_profile_mismatch") {
		t.Fatalf("profile mismatch metrics must use the stable structured reason code: %s", metricsSQL)
	}
	if !strings.Contains(lower, "error_category = 'terminal_upstream'") ||
		!strings.Contains(lower, "error_category is null") ||
		!strings.Contains(lower, "error_json #>> '{details,reason_code}' is null") {
		t.Fatalf("legacy unclassified terminal retrieval failures must fail closed: %s", metricsSQL)
	}
}

func TestMetricsScopeRemovalCohortsToDurableRunsExactResourceAndWindow(t *testing.T) {
	lower := strings.ToLower(metricsSQL)
	for _, required := range []string{
		"nullif($4, '')::uuid as resource_id",
		"run.runtime_mode = 'durable'",
		"run.resource_id = settings.resource_id",
		"comparison.resource_id = settings.resource_id",
		"run.created_at >= settings.window_start",
	} {
		if !strings.Contains(lower, required) {
			t.Fatalf("metrics SQL does not enforce exact durable cohort scope %q: %s", required, metricsSQL)
		}
	}
}

func TestComparisonReviewQueryIsExactBoundedAndTraceable(t *testing.T) {
	lower := strings.ToLower(comparisonListSQL)
	for _, required := range []string{
		"workspace_id = $1", "resource_id = $2", "created_at >= $3", "created_at <= $4",
		"order by created_at asc, id asc", "limit $5", "request_id",
		"legacy_result_hash", "typed_result_hash", "details_json",
	} {
		if !strings.Contains(lower, required) {
			t.Fatalf("comparison review SQL is missing %q: %s", required, comparisonListSQL)
		}
	}
}

func TestFrontendRunAndApprovalQueriesAreWorkspaceScopedAndBounded(t *testing.T) {
	for name, query := range map[string]string{
		"runs": runListSQL, "approvals": approvalListSQL, "approval detail": approvalDetailSQL,
	} {
		lower := strings.ToLower(query)
		if !strings.Contains(lower, "workspace_id = $1") {
			t.Fatalf("%s query is not workspace scoped: %s", name, query)
		}
	}
	runs := strings.ToLower(runListSQL)
	for _, required := range []string{"status = $2", "resource_id = nullif($3, '')::uuid", "order by run.updated_at desc, run.id desc", "limit $4"} {
		if !strings.Contains(runs, required) {
			t.Fatalf("run list query is missing %q: %s", required, runListSQL)
		}
	}
	approvals := strings.ToLower(approvalListSQL)
	for _, required := range []string{"approval.status = $2", "order by approval.created_at desc, approval.id desc", "limit $3"} {
		if !strings.Contains(approvals, required) {
			t.Fatalf("approval list query is missing %q: %s", required, approvalListSQL)
		}
	}
}

// TestSafeActionSQLIsAuditedIdempotentAndIdentityPreserving 验证对应场景下的正常路径与失败路径。
func TestSafeActionSQLIsAuditedIdempotentAndIdentityPreserving(t *testing.T) {
	combined := strings.ToLower(cancelSQL + retrySQL + replayDeadLetterSQL)
	for _, token := range []string{"agent_runtime_operator_actions", "request_id", "operator_id", "reason", "for update"} {
		if !strings.Contains(combined, token) {
			t.Fatalf("operator action SQL missing %s", token)
		}
	}
	retry := strings.ToLower(retrySQL)
	if strings.Contains(retry, "attempt_count = 0") || !strings.Contains(retry, "max_attempts = greatest(max_attempts, attempt_count + 1)") || !strings.Contains(retry, "error_category") {
		t.Fatalf("retry must preserve attempt identity and permit only a bounded next attempt: %s", retrySQL)
	}
	replay := strings.ToLower(replayDeadLetterSQL)
	if strings.Contains(replay, "insert into outbox_events") || !strings.Contains(replay, "status = 'dead_letter'") || !strings.Contains(replay, "id = $4") {
		t.Fatalf("dead-letter replay must reuse the original event identity: %s", replayDeadLetterSQL)
	}
}

// TestRepositoryRejectsInvalidOperationsBeforeDatabaseAccess 验证对应场景下的正常路径与失败路径。
func TestRepositoryRejectsInvalidOperationsBeforeDatabaseAccess(t *testing.T) {
	repo := NewRepository(nil)
	if _, err := repo.Diagnose(context.Background(), operations.DiagnosticRequest{}); err == nil {
		t.Fatal("invalid diagnostic must fail")
	}
	if _, err := repo.Metrics(context.Background(), operations.MetricsRequest{}); err == nil {
		t.Fatal("invalid metrics must fail")
	}
	if _, err := repo.Comparisons(context.Background(), operations.ComparisonListRequest{}); err == nil {
		t.Fatal("invalid comparison review must fail")
	}
	if _, err := repo.Retry(context.Background(), operations.ActionRequest{RequestedAt: time.Now()}); err == nil {
		t.Fatal("invalid retry must fail")
	}
}

// TestMigration024AddsOnlyOperatorAuditFacts 验证对应场景下的正常路径与失败路径。
func TestMigration024AddsOnlyOperatorAuditFacts(t *testing.T) {
	path := filepath.Join("..", "migrations", "024_agent_runtime_operations.sql")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(data))
	for _, token := range []string{"create table if not exists agent_runtime_operator_actions", "unique (workspace_id, request_id)", "action_type", "target_id", "operator_id", "reason", "result_json"} {
		if !strings.Contains(lower, token) {
			t.Fatalf("migration missing %s", token)
		}
	}
	for _, forbidden := range []string{"drop table", "drop column", "truncate ", "delete from", "alter column"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("migration must be expand-only; found %s", forbidden)
		}
	}
}
