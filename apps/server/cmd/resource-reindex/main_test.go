package main

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/storage/postgres"
)

func TestParseReindexModeAcceptsResourceID(t *testing.T) {
	mode, err := parseReindexMode([]string{"--resource-id", "00000000-0000-0000-0000-000000000001"})
	if err != nil {
		t.Fatalf("parse resource id mode: %v", err)
	}

	if mode.ResourceID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("expected resource id mode, got %#v", mode)
	}
	if mode.MissingCurrent {
		t.Fatalf("expected missing-current mode to be false")
	}
}

func TestParseReindexModeAcceptsMissingCurrent(t *testing.T) {
	mode, err := parseReindexMode([]string{"--missing-current"})
	if err != nil {
		t.Fatalf("parse missing-current mode: %v", err)
	}

	if !mode.MissingCurrent {
		t.Fatalf("expected missing-current mode, got %#v", mode)
	}
	if mode.ResourceID != "" {
		t.Fatalf("expected empty resource id, got %q", mode.ResourceID)
	}
}

func TestParseReindexModeRequiresOneMode(t *testing.T) {
	if _, err := parseReindexMode(nil); err == nil {
		t.Fatal("expected missing mode to fail")
	}

	if _, err := parseReindexMode([]string{
		"--resource-id", "00000000-0000-0000-0000-000000000001",
		"--missing-current",
	}); err == nil {
		t.Fatal("expected conflicting modes to fail")
	}
}

func TestParseReindexModeRejectsInvalidResourceID(t *testing.T) {
	if _, err := parseReindexMode([]string{"--resource-id", "not-a-uuid"}); err == nil {
		t.Fatal("expected invalid resource id to fail")
	}
}

func TestReindexSingleCurrentVersionPassesStructuredSectionsToIndexer(t *testing.T) {
	repo := &fakeReindexRepo{
		resource: &postgres.Resource{
			ID:    "00000000-0000-0000-0000-000000000001",
			Title: "结构化简历",
		},
		version: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "00000000-0000-0000-0000-000000000001",
			Content:    "fallback",
		},
		structure: &postgres.ResourceVersionStructure{
			VersionID:    "version-1",
			SourceFormat: "pdf",
		},
		sections: []postgres.ResourceSection{
			{
				ID:          "section-project-1",
				SectionKey:  "project-1",
				SectionType: "project",
				Title:       "CampusHub",
				Content:     "项目正文",
				PageStart:   1,
				PageEnd:     1,
				Metadata: map[string]any{
					"tech_stack": []string{"Go", "Redis"},
				},
			},
		},
	}
	reindexer := &fakeVersionReindexer{}

	if err := reindexSingleCurrentVersion(context.Background(), repo, reindexer, "00000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("reindex single current version: %v", err)
	}

	if reindexer.calls != 1 {
		t.Fatalf("expected exactly 1 reindex call, got %d", reindexer.calls)
	}
	if len(reindexer.lastInput.Sections) != 1 {
		t.Fatalf("expected structured sections to be passed through, got %d", len(reindexer.lastInput.Sections))
	}
	if reindexer.lastInput.Sections[0].SectionType != "project" {
		t.Fatalf("expected section type project, got %q", reindexer.lastInput.Sections[0].SectionType)
	}
}

func TestReindexSingleCurrentVersionWithIndexerSyncsSearchProjection(t *testing.T) {
	repo := &fakeReindexRepo{
		resource: &postgres.Resource{
			ID:    "00000000-0000-0000-0000-000000000001",
			Title: "结构化简历",
		},
		version: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "00000000-0000-0000-0000-000000000001",
			Content:    "## 项目经验\n负责跨区域项目交付。",
		},
	}
	versionSync := &fakeReindexVersionSync{}
	reindexer := indexer.NewService(repo, fakeReindexEmbedder{}, indexer.WithVersionSync(versionSync))

	if err := reindexSingleCurrentVersion(context.Background(), repo, reindexer, "00000000-0000-0000-0000-000000000001"); err != nil {
		t.Fatalf("reindex single current version with sync: %v", err)
	}

	if repo.replaceCalls != 1 {
		t.Fatalf("expected exactly 1 replace call, got %d", repo.replaceCalls)
	}
	if versionSync.calls != 1 {
		t.Fatalf("expected exactly 1 sync call, got %d", versionSync.calls)
	}
	if versionSync.lastResourceID != "00000000-0000-0000-0000-000000000001" {
		t.Fatalf("expected sync resource id, got %q", versionSync.lastResourceID)
	}
	if versionSync.lastVersionID != "version-1" {
		t.Fatalf("expected sync version id %q, got %q", "version-1", versionSync.lastVersionID)
	}
}

type fakeReindexRepo struct {
	resource     *postgres.Resource
	version      *postgres.ResourceVersion
	structure    *postgres.ResourceVersionStructure
	sections     []postgres.ResourceSection
	replaceCalls int
}

func (f *fakeReindexRepo) GetByID(context.Context, string) (*postgres.Resource, error) {
	return f.resource, nil
}

func (f *fakeReindexRepo) GetCurrentVersion(context.Context, string) (*postgres.ResourceVersion, error) {
	return f.version, nil
}

func (f *fakeReindexRepo) GetVersionStructureByVersionID(context.Context, string) (*postgres.ResourceVersionStructure, error) {
	return f.structure, nil
}

func (f *fakeReindexRepo) ListSectionsByVersion(context.Context, string) ([]postgres.ResourceSection, error) {
	return append([]postgres.ResourceSection(nil), f.sections...), nil
}

func (f *fakeReindexRepo) List(context.Context) ([]postgres.Resource, error) {
	if f.resource == nil {
		return nil, nil
	}

	return []postgres.Resource{*f.resource}, nil
}

func (f *fakeReindexRepo) CountChunksByVersion(context.Context, string) (int, error) {
	return 0, nil
}

func (f *fakeReindexRepo) ReplaceVersionChunks(context.Context, string, string, []postgres.ResourceChunkInput) error {
	f.replaceCalls++
	return nil
}

type fakeVersionReindexer struct {
	calls     int
	lastInput indexer.Input
}

func (f *fakeVersionReindexer) ReindexVersion(_ context.Context, input indexer.Input) error {
	f.calls++
	f.lastInput = input
	return nil
}

type fakeReindexVersionSync struct {
	calls          int
	lastResourceID string
	lastVersionID  string
}

func (f *fakeReindexVersionSync) SyncVersion(_ context.Context, resourceID string, versionID string) error {
	f.calls++
	f.lastResourceID = resourceID
	f.lastVersionID = versionID
	return nil
}

type fakeReindexEmbedder struct{}

func (fakeReindexEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for range texts {
		values := make([]float32, 1024)
		for index := range values {
			values[index] = 0.3 + float32(index%5)/10
		}
		vectors = append(vectors, values)
	}

	return vectors, nil
}
