package postgres

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestStructuredDocumentRepoCreatesAndGetsVersionStructure(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, version := seedStructuredResourceVersion(t, repo, ctx, "结构化版本测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	documentJSON := json.RawMessage(`{"source_format":"pdf","blocks":[{"id":"blk-1","type":"heading","text":"项目经验"}]}`)
	qualityFlagsJSON := json.RawMessage(`["layout_lost"]`)

	stored, err := repo.CreateVersionStructure(ctx, ResourceVersionStructureInput{
		ResourceID:       resource.ID,
		VersionID:        version.ID,
		SourceFormat:     "pdf",
		ParserName:       "tika",
		ParserVersion:    "1.0.0",
		DocumentJSON:     documentJSON,
		QualityFlagsJSON: qualityFlagsJSON,
	})
	if err != nil {
		t.Fatalf("create version structure: %v", err)
	}
	if stored.VersionID != version.ID {
		t.Fatalf("expected version id %q, got %q", version.ID, stored.VersionID)
	}
	if !jsonBytesEqual(t, stored.DocumentJSON, documentJSON) {
		t.Fatalf("expected semantically equal document json, got %s", string(stored.DocumentJSON))
	}

	got, err := repo.GetVersionStructureByVersionID(ctx, version.ID)
	if err != nil {
		t.Fatalf("get version structure by version id: %v", err)
	}
	if got == nil {
		t.Fatal("expected version structure, got nil")
	}
	if got.SourceFormat != "pdf" {
		t.Fatalf("expected source format %q, got %q", "pdf", got.SourceFormat)
	}
	if !jsonBytesEqual(t, got.QualityFlagsJSON, qualityFlagsJSON) {
		t.Fatalf("expected semantically equal quality flags, got %s", string(got.QualityFlagsJSON))
	}
}

func TestStructuredDocumentRepoReplacesSectionsIdempotently(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, version := seedStructuredResourceVersion(t, repo, ctx, "结构化 section 替换测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	inputs := []ResourceSectionInput{
		{
			SectionKey:  "project-1",
			SectionType: "project",
			Title:       "CampusHub",
			Summary:     "校内活动平台",
			Content:     "面向校园活动场景的平台",
			PageStart:   1,
			PageEnd:     1,
			Metadata: map[string]any{
				"confidence": "high",
			},
		},
	}

	if err := repo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, inputs); err != nil {
		t.Fatalf("replace sections first run: %v", err)
	}
	if err := repo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, inputs); err != nil {
		t.Fatalf("replace sections second run: %v", err)
	}

	sections, err := repo.ListSectionsByVersion(ctx, version.ID)
	if err != nil {
		t.Fatalf("list sections by version: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if sections[0].SectionKey != "project-1" {
		t.Fatalf("expected section key %q, got %q", "project-1", sections[0].SectionKey)
	}
	if sections[0].Metadata["confidence"] != "high" {
		t.Fatalf("expected metadata confidence high, got %#v", sections[0].Metadata["confidence"])
	}
}

func TestStructuredDocumentRepoListsSectionsByTypeInStableOrder(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, version := seedStructuredResourceVersion(t, repo, ctx, "结构化 section 顺序测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	err := repo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:  "profile-1",
			SectionType: "profile",
			Title:       "个人简介",
			Content:     "五年前端与后端经验",
			PageStart:   1,
			PageEnd:     1,
		},
		{
			SectionKey:  "project-1",
			SectionType: "project",
			Title:       "CampusHub",
			Content:     "项目一内容",
			PageStart:   1,
			PageEnd:     1,
		},
		{
			SectionKey:  "project-2",
			SectionType: "project",
			Title:       "TutorBridge",
			Content:     "项目二内容",
			PageStart:   2,
			PageEnd:     2,
		},
	})
	if err != nil {
		t.Fatalf("replace sections: %v", err)
	}

	projects, err := repo.ListSectionsByVersionAndType(ctx, version.ID, "project")
	if err != nil {
		t.Fatalf("list sections by type: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 project sections, got %d", len(projects))
	}
	if projects[0].SectionKey != "project-1" || projects[1].SectionKey != "project-2" {
		t.Fatalf("expected stable project order, got %q then %q", projects[0].SectionKey, projects[1].SectionKey)
	}
}

