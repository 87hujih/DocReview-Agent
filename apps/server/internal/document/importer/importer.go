// Package 导入器 converts 格式-specific 提取的 内容 into 一个 Canonical AST.
package importer

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"agent_project/apps/server/internal/document/model"
)

type Input struct {
	DocumentID string
	VersionID  string
	FileName   string
	Content    []byte
	Metadata   map[string]any
}

type Importer interface {
	Format() string
	Import(context.Context, Input) (*model.Document, error)
}

type formatImporter struct{ format string }

// NewMarkdown 校验依赖并创建对应实例。
func NewMarkdown() Importer { return formatImporter{format: "markdown"} }

// NewDOCX 校验依赖并创建对应实例。
func NewDOCX() Importer { return formatImporter{format: "docx"} }

// NewPDF 校验依赖并创建对应实例。
func NewPDF() Importer { return formatImporter{format: "pdf"} }

// 格式 执行该函数负责的核心处理逻辑。
func (i formatImporter) Format() string { return i.format }

// Import 执行该函数负责的核心处理逻辑。
func (i formatImporter) Import(ctx context.Context, input Input) (*model.Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.DocumentID) == "" || strings.TrimSpace(input.VersionID) == "" || strings.TrimSpace(input.FileName) == "" {
		return nil, fmt.Errorf("文档_id、version_id、和 file name 不能为空")
	}
	if !utf8.Valid(input.Content) {
		return nil, fmt.Errorf("%s 导入器需要 UTF-8 提取的内容", i.format)
	}
	root := &model.Node{
		NodeID: model.StableNodeID(input.DocumentID, "root", model.NodeDocument), Type: model.NodeDocument,
		Attributes: map[string]any{}, Children: []*model.Node{}, Metadata: map[string]any{}, PageMapping: []model.PageMapping{},
		SourceLocation: model.SourceLocation{FileName: input.FileName, EndOffset: len(input.Content), StartLine: 1},
	}
	// 根据当前状态或类型选择对应的处理分支。
	switch i.format {
	case "markdown":
		root.Children = markdownNodes(input)
	case "docx":
		// 说明： Extractors conventionally emit 一个 DOCX paragraph per line. Keeping
		// those boundaries prevents 一个 large 文档 来自 collapsing 用于 一个 blob.
		root.Children = paragraphNodes(input, strings.Split(normalize(string(input.Content)), "\n"), false)
	case "pdf":
		root.Children = pdfNodes(input)
	default:
		return nil, fmt.Errorf("不支持的导入器格式 %q", i.format)
	}
	document := &model.Document{
		DocumentID: input.DocumentID, VersionID: input.VersionID, Root: root,
		SourceFormat: i.format, Metadata: cloneMap(input.Metadata), SchemaVersion: model.SchemaVersion,
	}
	if err := model.Rehash(document); err != nil {
		return nil, err
	}
	if err := model.Validate(document); err != nil {
		return nil, err
	}
	return document, nil
}

// markdownNodes 执行该函数负责的核心处理逻辑。
func markdownNodes(input Input) []*model.Node {
	lines := strings.Split(normalize(string(input.Content)), "\n")
	result := make([]*model.Node, 0, len(lines))
	paragraph := make([]string, 0)
	paragraphStart := 0
	offset := 0
	flush := func(end int) {
		content := strings.TrimSpace(strings.Join(paragraph, "\n"))
		if content != "" {
			result = append(result, newNode(input, len(result), model.NodeParagraph, content, paragraphStart, end, 0, map[string]any{}))
		}
		paragraph = paragraph[:0]
	}
	for lineIndex, line := range lines {
		trimmed := strings.TrimSpace(line)
		if level, title := markdownHeading(trimmed); level > 0 {
			flush(offset)
			result = append(result, newNode(input, len(result), model.NodeHeading, title, offset, offset+len(line), 0, map[string]any{"level": level, "line": lineIndex + 1}))
		} else if trimmed == "" {
			flush(offset)
		} else {
			if len(paragraph) == 0 {
				paragraphStart = offset
			}
			paragraph = append(paragraph, line)
		}
		offset += len(line) + 1
	}
	flush(len(input.Content))
	return result
}

// paragraphNodes 执行该函数负责的核心处理逻辑。
func paragraphNodes(input Input, paragraphs []string, pageMode bool) []*model.Node {
	result := make([]*model.Node, 0, len(paragraphs))
	offset := 0
	for _, paragraph := range paragraphs {
		content := strings.TrimSpace(paragraph)
		if content == "" {
			offset += len(paragraph) + 1
			continue
		}
		page := 0
		if pageMode {
			page = len(result) + 1
		}
		result = append(result, newNode(input, len(result), model.NodeParagraph, content, offset, offset+len(paragraph), page, map[string]any{}))
		offset += len(paragraph) + 1
	}
	return result
}

// pdfNodes 执行该函数负责的核心处理逻辑。
func pdfNodes(input Input) []*model.Node {
	pages := strings.Split(normalize(string(input.Content)), "\f")
	result := make([]*model.Node, 0, len(pages))
	offset := 0
	for index, pageContent := range pages {
		content := strings.TrimSpace(pageContent)
		if content == "" {
			offset += len(pageContent) + 1
			continue
		}
		result = append(result, newNode(input, len(result), model.NodePage, content, offset, offset+len(pageContent), index+1, map[string]any{"page": index + 1}))
		offset += len(pageContent) + 1
	}
	return result
}

// newNode 执行该函数负责的核心处理逻辑。
func newNode(input Input, order int, nodeType model.NodeType, content string, start, end, page int, attributes map[string]any) *model.Node {
	node := &model.Node{
		NodeID: model.StableNodeID(input.DocumentID, fmt.Sprintf("root/%d", order), nodeType), Type: nodeType,
		Attributes: attributes, Content: content, Children: []*model.Node{}, Metadata: map[string]any{},
		SourceLocation: model.SourceLocation{FileName: input.FileName, StartOffset: start, EndOffset: end},
		PageMapping:    []model.PageMapping{},
	}
	if page > 0 {
		node.PageMapping = append(node.PageMapping, model.PageMapping{Page: page, StartOffset: start, EndOffset: end})
	}
	return node
}

// markdownHeading 执行该函数负责的核心处理逻辑。
func markdownHeading(line string) (int, string) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return 0, ""
	}
	return level, strings.TrimSpace(line[level:])
}

// normalize 执行该函数负责的核心处理逻辑。
func normalize(value string) string { return strings.ReplaceAll(value, "\r\n", "\n") }

// cloneMap 执行该函数负责的核心处理逻辑。
func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
