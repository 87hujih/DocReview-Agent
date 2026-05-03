package assistant

import (
	"strings"
	"testing"
)

// TestBuildGroundedAnalysisInputUsesCanonicalSectionContent 验证`buildGroundedAnalysisInputUsesCanonicalSectionContent`在特定边界条件下的行为，防止同类回归。
func TestBuildGroundedAnalysisInputUsesCanonicalSectionContent(t *testing.T) {
	result, err := BuildGroundedAnalysisInput(GroundedAnalysisInput{
		Message: "这个项目的问题是什么",
		ReadResult: CanonicalReadResult{
			Mode:         CanonicalReadModeSection,
			SectionID:    "section-3",
			SectionType:  "project",
			SectionTitle: "慢跑计划",
			Content:      "这是第三个项目的完整正文",
		},
	})
	if err != nil {
		t.Fatalf("build grounded analysis input: %v", err)
	}
	if result == nil {
		t.Fatal("expected grounded analysis result")
	}
	if !strings.Contains(result.AnalysisContext, "这是第三个项目的完整正文") {
		t.Fatalf("expected canonical section content in analysis context, got %q", result.AnalysisContext)
	}
	if !strings.Contains(result.AnalysisContext, "这个项目的问题是什么") {
		t.Fatalf("expected user message in analysis context, got %q", result.AnalysisContext)
	}
}
