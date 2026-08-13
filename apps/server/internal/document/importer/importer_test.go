package importer_test

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/document/importer"
	"agent_project/apps/server/internal/document/model"
)

// TestRepeatedImportKeepsStableDistinctNodeIDs 验证对应场景下的正常路径与失败路径。
func TestRepeatedImportKeepsStableDistinctNodeIDs(t *testing.T) {
	t.Parallel()

	input := importer.Input{
		DocumentID: "document-1",
		VersionID:  "version-1",
		FileName:   "guide.md",
		Content:    []byte("# Guide\n\nFirst paragraph.\n\n## Details\n\nSecond paragraph."),
	}
	first, err := importer.NewMarkdown().Import(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := importer.NewMarkdown().Import(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}

	firstNodes := model.Flatten(first.Root)
	secondNodes := model.Flatten(second.Root)
	if len(firstNodes) < 5 || len(firstNodes) != len(secondNodes) {
		t.Fatalf("unexpected node counts: %d and %d", len(firstNodes), len(secondNodes))
	}
	seen := make(map[string]struct{}, len(firstNodes))
	for index := range firstNodes {
		if firstNodes[index].NodeID != secondNodes[index].NodeID {
			t.Fatalf("node %d changed ID: %q != %q", index, firstNodes[index].NodeID, secondNodes[index].NodeID)
		}
		if _, exists := seen[firstNodes[index].NodeID]; exists {
			t.Fatalf("node ID collision: %s", firstNodes[index].NodeID)
		}
		seen[firstNodes[index].NodeID] = struct{}{}
	}
}

// TestMarkdownDOCXAndPDFImportersShareCanonicalContract 验证对应场景下的正常路径与失败路径。
func TestMarkdownDOCXAndPDFImportersShareCanonicalContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		importer importer.Importer
		fileName string
		content  string
		format   string
	}{
		{name: "markdown", importer: importer.NewMarkdown(), fileName: "a.md", content: "# A\n\nBody", format: "markdown"},
		{name: "docx", importer: importer.NewDOCX(), fileName: "a.docx", content: "A\n\nBody", format: "docx"},
		{name: "pdf", importer: importer.NewPDF(), fileName: "a.pdf", content: "Page one\fPage two", format: "pdf"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := test.importer.Import(context.Background(), importer.Input{
				DocumentID: "document-1", VersionID: "version-1", FileName: test.fileName, Content: []byte(test.content),
			})
			if err != nil {
				t.Fatal(err)
			}
			if document.SchemaVersion != model.SchemaVersion || document.SourceFormat != test.format {
				t.Fatalf("unexpected contract: schema=%q format=%q", document.SchemaVersion, document.SourceFormat)
			}
			if document.Root == nil || document.Root.Type != model.NodeDocument || document.ContentHash == "" {
				t.Fatalf("incomplete canonical document: %#v", document)
			}
			if err := model.Validate(document); err != nil {
				t.Fatalf("canonical AST invalid: %v", err)
			}
		})
	}
}

// TestPDFImporterPreservesPageMappings 验证对应场景下的正常路径与失败路径。
func TestPDFImporterPreservesPageMappings(t *testing.T) {
	document, err := importer.NewPDF().Import(context.Background(), importer.Input{
		DocumentID: "document-1", VersionID: "version-1", FileName: "a.pdf", Content: []byte("one\ftwo"),
	})
	if err != nil {
		t.Fatal(err)
	}
	nodes := model.Flatten(document.Root)
	if len(nodes) != 3 {
		t.Fatalf("expected root and two page nodes, got %d", len(nodes))
	}
	if nodes[1].PageMapping[0].Page != 1 || nodes[2].PageMapping[0].Page != 2 {
		t.Fatalf("page mappings were not preserved: %#v %#v", nodes[1].PageMapping, nodes[2].PageMapping)
	}
}

// TestLargeExtractedDOCXDoesNotCollapseIntoOneContentNode 验证对应场景下的正常路径与失败路径。
func TestLargeExtractedDOCXDoesNotCollapseIntoOneContentNode(t *testing.T) {
	document, err := importer.NewDOCX().Import(context.Background(), importer.Input{
		DocumentID: "document-1", VersionID: "version-1", FileName: "large.docx",
		Content: []byte("paragraph one\nparagraph two\nparagraph three"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if nodes := model.Flatten(document.Root); len(nodes) != 4 {
		t.Fatalf("DOCX collapsed into a blob: got %d nodes", len(nodes))
	}
}
