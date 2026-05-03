package assistant

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

// TestSectionLocatorPrefersOrdinalWithinCurrentFile 验证`sectionLocatorPrefersOrdinalWithinCurrentFile`在特定边界条件下的行为，防止同类回归。
func TestSectionLocatorPrefersOrdinalWithinCurrentFile(t *testing.T) {
	locator := NewSectionLocator(&fakeCurrentFileSectionReader{
		allSections: []postgres.ResourceSection{
			{ID: "section-1", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "项目一正文"},
			{ID: "section-2", VersionID: "version-1", SectionType: "project", SectionOrder: 2, Title: "选课助手", Content: "项目二正文"},
			{ID: "section-3", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "慢跑计划", Content: "项目三正文"},
		},
	})

	located, err := locator.Locate(context.Background(), LocateSectionInput{
		ResourceID: "resource-1",
		VersionID:  "version-1",
		Message:    "把第三个项目先输出一遍",
		Intent:     ClassifyReadIntent("把第三个项目先输出一遍"),
	})
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if located == nil {
		t.Fatal("expected locator to resolve the third project")
	}
	if located.Mode != LocatedSectionModeSection {
		t.Fatalf("expected locate mode %q, got %q", LocatedSectionModeSection, located.Mode)
	}
	if located.SectionID != "section-3" {
		t.Fatalf("expected section id %q, got %q", "section-3", located.SectionID)
	}
	if located.Reason != "ordinal_reference" {
		t.Fatalf("expected reason %q, got %q", "ordinal_reference", located.Reason)
	}
}

// TestSectionLocatorMatchesExplicitEntityWithinCurrentFile 验证`sectionLocatorMatchesExplicitEntityWithinCurrentFile`在特定边界条件下的行为，防止同类回归。
func TestSectionLocatorMatchesExplicitEntityWithinCurrentFile(t *testing.T) {
	locator := NewSectionLocator(&fakeCurrentFileSectionReader{
		allSections: []postgres.ResourceSection{
			{
				ID:                  "section-campushub",
				VersionID:           "version-1",
				SectionType:         "project",
				SectionOrder:        1,
				Title:               "CampusHub",
				CanonicalEntityName: stringPointer("CampusHub"),
				Content:             "CampusHub 项目正文",
			},
		},
	})

	located, err := locator.Locate(context.Background(), LocateSectionInput{
		ResourceID: "resource-1",
		VersionID:  "version-1",
		Message:    "把 CampusHub 项目先输出一遍",
		Intent:     ClassifyReadIntent("把 CampusHub 项目先输出一遍"),
	})
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if located == nil {
		t.Fatal("expected locator to resolve explicit entity")
	}
	if located.SectionID != "section-campushub" {
		t.Fatalf("expected section id %q, got %q", "section-campushub", located.SectionID)
	}
	if located.Reason != "explicit_entity" {
		t.Fatalf("expected reason %q, got %q", "explicit_entity", located.Reason)
	}
}

// TestSectionLocatorReturnsSectionListSignal 验证`sectionLocatorReturnsSectionListSignal`在特定边界条件下的行为，防止同类回归。
func TestSectionLocatorReturnsSectionListSignal(t *testing.T) {
	locator := NewSectionLocator(&fakeCurrentFileSectionReader{
		allSections: []postgres.ResourceSection{
			{ID: "section-1", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "项目一正文"},
			{ID: "section-2", VersionID: "version-1", SectionType: "project", SectionOrder: 2, Title: "选课助手", Content: "项目二正文"},
		},
	})

	located, err := locator.Locate(context.Background(), LocateSectionInput{
		ResourceID: "resource-1",
		VersionID:  "version-1",
		Message:    "这份简历里有哪些项目",
		Intent:     ClassifyReadIntent("这份简历里有哪些项目"),
	})
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if located == nil {
		t.Fatal("expected locator to emit section list signal")
	}
	if located.Mode != LocatedSectionModeSectionList {
		t.Fatalf("expected locate mode %q, got %q", LocatedSectionModeSectionList, located.Mode)
	}
	if located.SectionType != "project" {
		t.Fatalf("expected section type %q, got %q", "project", located.SectionType)
	}
}

// TestSectionLocatorFallsBackToDocumentWhenNoStructuredSections 验证`sectionLocatorFallsBackToDocumentWhenNoStructuredSections`在特定边界条件下的行为，防止同类回归。
func TestSectionLocatorFallsBackToDocumentWhenNoStructuredSections(t *testing.T) {
	locator := NewSectionLocator(&fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "这是整份文件正文。",
		},
	})

	located, err := locator.Locate(context.Background(), LocateSectionInput{
		ResourceID: "resource-1",
		VersionID:  "version-1",
		Message:    "把这份文件内容先输出一遍",
		Intent:     ClassifyReadIntent("把这份文件内容先输出一遍"),
	})
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if located == nil {
		t.Fatal("expected locator to fall back to document mode")
	}
	if located.Mode != LocatedSectionModeDocument {
		t.Fatalf("expected locate mode %q, got %q", LocatedSectionModeDocument, located.Mode)
	}
}

// fakeCurrentFileSectionReader 提供当前文件 section / version 读取替身，便于隔离 locator 与 reader 的行为测试。
type fakeCurrentFileSectionReader struct {
	currentVersion   *postgres.ResourceVersion
	versionStructure *postgres.ResourceVersionStructure
	allSections      []postgres.ResourceSection
}

// GetCurrentVersion 返回测试替身里的当前版本快照。
func (f *fakeCurrentFileSectionReader) GetCurrentVersion(_ context.Context, resourceID string) (*postgres.ResourceVersion, error) {
	if f.currentVersion == nil || f.currentVersion.ResourceID != resourceID {
		return nil, nil
	}

	return f.currentVersion, nil
}

// GetSectionByID 按主键返回测试替身里的 section。
func (f *fakeCurrentFileSectionReader) GetSectionByID(_ context.Context, sectionID string) (*postgres.ResourceSection, error) {
	for idx := range f.allSections {
		if f.allSections[idx].ID == sectionID {
			section := f.allSections[idx]
			return &section, nil
		}
	}

	return nil, nil
}

// GetSectionByOrder 按 section 类型和顺序返回测试替身里的 section。
func (f *fakeCurrentFileSectionReader) GetSectionByOrder(_ context.Context, versionID string, sectionType string, ordinal int) (*postgres.ResourceSection, error) {
	for idx := range f.allSections {
		section := f.allSections[idx]
		if section.VersionID != versionID || section.SectionType != sectionType || section.SectionOrder != ordinal {
			continue
		}

		return &section, nil
	}

	return nil, nil
}

// GetVersionStructureByVersionID 返回测试替身里的结构化文档 JSON。
func (f *fakeCurrentFileSectionReader) GetVersionStructureByVersionID(_ context.Context, versionID string) (*postgres.ResourceVersionStructure, error) {
	if f.versionStructure == nil || f.versionStructure.VersionID != versionID {
		return nil, nil
	}

	return f.versionStructure, nil
}

// ListSectionsForReading 返回测试替身里按条件过滤后的 section 列表。
func (f *fakeCurrentFileSectionReader) ListSectionsForReading(_ context.Context, versionID string, sectionType string) ([]postgres.ResourceSection, error) {
	sections := make([]postgres.ResourceSection, 0, len(f.allSections))
	for _, section := range f.allSections {
		if section.VersionID != versionID {
			continue
		}
		if sectionType != "" && section.SectionType != sectionType {
			continue
		}

		sections = append(sections, section)
	}

	return sections, nil
}
