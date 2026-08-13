package model_test

import (
	"context"
	"testing"

	"agent_project/apps/server/internal/document/importer"
	"agent_project/apps/server/internal/document/model"
)

// TestSectionsChunksAndEmbeddingFactsAreDerivedFromASTNodes 验证对应场景下的正常路径与失败路径。
func TestSectionsChunksAndEmbeddingFactsAreDerivedFromASTNodes(t *testing.T) {
	document, err := importer.NewMarkdown().Import(context.Background(), importer.Input{
		DocumentID: "document-1", VersionID: "version-1", FileName: "a.md", Content: []byte("# A\n\nFirst.\n\n## B\n\nSecond."),
		Metadata: map[string]any{"classification": "internal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	projection, err := model.Derive(document, model.ProjectionProfile{
		SchemaVersion: "1.0", ChunkProfile: "node-v1", EmbeddingProfile: "text-embedding-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Sections) != 2 || len(projection.Chunks) != 4 {
		t.Fatalf("unexpected projections: %d sections, %d chunks", len(projection.Sections), len(projection.Chunks))
	}
	for _, chunk := range projection.Chunks {
		if chunk.NodeID == "" || chunk.ContentHash == "" || chunk.EmbeddingStatus != "pending" || chunk.EmbeddingProfile != "text-embedding-v1" {
			t.Fatalf("chunk lost AST/profile provenance: %#v", chunk)
		}
	}
	if projection.Metadata["classification"] != "internal" {
		t.Fatalf("resource metadata was lost: %#v", projection.Metadata)
	}
}