func TestResourceChunkRepoKeepsStructuredMetadata(t *testing.T) {
	pool := newTestPool(t)
	repo := NewResourceRepo(pool)
	ctx := testContext(t)

	resource, version := seedStructuredResourceVersion(t, repo, ctx, "结构化 chunk 元数据测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	inputs := []ResourceChunkInput{
		{
			ChunkIndex:    0,
			SectionTitle:  "CampusHub",
			Content:       "项目摘要",
			Embedding:     testVector(0.61),
			SectionType:   "project",
			ChunkRole:     "section_summary",
			WindowGroupID: "project-1",
			PageStart:     1,
			PageEnd:       1,
			Metadata: map[string]any{
				"confidence": "high",
			},
		},
	}

	if err := repo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:  "project-1",
			SectionType: "project",
			Title:       "CampusHub",
			Content:     "项目摘要",
			PageStart:   1,
			PageEnd:     1,
		},
	}); err != nil {
		t.Fatalf("replace sections before chunks: %v", err)
	}

	sections, err := repo.ListSectionsByVersionAndType(ctx, version.ID, "project")
	if err != nil {
		t.Fatalf("list sections before chunks: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("expected one project section before chunk replace, got %d", len(sections))
	}
	inputs[0].SectionID = sections[0].ID

	if err := repo.ReplaceVersionChunks(ctx, version.ID, resource.ID, inputs); err != nil {
		t.Fatalf("replace version chunks first run: %v", err)
	}
	if err := repo.ReplaceVersionChunks(ctx, version.ID, resource.ID, inputs); err != nil {
		t.Fatalf("replace version chunks second run: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT section_id::text, section_type, chunk_role, window_group_id, page_start, page_end, metadata_json
		FROM resource_chunks
		WHERE version_id = $1
		ORDER BY chunk_index
	`, version.ID)
	if err != nil {
		t.Fatalf("query structured chunks: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("expected one structured chunk row")
	}

	var (
		sectionID     string
		sectionType   string
		chunkRole     string
		windowGroupID string
		pageStart     int
		pageEnd       int
		metadataJSON  []byte
	)
	if err := rows.Scan(&sectionID, &sectionType, &chunkRole, &windowGroupID, &pageStart, &pageEnd, &metadataJSON); err != nil {
		t.Fatalf("scan structured chunk: %v", err)
	}
	if rows.Next() {
		t.Fatal("expected exactly one structured chunk row")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate structured chunks: %v", err)
	}

	if sectionID != sections[0].ID {
		t.Fatalf("expected section id %q, got %q", sections[0].ID, sectionID)
	}
	if sectionType != "project" {
		t.Fatalf("expected section type %q, got %q", "project", sectionType)
	}
	if chunkRole != "section_summary" {
		t.Fatalf("expected chunk role %q, got %q", "section_summary", chunkRole)
	}
	if windowGroupID != "project-1" {
		t.Fatalf("expected window group %q, got %q", "project-1", windowGroupID)
	}
	if pageStart != 1 || pageEnd != 1 {
		t.Fatalf("expected page range 1-1, got %d-%d", pageStart, pageEnd)
	}

	var metadata map[string]any
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		t.Fatalf("unmarshal chunk metadata: %v", err)
	}
	if metadata["confidence"] != "high" {
		t.Fatalf("expected chunk metadata confidence high, got %#v", metadata["confidence"])
	}
}

func seedStructuredResourceVersion(t *testing.T, repo *ResourceRepo, ctx context.Context, title string) (*Resource, *ResourceVersion) {
	t.Helper()

	return seedResourceVersion(t, repo, ctx, title)
}

func jsonBytesEqual(t *testing.T, left []byte, right []byte) bool {
	t.Helper()

	var leftValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		t.Fatalf("unmarshal left json: %v", err)
	}

	var rightValue any
	if err := json.Unmarshal(right, &rightValue); err != nil {
		t.Fatalf("unmarshal right json: %v", err)
	}

	return reflect.DeepEqual(leftValue, rightValue)
}
