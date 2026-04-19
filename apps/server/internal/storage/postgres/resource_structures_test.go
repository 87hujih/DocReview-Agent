package postgres

import (
	"encoding/json"
	"testing"
)

// TestStructuredDocumentRepoPersistsVersionStructureAndSections 验证`structuredDocumentRepo`在写入或副作用路径下的行为，防止同类回归。
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

// TestStructuredDocumentRepoPersistsEmptyParserVersionAsNonNullText 验证`structuredDocumentRepo`在写入或副作用路径下的行为，防止同类回归。
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

// TestStructuredDocumentRepoReplaceSectionsDefaultsNonNullGroundedFields 验证`structuredDocumentRepoReplaceSectionsDefaultsNonNullGroundedFields`在特定边界条件下的行为，防止同类回归。
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

// TestStructuredDocumentRepoReplaceVersionChunksPreservesGroundedMetadata 验证`structuredDocumentRepoReplaceVersionChunks`在状态保持路径下的行为，防止同类回归。
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

// TestResourceStructureRepoGetsSectionByID 验证`resourceStructureRepoGetsSectionByID`在特定边界条件下的行为，防止同类回归。
func TestResourceStructureRepoGetsSectionByID(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "按ID读取分节测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	inserted, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:          "project-1",
			SectionType:         "project",
			SectionOrder:        1,
			Title:               "CampusHub",
			CanonicalEntityName: stringPointer("CampusHub"),
			AliasesJSON:         mustMarshalJSON(t, []string{"CampusHub校园活动平台"}),
			Content:             "项目一正文",
		},
	})
	if err != nil {
		t.Fatalf("replace sections: %v", err)
	}
	if len(inserted) != 1 {
		t.Fatalf("expected 1 inserted section, got %d", len(inserted))
	}

	got, err := structureRepo.GetSectionByID(ctx, inserted[0].ID)
	if err != nil {
		t.Fatalf("get section by id: %v", err)
	}
	if got == nil {
		t.Fatal("expected section, got nil")
	}
	if got.ID != inserted[0].ID {
		t.Fatalf("expected section id %q, got %q", inserted[0].ID, got.ID)
	}
	if got.Title != "CampusHub" {
		t.Fatalf("expected title %q, got %q", "CampusHub", got.Title)
	}
}

// TestResourceStructureRepoGetsSectionByOrder 验证`resourceStructureRepoGetsSectionByOrder`在特定边界条件下的行为，防止同类回归。
func TestResourceStructureRepoGetsSectionByOrder(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "按序号读取分节测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	_, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:          "project-1",
			SectionType:         "project",
			SectionOrder:        1,
			Title:               "CampusHub",
			CanonicalEntityName: stringPointer("CampusHub"),
			Content:             "项目一正文",
		},
		{
			SectionKey:          "project-2",
			SectionType:         "project",
			SectionOrder:        2,
			Title:               "选课助手",
			CanonicalEntityName: stringPointer("选课助手"),
			Content:             "项目二正文",
		},
	})
	if err != nil {
		t.Fatalf("replace sections: %v", err)
	}

	got, err := structureRepo.GetSectionByOrder(ctx, version.ID, "project", 2)
	if err != nil {
		t.Fatalf("get section by order: %v", err)
	}
	if got == nil {
		t.Fatal("expected second project section, got nil")
	}
	if got.Title != "选课助手" {
		t.Fatalf("expected second project title %q, got %q", "选课助手", got.Title)
	}
}

// TestResourceStructureRepoFindsSectionByEntityNameOrAlias 验证`resourceStructureRepoFindsSectionByEntityNameOrAlias`在特定边界条件下的行为，防止同类回归。
func TestResourceStructureRepoFindsSectionByEntityNameOrAlias(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "按实体读取分节测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	_, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:          "project-1",
			SectionType:         "project",
			SectionOrder:        1,
			Title:               "CampusHub",
			CanonicalEntityName: stringPointer("CampusHub校园活动平台"),
			AliasesJSON:         mustMarshalJSON(t, []string{"CampusHub", "活动平台"}),
			Content:             "项目一正文",
		},
	})
	if err != nil {
		t.Fatalf("replace sections: %v", err)
	}

	got, err := structureRepo.FindSectionByEntity(ctx, version.ID, "CampusHub")
	if err != nil {
		t.Fatalf("find section by alias: %v", err)
	}
	if got == nil {
		t.Fatal("expected section by alias, got nil")
	}
	if got.Title != "CampusHub" {
		t.Fatalf("expected alias match title %q, got %q", "CampusHub", got.Title)
	}

	got, err = structureRepo.FindSectionByEntity(ctx, version.ID, "CampusHub校园活动平台")
	if err != nil {
		t.Fatalf("find section by canonical entity: %v", err)
	}
	if got == nil {
		t.Fatal("expected section by canonical entity, got nil")
	}
	if got.SectionKey != "project-1" {
		t.Fatalf("expected section key %q, got %q", "project-1", got.SectionKey)
	}
}

