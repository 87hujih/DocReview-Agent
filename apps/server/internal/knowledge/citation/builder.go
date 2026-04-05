package citation

import (
	"fmt"

	"agent_project/apps/server/internal/storage/postgres"
)

type Citation struct {
	CitationID   string `json:"citation_id"`
	ResourceID   string `json:"resource_id"`
	SectionTitle string `json:"section_title"`
	Snippet      string `json:"snippet"`
}

func BuildFromChunks(chunks []postgres.ResourceChunk) []Citation {
	citations := make([]Citation, 0, len(chunks))

	for index, chunk := range chunks {
		citations = append(citations, Citation{
			CitationID:   fmt.Sprintf("cite_%d", index+1),
			ResourceID:   chunk.ResourceID,
			SectionTitle: chunk.SectionTitle,
			Snippet:      truncateSnippet(chunk.Content, 200),
		})
	}

	return citations
}

func truncateSnippet(content string, maxLength int) string {
	runes := []rune(content)
	if len(runes) <= maxLength {
		return content
	}

	return string(runes[:maxLength]) + "..."
}
