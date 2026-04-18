package postgres

import (
	"encoding/json"
	"testing"
)

func TestStructuredDocumentRepoPersistsVersionStructureAndSections(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "结构化文档仓储测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	documentJSON := mustMarshalJSON(t, map[string]any{
		"source_format": "pdf",
		"blocks": []map[string]any{
			{"type": "heading", "text": "CampusHub校园活动平台"},
		},
	})
	qualityFlagsJSON := mustMarshalJSON(t, []string{"too_many_short_blocks"})

	createdStructure, err := structureRepo.CreateVersionStructure(ctx, CreateVersionStructureParams{
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
	if createdStructure.VersionID != version.ID {
		t.Fatalf("expected version id %q, got %q", version.ID, createdStructure.VersionID)
	}

	loadedStructure, err := structureRepo.GetVersionStructureByVersionID(ctx, version.ID)
	if err != nil {
		t.Fatalf("get version structure: %v", err)
	}
	if loadedStructure == nil {
		t.Fatal("expected version structure, got nil")
	}
	if !jsonBodiesEqual(documentJSON, loadedStructure.DocumentJSON) {
		t.Fatalf("expected document json %s, got %s", string(documentJSON), string(loadedStructure.DocumentJSON))
	}
	if !jsonBodiesEqual(qualityFlagsJSON, loadedStructure.QualityFlagsJSON) {
		t.Fatalf("expected quality flags json %s, got %s", string(qualityFlagsJSON), string(loadedStructure.QualityFlagsJSON))
	}

	sections, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:          "project-1",
			SectionType:         "project",
			SectionOrder:        1,
			Title:               "CampusHub校园活动平台",
			CanonicalEntityName: stringPointer("CampusHub校园活动平台"),
			AliasesJSON:         mustMarshalJSON(t, []string{"CampusHub校园活动平台", "CampusHub"}),
			Summary:             "校园活动统一平台",
			Content:             "负责活动发布、报名与签到链路。",
			PageStart:           intPointer(1),
			PageEnd:             intPointer(1),
			MetadataJSON:        mustMarshalJSON(t, map[string]any{"confidence": "high"}),
		},
		{
			SectionKey:   "skills",
			SectionType:  "skills",
			SectionOrder: 2,
			Title:        "技术栈",
			AliasesJSON:  mustMarshalJSON(t, []string{"技术栈"}),
			Summary:      "Go Redis gRPC",
			Content:      "Go Redis gRPC",
			MetadataJSON: mustMarshalJSON(t, map[string]any{"source": "resume"}),
		},
	})
	if err != nil {
		t.Fatalf("replace sections for version: %v", err)
	}
	if len(sections) != 2 {
		t.Fatalf("expected 2 inserted sections, got %d", len(sections))
	}

	allSections, err := structureRepo.ListSectionsByVersion(ctx, version.ID)
	if err != nil {
		t.Fatalf("list sections by version: %v", err)
	}
	if len(allSections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(allSections))
	}
	if allSections[0].SectionOrder != 1 || allSections[0].SectionType != "project" {
		t.Fatalf("expected first ordered section to be project order=1, got %#v", allSections[0])
	}

	projectSections, err := structureRepo.ListSectionsByVersionAndType(ctx, version.ID, "project")
	if err != nil {
		t.Fatalf("list project sections by version: %v", err)
	}
	if len(projectSections) != 1 {
		t.Fatalf("expected 1 project section, got %d", len(projectSections))
	}
	if projectSections[0].CanonicalEntityName == nil || *projectSections[0].CanonicalEntityName != "CampusHub校园活动平台" {
		t.Fatalf("expected canonical entity name to persist, got %#v", projectSections[0].CanonicalEntityName)
	}
}

func TestResourceStructureRepoGetsSectionByID(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "读取模式按ID测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	sections, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:   "project-1",
			SectionType:  "project",
			SectionOrder: 1,
			Title:        "CampusHub",
			Content:      "项目一正文",
		},
		{
			SectionKey:   "project-2",
			SectionType:  "project",
			SectionOrder: 2,
			Title:        "选课助手",
			Content:      "项目二正文",
		},
	})
	if err != nil {
		t.Fatalf("replace sections: %v", err)
	}

	got, err := structureRepo.GetSectionByID(ctx, sections[1].ID)
	if err != nil {
		t.Fatalf("get section by id: %v", err)
	}
	if got == nil || got.Title != "选课助手" {
		t.Fatalf("expected section %q, got %#v", "选课助手", got)
	}
}

