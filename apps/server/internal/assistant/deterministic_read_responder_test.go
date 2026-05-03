package assistant

import (
	"context"
	"testing"
)

// TestDeterministicReadResponderReturnsCanonicalSectionContent 验证`deterministicReadResponderReturnsCanonicalSectionContent`在特定边界条件下的行为，防止同类回归。
func TestDeterministicReadResponderReturnsCanonicalSectionContent(t *testing.T) {
	responder := NewDeterministicReadResponder()

	result, err := responder.Respond(context.Background(), DeterministicReadInput{
		Message: "把第三个项目先输出一遍",
		Intent:  ClassifyReadIntent("把第三个项目先输出一遍"),
		Located: &LocatedSection{
			Mode:        LocatedSectionModeSection,
			SectionID:   "section-3",
			SectionType: "project",
		},
		ReadResult: &CanonicalReadResult{
			Mode:         CanonicalReadModeSection,
			SectionID:    "section-3",
			SectionType:  "project",
			SectionTitle: "慢跑计划",
			Content:      "第三个项目正文",
		},
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if result == nil {
		t.Fatal("expected deterministic read result")
	}
	if result.Content != "第三个项目正文" {
		t.Fatalf("expected content %q, got %q", "第三个项目正文", result.Content)
	}
}

// TestDeterministicReadResponderFormatsSectionList 验证`deterministicReadResponderFormatsSectionList`在特定边界条件下的行为，防止同类回归。
func TestDeterministicReadResponderFormatsSectionList(t *testing.T) {
	responder := NewDeterministicReadResponder()

	result, err := responder.Respond(context.Background(), DeterministicReadInput{
		Message: "这份简历里有哪些项目",
		Intent:  ClassifyReadIntent("这份简历里有哪些项目"),
		Located: &LocatedSection{
			Mode:        LocatedSectionModeSectionList,
			SectionType: "project",
		},
		ReadResult: &CanonicalReadResult{
			Mode: CanonicalReadModeSectionList,
			Sections: []CanonicalReadSectionItem{
				{SectionID: "section-1", SectionType: "project", SectionTitle: "CampusHub", Ordinal: 1},
				{SectionID: "section-2", SectionType: "project", SectionTitle: "选课助手", Ordinal: 2},
			},
		},
	})
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if result == nil {
		t.Fatal("expected deterministic list result")
	}
	if result.Content != "1. CampusHub\n2. 选课助手" {
		t.Fatalf("expected formatted section list, got %q", result.Content)
	}
}
