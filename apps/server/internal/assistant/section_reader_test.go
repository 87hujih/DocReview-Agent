package assistant

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

// TestSectionReaderReadsCanonicalSectionContent 验证`sectionReaderReadsCanonicalSectionContent`在特定边界条件下的行为，防止同类回归。
func TestSectionReaderReadsCanonicalSectionContent(t *testing.T) {
	reader := NewSectionReader(&fakeCurrentFileSectionReader{
		allSections: []postgres.ResourceSection{
			{ID: "section-3", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "慢跑计划", Content: "第三个项目正文"},
		},
	})

	result, err := reader.Read(context.Background(), CanonicalReadInput{
		ResourceID: "resource-1",
		VersionID:  "version-1",
		Located: &LocatedSection{
			Mode:        LocatedSectionModeSection,
			SectionID:   "section-3",
			SectionType: "project",
		},
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if result == nil {
		t.Fatal("expected canonical section result")
	}
	if result.Mode != CanonicalReadModeSection {
		t.Fatalf("expected read mode %q, got %q", CanonicalReadModeSection, result.Mode)
	}
	if result.Content != "第三个项目正文" {
		t.Fatalf("expected section content %q, got %q", "第三个项目正文", result.Content)
	}
	if result.SectionTitle != "慢跑计划" {
		t.Fatalf("expected section title %q, got %q", "慢跑计划", result.SectionTitle)
	}
}

// TestSectionReaderReadsSectionListCanonically 验证`sectionReaderReadsSectionListCanonically`在特定边界条件下的行为，防止同类回归。
func TestSectionReaderReadsSectionListCanonically(t *testing.T) {
	reader := NewSectionReader(&fakeCurrentFileSectionReader{
		allSections: []postgres.ResourceSection{
			{ID: "section-1", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "项目一正文"},
			{ID: "section-2", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 2, Title: "选课助手", Content: "项目二正文"},
		},
	})

	result, err := reader.Read(context.Background(), CanonicalReadInput{
		ResourceID: "resource-1",
		VersionID:  "version-1",
		Located: &LocatedSection{
			Mode:        LocatedSectionModeSectionList,
			SectionType: "project",
		},
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if result == nil {
		t.Fatal("expected canonical list result")
	}
	if result.Mode != CanonicalReadModeSectionList {
		t.Fatalf("expected read mode %q, got %q", CanonicalReadModeSectionList, result.Mode)
	}
	if len(result.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(result.Sections))
	}
	if result.Sections[0].SectionTitle != "CampusHub" || result.Sections[1].SectionTitle != "选课助手" {
		t.Fatalf("expected ordered section titles, got %#v", result.Sections)
	}
}

// TestSectionReaderFallsBackToCurrentVersionContent 验证`sectionReaderFallsBackToCurrentVersionContent`在特定边界条件下的行为，防止同类回归。
func TestSectionReaderFallsBackToCurrentVersionContent(t *testing.T) {
	reader := NewSectionReader(&fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "这是整份文件正文。",
		},
	})

	result, err := reader.Read(context.Background(), CanonicalReadInput{
		ResourceID: "resource-1",
		VersionID:  "version-1",
		Located: &LocatedSection{
			Mode: LocatedSectionModeDocument,
		},
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if result == nil {
		t.Fatal("expected document fallback result")
	}
	if result.Mode != CanonicalReadModeDocument {
		t.Fatalf("expected read mode %q, got %q", CanonicalReadModeDocument, result.Mode)
	}
	if result.Content != "这是整份文件正文。" {
		t.Fatalf("expected document content %q, got %q", "这是整份文件正文。", result.Content)
	}
}
