package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/cutover"
)

// TestRunEmitsIneligibleReportWithConcreteLegacyDependency 验证 CLI 从显式证据生成可定位的删除阻塞报告。
func TestRunEmitsIneligibleReportWithConcreteLegacyDependency(t *testing.T) {
	evidence := cutover.LegacyRemovalEvidence{
		SchemaVersion: "1.1", EvidenceVersion: "legacy-removal-evidence-v2",
		Thresholds: cutover.LegacyRemovalThresholds{
			MinimumShadowComparisons: 100, MinimumShadowMatchRate: 0.99,
			MaximumShadowUnavailableRate: 0, MinimumDurableRuns: 20, MinimumDurableSuccessRate: 0.99,
		},
		ProductionCallers:     []string{"apps/server/cmd/server/main.go: workflow.NewOrchestratorRunner"},
		ProductionCallerAudit: true,
		ConfigurationAudit:    true,
	}
	body, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"-evidence", path}, &stdout, &stderr); code != 1 {
		t.Fatalf("expected ineligible exit 1, got %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"eligible": false`) ||
		!strings.Contains(stdout.String(), "workflow.NewOrchestratorRunner") {
		t.Fatalf("report is incomplete: %s", stdout.String())
	}
}

// TestRunReturnsZeroOnlyForCompleteEligibleEvidence 验证 CLI 只有在完整删除证据通过保险丝时返回成功。
func TestRunReturnsZeroOnlyForCompleteEligibleEvidence(t *testing.T) {
	evidence := cutover.LegacyRemovalEvidence{
		SchemaVersion: "1.1", EvidenceVersion: "legacy-removal-evidence-v2",
		Thresholds: cutover.LegacyRemovalThresholds{
			MinimumShadowComparisons: 100, MinimumShadowMatchRate: 0.99,
			MaximumShadowUnavailableRate: 0, MinimumDurableRuns: 20, MinimumDurableSuccessRate: 0.99,
		},
		Shadow:                     cutover.CohortCounts{Total: 100, Reviewed: 100, Matched: 100},
		Durable:                    cutover.DurableCounts{Total: 20, Succeeded: 20},
		ProjectionMetricsVerified:  true,
		RetrievalMetricsVerified:   true,
		DatabaseRoundTripsVerified: true,
		ProtectedIngressVerified:   true,
		DurableCanaryVerified:      true,
		DataCompatibilityVerified:  true,
		RollbackVerified:           true,
		PublicBehaviorVerified:     true,
		SecurityAcceptanceVerified: true,
		AcceptanceScenarios: map[string]bool{
			"offline_evaluation": true, "worker_crash_lease_recovery": true,
			"request_tool_commit_idempotency": true, "stream_interruption_recovery": true,
			"approval_approve_reject_duplicate": true, "approval_commit_consistency": true,
			"patch_version_hash_conflict": true, "outbox_retry_dead_letter_replay": true,
			"cross_workspace_isolation": true, "canonical_ast_projection_reconciliation": true,
			"durable_to_legacy_rollback_drain": true, "migrations_021_024_postgres_round_trip": true,
		},
		ProductionCallerAudit: true,
		ConfigurationAudit:    true,
	}
	body, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"-evidence", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("expected eligible exit 0, got %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"eligible": true`) {
		t.Fatalf("eligible report is missing: %s", stdout.String())
	}
}

// TestRunRejectsUnknownEvidenceFields 验证 CLI 不会忽略 evidence 合约外字段。
func TestRunRejectsUnknownEvidenceFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"1.1","evidence_version":"v2","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"-evidence", path}, &stdout, &stderr); code != 2 {
		t.Fatalf("expected invalid-evidence exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown field") || stdout.Len() != 0 {
		t.Fatalf("invalid evidence was not rejected strictly: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
