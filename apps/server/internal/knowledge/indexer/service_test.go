package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	documentnormalize "agent_project/apps/server/internal/document/normalize"
	"agent_project/apps/server/internal/knowledge/sections"
	"agent_project/apps/server/internal/storage/postgres"
)

// TestReindexVersionRebuildsMarkdownChunks 验证`reindexVersionRebuildsMarkdownChunks`在特定边界条件下的行为，防止同类回归。
func TestReindexVersionRebuildsMarkdownChunks(t *testing.T) {
	repo := &fakeIndexRepo{}
	service := NewService(repo, fakeEmbedder{})

	err := service.ReindexVersion(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-1",
			Title: "考勤制度",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content: strings.Join([]string{
				"# 考勤制度",
				"",
				"## 考勤管理",
				"员工必须在九点前签到。",
				"",
				"## 请假管理",
				"请假需要提前审批。",
			}, "\n"),
		},
	})
	if err != nil {
		t.Fatalf("reindex version: %v", err)
	}

	if repo.replaceCalls != 1 {
		t.Fatalf("expected exactly 1 replace call, got %d", repo.replaceCalls)
	}
	if repo.lastVersionID != "version-1" {
		t.Fatalf("expected version id %q, got %q", "version-1", repo.lastVersionID)
	}
	if repo.lastResourceID != "resource-1" {
		t.Fatalf("expected resource id %q, got %q", "resource-1", repo.lastResourceID)
	}
	if len(repo.lastChunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(repo.lastChunks))
	}
	if repo.lastChunks[0].SectionTitle != "考勤管理" {
		t.Fatalf("expected first section title %q, got %q", "考勤管理", repo.lastChunks[0].SectionTitle)
	}
	if repo.lastChunks[0].SectionType == nil || *repo.lastChunks[0].SectionType != string(documentnormalize.SectionTypeSection) {
		t.Fatalf("expected first section type %q, got %#v", documentnormalize.SectionTypeSection, repo.lastChunks[0].SectionType)
	}
	if repo.lastChunks[0].WindowGroupID == nil || *repo.lastChunks[0].WindowGroupID != "考勤管理" {
		t.Fatalf("expected first window_group_id %q, got %#v", "考勤管理", repo.lastChunks[0].WindowGroupID)
	}
	if repo.lastChunks[1].SectionTitle != "请假管理" {
		t.Fatalf("expected second section title %q, got %q", "请假管理", repo.lastChunks[1].SectionTitle)
	}
	if repo.lastChunks[1].SectionType == nil || *repo.lastChunks[1].SectionType != string(documentnormalize.SectionTypeSection) {
		t.Fatalf("expected second section type %q, got %#v", documentnormalize.SectionTypeSection, repo.lastChunks[1].SectionType)
	}
	if repo.lastChunks[1].WindowGroupID == nil || *repo.lastChunks[1].WindowGroupID != "请假管理" {
		t.Fatalf("expected second window_group_id %q, got %#v", "请假管理", repo.lastChunks[1].WindowGroupID)
	}
}

// TestReindexVersionUsesSingleChunkFallback 验证`reindexVersion`在依赖选择路径下的行为，防止同类回归。
func TestReindexVersionUsesSingleChunkFallback(t *testing.T) {
	repo := &fakeIndexRepo{}
	service := NewService(repo, fakeEmbedder{})

	err := service.ReindexVersion(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-2",
			Title: "员工手册",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-2",
			ResourceID: "resource-2",
			Content:    "这是没有二级标题的正文。",
		},
	})
	if err != nil {
		t.Fatalf("reindex version with fallback: %v", err)
	}

	if len(repo.lastChunks) != 1 {
		t.Fatalf("expected 1 fallback chunk, got %d", len(repo.lastChunks))
	}
	if repo.lastChunks[0].SectionTitle != sections.WholeDocumentTitle {
		t.Fatalf("expected fallback section title %q, got %q", sections.WholeDocumentTitle, repo.lastChunks[0].SectionTitle)
	}
	if repo.lastChunks[0].Content != "这是没有二级标题的正文。" {
		t.Fatalf("expected fallback content %q, got %q", "这是没有二级标题的正文。", repo.lastChunks[0].Content)
	}
	if repo.lastChunks[0].SectionType == nil || *repo.lastChunks[0].SectionType != string(documentnormalize.SectionTypeDocument) {
		t.Fatalf("expected fallback section type %q, got %#v", documentnormalize.SectionTypeDocument, repo.lastChunks[0].SectionType)
	}
	if repo.lastChunks[0].ChunkRole == nil || *repo.lastChunks[0].ChunkRole != "section_body" {
		t.Fatalf("expected fallback chunk role %q, got %#v", "section_body", repo.lastChunks[0].ChunkRole)
	}
	if repo.lastChunks[0].WindowGroupID == nil || *repo.lastChunks[0].WindowGroupID != sections.WholeDocumentTitle {
		t.Fatalf("expected fallback window_group_id %q, got %#v", sections.WholeDocumentTitle, repo.lastChunks[0].WindowGroupID)
	}
}

