package cutover

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/operations"
)

type ReviewDecision string

const (
	ReviewConfirmed ReviewDecision = "confirmed"
	ReviewDisputed  ReviewDecision = "disputed"
)

type ShadowReviewEntry struct {
	ComparisonID     string         `json:"comparison_id"`
	RunID            string         `json:"run_id,omitempty"`
	RequestID        string         `json:"request_id"`
	Status           string         `json:"status"`
	LegacyResultHash string         `json:"legacy_result_hash,omitempty"`
	TypedResultHash  string         `json:"typed_result_hash,omitempty"`
	LegacyEventHash  string         `json:"legacy_event_hash,omitempty"`
	TypedEventHash   string         `json:"typed_event_hash,omitempty"`
	LegacyDTOHash    string         `json:"legacy_dto_hash,omitempty"`
	TypedDTOHash     string         `json:"typed_dto_hash,omitempty"`
	Decision         ReviewDecision `json:"decision"`
	Notes            string         `json:"notes,omitempty"`
}

type ShadowReviewManifest struct {
	SchemaVersion          string              `json:"schema_version"`
	ReviewID               string              `json:"review_id"`
	WorkspaceID            string              `json:"workspace_id"`
	ResourceID             string              `json:"resource_id"`
	ComparisonExportSHA256 string              `json:"comparison_export_sha256"`
	ReviewerID             string              `json:"reviewer_id"`
	ReviewedAt             time.Time           `json:"reviewed_at"`
	Entries                []ShadowReviewEntry `json:"entries"`
}

type ShadowReviewReport struct {
	SchemaVersion          string   `json:"schema_version"`
	ReviewID               string   `json:"review_id"`
	WorkspaceID            string   `json:"workspace_id"`
	ResourceID             string   `json:"resource_id"`
	ComparisonExportSHA256 string   `json:"comparison_export_sha256"`
	Total                  int      `json:"total"`
	Reviewed               int      `json:"reviewed"`
	Matched                int      `json:"matched"`
	Diverged               int      `json:"diverged"`
	Unavailable            int      `json:"unavailable"`
	Complete               bool     `json:"complete"`
	EligibleForEvidence    bool     `json:"eligible_for_evidence"`
	Blockers               []string `json:"blockers"`
}

// NewShadowReviewTemplate copies immutable export identities into an unreviewed manifest.
func NewShadowReviewTemplate(export operations.ComparisonList, exportDigest string) (ShadowReviewManifest, error) {
	if err := validateComparisonExport(export); err != nil {
		return ShadowReviewManifest{}, err
	}
	if !validSHA256(exportDigest) {
		return ShadowReviewManifest{}, fmt.Errorf("comparison export SHA-256 无效")
	}
	manifest := ShadowReviewManifest{
		SchemaVersion: "1.0", WorkspaceID: strings.TrimSpace(export.WorkspaceID),
		ResourceID: strings.TrimSpace(export.ResourceID), ComparisonExportSHA256: exportDigest,
		Entries: make([]ShadowReviewEntry, 0, len(export.Comparisons)),
	}
	for _, item := range export.Comparisons {
		manifest.Entries = append(manifest.Entries, reviewEntry(item))
	}
	return manifest, nil
}