func TestResourceStructureRepoGetsSectionByOrder(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "读取模式按序号测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	_, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:   "project-1",
			SectionType:  "project",
			SectionOrder: 1,
			Title:        "CampusHub",
			Content:      "项目一正文",
		},
		{
			SectionKey:   "project-2",
			SectionType:  "project",
			SectionOrder: 2,
			Title:        "选课助手",
			Content:      "项目二正文",
		},
		{
			SectionKey:   "skills",
			SectionType:  "skills",
			SectionOrder: 1,
			Title:        "技术栈",
			Content:      "Go PostgreSQL",
		},
	})
	if err != nil {
		t.Fatalf("replace sections: %v", err)
	}

	got, err := structureRepo.GetSectionByOrder(ctx, version.ID, "project", 2)
	if err != nil {
		t.Fatalf("get section by order: %v", err)
	}
	if got == nil || got.Title != "选课助手" {
		t.Fatalf("expected second project, got %#v", got)
	}
}

func TestResourceStructureRepoFindsSectionByEntityNameOrAlias(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "读取模式实体定位测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	_, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:          "project-1",
			SectionType:         "project",
			SectionOrder:        1,
			Title:               "CampusHub校园活动平台",
			CanonicalEntityName: stringPointer("CampusHub校园活动平台"),
			AliasesJSON:         mustMarshalJSON(t, []string{"CampusHub", "活动平台"}),
			Content:             "项目一正文",
		},
	})
	if err != nil {
		t.Fatalf("replace sections: %v", err)
	}

	canonical, err := structureRepo.FindSectionByEntity(ctx, version.ID, "CampusHub校园活动平台")
	if err != nil {
		t.Fatalf("find section by canonical entity: %v", err)
	}
	if canonical == nil || canonical.Title != "CampusHub校园活动平台" {
		t.Fatalf("expected canonical match, got %#v", canonical)
	}

	alias, err := structureRepo.FindSectionByEntity(ctx, version.ID, "CampusHub")
	if err != nil {
		t.Fatalf("find section by alias: %v", err)
	}
	if alias == nil || alias.Title != "CampusHub校园活动平台" {
		t.Fatalf("expected alias match, got %#v", alias)
	}
}

func TestResourceRepoExposesActiveFileSectionReaders(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "读取模式资源包装测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	sections, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:          "project-1",
			SectionType:         "project",
			SectionOrder:        1,
			Title:               "CampusHub校园活动平台",
			CanonicalEntityName: stringPointer("CampusHub校园活动平台"),
			AliasesJSON:         mustMarshalJSON(t, []string{"CampusHub"}),
			Content:             "项目一正文",
		},
		{
			SectionKey:   "project-2",
			SectionType:  "project",
			SectionOrder: 2,
			Title:        "选课助手",
			Content:      "项目二正文",
		},
		{
			SectionKey:   "skills",
			SectionType:  "skills",
			SectionOrder: 3,
			Title:        "技术栈",
			Content:      "Go PostgreSQL",
		},
	})
	if err != nil {
		t.Fatalf("replace sections: %v", err)
	}

	gotByID, err := resourceRepo.GetSectionByID(ctx, sections[0].ID)
	if err != nil {
		t.Fatalf("resource repo get section by id: %v", err)
	}
	if gotByID == nil || gotByID.Title != "CampusHub校园活动平台" {
		t.Fatalf("expected get section by id to return CampusHub, got %#v", gotByID)
	}

	gotByOrder, err := resourceRepo.GetSectionByOrder(ctx, version.ID, "project", 2)
	if err != nil {
		t.Fatalf("resource repo get section by order: %v", err)
	}
	if gotByOrder == nil || gotByOrder.Title != "选课助手" {
		t.Fatalf("expected second project, got %#v", gotByOrder)
	}

	gotByEntity, err := resourceRepo.FindSectionByEntity(ctx, version.ID, "CampusHub")
	if err != nil {
		t.Fatalf("resource repo find section by entity: %v", err)
	}
	if gotByEntity == nil || gotByEntity.Title != "CampusHub校园活动平台" {
		t.Fatalf("expected entity match, got %#v", gotByEntity)
	}

	readingSections, err := resourceRepo.ListSectionsForReading(ctx, version.ID, "project")
	if err != nil {
		t.Fatalf("resource repo list sections for reading: %v", err)
	}
	if len(readingSections) != 2 {
		t.Fatalf("expected 2 reading sections, got %d", len(readingSections))
	}
	if readingSections[0].SectionOrder != 1 || readingSections[1].SectionOrder != 2 {
		t.Fatalf("expected reading sections ordered by section_order, got %#v", readingSections)
	}
}

