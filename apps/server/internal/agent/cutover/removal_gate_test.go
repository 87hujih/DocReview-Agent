package cutover_test

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/cutover"
)

// TestLegacyRemovalGateRequiresCohortEvidenceAndNoProductionDependencies 验证对应场景下的正常路径与失败路径。
func TestLegacyRemovalGateRequiresCohortEvidenceAndNoProductionDependencies(t *testing.T) {
	report, err := cutover.EvaluateLegacyRemoval(cutover.LegacyRemovalEvidence{
		SchemaVersion: "1.1", EvidenceVersion: "legacy-removal-evidence-v2",
		Thresholds: cutover.LegacyRemovalThresholds{
			MinimumShadowComparisons: 100, MinimumShadowMatchRate: 0.99,
			MaximumShadowUnavailableRate: 0, MinimumDurableRuns: 20, MinimumDurableSuccessRate: 0.99,
		},
		Shadow:                     cutover.CohortCounts{Total: 120, Reviewed: 120, Matched: 120},
		Durable:                    cutover.DurableCounts{Total: 25, Succeeded: 25},
		ProjectionMetricsVerified:  true,
		RetrievalMetricsVerified:   true,
		DatabaseRoundTripsVerified: true, ProtectedIngressVerified: true, DurableCanaryVerified: true,
		DataCompatibilityVerified: true, RollbackVerified: true, PublicBehaviorVerified: true, SecurityAcceptanceVerified: true,
		AcceptanceScenarios: map[string]bool{
			"offline_evaluation": true, "worker_crash_lease_recovery": true, "request_tool_commit_idempotency": true,
			"stream_interruption_recovery": true, "approval_approve_reject_duplicate": true,
			"approval_commit_consistency": true, "patch_version_hash_conflict": true,
			"outbox_retry_dead_letter_replay": true, "cross_workspace_isolation": true,
			"canonical_ast_projection_reconciliation": true, "durable_to_legacy_rollback_drain": true,
			"migrations_021_024_postgres_round_trip": true,
		},
		ProductionCallers:         []string{"apps/server/cmd/server/main.go: workflow.NewOrchestratorRunner"},
		ProductionCallerAudit:     true,
		ConfigurationDependencies: []string{"AGENT_RUNTIME_MODE=legacy"},
		ConfigurationAudit:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Eligible {
		t.Fatal("production callers and legacy configuration must block removal")
	}
	joined := strings.Join(report.Blockers, "\n")
	if !strings.Contains(joined, "apps/server/cmd/server/main.go: workflow.NewOrchestratorRunner") ||
		!strings.Contains(joined, "AGENT_RUNTIME_MODE=legacy") {
		t.Fatalf("removal blockers are incomplete: %#v", report)
	}
}

// TestLegacyRemovalGatePassesOnlyCompleteEvidence 验证对应场景下的正常路径与失败路径。
func TestLegacyRemovalGatePassesOnlyCompleteEvidence(t *testing.T) {
	report, err := cutover.EvaluateLegacyRemoval(cutover.LegacyRemovalEvidence{
		SchemaVersion: "1.1", EvidenceVersion: "legacy-removal-evidence-v2",
		Thresholds: cutover.LegacyRemovalThresholds{
			MinimumShadowComparisons: 100, MinimumShadowMatchRate: 0.99,
			MaximumShadowUnavailableRate: 0, MinimumDurableRuns: 20, MinimumDurableSuccessRate: 0.99,
		},
		Shadow:                     cutover.CohortCounts{Total: 100, Reviewed: 100, Matched: 100},
		Durable:                    cutover.DurableCounts{Total: 20, Succeeded: 20},
		ProjectionMetricsVerified:  true,
		RetrievalMetricsVerified:   true,
		DatabaseRoundTripsVerified: true, ProtectedIngressVerified: true, DurableCanaryVerified: true,
		DataCompatibilityVerified: true, RollbackVerified: true, PublicBehaviorVerified: true, SecurityAcceptanceVerified: true,
		AcceptanceScenarios: map[string]bool{
			"offline_evaluation": true, "worker_crash_lease_recovery": true, "request_tool_commit_idempotency": true,
			"stream_interruption_recovery": true, "approval_approve_reject_duplicate": true,
			"approval_commit_consistency": true, "patch_version_hash_conflict": true,
			"outbox_retry_dead_letter_replay": true, "cross_workspace_isolation": true,
			"canonical_ast_projection_reconciliation": true, "durable_to_legacy_rollback_drain": true,
			"migrations_021_024_postgres_round_trip": true,
		},
		ProductionCallerAudit: true,
		ConfigurationAudit:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Eligible || len(report.Blockers) != 0 || report.ShadowMatchRate != 1 || report.DurableSuccessRate != 1 {
		t.Fatalf("complete evidence should pass: %#v", report)
	}
}

// TestLegacyRemovalGateRejectsThresholdsBelowSafetyFloor 验证删除证据不能通过降低固定生产门槛来获得资格。
func TestLegacyRemovalGateRejectsThresholdsBelowSafetyFloor(t *testing.T) {
	_, err := cutover.EvaluateLegacyRemoval(cutover.LegacyRemovalEvidence{
		SchemaVersion: "1.1", EvidenceVersion: "legacy-removal-evidence-v2",
		Thresholds: cutover.LegacyRemovalThresholds{
			MinimumShadowComparisons: 1, MinimumShadowMatchRate: 0,
			MaximumShadowUnavailableRate: 1, MinimumDurableRuns: 1, MinimumDurableSuccessRate: 0,
		},
		Shadow:  cutover.CohortCounts{Total: 1, Reviewed: 1, Matched: 1},
		Durable: cutover.DurableCounts{Total: 1, Succeeded: 1},
	})
	if err == nil {
		t.Fatal("lowering the fixed production removal thresholds must be rejected")
	}
}

// TestLegacyRemovalGateRequiresReviewedShadowComparisons 验证未复核样本不能计入 shadow 删除门槛。
func TestLegacyRemovalGateRequiresReviewedShadowComparisons(t *testing.T) {
	report, err := cutover.EvaluateLegacyRemoval(cutover.LegacyRemovalEvidence{
		SchemaVersion: "1.1", EvidenceVersion: "legacy-removal-evidence-v2",
		Thresholds: cutover.LegacyRemovalThresholds{
			MinimumShadowComparisons: 100, MinimumShadowMatchRate: 0.99,
			MaximumShadowUnavailableRate: 0, MinimumDurableRuns: 20, MinimumDurableSuccessRate: 0.99,
		},
		Shadow:                     cutover.CohortCounts{Total: 100, Reviewed: 99, Matched: 99},
		Durable:                    cutover.DurableCounts{Total: 20, Succeeded: 20},
		ProjectionMetricsVerified:  true,
		RetrievalMetricsVerified:   true,
		DatabaseRoundTripsVerified: true, ProtectedIngressVerified: true, DurableCanaryVerified: true,
		DataCompatibilityVerified: true, RollbackVerified: true, PublicBehaviorVerified: true,
		AcceptanceScenarios: map[string]bool{
			"offline_evaluation": true, "worker_crash_recovery": true, "request_tool_commit_idempotency": true,
			"approval_commit_consistency": true, "cross_workspace_isolation": true,
		},
		ProductionCallerAudit: true,
		ConfigurationAudit:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Eligible || !strings.Contains(strings.Join(report.Blockers, "\n"), "reviewed shadow comparison") {
		t.Fatalf("unreviewed comparisons must not qualify: %#v", report)
	}
}

// TestLegacyRemovalGateDoesNotTreatMissingOperationalAuditsAsZero 验证缺失的生产指标和依赖审计不能按零值通过。
func TestLegacyRemovalGateDoesNotTreatMissingOperationalAuditsAsZero(t *testing.T) {
	report, err := cutover.EvaluateLegacyRemoval(cutover.LegacyRemovalEvidence{
		SchemaVersion: "1.1", EvidenceVersion: "legacy-removal-evidence-v2",
		Thresholds: cutover.LegacyRemovalThresholds{
			MinimumShadowComparisons: 100, MinimumShadowMatchRate: 0.99,
			MaximumShadowUnavailableRate: 0, MinimumDurableRuns: 20, MinimumDurableSuccessRate: 0.99,
		},
		Shadow:                     cutover.CohortCounts{Total: 100, Reviewed: 100, Matched: 100},
		Durable:                    cutover.DurableCounts{Total: 20, Succeeded: 20},
		DatabaseRoundTripsVerified: true, ProtectedIngressVerified: true, DurableCanaryVerified: true,
		DataCompatibilityVerified: true, RollbackVerified: true, PublicBehaviorVerified: true,
		AcceptanceScenarios: map[string]bool{
			"offline_evaluation": true, "worker_crash_recovery": true, "request_tool_commit_idempotency": true,
			"approval_commit_consistency": true, "cross_workspace_isolation": true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(report.Blockers, "\n")
	if report.Eligible || !strings.Contains(joined, "projection dead-letter metrics are unverified") ||
		!strings.Contains(joined, "production caller audit is unverified") {
		t.Fatalf("missing operational audits must block removal: %#v", report)
	}
}

// TestLegacyRemovalGateRequiresCompleteFailureConsistencyAndSecurityEvidence 验证局部验收不能替代完整故障、一致性和安全证据。
func TestLegacyRemovalGateRequiresCompleteFailureConsistencyAndSecurityEvidence(t *testing.T) {
	report, err := cutover.EvaluateLegacyRemoval(cutover.LegacyRemovalEvidence{
		SchemaVersion: "1.1", EvidenceVersion: "legacy-removal-evidence-v2",
		Thresholds: cutover.LegacyRemovalThresholds{
			MinimumShadowComparisons: 100, MinimumShadowMatchRate: 0.99,
			MaximumShadowUnavailableRate: 0, MinimumDurableRuns: 20, MinimumDurableSuccessRate: 0.99,
		},
		Shadow:                     cutover.CohortCounts{Total: 100, Reviewed: 100, Matched: 100},
		Durable:                    cutover.DurableCounts{Total: 20, Succeeded: 20},
		ProjectionMetricsVerified:  true,
		RetrievalMetricsVerified:   true,
		DatabaseRoundTripsVerified: true, ProtectedIngressVerified: true, DurableCanaryVerified: true,
		DataCompatibilityVerified: true, RollbackVerified: true, PublicBehaviorVerified: true,
		AcceptanceScenarios: map[string]bool{
			"offline_evaluation": true, "worker_crash_recovery": true, "request_tool_commit_idempotency": true,
			"approval_commit_consistency": true, "cross_workspace_isolation": true,
		},
		ProductionCallerAudit: true,
		ConfigurationAudit:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(report.Blockers, "\n")
	if report.Eligible || !strings.Contains(joined, "stream_interruption_recovery") ||
		!strings.Contains(joined, "security acceptance is unverified") {
		t.Fatalf("partial acceptance evidence must block removal: %#v", report)
	}
}