// EvaluateShadowReview verifies complete, untampered human review metadata without mutating removal evidence.
func EvaluateShadowReview(export operations.ComparisonList, exportDigest string, manifest ShadowReviewManifest) (ShadowReviewReport, error) {
	if err := validateComparisonExport(export); err != nil {
		return ShadowReviewReport{}, err
	}
	if !validSHA256(exportDigest) {
		return ShadowReviewReport{}, fmt.Errorf("comparison export SHA-256 无效")
	}
	if manifest.SchemaVersion != "1.0" {
		return ShadowReviewReport{}, fmt.Errorf("不支持的 shadow review schema")
	}
	manifest.WorkspaceID = strings.TrimSpace(manifest.WorkspaceID)
	manifest.ResourceID = strings.TrimSpace(manifest.ResourceID)
	if manifest.WorkspaceID != export.WorkspaceID || manifest.ResourceID != export.ResourceID {
		return ShadowReviewReport{}, fmt.Errorf("shadow review cohort 与 comparison export 不匹配")
	}
	if manifest.ComparisonExportSHA256 != exportDigest {
		return ShadowReviewReport{}, fmt.Errorf("shadow review comparison export SHA-256 不匹配")
	}
	if !manifest.ReviewedAt.IsZero() && manifest.ReviewedAt.Before(export.CollectedAt) {
		return ShadowReviewReport{}, fmt.Errorf("shadow review 时间早于 comparison export")
	}

	report := ShadowReviewReport{
		SchemaVersion: "1.0", ReviewID: strings.TrimSpace(manifest.ReviewID),
		WorkspaceID: export.WorkspaceID, ResourceID: export.ResourceID,
		ComparisonExportSHA256: exportDigest, Total: len(export.Comparisons),
	}
	if report.Total == 0 {
		report.Blockers = append(report.Blockers, "comparison export is empty")
	}
	if len(export.Comparisons) >= export.Limit {
		report.Blockers = append(report.Blockers, "comparison export may be truncated; use a higher limit or narrower window")
	}
	if report.ReviewID == "" {
		report.Blockers = append(report.Blockers, "review_id is missing")
	}
	if strings.TrimSpace(manifest.ReviewerID) == "" {
		report.Blockers = append(report.Blockers, "reviewer_id is missing")
	}
	if manifest.ReviewedAt.IsZero() {
		report.Blockers = append(report.Blockers, "reviewed_at is missing")
	}

	sourceByID := make(map[string]operations.ComparisonView, len(export.Comparisons))
	for _, item := range export.Comparisons {
		sourceByID[item.ID] = item
	}
	reviewedIDs := make(map[string]struct{}, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		entry.ComparisonID = strings.TrimSpace(entry.ComparisonID)
		if _, duplicate := reviewedIDs[entry.ComparisonID]; duplicate {
			return ShadowReviewReport{}, fmt.Errorf("shadow review comparison_id 重复：%s", entry.ComparisonID)
		}
		source, exists := sourceByID[entry.ComparisonID]
		if !exists {
			return ShadowReviewReport{}, fmt.Errorf("shadow review 包含未知 comparison_id：%s", entry.ComparisonID)
		}
		if err := compareReviewEntry(source, entry); err != nil {
			return ShadowReviewReport{}, err
		}
		reviewedIDs[entry.ComparisonID] = struct{}{}
		switch entry.Decision {
		case ReviewConfirmed, ReviewDisputed:
			report.Reviewed++
			incrementReviewStatus(&report, source.Status)
		default:
			report.Blockers = append(report.Blockers, "comparison is unreviewed: "+source.ID)
			continue
		}
		if entry.Decision == ReviewDisputed {
			report.Blockers = append(report.Blockers, "comparison review is disputed: "+source.ID)
		}
		if (entry.Decision == ReviewDisputed || source.Status != "matched") && strings.TrimSpace(entry.Notes) == "" {
			report.Blockers = append(report.Blockers, "comparison review notes are missing: "+source.ID)
		}
	}
	for _, source := range export.Comparisons {
		if _, exists := reviewedIDs[source.ID]; !exists {
			report.Blockers = append(report.Blockers, "comparison review entry is missing: "+source.ID)
		}
	}
	sort.Strings(report.Blockers)
	report.Complete = report.Reviewed == report.Total && len(reviewedIDs) == report.Total
	report.EligibleForEvidence = report.Complete && len(report.Blockers) == 0
	return report, nil
}

