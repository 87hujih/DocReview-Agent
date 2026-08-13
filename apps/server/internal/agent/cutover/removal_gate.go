package cutover

import (
	"fmt"
	"sort"
	"strings"
)

const (
	minimumReviewedShadowComparisons = 100
	minimumShadowMatchRate           = 0.99
	maximumShadowUnavailableRate     = 0
	minimumDurableRuns               = 20
	minimumDurableSuccessRate        = 0.99
)

type LegacyRemovalThresholds struct {
	MinimumShadowComparisons     int     `json:"minimum_shadow_comparisons"`
	MinimumShadowMatchRate       float64 `json:"minimum_shadow_match_rate"`
	MaximumShadowUnavailableRate float64 `json:"maximum_shadow_unavailable_rate"`
	MinimumDurableRuns           int     `json:"minimum_durable_runs"`
	MinimumDurableSuccessRate    float64 `json:"minimum_durable_success_rate"`
}

type CohortCounts struct {
	Total       int `json:"total"`
	Reviewed    int `json:"reviewed"`
	Matched     int `json:"matched"`
	Diverged    int `json:"diverged"`
	Unavailable int `json:"unavailable"`
}

type DurableCounts struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

type LegacyRemovalEvidence struct {
	SchemaVersion              string                  `json:"schema_version"`
	EvidenceVersion            string                  `json:"evidence_version"`
	Thresholds                 LegacyRemovalThresholds `json:"thresholds"`
	Shadow                     CohortCounts            `json:"shadow"`
	Durable                    DurableCounts           `json:"durable"`
	ProjectionDeadLetters      int                     `json:"projection_dead_letters"`
	ProjectionMetricsVerified  bool                    `json:"projection_metrics_verified"`
	RetrievalProfileMismatches int                     `json:"retrieval_profile_mismatches"`
	RetrievalMetricsVerified   bool                    `json:"retrieval_metrics_verified"`
	DatabaseRoundTripsVerified bool                    `json:"database_round_trips_verified"`
	ProtectedIngressVerified   bool                    `json:"protected_ingress_verified"`
	DurableCanaryVerified      bool                    `json:"durable_canary_verified"`
	DataCompatibilityVerified  bool                    `json:"data_compatibility_verified"`
	RollbackVerified           bool                    `json:"rollback_verified"`
	PublicBehaviorVerified     bool                    `json:"public_behavior_verified"`
	SecurityAcceptanceVerified bool                    `json:"security_acceptance_verified"`
	AcceptanceScenarios        map[string]bool         `json:"acceptance_scenarios"`
	ProductionCallers          []string                `json:"production_callers"`
	ProductionCallerAudit      bool                    `json:"production_caller_audit_verified"`
	ConfigurationDependencies  []string                `json:"configuration_dependencies"`
	ConfigurationAudit         bool                    `json:"configuration_dependency_audit_verified"`
}

type LegacyRemovalReport struct {
	SchemaVersion         string   `json:"schema_version"`
	ReportVersion         string   `json:"report_version"`
	EvidenceVersion       string   `json:"evidence_version"`
	Eligible              bool     `json:"eligible"`
	ShadowMatchRate       float64  `json:"shadow_match_rate"`
	ShadowUnavailableRate float64  `json:"shadow_unavailable_rate"`
	DurableSuccessRate    float64  `json:"durable_success_rate"`
	Blockers              []string `json:"blockers"`
}

var requiredRemovalScenarios = []string{
	"offline_evaluation",
	"worker_crash_lease_recovery",
	"request_tool_commit_idempotency",
	"stream_interruption_recovery",
	"approval_approve_reject_duplicate",
	"approval_commit_consistency",
	"patch_version_hash_conflict",
	"outbox_retry_dead_letter_replay",
	"cross_workspace_isolation",
	"canonical_ast_projection_reconciliation",
	"durable_to_legacy_rollback_drain",
	"migrations_021_024_postgres_round_trip",
}

