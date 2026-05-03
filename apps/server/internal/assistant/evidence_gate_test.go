package assistant

import (
	"testing"

	"agent_project/apps/server/internal/knowledge/citation"
)

// TestEvidenceGateRejectsOrdinalQuestionWithoutConcreteSection 验证`evidenceGate`在非法输入或失败路径下的行为，防止同类回归。
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

// TestEvidenceGateRejectsHeadingOnlyCitation 验证`evidenceGate`在非法输入或失败路径下的行为，防止同类回归。
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

// TestEvidenceGateAcceptsConcreteSectionEvidence 验证`evidenceGate`在合法输入或兼容路径下的行为，防止同类回归。
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

// TestEvidenceGateAcceptsCanonicalSectionRead 验证`evidenceGateAcceptsCanonicalSectionRead`在合法输入或兼容路径下的行为，防止同类回归。
func TestEvidenceGateAcceptsCanonicalSectionRead(t *testing.T) {
	ok, reason := EvaluateEvidenceQuality(EvidenceEvaluationInput{
		QueryIntent: "detail_by_entity",
		ResolvedTarget: &ResolvedReference{
			SectionID:   "section-campushub",
			SectionType: "project",
			EntityName:  "CampusHub",
		},
		CanonicalRead: &CanonicalReadResult{
			Mode:        CanonicalReadModeSection,
			SectionID:   "section-campushub",
			SectionType: "project",
			Content:     "负责活动发布、报名与签到全流程。",
		},
	})
	if !ok {
		t.Fatalf("expected canonical read to pass evidence gate, got reason %q", reason)
	}
}