func validateComparisonExport(export operations.ComparisonList) error {
	export.WorkspaceID = strings.TrimSpace(export.WorkspaceID)
	export.ResourceID = strings.TrimSpace(export.ResourceID)
	if export.SchemaVersion != "1.1" || export.WorkspaceID == "" || export.ResourceID == "" ||
		export.WindowSeconds < 60 || export.WindowSeconds > int64((30*24*time.Hour).Seconds()) || export.CollectedAt.IsZero() ||
		export.Limit < 1 || export.Limit > 1000 || len(export.Comparisons) > export.Limit {
		return fmt.Errorf("comparison export 元数据无效")
	}
	windowStart := export.CollectedAt.Add(-time.Duration(export.WindowSeconds) * time.Second)
	ids := make(map[string]struct{}, len(export.Comparisons))
	requests := make(map[string]struct{}, len(export.Comparisons))
	for _, item := range export.Comparisons {
		item.ID = strings.TrimSpace(item.ID)
		item.RequestID = strings.TrimSpace(item.RequestID)
		if item.ID == "" || item.RequestID == "" || item.WorkspaceID != export.WorkspaceID || item.ResourceID != export.ResourceID ||
			item.ComparisonKind != "public_turn" || item.CreatedAt.IsZero() || item.CreatedAt.Before(windowStart) || item.CreatedAt.After(export.CollectedAt) {
			return fmt.Errorf("comparison export row 无效：%s", item.ID)
		}
		if _, exists := ids[item.ID]; exists {
			return fmt.Errorf("comparison export id 重复：%s", item.ID)
		}
		if _, exists := requests[item.RequestID]; exists {
			return fmt.Errorf("comparison export request_id 重复：%s", item.RequestID)
		}
		ids[item.ID] = struct{}{}
		requests[item.RequestID] = struct{}{}
		if err := validateComparisonHashes(item); err != nil {
			return err
		}
		var details map[string]any
		if json.Unmarshal(item.DetailsJSON, &details) != nil || details == nil {
			return fmt.Errorf("comparison export details_json 无效：%s", item.ID)
		}
	}
	return nil
}

func validateComparisonHashes(item operations.ComparisonView) error {
	legacy := []string{item.LegacyResultHash, item.LegacyEventHash, item.LegacyDTOHash}
	typed := []string{item.TypedResultHash, item.TypedEventHash, item.TypedDTOHash}
	for _, value := range legacy {
		if !validSHA256(value) {
			return fmt.Errorf("comparison export legacy hash 无效：%s", item.ID)
		}
	}
	switch item.Status {
	case "matched", "diverged":
		for _, value := range typed {
			if !validSHA256(value) {
				return fmt.Errorf("comparison export typed hash 无效：%s", item.ID)
			}
		}
		allEqual := item.LegacyResultHash == item.TypedResultHash && item.LegacyEventHash == item.TypedEventHash && item.LegacyDTOHash == item.TypedDTOHash
		if (item.Status == "matched") != allEqual {
			return fmt.Errorf("comparison export status/hash 不一致：%s", item.ID)
		}
	case "unavailable":
		if item.TypedResultHash != "" || item.TypedEventHash != "" || item.TypedDTOHash != "" {
			return fmt.Errorf("unavailable comparison 包含 typed hash：%s", item.ID)
		}
	default:
		return fmt.Errorf("comparison export status 无效：%s", item.ID)
	}
	return nil
}

func reviewEntry(item operations.ComparisonView) ShadowReviewEntry {
	return ShadowReviewEntry{
		ComparisonID: item.ID, RunID: item.RunID, RequestID: item.RequestID, Status: item.Status,
		LegacyResultHash: item.LegacyResultHash, TypedResultHash: item.TypedResultHash,
		LegacyEventHash: item.LegacyEventHash, TypedEventHash: item.TypedEventHash,
		LegacyDTOHash: item.LegacyDTOHash, TypedDTOHash: item.TypedDTOHash,
	}
}

func compareReviewEntry(source operations.ComparisonView, entry ShadowReviewEntry) error {
	expected := reviewEntry(source)
	if strings.TrimSpace(entry.RunID) != expected.RunID || strings.TrimSpace(entry.RequestID) != expected.RequestID || entry.Status != expected.Status ||
		entry.LegacyResultHash != expected.LegacyResultHash || entry.TypedResultHash != expected.TypedResultHash ||
		entry.LegacyEventHash != expected.LegacyEventHash || entry.TypedEventHash != expected.TypedEventHash ||
		entry.LegacyDTOHash != expected.LegacyDTOHash || entry.TypedDTOHash != expected.TypedDTOHash {
		return fmt.Errorf("shadow review identity/status/hash 不匹配：%s", source.ID)
	}
	return nil
}

func incrementReviewStatus(report *ShadowReviewReport, status string) {
	switch status {
	case "matched":
		report.Matched++
	case "diverged":
		report.Diverged++
	case "unavailable":
		report.Unavailable++
	}
}

func validSHA256(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[7:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
