package parser

import (
	"fmt"
	"strings"
)

// BlockType 表示结构化文档里的基础块类型。
type BlockType string

const (
	// BlockHeading 表示标题块。
	BlockHeading BlockType = "heading"
	// BlockParagraph 表示正文段落块。
	BlockParagraph BlockType = "paragraph"
	// BlockListItem 表示列表项块。
	BlockListItem BlockType = "list_item"
)

// ParsedDocument 表示解析器输出的结构化文档。
type ParsedDocument struct {
	SourceFormat string
	Blocks       []Block
	Metadata     DocumentMetadata
}

// Block 表示文档中的一个逻辑文本块。
type Block struct {
	ID           string
	Type         BlockType
	Text         string
	Page         int
	ReadingOrder int
	BBox         *BoundingBox
	Confidence   float64
}

// DocumentMetadata 保存结构化文档的附加质量信息。
type DocumentMetadata struct {
	QualityFlags []string
}

// BoundingBox 预留给后续版面坐标信息。
type BoundingBox struct {
	Left   float64
	Top    float64
	Width  float64
	Height float64
}

func buildTextDocument(sourceFormat string, text string) *ParsedDocument {
	lines := splitNormalizedLines(text)
	blocks := make([]Block, 0)
	paragraphLines := make([]string, 0)

	flushParagraph := func() {
		if len(paragraphLines) == 0 {
			return
		}

		blocks = append(blocks, newBlock(len(blocks), BlockParagraph, strings.Join(paragraphLines, "\n")))
		paragraphLines = paragraphLines[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flushParagraph()
			continue
		}

		if headingText, ok := parseMarkdownHeading(trimmed); ok {
			flushParagraph()
			blocks = append(blocks, newBlock(len(blocks), BlockHeading, headingText))
			continue
		}

		if itemText, ok := parseMarkdownListItem(trimmed); ok {
			flushParagraph()
			blocks = append(blocks, newBlock(len(blocks), BlockListItem, itemText))
			continue
		}

		paragraphLines = append(paragraphLines, trimmed)
	}

	flushParagraph()

	return &ParsedDocument{
		SourceFormat: sourceFormat,
		Blocks:       blocks,
		Metadata: DocumentMetadata{
			QualityFlags: buildQualityFlags(blocks, false),
		},
	}
}

func buildTikaDocument(sourceFormat string, text string) *ParsedDocument {
	lines := splitNormalizedLines(text)
	blocks := make([]Block, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		blockType := BlockParagraph
		if looksHeadingLike(trimmed) {
			blockType = BlockHeading
		}

		blocks = append(blocks, newBlock(len(blocks), blockType, trimmed))
	}

	return &ParsedDocument{
		SourceFormat: sourceFormat,
		Blocks:       blocks,
		Metadata: DocumentMetadata{
			QualityFlags: buildQualityFlags(blocks, true),
		},
	}
}

func buildQualityFlags(blocks []Block, tikaMode bool) []string {
	if len(blocks) == 0 {
		return []string{"text_empty"}
	}

	shortCount := 0
	for _, block := range blocks {
		if len([]rune(strings.TrimSpace(block.Text))) <= 8 {
			shortCount++
		}
	}

	flags := make([]string, 0, 2)
	if tikaMode && len(blocks) >= 3 && shortCount*2 >= len(blocks) {
		flags = append(flags, "too_many_short_blocks")
	}
	if tikaMode && len(blocks) >= 5 && shortCount*3 >= len(blocks)*2 {
		flags = append(flags, "layout_lost")
	}

	return flags
}

func newBlock(index int, blockType BlockType, text string) Block {
	return Block{
		ID:           fmt.Sprintf("block-%d", index+1),
		Type:         blockType,
		Text:         strings.TrimSpace(text),
		ReadingOrder: index + 1,
		Confidence:   1,
	}
}

func splitNormalizedLines(text string) []string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Split(normalized, "\n")
}

func parseMarkdownHeading(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}

	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level >= len(line) || line[level] != ' ' {
		return "", false
	}

	return strings.TrimSpace(line[level:]), true
}

func parseMarkdownListItem(line string) (string, bool) {
	for _, prefix := range []string{"- ", "* "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
		}
	}

	return "", false
}

func looksHeadingLike(line string) bool {
	if strings.HasSuffix(line, "：") || strings.HasSuffix(line, ":") {
		return true
	}

	if len([]rune(line)) <= 12 {
		return true
	}

	return false
}

func sourceFormatFromFileName(fileName string) string {
	extension := strings.TrimPrefix(normalizeExtension(fileName), ".")
	if extension == "" || extension == "(无扩展名)" {
		return "text"
	}

	return extension
}
