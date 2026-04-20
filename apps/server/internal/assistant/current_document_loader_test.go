package assistant

import (
	"context"
	"reflect"
	"testing"

	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/storage/postgres"
)

// TestCurrentDocumentLoaderBuildsDocumentFromActiveResource 验证 loader 会从当前活跃资源组装完整文档视图。
func TestCurrentDocumentLoaderBuildsDocumentFromActiveResource(t *testing.T) {
	loader := NewCurrentDocumentLoader(&fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "这是整份简历正文。",
		},
		allSections: []postgres.ResourceSection{
			{ID: "section-1", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 1, Title: "CampusHub", Content: "项目一正文"},
			{ID: "section-2", ResourceID: "resource-1", VersionID: "version-1", SectionType: "project", SectionOrder: 2, Title: "选课助手", Content: "项目二正文"},
		},
	})

	document, err := loader.Load(context.Background(), &resourceContext{
		ID:     "resource-1",
		Title:  "产品经理简历",
		Source: "upload",
	})
	if err != nil {
		t.Fatalf("load current document: %v", err)
	}
	if document == nil {
		t.Fatal("expected current document")
	}
	if !document.Ready {
		t.Fatal("expected current document to be ready")
	}
	if document.ResourceID != "resource-1" || document.VersionID != "version-1" {
		t.Fatalf("unexpected current document identity: %#v", document)
	}
	if document.Title != "产品经理简历" || document.SourceType != "upload" {
		t.Fatalf("unexpected current document metadata: %#v", document)
	}
	if document.FullText != "这是整份简历正文。" {
		t.Fatalf("expected full text to be preserved, got %q", document.FullText)
	}
	if len(document.Sections) != 2 || document.Sections[0].ID != "section-1" || document.Sections[1].ID != "section-2" {
		t.Fatalf("expected ordered sections, got %#v", document.Sections)
	}
}

// TestCurrentDocumentLoaderReturnsNilWhenNoActiveResource 验证 loader 在没有当前资源时不会伪造文档对象。
func TestCurrentDocumentLoaderReturnsNilWhenNoActiveResource(t *testing.T) {
	loader := NewCurrentDocumentLoader(&fakeCurrentFileSectionReader{})

	document, err := loader.Load(context.Background(), nil)
	if err != nil {
		t.Fatalf("load current document: %v", err)
	}
	if document != nil {
		t.Fatalf("expected nil current document, got %#v", document)
	}
}

// TestCurrentDocumentLoaderKeepsFullTextEvenWhenSectionsEmpty 验证 loader 不会因为 sections 为空而丢掉全文可见性。
func TestCurrentDocumentLoaderKeepsFullTextEvenWhenSectionsEmpty(t *testing.T) {
	loader := NewCurrentDocumentLoader(&fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "这是只有全文没有结构化 section 的文档。",
		},
	})

	document, err := loader.Load(context.Background(), &resourceContext{
		ID:     "resource-1",
		Title:  "纯文本说明",
		Source: "upload",
	})
	if err != nil {
		t.Fatalf("load current document: %v", err)
	}
	if document == nil {
		t.Fatal("expected current document")
	}
	if !document.Ready {
		t.Fatal("expected current document to stay ready")
	}
	if document.FullText != "这是只有全文没有结构化 section 的文档。" {
		t.Fatalf("expected full text to be preserved, got %q", document.FullText)
	}
	if len(document.Sections) != 0 {
		t.Fatalf("expected empty sections, got %#v", document.Sections)
	}
}

// TestCurrentDocumentLoaderIncludesOutline 验证 loader 会把当前版本的 outline 一并装载进 current document。
func TestCurrentDocumentLoaderIncludesOutline(t *testing.T) {
	loader := NewCurrentDocumentLoader(&fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "## 项目经历\n### 1. CampusHub\n负责活动报名。\n### 2. 选课助手\n负责选课推荐。",
		},
		versionStructure: &postgres.ResourceVersionStructure{
			VersionID: "version-1",
			DocumentJSON: mustMarshalOutlineDocumentJSON(t, documentparser.ParsedDocument{
				SourceFormat: "markdown",
				Blocks: []documentparser.Block{
					{Type: documentparser.BlockHeading, Level: 2, Text: "项目经历"},
					{Type: documentparser.BlockHeading, Level: 3, Text: "1. CampusHub"},
					{Type: documentparser.BlockParagraph, Text: "负责活动报名。"},
					{Type: documentparser.BlockHeading, Level: 3, Text: "2. 选课助手"},
					{Type: documentparser.BlockParagraph, Text: "负责选课推荐。"},
				},
			}),
		},
	})

	document, err := loader.Load(context.Background(), &resourceContext{
		ID:     "resource-1",
		Title:  "产品经理简历",
		Source: "upload",
	})
	if err != nil {
		t.Fatalf("load current document: %v", err)
	}
	if document == nil {
		t.Fatal("expected current document")
	}

	projects := filterOutlineNodesByKind(document.Outline, OutlineNodeProjectItem)
	if len(projects) != 2 {
		t.Fatalf("expected 2 outline project nodes, got %#v", document.Outline)
	}
}

// TestCurrentDocumentLoaderBuildsStableNodeIDs 验证同一当前文件重复装载时 outline node_id 保持稳定。
func TestCurrentDocumentLoaderBuildsStableNodeIDs(t *testing.T) {
	reader := &fakeCurrentFileSectionReader{
		currentVersion: &postgres.ResourceVersion{
			ID:         "version-1",
			ResourceID: "resource-1",
			Content:    "## 项目经历\n### 1. CampusHub\n负责活动报名。\n### 2. 选课助手\n负责选课推荐。",
		},
		versionStructure: &postgres.ResourceVersionStructure{
			VersionID: "version-1",
			DocumentJSON: mustMarshalOutlineDocumentJSON(t, documentparser.ParsedDocument{
				SourceFormat: "markdown",
				Blocks: []documentparser.Block{
					{Type: documentparser.BlockHeading, Level: 2, Text: "项目经历"},
					{Type: documentparser.BlockHeading, Level: 3, Text: "1. CampusHub"},
					{Type: documentparser.BlockParagraph, Text: "负责活动报名。"},
					{Type: documentparser.BlockHeading, Level: 3, Text: "2. 选课助手"},
					{Type: documentparser.BlockParagraph, Text: "负责选课推荐。"},
				},
			}),
		},
	}
	loader := NewCurrentDocumentLoader(reader)

	first, err := loader.Load(context.Background(), &resourceContext{
		ID:     "resource-1",
		Title:  "产品经理简历",
		Source: "upload",
	})
	if err != nil {
		t.Fatalf("first load current document: %v", err)
	}
	second, err := loader.Load(context.Background(), &resourceContext{
		ID:     "resource-1",
		Title:  "产品经理简历",
		Source: "upload",
	})
	if err != nil {
		t.Fatalf("second load current document: %v", err)
	}

	if !reflect.DeepEqual(collectOutlineNodeIDs(first.Outline), collectOutlineNodeIDs(second.Outline)) {
		t.Fatalf("expected stable outline node ids, got first=%#v second=%#v", collectOutlineNodeIDs(first.Outline), collectOutlineNodeIDs(second.Outline))
	}
}

func collectOutlineNodeIDs(nodes []OutlineNode) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.NodeID)
	}

	return ids
}