func TestStructuredDocumentRepoPersistsEmptyParserVersionAsNonNullText(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "结构化解析版本默认值测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	documentJSON := mustMarshalJSON(t, map[string]any{
		"source_format": "markdown",
		"blocks": []map[string]any{
			{"type": "paragraph", "text": "解析版本为空时也要能写库"},
		},
	})

	createdStructure, err := structureRepo.CreateVersionStructure(ctx, CreateVersionStructureParams{
		ResourceID:    resource.ID,
		VersionID:     version.ID,
		SourceFormat:  "markdown",
		ParserName:    "text",
		DocumentJSON:  documentJSON,
		ParserVersion: "",
	})
	if err != nil {
		t.Fatalf("create version structure with empty parser version: %v", err)
	}
	if createdStructure.ParserVersion == nil {
		t.Fatal("expected parser version pointer, got nil")
	}
	if *createdStructure.ParserVersion != "" {
		t.Fatalf("expected empty parser version, got %q", *createdStructure.ParserVersion)
	}

	var storedParserVersion string
	if err := pool.QueryRow(ctx, `
		SELECT parser_version
		FROM resource_version_structures
		WHERE version_id = $1
	`, version.ID).Scan(&storedParserVersion); err != nil {
		t.Fatalf("query parser_version: %v", err)
	}
	if storedParserVersion != "" {
		t.Fatalf("expected stored parser_version to be empty string, got %q", storedParserVersion)
	}
}

func TestStructuredDocumentRepoReplaceSectionsDefaultsNonNullGroundedFields(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "结构化分节默认值测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	sections, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{{
		SectionKey:   "general-1",
		SectionType:  " ",
		SectionOrder: 0,
		Title:        "概览",
		Summary:      "摘要",
		Content:      "正文",
	}})
	if err != nil {
		t.Fatalf("replace sections for version with defaults: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("expected 1 inserted section, got %d", len(sections))
	}

	if sections[0].SectionType != "unknown" {
		t.Fatalf("expected default section type %q, got %q", "unknown", sections[0].SectionType)
	}
	if sections[0].PageStart == nil || *sections[0].PageStart != 0 {
		t.Fatalf("expected default page_start 0, got %#v", sections[0].PageStart)
	}
	if sections[0].PageEnd == nil || *sections[0].PageEnd != 0 {
		t.Fatalf("expected default page_end 0, got %#v", sections[0].PageEnd)
	}

	var (
		storedSectionType string
		storedPageStart   int
		storedPageEnd     int
	)
	if err := pool.QueryRow(ctx, `
		SELECT section_type, page_start, page_end
		FROM resource_sections
		WHERE version_id = $1
	`, version.ID).Scan(&storedSectionType, &storedPageStart, &storedPageEnd); err != nil {
		t.Fatalf("query section defaults: %v", err)
	}
	if storedSectionType != "unknown" {
		t.Fatalf("expected stored section_type %q, got %q", "unknown", storedSectionType)
	}
	if storedPageStart != 0 || storedPageEnd != 0 {
		t.Fatalf("expected stored pages 0-0, got %d-%d", storedPageStart, storedPageEnd)
	}
}

