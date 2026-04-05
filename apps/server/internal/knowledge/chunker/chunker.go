package chunker

import "strings"

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

	lines := strings.Split(content, "\n")
	chunks := make([]Chunk, 0)
	currentTitle := ""
	currentLines := make([]string, 0)
	inSection := false

	flushSection := func() {
		if !inSection {
			return
		}

		sectionContent := strings.TrimSpace(strings.Join(currentLines, "\n"))
		if sectionContent == "" {
			return
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
					SectionTitle: currentTitle,
					Content:      paragraph,
				})
			}

			return
		}

		chunks = append(chunks, Chunk{
			SectionTitle: currentTitle,
			Content:      sectionContent,
		})
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flushSection()

			currentTitle = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			currentLines = currentLines[:0]
			inSection = true
			continue
		}

		if inSection {
			currentLines = append(currentLines, line)
		}
	}

	flushSection()

	for index := range chunks {
		chunks[index].ChunkIndex = index
	}

	return chunks
}
