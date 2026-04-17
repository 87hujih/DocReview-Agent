package parser

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// BlockType 表示结构化文档中的基础 block 类型。
type BlockType string

const (
	// BlockHeading 表示标题型 block。
	BlockHeading BlockType = "heading"
	// BlockParagraph 表示正文段落型 block。
	BlockParagraph BlockType = "paragraph"
)

// Block 表示结构化文档里的最小内容单元。
type Block struct {
	Type  BlockType
	Text  string
	Level int
}

// DocumentMetadata 保存结构化解析的基础来源信息。
type DocumentMetadata struct {
	FileName   string
	ParserName string
}

// QualityFlags 保存解析质量标记。
type QualityFlags []string

// Has 判断当前质量标记中是否包含指定 flag。
func (flags QualityFlags) Has(flag string) bool {
	for _, candidate := range flags {
		if candidate == flag {
			return true
		}
	}

	return false
}

// ParsedDocument 表示结构化解析结果。
type ParsedDocument struct {
	SourceFormat string
	Blocks       []Block
	Metadata     DocumentMetadata
	QualityFlags QualityFlags
}

func buildTextDocument(fileName string, content string) *ParsedDocument {
	return &ParsedDocument{
		SourceFormat: detectSourceFormat(fileName),
		Blocks:       buildTextBlocks(fileName, content),
		Metadata: DocumentMetadata{
			FileName:   fileName,
			ParserName: "text",
		},
		QualityFlags: deriveTextQualityFlags(content),
	}
}

func buildTikaDocument(fileName string, content string) *ParsedDocument {
	blocks := buildPlainTextBlocks(content)
	flags := deriveTikaQualityFlags(fileName, content, blocks)

	return &ParsedDocument{
		SourceFormat: detectSourceFormat(fileName),
		Blocks:       blocks,
		Metadata: DocumentMetadata{
			FileName:   fileName,
			ParserName: "tika",
		},
		QualityFlags: flags,
	}
}

func buildTextBlocks(fileName string, content string) []Block {
	if detectSourceFormat(fileName) == "markdown" {
		return buildMarkdownBlocks(content)
	}

	return buildPlainTextBlocks(content)
}

func buildMarkdownBlocks(content string) []Block {
	lines := strings.Split(normalizeNewlines(content), "\n")
	blocks := make([]Block, 0, len(lines))
	paragraphLines := make([]string, 0)

	flushParagraph := func() {
		text := strings.TrimSpace(strings.Join(paragraphLines, "\n"))
		if text != "" {
			blocks = append(blocks, Block{Type: BlockParagraph, Text: text})
		}
		paragraphLines = paragraphLines[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flushParagraph()
			continue
		}

		if level := markdownHeadingLevel(trimmed); level > 0 {
			flushParagraph()
			headingText := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if headingText != "" {
				blocks = append(blocks, Block{
					Type:  BlockHeading,
					Text:  headingText,
					Level: level,
				})
			}
			continue
		}

		paragraphLines = append(paragraphLines, trimmed)
	}

	flushParagraph()
	return blocks
}

func buildPlainTextBlocks(content string) []Block {
	lines := strings.Split(normalizeNewlines(content), "\n")
	blocks := make([]Block, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		blocks = append(blocks, Block{
			Type: BlockParagraph,
			Text: trimmed,
		})
	}

	return blocks
}

func deriveTextQualityFlags(content string) QualityFlags {
	flags := make(QualityFlags, 0, 1)
	if strings.TrimSpace(content) == "" {
		flags = appendQualityFlag(flags, "text_empty")
	}

	return flags
}

func deriveTikaQualityFlags(fileName string, content string, blocks []Block) QualityFlags {
	flags := make(QualityFlags, 0, 3)
	if strings.TrimSpace(content) == "" {
		flags = appendQualityFlag(flags, "text_empty")
		if detectSourceFormat(fileName) == "pdf" {
			flags = appendQualityFlag(flags, "requires_ocr")
		}
		return flags
	}

	if detectSourceFormat(fileName) == "pdf" && hasTooManyShortBlocks(blocks) {
		flags = appendQualityFlag(flags, "too_many_short_blocks")
	}

	return flags
}

func appendQualityFlag(flags QualityFlags, flag string) QualityFlags {
	if flag == "" || flags.Has(flag) {
		return flags
	}

	return append(flags, flag)
}

func hasTooManyShortBlocks(blocks []Block) bool {
	if len(blocks) < 4 {
		return false
	}

	shortCount := 0
	for _, block := range blocks {
		if utf8.RuneCountInString(strings.TrimSpace(block.Text)) <= 8 {
			shortCount++
		}
	}

	return shortCount*100/len(blocks) >= 70
}

func markdownHeadingLevel(line string) int {
	level := 0
	for _, ch := range line {
		if ch != '#' {
			break
		}
		level++
	}
	if level == 0 {
		return 0
	}
	if len(line) > level && line[level] != ' ' {
		return 0
	}

	return level
}

func normalizeNewlines(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}

func detectSourceFormat(fileName string) string {
	switch strings.ToLower(strings.TrimSpace(filepath.Ext(fileName))) {
	case ".md":
		return "markdown"
	case ".txt":
		return "text"
	case "":
		return "unknown"
	default:
		return strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	}
}