// EvaluateLegacyRemoval 执行该函数负责的核心处理逻辑。
func EvaluateLegacyRemoval(evidence LegacyRemovalEvidence) (LegacyRemovalReport, error) {
	if evidence.SchemaVersion != "1.1" || strings.TrimSpace(evidence.EvidenceVersion) == "" {
		return LegacyRemovalReport{}, fmt.Errorf("不支持或缺少旧版移除证据标识")
	}
	thresholds := evidence.Thresholds
	if thresholds.MinimumShadowComparisons <= 0 || thresholds.MinimumDurableRuns <= 0 ||
		!rate(thresholds.MinimumShadowMatchRate) || !rate(thresholds.MaximumShadowUnavailableRate) ||
		!rate(thresholds.MinimumDurableSuccessRate) || !meetsRemovalSafetyFloor(thresholds) {
		return LegacyRemovalReport{}, fmt.Errorf("旧版移除阈值无效")
	}
	if evidence.Shadow.Total < 0 || evidence.Shadow.Reviewed < 0 || evidence.Shadow.Reviewed > evidence.Shadow.Total ||
		evidence.Shadow.Matched < 0 || evidence.Shadow.Diverged < 0 || evidence.Shadow.Unavailable < 0 ||
		evidence.Shadow.Matched+evidence.Shadow.Diverged+evidence.Shadow.Unavailable != evidence.Shadow.Reviewed {
		return LegacyRemovalReport{}, fmt.Errorf("影子分组数量无效")
	}
	if evidence.Durable.Total < 0 || evidence.Durable.Succeeded < 0 || evidence.Durable.Failed < 0 || evidence.Durable.Cancelled < 0 ||
		evidence.Durable.Succeeded+evidence.Durable.Failed+evidence.Durable.Cancelled > evidence.Durable.Total {
		return LegacyRemovalReport{}, fmt.Errorf("持久化的分组数量无效")
	}
	if evidence.ProjectionDeadLetters < 0 || evidence.RetrievalProfileMismatches < 0 {
		return LegacyRemovalReport{}, fmt.Errorf("生产指标数量无效")
	}
	report := LegacyRemovalReport{
		SchemaVersion: "1.1", ReportVersion: "legacy-removal-report-v2",
		EvidenceVersion:       evidence.EvidenceVersion,
		ShadowMatchRate:       fraction(evidence.Shadow.Matched, evidence.Shadow.Reviewed),
		ShadowUnavailableRate: fraction(evidence.Shadow.Unavailable, evidence.Shadow.Reviewed),
		DurableSuccessRate:    fraction(evidence.Durable.Succeeded, evidence.Durable.Total),
	}
	if evidence.Shadow.Reviewed < thresholds.MinimumShadowComparisons {
		report.Blockers = append(report.Blockers, "reviewed shadow comparison sample is below threshold")
	}
	if report.ShadowMatchRate < thresholds.MinimumShadowMatchRate {
		report.Blockers = append(report.Blockers, "shadow match rate is below threshold")
	}
	if report.ShadowUnavailableRate > thresholds.MaximumShadowUnavailableRate {
		report.Blockers = append(report.Blockers, "shadow unavailable rate exceeds threshold")
	}
	if evidence.Durable.Total < thresholds.MinimumDurableRuns {
		report.Blockers = append(report.Blockers, "durable cohort sample is below threshold")
	}
	if report.DurableSuccessRate < thresholds.MinimumDurableSuccessRate {
		report.Blockers = append(report.Blockers, "durable success rate is below threshold")
	}
	if evidence.ProjectionDeadLetters != 0 {
		report.Blockers = append(report.Blockers, "projection dead letters are present")
	}
	if !evidence.ProjectionMetricsVerified {
		report.Blockers = append(report.Blockers, "projection dead-letter metrics are unverified")
	}
	if evidence.RetrievalProfileMismatches != 0 {
		report.Blockers = append(report.Blockers, "retrieval profile mismatches are present")
	}
	if !evidence.RetrievalMetricsVerified {
		report.Blockers = append(report.Blockers, "retrieval profile-mismatch metrics are unverified")
	}
	checks := []struct {
		passed  bool
		blocker string
	}{
		{evidence.DatabaseRoundTripsVerified, "authorized PostgreSQL round trips are unverified"},
		{evidence.ProtectedIngressVerified, "protected ingress is unverified"},
		{evidence.DurableCanaryVerified, "durable canary is unverified"},
		{evidence.DataCompatibilityVerified, "data compatibility is unverified"},
		{evidence.RollbackVerified, "rollback is unverified"},
		{evidence.PublicBehaviorVerified, "public behavior compatibility is unverified"},
		{evidence.SecurityAcceptanceVerified, "security acceptance is unverified"},
	}
	for _, check := range checks {
		if !check.passed {
			report.Blockers = append(report.Blockers, check.blocker)
		}
	}
	for _, scenario := range requiredRemovalScenarios {
		if !evidence.AcceptanceScenarios[scenario] {
			report.Blockers = append(report.Blockers, "acceptance scenario is unverified: "+scenario)
		}
	}
	if values := nonBlank(evidence.ProductionCallers); len(values) > 0 {
		report.Blockers = append(report.Blockers, fmt.Sprintf("production callers remain (%d)", len(values)))
		for _, value := range values {
			report.Blockers = append(report.Blockers, "production caller remains: "+value)
		}
	}
	if !evidence.ProductionCallerAudit {
		report.Blockers = append(report.Blockers, "production caller audit is unverified")
	}
	if values := nonBlank(evidence.ConfigurationDependencies); len(values) > 0 {
		report.Blockers = append(report.Blockers, fmt.Sprintf("configuration dependencies remain (%d)", len(values)))
		for _, value := range values {
			report.Blockers = append(report.Blockers, "configuration dependency remains: "+value)
		}
	}
	if !evidence.ConfigurationAudit {
		report.Blockers = append(report.Blockers, "configuration dependency audit is unverified")
	}
	report.Eligible = len(report.Blockers) == 0
	return report, nil
}

// fraction 执行该函数负责的核心处理逻辑。
func fraction(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

// rate 执行该函数负责的核心处理逻辑。
func rate(value float64) bool { return value >= 0 && value <= 1 }

// meetsRemovalSafetyFloor 禁止证据输入静默降低生产删除门槛；调用方只能保持或提高门槛。
func meetsRemovalSafetyFloor(thresholds LegacyRemovalThresholds) bool {
	return thresholds.MinimumShadowComparisons >= minimumReviewedShadowComparisons &&
		thresholds.MinimumShadowMatchRate >= minimumShadowMatchRate &&
		thresholds.MaximumShadowUnavailableRate <= maximumShadowUnavailableRate &&
		thresholds.MinimumDurableRuns >= minimumDurableRuns &&
		thresholds.MinimumDurableSuccessRate >= minimumDurableSuccessRate
}

// nonBlank 执行该函数负责的核心处理逻辑。
func nonBlank(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
