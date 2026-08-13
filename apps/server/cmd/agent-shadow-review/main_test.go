package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/cutover"
	"agent_project/apps/server/internal/agent/operations"
)

func TestShadowReviewCLIProducesTemplateAndVerifiesCompleteReview(t *testing.T) {
	exportPath := writeJSON(t, "comparisons.json", cliComparisonExport())
	var templateOut, stderr bytes.Buffer
	if code := run([]string{"-action", "template", "-comparisons", exportPath}, &templateOut, &stderr); code != 0 {
		t.Fatalf("template exit=%d stderr=%s", code, stderr.String())
	}
	var manifest cutover.ShadowReviewManifest
	if err := json.Unmarshal(templateOut.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ComparisonExportSHA256 == "" || len(manifest.Entries) != 1 || manifest.Entries[0].Decision != "" {
		t.Fatalf("unexpected review template: %#v", manifest)
	}
	manifest.ReviewID = "review-1"
	manifest.ReviewerID = "operator-42"
	manifest.ReviewedAt = time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	manifest.Entries[0].Decision = cutover.ReviewConfirmed
	reviewPath := writeJSON(t, "review.json", manifest)
	var reportOut bytes.Buffer
	stderr.Reset()
	if code := run([]string{"-action", "verify", "-comparisons", exportPath, "-review", reviewPath}, &reportOut, &stderr); code != 0 {
		t.Fatalf("verify exit=%d stderr=%s", code, stderr.String())
	}
	var report cutover.ShadowReviewReport
	if json.Unmarshal(reportOut.Bytes(), &report) != nil || !report.EligibleForEvidence || report.Reviewed != 1 {
		t.Fatalf("unexpected review report: %s", reportOut.String())
	}
}

func TestShadowReviewCLIBlocksIncompleteReviewAndRejectsTamperedExport(t *testing.T) {
	export := cliComparisonExport()
	exportPath := writeJSON(t, "comparisons.json", export)
	var templateOut, stderr bytes.Buffer
	if code := run([]string{"-action", "template", "-comparisons", exportPath}, &templateOut, &stderr); code != 0 {
		t.Fatal(stderr.String())
	}
	var manifest cutover.ShadowReviewManifest
	if err := json.Unmarshal(templateOut.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	reviewPath := writeJSON(t, "review.json", manifest)
	var reportOut bytes.Buffer
	stderr.Reset()
	if code := run([]string{"-action", "verify", "-comparisons", exportPath, "-review", reviewPath}, &reportOut, &stderr); code != 1 {
		t.Fatalf("incomplete review exit=%d stderr=%s", code, stderr.String())
	}
	export.Comparisons[0].RequestID = "tampered-request"
	exportPath = writeJSON(t, "comparisons-tampered.json", export)
	reportOut.Reset()
	stderr.Reset()
	if code := run([]string{"-action", "verify", "-comparisons", exportPath, "-review", reviewPath}, &reportOut, &stderr); code != 2 || !strings.Contains(stderr.String(), "SHA-256") {
		t.Fatalf("tampered export exit=%d stderr=%s", code, stderr.String())
	}
}

func TestShadowReviewCLIRejectsUnknownOrTrailingJSON(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "invalid.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"1.1","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-action", "template", "-comparisons", path}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown JSON exit=%d stderr=%s", code, stderr.String())
	}
	if err := os.WriteFile(path, []byte(`{} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"-action", "template", "-comparisons", path}, &stdout, &stderr); code != 2 {
		t.Fatalf("trailing JSON exit=%d stderr=%s", code, stderr.String())
	}
}

func cliComparisonExport() operations.ComparisonList {
	hash := "sha256:" + strings.Repeat("a", 64)
	collected := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
	return operations.ComparisonList{
		SchemaVersion: "1.1", WorkspaceID: "workspace-1", ResourceID: "resource-1",
		WindowSeconds: 3600, CollectedAt: collected, Limit: 200,
		Comparisons: []operations.ComparisonView{{
			ID: "comparison-1", WorkspaceID: "workspace-1", ResourceID: "resource-1",
			RequestID: "request-1", ComparisonKind: "public_turn", Status: "matched",
			LegacyResultHash: hash, TypedResultHash: hash, LegacyEventHash: hash,
			TypedEventHash: hash, LegacyDTOHash: hash, TypedDTOHash: hash,
			DetailsJSON: []byte(`{"dto_match":true,"event_match":true}`), CreatedAt: collected.Add(-time.Minute),
		}},
	}
}

func writeJSON(t *testing.T, name string, value any) string {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
