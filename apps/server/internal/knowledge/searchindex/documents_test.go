package searchindex

import (
	"testing"

	"agent_project/apps/server/internal/storage/postgres"
)

func TestBuildChunkDocumentPreservesStructuredChunkFields(t *testing.T) {
	chunk := postgres.ResourceChunk{
		ID:            "chunk-1",
		ResourceID:    "resource-1",
		VersionID:     "version-1",
		SectionID:     "section-1",
		SectionType:   "project",
		ChunkRole:     "section_body",
		ChunkIndex:    2,
		SectionTitle:  "项目经验",
		Content:       "负责跨区域项目交付。",
		WindowGroupID: "window-1",
		PageStart:     3,
		PageEnd:       5,
		Metadata: map[string]any{
			"source": "normalized",
		},
	}

	doc := BuildChunkDocument(chunk)

	if doc.DocumentID != chunk.ID {
		t.Fatalf("expected document id %q, got %q", chunk.ID, doc.DocumentID)
	}
	if doc.ResourceID != chunk.ResourceID {
		t.Fatalf("expected resource id %q, got %q", chunk.ResourceID, doc.ResourceID)
	}
	if doc.VersionID != chunk.VersionID {
		t.Fatalf("expected version id %q, got %q", chunk.VersionID, doc.VersionID)
	}
	if doc.SectionID != chunk.SectionID {
		t.Fatalf("expected section id %q, got %q", chunk.SectionID, doc.SectionID)
	}
	if doc.SectionType != chunk.SectionType {
		t.Fatalf("expected section type %q, got %q", chunk.SectionType, doc.SectionType)
	}
	if doc.ChunkID != chunk.ID {
		t.Fatalf("expected chunk id %q, got %q", chunk.ID, doc.ChunkID)
	}
	if doc.ChunkRole != chunk.ChunkRole {
		t.Fatalf("expected chunk role %q, got %q", chunk.ChunkRole, doc.ChunkRole)
	}
	if doc.ChunkIndex != chunk.ChunkIndex {
		t.Fatalf("expected chunk index %d, got %d", chunk.ChunkIndex, doc.ChunkIndex)
	}
	if doc.SectionTitle != chunk.SectionTitle {
		t.Fatalf("expected section title %q, got %q", chunk.SectionTitle, doc.SectionTitle)
	}
	if doc.Content != chunk.Content {
		t.Fatalf("expected content %q, got %q", chunk.Content, doc.Content)
	}
	if doc.WindowGroupID != chunk.WindowGroupID {
		t.Fatalf("expected window group id %q, got %q", chunk.WindowGroupID, doc.WindowGroupID)
	}
	if doc.PageStart != chunk.PageStart {
		t.Fatalf("expected page start %d, got %d", chunk.PageStart, doc.PageStart)
	}
	if doc.PageEnd != chunk.PageEnd {
		t.Fatalf("expected page end %d, got %d", chunk.PageEnd, doc.PageEnd)
	}
	if doc.Metadata["source"] != "normalized" {
		t.Fatalf("expected metadata source %q, got %#v", "normalized", doc.Metadata["source"])
	}
}