func TestStructuredDocumentRepoReplaceVersionChunksPreservesGroundedMetadata(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "结构化分块替换测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	sections, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{{
		SectionKey:          "project-1",
		SectionType:         "project",
		SectionOrder:        1,
		Title:               "CampusHub校园活动平台",
		CanonicalEntityName: stringPointer("CampusHub校园活动平台"),
		AliasesJSON:         mustMarshalJSON(t, []string{"CampusHub"}),
		Summary:             "校园活动统一平台",
		Content:             "负责活动发布、报名与签到链路。",
		MetadataJSON:        mustMarshalJSON(t, map[string]any{"confidence": "high"}),
	}})
	if err != nil {
		t.Fatalf("replace sections for chunk test: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("expected 1 inserted section, got %d", len(sections))
	}

	metadataJSON := mustMarshalJSON(t, map[string]any{
		"labels": []string{"resume", "grounded"},
		"score":  0.92,
	})
	orderInSection := 2
	pageStart := 1
	pageEnd := 2

	if err := resourceRepo.ReplaceVersionChunks(ctx, version.ID, resource.ID, []ResourceChunkInput{{
		ChunkIndex:     0,
		SectionTitle:   "CampusHub校园活动平台",
		Content:        "负责活动发布、报名与签到链路。",
		Embedding:      testVector(0.66),
		SectionID:      &sections[0].ID,
		SectionType:    stringPointer("project"),
		ChunkRole:      stringPointer("project_work"),
		WindowGroupID:  stringPointer("project-1"),
		OrderInSection: &orderInSection,
		PageStart:      &pageStart,
		PageEnd:        &pageEnd,
		MetadataJSON:   metadataJSON,
	}}); err != nil {
		t.Fatalf("replace version chunks: %v", err)
	}

	var (
		storedSectionID      *string
		storedSectionType    *string
		storedChunkRole      *string
		storedWindowGroupID  *string
		storedOrderInSection *int
		storedPageStart      *int
		storedPageEnd        *int
		storedMetadataJSON   []byte
	)
	if err := pool.QueryRow(ctx, `
		SELECT section_id,
		       section_type,
		       chunk_role,
		       window_group_id,
		       order_in_section,
		       page_start,
		       page_end,
		       metadata_json
		FROM resource_chunks
		WHERE version_id = $1
	`, version.ID).Scan(
		&storedSectionID,
		&storedSectionType,
		&storedChunkRole,
		&storedWindowGroupID,
		&storedOrderInSection,
		&storedPageStart,
		&storedPageEnd,
		&storedMetadataJSON,
	); err != nil {
		t.Fatalf("query grounded chunk fields: %v", err)
	}

	if storedSectionID == nil || *storedSectionID != sections[0].ID {
		t.Fatalf("expected section_id %q, got %#v", sections[0].ID, storedSectionID)
	}
	if storedChunkRole == nil || *storedChunkRole != "project_work" {
		t.Fatalf("expected chunk_role %q, got %#v", "project_work", storedChunkRole)
	}
	if storedWindowGroupID == nil || *storedWindowGroupID != "project-1" {
		t.Fatalf("expected window_group_id %q, got %#v", "project-1", storedWindowGroupID)
	}
	if storedOrderInSection == nil || *storedOrderInSection != orderInSection {
		t.Fatalf("expected order_in_section %d, got %#v", orderInSection, storedOrderInSection)
	}
	if storedPageStart == nil || *storedPageStart != pageStart || storedPageEnd == nil || *storedPageEnd != pageEnd {
		t.Fatalf("expected pages %d-%d, got %#v-%#v", pageStart, pageEnd, storedPageStart, storedPageEnd)
	}
	if !jsonBodiesEqual(metadataJSON, storedMetadataJSON) {
		t.Fatalf("expected metadata json %s, got %s", string(metadataJSON), string(storedMetadataJSON))
	}
	if storedSectionType == nil || *storedSectionType != "project" {
		t.Fatalf("expected section_type %q, got %#v", "project", storedSectionType)
	}
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	return body
}

func jsonBodiesEqual(expected []byte, actual []byte) bool {
	var expectedValue any
	if err := json.Unmarshal(expected, &expectedValue); err != nil {
		return false
	}

	var actualValue any
	if err := json.Unmarshal(actual, &actualValue); err != nil {
		return false
	}

	expectedNormalized, err := json.Marshal(expectedValue)
	if err != nil {
		return false
	}
	actualNormalized, err := json.Marshal(actualValue)
	if err != nil {
		return false
	}

	return string(expectedNormalized) == string(actualNormalized)
}

func stringPointer(value string) *string {
	return &value
}

func intPointer(value int) *int {
	return &value
}
