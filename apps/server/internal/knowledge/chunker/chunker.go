package chunker

import "strings"

const maxChunkChars = 800

type Chunk struct {
	ChunkIndex   int
	SectionTitle string
	Content      string
}

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
