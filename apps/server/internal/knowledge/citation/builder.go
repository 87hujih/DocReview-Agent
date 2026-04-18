package citation

import (
	"fmt"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

// Window 描述 citation 命中的 grounded 证据窗口。
type Window struct {
	GroupID    string `json:"group_id,omitempty"`
	StartOrder int    `json:"start_order,omitempty"`
	EndOrder   int    `json:"end_order,omitempty"`
}

// Citation 是检索后返回给客户端的引用对象。
type Citation struct {
	CitationID   string  `json:"citation_id"`
	ResourceID   string  `json:"resource_id"`
	SectionID    string  `json:"section_id,omitempty"`
	SectionType  string  `json:"section_type,omitempty"`
	SectionTitle string  `json:"section_title"`
	Snippet      string  `json:"snippet"`
	Window       *Window `json:"window,omitempty"`
}

// BuildFromChunks 把检索命中的分块映射为稳定且适合前端消费的引用结果。
func BuildFromChunks(chunks []postgres.ResourceChunk) []Citation {
	citations := make([]Citation, 0, len(chunks))

	for index, chunk := range chunks {
		citations = append(citations, Citation{
			CitationID:   fmt.Sprintf("cite_%d", index+1),
			ResourceID:   chunk.ResourceID,
			SectionID:    optionalChunkText(chunk.SectionID),
			SectionType:  optionalChunkText(chunk.SectionType),
			SectionTitle: chunk.SectionTitle,
			Snippet:      truncateSnippet(chunk.Content, 200),
			Window:       buildChunkWindow(chunk),
		})
	}

	return citations
}

// BuildFromWindowGroups 把同一 grounded window 下的多个 chunks 合并成单条 citation。
func BuildFromWindowGroups(groups [][]postgres.ResourceChunk) []Citation {
	citations := make([]Citation, 0, len(groups))

	for index, group := range groups {
		if len(group) == 0 {
			continue
		}

		first := group[0]
		lines := make([]string, 0, len(group))
		seen := make(map[string]struct{}, len(group))
		for _, chunk := range group {
			content := strings.TrimSpace(chunk.Content)
			if content == "" {
				continue
			}
			if _, ok := seen[content]; ok {
				continue
			}

			seen[content] = struct{}{}
			lines = append(lines, content)
		}

		citations = append(citations, Citation{
			CitationID:   fmt.Sprintf("cite_%d", index+1),
			ResourceID:   first.ResourceID,
			SectionID:    optionalChunkText(first.SectionID),
			SectionType:  optionalChunkText(first.SectionType),
			SectionTitle: first.SectionTitle,
			Snippet:      truncateSnippet(strings.Join(lines, "\n"), 200),
			Window:       buildChunkWindowRange(group),
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

// optionalChunkText 仅在窗口文本非空时返回对应字符串指针，避免把空窗口内容写进引用结果。
func optionalChunkText(value *string) string {
	if value == nil {
		return ""
	}

	return strings.TrimSpace(*value)
}

// buildChunkWindow 根据命中的 chunk 和相邻 chunk 构造引用窗口文本，尽量保留回答所需的局部上下文。
func buildChunkWindow(chunk postgres.ResourceChunk) *Window {
	return buildChunkWindowRange([]postgres.ResourceChunk{chunk})
}

// buildChunkWindowRange 计算引用窗口覆盖的 chunk 索引范围，保证前后文扩展不越界。
func buildChunkWindowRange(chunks []postgres.ResourceChunk) *Window {
	if len(chunks) == 0 {
		return nil
	}

	window := &Window{}
	for _, chunk := range chunks {
		if window.GroupID == "" && chunk.WindowGroupID != nil {
			window.GroupID = strings.TrimSpace(*chunk.WindowGroupID)
		}
		if chunk.OrderInSection == nil || *chunk.OrderInSection <= 0 {
			continue
		}
		if window.StartOrder == 0 || *chunk.OrderInSection < window.StartOrder {
			window.StartOrder = *chunk.OrderInSection
		}
		if *chunk.OrderInSection > window.EndOrder {
			window.EndOrder = *chunk.OrderInSection
		}
	}

	if window.GroupID == "" && window.StartOrder == 0 && window.EndOrder == 0 {
		return nil
	}

	return window
}
