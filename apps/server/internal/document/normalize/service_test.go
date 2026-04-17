package normalize

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/document/parser"
)

func TestNormalizeResumeBuildsProjectSections(t *testing.T) {
	doc := parser.ParsedDocument{
		SourceFormat: "pdf",
		Blocks: []parser.Block{
			{Type: parser.BlockParagraph, Text: "项目"},
			{Type: parser.BlockParagraph, Text: "CampusHub校园活动平台（2026.2-2026.3）"},
			{Type: parser.BlockParagraph, Text: "Go  go-zero  gRPC  Redis"},
			{Type: parser.BlockParagraph, Text: "项目描述："},
			{Type: parser.BlockParagraph, Text: "面向校园活动场景的平台"},
		},
	}

	normalized := NewService().Normalize(doc)
	project := findSectionByType(normalized.Sections, SectionTypeProject)
	if project == nil {
		t.Fatal("expected project section")
	}
	if !strings.Contains(project.Title, "CampusHub") {
		t.Fatalf("expected project title to contain CampusHub, got %q", project.Title)
	}
	if len(project.TechStack) == 0 {
		t.Fatal("expected tech stack to be extracted")
	}
	if !strings.Contains(project.Content, "面向校园活动场景的平台") {
		t.Fatalf("expected project content to contain description, got %q", project.Content)
	}
}

func TestNormalizeMergesBrokenLines(t *testing.T) {
	doc := parser.ParsedDocument{
		SourceFormat: "pdf",
		Blocks: []parser.Block{
			{Type: parser.BlockParagraph, Text: "自动考\n勤平台支持多端签到"},
		},
	}

	normalized := NewService().Normalize(doc)
	if len(normalized.Sections) == 0 {
		t.Fatal("expected normalized sections")
	}
	if !strings.Contains(normalized.Sections[0].Content, "自动考勤平台") {
		t.Fatalf("expected broken line to merge, got %q", normalized.Sections[0].Content)
	}
}

func TestNormalizeSkipsNoiseAndLabelOnlyProjectBlocks(t *testing.T) {
	doc := parser.ParsedDocument{
		SourceFormat: "pdf",
		Blocks: []parser.Block{
			{Type: parser.BlockParagraph, Text: "项目"},
			{Type: parser.BlockParagraph, Text: "项目描述："},
			{Type: parser.BlockParagraph, Text: "工作内容："},
			{Type: parser.BlockParagraph, Text: "https://github.com/example"},
			{Type: parser.BlockParagraph, Text: "user@example.com"},
		},
	}

	normalized := NewService().Normalize(doc)
	if project := findSectionByType(normalized.Sections, SectionTypeProject); project != nil {
		t.Fatalf("expected label-only and noise blocks to avoid project section, got %#v", project)
	}
}

func findSectionByType(sections []NormalizedSection, target SectionType) *NormalizedSection {
	for index := range sections {
		if sections[index].Type == target {
			return &sections[index]
		}
	}

	return nil
}