// TestBuildVersionChunksKeepsWholeDocumentFallback 验证`buildVersionChunks`在状态保持路径下的行为，防止同类回归。
func TestBuildVersionChunksKeepsWholeDocumentFallback(t *testing.T) {
	repo := &fakeIndexRepo{}
	service := NewService(repo, fakeEmbedder{})

	chunks, err := service.BuildVersionChunks(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-5",
			Title: "员工手册",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-5",
			ResourceID: "resource-5",
			Content:    "这是没有二级标题的正文。",
		},
	})
	if err != nil {
		t.Fatalf("build version chunks: %v", err)
	}

	if len(chunks) != 1 {
		t.Fatalf("expected 1 fallback chunk, got %d", len(chunks))
	}
	if chunks[0].SectionTitle != sections.WholeDocumentTitle {
		t.Fatalf("expected fallback section title %q, got %q", sections.WholeDocumentTitle, chunks[0].SectionTitle)
	}
	if chunks[0].SectionType == nil || *chunks[0].SectionType != string(documentnormalize.SectionTypeDocument) {
		t.Fatalf("expected fallback section type %q, got %#v", documentnormalize.SectionTypeDocument, chunks[0].SectionType)
	}
	if chunks[0].ChunkRole == nil || *chunks[0].ChunkRole != "section_body" {
		t.Fatalf("expected fallback chunk role %q, got %#v", "section_body", chunks[0].ChunkRole)
	}
	if chunks[0].WindowGroupID == nil || *chunks[0].WindowGroupID != sections.WholeDocumentTitle {
		t.Fatalf("expected fallback window_group_id %q, got %#v", sections.WholeDocumentTitle, chunks[0].WindowGroupID)
	}
}

// TestBuildVersionChunksFromProjectSections 验证`buildVersionChunksFromProjectSections`在特定边界条件下的行为，防止同类回归。
func TestBuildVersionChunksFromProjectSections(t *testing.T) {
	repo := &fakeIndexRepo{}
	service := NewService(repo, fakeEmbedder{})

	chunks, err := service.BuildVersionChunks(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-project",
			Title: "简历",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-project",
			ResourceID: "resource-project",
			Content:    "CampusHub校园活动平台\n负责活动发布与报名。",
		},
		Sections: []postgres.ResourceSection{
			{
				ID:                  "00000000-0000-0000-0000-000000000001",
				ResourceID:          "resource-project",
				VersionID:           "version-project",
				SectionKey:          "project-1",
				SectionType:         "project",
				SectionOrder:        1,
				Title:               "CampusHub校园活动平台",
				CanonicalEntityName: stringPointer("CampusHub校园活动平台"),
				AliasesJSON:         mustMarshalJSON(t, []string{"CampusHub校园活动平台", "CampusHub"}),
				Summary:             "校园活动统一平台",
				Content:             "负责活动发布、报名与签到链路。",
				MetadataJSON: mustMarshalJSON(t, map[string]any{
					"tech_stack": []string{"Go", "Redis", "gRPC"},
				}),
			},
		},
	})
	if err != nil {
		t.Fatalf("build version chunks from sections: %v", err)
	}

	if len(chunks) != 5 {
		t.Fatalf("expected 5 grounded project chunks, got %d", len(chunks))
	}
	expectedRoles := []string{"section_summary", "project_name", "tech_stack", "project_description", "project_work"}
	for index, role := range expectedRoles {
		if chunks[index].ChunkRole == nil || *chunks[index].ChunkRole != role {
			t.Fatalf("expected chunk %d role %q, got %#v", index, role, chunks[index].ChunkRole)
		}
		if chunks[index].WindowGroupID == nil || *chunks[index].WindowGroupID != "project-1" {
			t.Fatalf("expected chunk %d to reuse window_group_id project-1, got %#v", index, chunks[index].WindowGroupID)
		}
	}
	if chunks[2].Content != "Go Redis gRPC" {
		t.Fatalf("expected tech stack chunk content, got %q", chunks[2].Content)
	}
	if strings.Contains(chunks[3].Content, "项目描述：") {
		t.Fatalf("expected low-value labels to be removed from chunk content, got %q", chunks[3].Content)
	}
}

