package workflow

import (
	"sort"
	"strings"

	"agent_project/apps/server/internal/knowledge/citation"
)

// ContextResult 保存 ContextBuilder.Build 生成的聚焦上下文。
type ContextResult struct {
	Content      string   // 聚焦后的文本，最多 maxRunes 个字符
	UsedSections []string // 与文档段落匹配的重点章节标题
	TrimmedRunes int      // 为满足 maxRunes 预算而裁剪的字符数
	TrimReason   string   // 裁剪原因，未发生裁剪时为空
}

// ContextBuilder 从文档正文中选取聚焦段落，供下游代理使用。
type ContextBuilder struct{}

// Build 在字符预算内构建聚焦上下文。
//
// 选取优先级：
//  1. 包含 focusSections 标题子串的段落。
//  2. 包含 citations 片段子串的段落。
//  3. 文档第一段（兜底）。
//
// 若 len([]rune(content)) <= maxRunes，直接返回完整原文。
// maxRunes <= 0 时默认使用 24000。
func (ContextBuilder) Build(content string, focusSections []string, citations []citation.Citation, maxRunes int) ContextResult {
	if maxRunes <= 0 {
		maxRunes = 24000
	}

	contentRunes := []rune(content)
	if len(contentRunes) <= maxRunes {
		return ContextResult{Content: content, UsedSections: focusSections}
	}

	paragraphs := splitParagraphs(content)

	type indexedPara struct {
		idx  int
		text string
	}

	usedIdx := make(map[int]bool, len(paragraphs))
	selected := make([]indexedPara, 0, len(paragraphs))
	var usedSections []string

	// 优先级 1：重点章节匹配
	for _, section := range focusSections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}
		for i, para := range paragraphs {
			if !usedIdx[i] && strings.Contains(para, section) {
				usedIdx[i] = true
				selected = append(selected, indexedPara{i, para})
				usedSections = append(usedSections, section)
				break // 每个章节标题只匹配一个段落
			}
		}
	}

	// 优先级 2：引用片段匹配
	for _, cit := range citations {
		snippet := strings.TrimSpace(cit.Snippet)
		if snippet == "" {
			continue
		}
		for i, para := range paragraphs {
			if !usedIdx[i] && strings.Contains(para, snippet) {
				usedIdx[i] = true
				selected = append(selected, indexedPara{i, para})
				break
			}
		}
	}

	// 优先级 3：文档首段兜底（未被选中时强制加入）
	if len(paragraphs) > 0 && !usedIdx[0] {
		usedIdx[0] = true
		selected = append(selected, indexedPara{0, paragraphs[0]})
	}

	// 按文档原始顺序排列
	sort.Slice(selected, func(i, j int) bool { return selected[i].idx < selected[j].idx })

	// 在预算内拼装结果
	parts := make([]string, 0, len(selected))
	totalRunes := 0
	for _, p := range selected {
		pRunes := []rune(p.text)
		remaining := maxRunes - totalRunes
		if remaining <= 0 {
			break
		}
		if len(pRunes) > remaining {
			parts = append(parts, string(pRunes[:remaining]))
			totalRunes += remaining
			break
		}
		parts = append(parts, p.text)
		totalRunes += len(pRunes)
	}

	result := strings.Join(parts, "\n\n")
	trimmedRunes := len(contentRunes) - totalRunes
	if trimmedRunes < 0 {
		trimmedRunes = 0
	}
	trimReason := ""
	if trimmedRunes > 0 {
		trimReason = "已超出最大字符预算"
	}

	return ContextResult{
		Content:      result,
		UsedSections: usedSections,
		TrimmedRunes: trimmedRunes,
		TrimReason:   trimReason,
	}
}

// splitParagraphs 按空行分割内容，丢弃空段落。
func splitParagraphs(content string) []string {
	raw := strings.Split(content, "\n\n")
	out := make([]string, 0, len(raw))
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	// 兜底：将整个内容作为一个段落
	if len(out) == 0 && strings.TrimSpace(content) != "" {
		out = append(out, strings.TrimSpace(content))
	}
	return out
}