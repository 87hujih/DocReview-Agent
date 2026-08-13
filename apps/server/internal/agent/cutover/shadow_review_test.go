package cutover_test

import (
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/cutover"
	"agent_project/apps/server/internal/agent/operations"
)

func TestShadowReviewProducesCountsOnlyFromCompleteBoundExport(t *testing.T) {
	export := shadowComparisonExport()
	manifest, err := cutover.NewShadowReviewTemplate(export, reviewDigest("a"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.ReviewID = "review-1"
	manifest.ReviewerID = "operator-42"
	manifest.ReviewedAt = export.CollectedAt.Add(time.Minute)
	for index := range manifest.Entries {
		manifest.Entries[index].Decision = cutover.ReviewConfirmed
		if manifest.Entries[index].Status != "matched" {
			manifest.Entries[index].Notes = "reviewed divergence or unavailable trace"
		}
	}
	report, err := cutover.EvaluateShadowReview(export, reviewDigest("a"), manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || !report.EligibleForEvidence || report.Reviewed != 3 ||
		report.Matched != 1 || report.Diverged != 1 || report.Unavailable != 1 || len(report.Blockers) != 0 {
		t.Fatalf("unexpected complete review report: %#v", report)
	}
}

func TestShadowReviewRejectsTamperingDuplicatesAndWrongCohort(t *testing.T) {
	export := shadowComparisonExport()
	template, err := cutover.NewShadowReviewTemplate(export, reviewDigest("b"))
	if err != nil {
		t.Fatal(err)
	}
	template.ReviewID = "review-2"
	template.ReviewerID = "operator-42"
	template.ReviewedAt = export.CollectedAt.Add(time.Minute)
	for index := range template.Entries {
		template.Entries[index].Decision = cutover.ReviewConfirmed
		template.Entries[index].Notes = "reviewed"
	}
	for _, test := range []struct {
		name   string
		mutate func(*cutover.ShadowReviewManifest)
	}{
		{"wrong digest", func(value *cutover.ShadowReviewManifest) { value.ComparisonExportSHA256 = reviewDigest("c") }},
		{"wrong cohort", func(value *cutover.ShadowReviewManifest) { value.ResourceID = "resource-other" }},
		{"wrong run", func(value *cutover.ShadowReviewManifest) { value.Entries[0].RunID = "run-other" }},
		{"wrong hash", func(value *cutover.ShadowReviewManifest) { value.Entries[0].LegacyResultHash = reviewDigest("d") }},
		{"duplicate id", func(value *cutover.ShadowReviewManifest) {
			value.Entries[1].ComparisonID = value.Entries[0].ComparisonID
		}},
		{"unknown id", func(value *cutover.ShadowReviewManifest) { value.Entries[0].ComparisonID = "comparison-other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := template
			candidate.Entries = append([]cutover.ShadowReviewEntry(nil), template.Entries...)
			test.mutate(&candidate)
			if _, err := cutover.EvaluateShadowReview(export, reviewDigest("b"), candidate); err == nil {
				t.Fatal("tampered review must be rejected")
			}
		})
	}
}

func TestShadowReviewFailsClosedForIncompleteDisputedOrTruncatedReview(t *testing.T) {
	export := shadowComparisonExport()
	manifest, err := cutover.NewShadowReviewTemplate(export, reviewDigest("e"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.ReviewID = "review-3"
	manifest.ReviewerID = "operator-42"
	manifest.ReviewedAt = export.CollectedAt.Add(time.Minute)
	manifest.Entries[0].Decision = cutover.ReviewConfirmed
	manifest.Entries[1].Decision = cutover.ReviewDisputed
	manifest.Entries[1].Notes = "hash status disagrees with retained trace"
	manifest.Entries = manifest.Entries[:2]
	export.Limit = len(export.Comparisons)
	report, err := cutover.EvaluateShadowReview(export, reviewDigest("e"), manifest)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(report.Blockers, "\n")
	if report.Complete || report.EligibleForEvidence || report.Reviewed != 2 ||
		!strings.Contains(joined, "truncated") || !strings.Contains(joined, "missing") || !strings.Contains(joined, "disputed") {
		t.Fatalf("incomplete review must fail closed: %#v", report)
	}
}

func shadowComparisonExport() operations.ComparisonList {
	collected := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
	makeComparison := func(id, requestID, status string, createdAt time.Time) operations.ComparisonView {
		item := operations.ComparisonView{
			ID: id, WorkspaceID: "workspace-1", ResourceID: "resource-1", RequestID: requestID,
			ComparisonKind: "public_turn", Status: status,
			LegacyResultHash: reviewDigest("1"), LegacyEventHash: reviewDigest("2"), LegacyDTOHash: reviewDigest("3"),
			DetailsJSON: []byte(`{"review_fixture":true}`), CreatedAt: createdAt,
		}
		if status != "unavailable" {
			item.TypedResultHash = item.LegacyResultHash
			item.TypedEventHash = item.LegacyEventHash
			item.TypedDTOHash = item.LegacyDTOHash
			if status == "diverged" {
				item.TypedResultHash = reviewDigest("4")
				item.TypedEventHash = reviewDigest("5")
				item.TypedDTOHash = reviewDigest("6")
			}
		}
		return item
	}
	return operations.ComparisonList{
		SchemaVersion: "1.1", WorkspaceID: "workspace-1", ResourceID: "resource-1",
		WindowSeconds: 3600, CollectedAt: collected, Limit: 200,
		Comparisons: []operations.ComparisonView{
			makeComparison("comparison-1", "request-1", "matched", collected.Add(-30*time.Minute)),
			makeComparison("comparison-2", "request-2", "diverged", collected.Add(-20*time.Minute)),
			makeComparison("comparison-3", "request-3", "unavailable", collected.Add(-10*time.Minute)),
		},
	}
}

func reviewDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
