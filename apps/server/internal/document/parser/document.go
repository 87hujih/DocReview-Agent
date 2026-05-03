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

// buildTextDocument 组装 `文本文档`，统一解析结果在后续链路里的结构表达。
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

// buildTikaDocument 组装 `Tika文档`，统一解析结果在后续链路里的结构表达。
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

// buildTextBlocks 组装 `文本块`，便于后续归一化、切块或展示逻辑复用。
func buildTextBlocks(fileName string, content string) []Block {
	if detectSourceFormat(fileName) == "markdown" {
		return buildMarkdownBlocks(content)
	}

	return buildPlainTextBlocks(content)
}

// buildMarkdownBlocks 组装 `Markdown块`，便于后续归一化、切块或展示逻辑复用。
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

// buildPlainTextBlocks 组装 `纯文本文本块`，便于后续归一化、切块或展示逻辑复用。
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

// deriveTextQualityFlags 从已有上下文推导 `文本QualityFlags`，让后续链路只消费归一化结果。
func deriveTextQualityFlags(content string) QualityFlags {
	flags := make(QualityFlags, 0, 1)
	if strings.TrimSpace(content) == "" {
		flags = appendQualityFlag(flags, "text_empty")
	}

	return flags
}

// deriveTikaQualityFlags 从已有上下文推导 `TikaQualityFlags`，让后续链路只消费归一化结果。
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

// appendQualityFlag 追加 `QualityFlag`，保持消息和副作用写入顺序一致。
func appendQualityFlag(flags QualityFlags, flag string) QualityFlags {
	if flag == "" || flags.Has(flag) {
		return flags
	}

	return append(flags, flag)
}

// hasTooManyShortBlocks 判断 `TooManyShort块` 是否满足当前流程的条件，避免同一谓词在多处分散实现。
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

// markdownHeadingLevel 返回 `Markdown标题` 的层级值，供后续结构判断复用。
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

// normalizeNewlines 归一化 `Newlines`，避免后续流程重复处理边界输入。
func normalizeNewlines(content string) string {
	return strings.ReplaceAll(content, "\r\n", "\n")
}

// detectSourceFormat 识别 `来源Format`，把格式或类型判断规则收口在单点。
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
