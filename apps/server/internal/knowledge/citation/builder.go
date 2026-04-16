package citation

import (
	"fmt"

	"agent_project/apps/server/internal/storage/postgres"
)

// Citation 是检索后返回给客户端的引用对象。
type Citation struct {
	CitationID   string   `json:"citation_id"`
	ResourceID   string   `json:"resource_id"`
	SectionID    string   `json:"section_id,omitempty"`
	SectionType  string   `json:"section_type,omitempty"`
	SectionTitle string   `json:"section_title"`
	Snippet      string   `json:"snippet"`
	Window       []string `json:"window,omitempty"`
}

// BuildFromChunks 把检索命中的分块映射为稳定且适合前端消费的引用结果。
func BuildFromChunks(chunks []postgres.ResourceChunk) []Citation {
	citations := make([]Citation, 0, len(chunks))

	for index, chunk := range chunks {
		citations = append(citations, Citation{
			CitationID:   fmt.Sprintf("cite_%d", index+1),
			ResourceID:   chunk.ResourceID,
			SectionID:    chunk.SectionID,
			SectionType:  chunk.SectionType,
			SectionTitle: chunk.SectionTitle,
			Snippet:      truncateSnippet(chunk.Content, 200),
			Window:       extractWindow(chunk.Metadata),
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

func extractWindow(metadata map[string]any) []string {
	if len(metadata) == 0 {
		return nil
	}

	value, ok := metadata["window"]
	if !ok {
		return nil
	}

	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		window := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			window = append(window, text)
		}
		return window
	default:
		return nil
	}
}
