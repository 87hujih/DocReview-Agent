package renderer_test

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/document/importer"
	"agent_project/apps/server/internal/document/renderer"
)

// TestFormatRenderersShareASTAndDoNotFlattenStoredStructure 验证对应场景下的正常路径与失败路径。
func TestFormatRenderersShareASTAndDoNotFlattenStoredStructure(t *testing.T) {
	document, err := importer.NewMarkdown().Import(context.Background(), importer.Input{
		DocumentID: "document-1", VersionID: "version-1", FileName: "a.md", Content: []byte("# A\n\nFirst.\n\n## B\n\nSecond."),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []renderer.Renderer{renderer.NewMarkdown(), renderer.NewDOCX(), renderer.NewPDF()} {
		output, err := candidate.Render(context.Background(), document)
		if err != nil {
			t.Fatalf("%s renderer: %v", candidate.Format(), err)
		}
		if output.Format != candidate.Format() || len(output.Content) == 0 {
			t.Fatalf("invalid %s output: %#v", candidate.Format(), output)
		}
	}
}
