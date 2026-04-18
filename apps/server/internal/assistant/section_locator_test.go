package assistant

import (
	"context"
	"strconv"
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

func TestSectionLocatorPrefersExplicitEntityMatch(t *testing.T) {
	reader := &fakeActiveFileResourceReader{
		currentVersion: &postgres.ResourceVersion{ID: "version-1", ResourceID: "resource-1"},
	}
	locator := NewSectionLocator(reader)

	got, err := locator.Locate(context.Background(), LocateSectionInput{
		ResourceID: "resource-1",
		Message:    "CampusHub 怎么改",
		Snapshot: &SessionContextSnapshot{
			LastEnumeratedEntities: []EnumeratedEntity{
				{SectionID: "section-campushub", SectionType: "project", EntityName: "CampusHub", Ordinal: 1},
			},
		},
	})
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got == nil || got.SectionID != "section-campushub" || got.Reason != "explicit_entity" {
		t.Fatalf("expected explicit entity match, got %#v", got)
	}
}

func TestSectionLocatorFallsBackToSectionOrderWhenOrdinalFrameMissing(t *testing.T) {
	reader := &fakeActiveFileResourceReader{
		currentVersion: &postgres.ResourceVersion{ID: "version-1", ResourceID: "resource-1"},
		sectionByOrder: map[string]*postgres.ResourceSection{
			sectionOrderKey("project", 3): {ID: "section-3", VersionID: "version-1", SectionType: "project", SectionOrder: 3, Title: "第三个项目", Content: "第三个项目正文"},
		},
	}
	locator := NewSectionLocator(reader)

	got, err := locator.Locate(context.Background(), LocateSectionInput{
		ResourceID: "resource-1",
		Message:    "把第三个项目先输出一遍",
	})
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got == nil || got.SectionID != "section-3" || got.Reason != "ordinal_lookup" {
		t.Fatalf("expected ordinal fallback match, got %#v", got)
	}
}

func TestSectionLocatorFallsBackToActiveSectionForAnaphora(t *testing.T) {
	reader := &fakeActiveFileResourceReader{
		currentVersion: &postgres.ResourceVersion{ID: "version-1", ResourceID: "resource-1"},
		sectionByID: map[string]*postgres.ResourceSection{
			"section-campushub": {ID: "section-campushub", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "CampusHub 正文"},
		},
	}
	locator := NewSectionLocator(reader)

	got, err := locator.Locate(context.Background(), LocateSectionInput{
		ResourceID: "resource-1",
		Message:    "这一节可以怎么改",
		Snapshot: &SessionContextSnapshot{
			ActiveSection: &SnapshotActiveSection{ID: "section-campushub", Type: "project"},
		},
	})
	if err != nil {
		t.Fatalf("locate: %v", err)
	}
	if got == nil || got.SectionID != "section-campushub" || got.Reason != "anaphora" {
		t.Fatalf("expected anaphora match, got %#v", got)
	}
}

type fakeActiveFileResourceReader struct {
	currentVersion *postgres.ResourceVersion
	currentErr     error

	sectionByID     map[string]*postgres.ResourceSection
	sectionByOrder  map[string]*postgres.ResourceSection
	sectionByEntity map[string]*postgres.ResourceSection
	sectionsByType  map[string][]postgres.ResourceSection
	sectionErr      error

	currentVersionCalls    int
	getSectionByIDCalls    int
	getSectionOrderCalls   []string
	findSectionEntityCalls []string
	listSectionsCalls      []string
}

func (r *fakeActiveFileResourceReader) GetCurrentVersion(_ context.Context, _ string) (*postgres.ResourceVersion, error) {
	r.currentVersionCalls++
	if r.currentErr != nil {
		return nil, r.currentErr
	}
	return cloneResourceVersion(r.currentVersion), nil
}

func (r *fakeActiveFileResourceReader) GetSectionByID(_ context.Context, sectionID string) (*postgres.ResourceSection, error) {
	r.getSectionByIDCalls++
	if r.sectionErr != nil {
		return nil, r.sectionErr
	}
	return cloneResourceSection(r.sectionByID[sectionID]), nil
}

func (r *fakeActiveFileResourceReader) GetSectionByOrder(_ context.Context, versionID string, sectionType string, ordinal int) (*postgres.ResourceSection, error) {
	r.getSectionOrderCalls = append(r.getSectionOrderCalls, versionID+"|"+sectionOrderKey(sectionType, ordinal))
	if r.sectionErr != nil {
		return nil, r.sectionErr
	}
	return cloneResourceSection(r.sectionByOrder[sectionOrderKey(sectionType, ordinal)]), nil
}

func (r *fakeActiveFileResourceReader) FindSectionByEntity(_ context.Context, versionID string, entityName string) (*postgres.ResourceSection, error) {
	r.findSectionEntityCalls = append(r.findSectionEntityCalls, versionID+"|"+entityName)
	if r.sectionErr != nil {
		return nil, r.sectionErr
	}
	return cloneResourceSection(r.sectionByEntity[entityName]), nil
}

func (r *fakeActiveFileResourceReader) ListSectionsForReading(_ context.Context, versionID string, sectionType string) ([]postgres.ResourceSection, error) {
	r.listSectionsCalls = append(r.listSectionsCalls, versionID+"|"+sectionType)
	if r.sectionErr != nil {
		return nil, r.sectionErr
	}

	items := r.sectionsByType[sectionType]
	cloned := make([]postgres.ResourceSection, 0, len(items))
	for _, item := range items {
		cloned = append(cloned, item)
	}
	return cloned, nil
}

func cloneResourceSection(section *postgres.ResourceSection) *postgres.ResourceSection {
	if section == nil {
		return nil
	}

	cloned := *section
	cloned.CanonicalEntityName = cloneStringPointer(section.CanonicalEntityName)
	cloned.PageStart = cloneIntPointer(section.PageStart)
	cloned.PageEnd = cloneIntPointer(section.PageEnd)
	cloned.AliasesJSON = append([]byte(nil), section.AliasesJSON...)
	cloned.MetadataJSON = append([]byte(nil), section.MetadataJSON...)
	return &cloned
}

func cloneResourceVersion(version *postgres.ResourceVersion) *postgres.ResourceVersion {
	if version == nil {
		return nil
	}

	cloned := *version
	return &cloned
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

func sectionOrderKey(sectionType string, ordinal int) string {
	return sectionType + "|" + strconv.Itoa(ordinal)
}