// TestResourceRepoExposesCurrentFileReadingPrimitives 验证`resourceRepoExposesCurrentFileReadingPrimitives`在特定边界条件下的行为，防止同类回归。
func TestResourceRepoExposesCurrentFileReadingPrimitives(t *testing.T) {
	pool := newTestPool(t)
	resourceRepo := NewResourceRepo(pool)
	structureRepo := NewResourceStructureRepo(pool)
	ctx := testContext(t)

	resource, version := seedResourceVersion(t, resourceRepo, ctx, "资源仓储读取包装测试-"+uniqueSuffix())
	t.Cleanup(func() {
		cleanupResource(t, pool, resource.ID)
	})

	inserted, err := structureRepo.ReplaceSectionsForVersion(ctx, version.ID, resource.ID, []ResourceSectionInput{
		{
			SectionKey:          "project-1",
			SectionType:         "project",
			SectionOrder:        1,
			Title:               "CampusHub",
			CanonicalEntityName: stringPointer("CampusHub校园活动平台"),
			AliasesJSON:         mustMarshalJSON(t, []string{"CampusHub"}),
			Content:             "项目一正文",
		},
		{
			SectionKey:          "project-2",
			SectionType:         "project",
			SectionOrder:        2,
			Title:               "选课助手",
			CanonicalEntityName: stringPointer("选课助手"),
			Content:             "项目二正文",
		},
	})
	if err != nil {
		t.Fatalf("replace sections: %v", err)
	}

	gotByID, err := resourceRepo.GetSectionByID(ctx, inserted[0].ID)
	if err != nil {
		t.Fatalf("resource repo get section by id: %v", err)
	}
	if gotByID == nil || gotByID.ID != inserted[0].ID {
		t.Fatalf("expected section by id %q, got %#v", inserted[0].ID, gotByID)
	}

	gotByOrder, err := resourceRepo.GetSectionByOrder(ctx, version.ID, "project", 2)
	if err != nil {
		t.Fatalf("resource repo get section by order: %v", err)
	}
	if gotByOrder == nil || gotByOrder.Title != "选课助手" {
		t.Fatalf("expected second project via resource repo, got %#v", gotByOrder)
	}

	gotByEntity, err := resourceRepo.FindSectionByEntity(ctx, version.ID, "CampusHub")
	if err != nil {
		t.Fatalf("resource repo find section by entity: %v", err)
	}
	if gotByEntity == nil || gotByEntity.SectionKey != "project-1" {
		t.Fatalf("expected entity match project-1 via resource repo, got %#v", gotByEntity)
	}

	sections, err := resourceRepo.ListSectionsForReading(ctx, version.ID, "project")
	if err != nil {
		t.Fatalf("resource repo list sections for reading: %v", err)
	}
	if len(sections) != 2 {
		t.Fatalf("expected 2 project sections, got %d", len(sections))
	}
	if sections[0].Title != "CampusHub" || sections[1].Title != "选课助手" {
		t.Fatalf("expected ordered project sections, got %#v", sections)
	}
}

// mustMarshalJSON 在测试里强制构造 `MarshalJSON`，失败时立即终止当前用例。
func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()

	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	return body
}

// jsonBodiesEqual 为测试场景处理 `JSONBodiesEqual` 的辅助步骤，减少重复搭建逻辑。
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

// stringPointer 返回字符串指针，简化构造可选文本字段时的样板代码。
func stringPointer(value string) *string {
	return &value
}

// intPointer 返回整数指针，减少测试里构造可选页码字段时的重复样板。
func intPointer(value int) *int {
	return &value
}
