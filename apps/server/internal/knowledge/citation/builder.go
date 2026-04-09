package citation

import (
	"fmt"

	"agent_project/apps/server/internal/storage/postgres"
)

// Citation 是检索后返回给客户端的引用对象。
type Citation struct {
	CitationID   string `json:"citation_id"`
	ResourceID   string `json:"resource_id"`
	SectionTitle string `json:"section_title"`
	Snippet      string `json:"snippet"`
}

// BuildFromChunks 把检索命中的分块映射为稳定且适合前端消费的引用结果。
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

// truncateSnippet 按 rune 长度截断内容，避免中文等多字节字符被截坏。
func truncateSnippet(content string, maxLength int) string {
	runes := []rune(content)
	if len(runes) <= maxLength {
		return content
	}

	return string(runes[:maxLength]) + "..."
}
