package normalize

import (
	"testing"

	"agent_project/apps/server/internal/document/parser"
)

// TestNormalizeResumeBuildsOrderedProjectSections 验证`normalizeResumeBuildsOrderedProjectSections`在特定边界条件下的行为，防止同类回归。
func TestNormalizeResumeBuildsOrderedProjectSections(t *testing.T) {
	doc := parser.ParsedDocument{
		SourceFormat: "pdf",
		Blocks: []parser.Block{
			{Type: parser.BlockParagraph, Text: "CampusHub校园活动平台（2026.2-2026.3）"},
			{Type: parser.BlockParagraph, Text: "Go Redis gRPC"},
			{Type: parser.BlockParagraph, Text: "项目描述："},
			{Type: parser.BlockParagraph, Text: "面向校园活动的统一平台"},
			{Type: parser.BlockParagraph, Text: "工作内容："},
			{Type: parser.BlockParagraph, Text: "负责活动发布、报名与签到链路"},
		},
	}

	normalized := NewService().Normalize(doc)
	if len(normalized.Sections) != 1 {
		t.Fatalf("expected 1 project section, got %d", len(normalized.Sections))
	}

	section := normalized.Sections[0]
	if section.Type != SectionTypeProject {
		t.Fatalf("expected project section type, got %q", section.Type)
	}
	if section.Order != 1 {
		t.Fatalf("expected section order 1, got %d", section.Order)
	}
	if section.CanonicalEntityName != "CampusHub校园活动平台" {
		t.Fatalf("unexpected canonical entity name %q", section.CanonicalEntityName)
	}
	if len(section.Aliases) < 2 {
		t.Fatalf("expected aliases for original title and canonical title, got %#v", section.Aliases)
	}
	if len(section.TechStack) != 3 {
		t.Fatalf("expected 3 tech stack entries, got %#v", section.TechStack)
	}
	if section.Metadata["low_confidence"] == true {
		t.Fatalf("expected project section to be high confidence, got %#v", section.Metadata)
	}
}

// TestNormalizeMarksHeadingOnlyProjectAsLowConfidence 验证`normalize`在流程控制路径下的行为，防止同类回归。
func TestNormalizeMarksHeadingOnlyProjectAsLowConfidence(t *testing.T) {
	doc := parser.ParsedDocument{
		SourceFormat: "pdf",
		Blocks: []parser.Block{
			{Type: parser.BlockParagraph, Text: "CampusHub校园活动平台（2026.2-2026.3）"},
			{Type: parser.BlockParagraph, Text: "项目描述："},
		},
	}

	normalized := NewService().Normalize(doc)
	if len(normalized.Sections) != 1 {
		t.Fatalf("expected 1 project section, got %d", len(normalized.Sections))
	}

	section := normalized.Sections[0]
	if section.Metadata["low_confidence"] != true {
		t.Fatalf("expected low_confidence=true, got %#v", section.Metadata)
	}
	if section.Metadata["quality_flag"] != "heading_only" {
		t.Fatalf("expected heading_only quality flag, got %#v", section.Metadata)
	}
}
