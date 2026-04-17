package searchindex

import "agent_project/apps/server/internal/storage/postgres"

// ChunkDocument 表示写入 OpenSearch 的 chunk 投影。
type ChunkDocument struct {
	DocumentID    string         `json:"-"`
	ResourceID    string         `json:"resource_id"`
	VersionID     string         `json:"version_id"`
	SectionID     string         `json:"section_id"`
	SectionType   string         `json:"section_type"`
	ChunkID       string         `json:"chunk_id"`
	ChunkRole     string         `json:"chunk_role"`
	ChunkIndex    int            `json:"chunk_index"`
	SectionTitle  string         `json:"section_title"`
	Content       string         `json:"content"`
	WindowGroupID string         `json:"window_group_id"`
	PageStart     int            `json:"page_start"`
	PageEnd       int            `json:"page_end"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

// BuildChunkDocument 把 PostgreSQL 真源 chunk 投影成 OpenSearch 文档。
func BuildChunkDocument(chunk postgres.ResourceChunk) ChunkDocument {
	return ChunkDocument{
		DocumentID:    chunk.ID,
		ResourceID:    chunk.ResourceID,
		VersionID:     chunk.VersionID,
		SectionID:     chunk.SectionID,
		SectionType:   chunk.SectionType,
		ChunkID:       chunk.ID,
		ChunkRole:     chunk.ChunkRole,
		ChunkIndex:    chunk.ChunkIndex,
		SectionTitle:  chunk.SectionTitle,
		Content:       chunk.Content,
		WindowGroupID: chunk.WindowGroupID,
		PageStart:     chunk.PageStart,
		PageEnd:       chunk.PageEnd,
		Metadata:      cloneMetadata(chunk.Metadata),
	}
}

func buildChunkDocuments(chunks []postgres.ResourceChunk) []ChunkDocument {
	documents := make([]ChunkDocument, 0, len(chunks))
	for _, chunk := range chunks {
		documents = append(documents, BuildChunkDocument(chunk))
	}

	return documents
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}

	cloned := make(map[string]any, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}

	return cloned
}
