package chunker

import (
	"strings"

	"agent_project/apps/server/internal/knowledge/sections"
)

// maxChunkChars 用来限制分块长度，避免 embedding 过大并保持引用片段可读。
const maxChunkChars = 800

// Chunk 是从 Markdown 文档中产出的可检索单元。
type Chunk struct {
	ChunkIndex   int
	SectionTitle string
	Content      string
}

// ChunkMarkdown 只保留二级标题下的内容，并按段落拆分过长章节。
func ChunkMarkdown(content string) []Chunk {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	chunks := make([]Chunk, 0)
	for _, section := range sections.ParseMarkdown(content) {
		sectionContent := strings.TrimSpace(section.Body)
		if sectionContent == "" {
			continue
		}

		if len(sectionContent) > maxChunkChars {
			paragraphs := strings.Split(sectionContent, "\n\n")
			// 当一个章节过长时，按段落边界拆分，避免一个 embedding 吃下整段大文本。
			for _, paragraph := range paragraphs {
				paragraph = strings.TrimSpace(paragraph)
				if paragraph == "" {
					continue
				}

				chunks = append(chunks, Chunk{
					SectionTitle: section.Title,
					Content:      paragraph,
				})
			}

			continue
		}

		chunks = append(chunks, Chunk{
			SectionTitle: section.Title,
			Content:      sectionContent,
		})
	}

	for index := range chunks {
		chunks[index].ChunkIndex = index
	}

	return chunks
}
