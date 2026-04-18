package assistant

import "testing"

func TestGroundedAnalysisInputBuilderBuildsFromLocatedSection(t *testing.T) {
	builder := GroundedAnalysisInputBuilder{}

	got := builder.Build(
		&LocatedSection{SectionID: "section-3", SectionType: "project", SectionOrder: 3, Title: "第三个项目"},
		&SectionReadResult{SectionID: "section-3", SectionType: "project", SectionOrder: 3, Title: "第三个项目", Content: "原始第三个项目正文"},
		"第三个项目怎么优化",
	)
	if got == nil {
		t.Fatal("expected grounded analysis input, got nil")
	}
	if got.SectionID != "section-3" || got.SectionType != "project" || got.SectionOrder != 3 {
		t.Fatalf("expected section metadata to persist, got %#v", got)
	}
	if got.SectionText != "原始第三个项目正文" || got.UserInstruction != "第三个项目怎么优化" {
		t.Fatalf("expected section text and user instruction, got %#v", got)
	}
}