// TestBuildVersionChunksKeepsOrderInSection 验证`buildVersionChunks`在状态保持路径下的行为，防止同类回归。
func TestBuildVersionChunksKeepsOrderInSection(t *testing.T) {
	repo := &fakeIndexRepo{}
	service := NewService(repo, fakeEmbedder{})

	chunks, err := service.BuildVersionChunks(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-project-order",
			Title: "简历",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-project-order",
			ResourceID: "resource-project-order",
			Content:    "CampusHub校园活动平台\n负责活动发布与报名。",
		},
		Sections: []postgres.ResourceSection{
			{
				ID:                  "00000000-0000-0000-0000-000000000002",
				ResourceID:          "resource-project-order",
				VersionID:           "version-project-order",
				SectionKey:          "project-1",
				SectionType:         "project",
				SectionOrder:        1,
				Title:               "CampusHub校园活动平台",
				CanonicalEntityName: stringPointer("CampusHub校园活动平台"),
				Summary:             "校园活动统一平台",
				Content:             "负责活动发布、报名与签到链路。",
				MetadataJSON: mustMarshalJSON(t, map[string]any{
					"tech_stack": []string{"Go", "Redis"},
				}),
			},
		},
	})
	if err != nil {
		t.Fatalf("build version chunks from ordered sections: %v", err)
	}

	for index, chunk := range chunks {
		expectedOrder := index + 1
		if chunk.OrderInSection == nil || *chunk.OrderInSection != expectedOrder {
			t.Fatalf("expected order_in_section %d, got %#v", expectedOrder, chunk.OrderInSection)
		}
	}
}

// TestReindexVersionClearsChunksForBlankContent 验证`reindexVersionClearsChunksForBlankContent`在特定边界条件下的行为，防止同类回归。
func TestReindexVersionClearsChunksForBlankContent(t *testing.T) {
	repo := &fakeIndexRepo{}
	service := NewService(repo, fakeEmbedder{})

	err := service.ReindexVersion(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-3",
			Title: "空文档",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-3",
			ResourceID: "resource-3",
			Content:    "   \n\t",
		},
	})
	if err != nil {
		t.Fatalf("reindex blank version: %v", err)
	}

	if repo.replaceCalls != 1 {
		t.Fatalf("expected replace call for blank content, got %d", repo.replaceCalls)
	}
	if len(repo.lastChunks) != 0 {
		t.Fatalf("expected blank content to clear chunks, got %d", len(repo.lastChunks))
	}
}

// TestReindexVersionReturnsEmbedderError 验证`reindexVersion`在返回值分支下的行为，防止同类回归。
func TestReindexVersionReturnsEmbedderError(t *testing.T) {
	repo := &fakeIndexRepo{}
	expectedErr := errors.New("embedding 失败")
	service := NewService(repo, fakeEmbedder{
		err: expectedErr,
	})

	err := service.ReindexVersion(context.Background(), Input{
		Resource: postgres.Resource{
			ID:    "resource-4",
			Title: "错误文档",
		},
		Version: postgres.ResourceVersion{
			ID:         "version-4",
			ResourceID: "resource-4",
			Content:    "## 章节\n正文",
		},
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected embedder error %v, got %v", expectedErr, err)
	}

	if repo.replaceCalls != 0 {
		t.Fatalf("expected no replace call when embedder fails, got %d", repo.replaceCalls)
	}
}

// fakeIndexRepo 作为索引仓储的测试替身，用于在用例里提供可控的依赖行为。
type fakeIndexRepo struct {
	replaceCalls   int
	lastVersionID  string
	lastResourceID string
	lastChunks     []postgres.ResourceChunkInput
}

// ReplaceVersionChunks 实现测试替身需要的 `ReplaceVersionChunks` 接口方法，为用例分支提供可控返回。
func (r *fakeIndexRepo) ReplaceVersionChunks(_ context.Context, versionID string, resourceID string, chunks []postgres.ResourceChunkInput) error {
	r.replaceCalls++
	r.lastVersionID = versionID
	r.lastResourceID = resourceID
	r.lastChunks = append([]postgres.ResourceChunkInput(nil), chunks...)
	return nil
}

// fakeEmbedder 作为Embedder的测试替身，用于在用例里提供可控的依赖行为。
type fakeEmbedder struct {
	err error
}

// Embed 实现测试替身需要的 `Embed` 接口方法，为用例分支提供可控返回。
func (e fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}

	vectors := make([][]float32, 0, len(texts))
	for index := range texts {
		values := make([]float32, 1024)
		for offset := range values {
			values[offset] = float32(index+1) + float32(offset%5)/10
		}
		vectors = append(vectors, values)
	}

	return vectors, nil
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

// stringPointer 返回字符串指针，简化构造可选文本字段时的样板代码。
func stringPointer(value string) *string {
	return &value
}
