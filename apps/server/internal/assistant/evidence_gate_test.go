package assistant

import (
	"testing"

	"agent_project/apps/server/internal/knowledge/citation"
)

func TestEvidenceGateRejectsOrdinalQuestionWithoutConcreteSection(t *testing.T) {
	ok, reason := EvaluateEvidenceQuality(EvidenceEvaluationInput{
		QueryIntent: "detail_by_ordinal",
		Citations: []citation.Citation{
			{SectionTitle: "全文", Snippet: "项目描述："},
		},
	})
	if ok {
		t.Fatal("expected low quality evidence")
	}
	if reason == "" {
		t.Fatal("expected rejection reason")
	}
}

func TestEvidenceGateRejectsHeadingOnlyCitation(t *testing.T) {
	ok, reason := EvaluateEvidenceQuality(EvidenceEvaluationInput{
		QueryIntent: "detail_by_entity",
		ResolvedTarget: &ResolvedReference{
			SectionID:   "section-campushub",
			SectionType: "project",
			EntityName:  "CampusHub",
		},
		Citations: []citation.Citation{
			{
				SectionID:    "section-campushub",
				SectionType:  "project",
				SectionTitle: "CampusHub",
				Snippet:      "项目描述：",
			},
		},
	})
	if ok {
		t.Fatal("expected heading-only evidence to be rejected")
	}
	if reason != "heading_only" {
		t.Fatalf("expected heading_only, got %q", reason)
	}
}

func TestEvidenceGateAcceptsConcreteSectionEvidence(t *testing.T) {
	ok, reason := EvaluateEvidenceQuality(EvidenceEvaluationInput{
		QueryIntent: "detail_by_entity",
		ResolvedTarget: &ResolvedReference{
			SectionID:   "section-campushub",
			SectionType: "project",
			EntityName:  "CampusHub",
		},
		Citations: []citation.Citation{
			{
				SectionID:    "section-campushub",
				SectionType:  "project",
				SectionTitle: "CampusHub",
				Snippet:      "负责活动发布、报名与签到全流程。",
			},
		},
	})
	if !ok {
		t.Fatalf("expected concrete section evidence to pass, got reason %q", reason)
	}
}
