package workflow

import (
	"fmt"
	"strings"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/sections"
)

// buildEditorContent 为 editor 选择可编辑正文，优先提供完整章节，避免把截断段落当成基线原文。
func buildEditorContent(baseContent string, focusSections []string, citations []citation.Citation) string {
	normalized := strings.ReplaceAll(baseContent, "\r\n", "\n")
	parsedSections := sections.ParseMarkdown(normalized)
	if len(parsedSections) == 0 {
		return baseContent
	}
	if len(parsedSections) == 1 && parsedSections[0].Heading == "" {
		return baseContent
	}

	selected := make(map[string]struct{}, len(parsedSections))
	for _, focusSection := range focusSections {
		focusSection = strings.TrimSpace(focusSection)
		if focusSection == "" {
			continue
		}

		for _, section := range parsedSections {
			if !sectionMatchesHint(section.Title, focusSection) {
				continue
			}

			selected[buildWorkflowSectionKey(section.Title, section.Occurrence)] = struct{}{}
		}
	}

	for _, item := range citations {
		snippet := strings.TrimSpace(item.Snippet)
		if snippet == "" {
			continue
		}

		for _, section := range parsedSections {
			if !strings.Contains(section.Body, snippet) {
				continue
			}

			selected[buildWorkflowSectionKey(section.Title, section.Occurrence)] = struct{}{}
		}
	}

	if len(selected) == 0 {
		return baseContent
	}

	lines := strings.Split(normalized, "\n")
	parts := make([]string, 0, len(selected))
	for _, section := range parsedSections {
		if _, ok := selected[buildWorkflowSectionKey(section.Title, section.Occurrence)]; !ok {
			continue
		}

		parts = append(parts, strings.Join(lines[section.StartLine:section.EndLine], "\n"))
	}

	if len(parts) == 0 {
		return baseContent
	}
	return strings.Join(parts, "\n\n")
}

// validateDiffPreviewAgainstBaseContent 用真实基线正文校验 diff_preview，避免无效审批流入执行阶段。
func validateDiffPreviewAgainstBaseContent(preview *editor.DiffPreview, baseContent string) error {
	if preview == nil || preview.NoChange {
		return nil
	}

	parsedSections := sections.ParseMarkdown(baseContent)
	if len(parsedSections) == 0 {
		return fmt.Errorf("基线正文为空，无法校验 diff 预览")
	}

	seen := make(map[string]struct{}, len(preview.Sections))
	for index, diff := range preview.Sections {
		matchedSection, err := matchPreviewSection(parsedSections, diff)
		if err != nil {
			return fmt.Errorf("diff 预览第 %d 个章节无法映射到基线正文：%w", index, err)
		}

		key := buildWorkflowSectionKey(matchedSection.Title, matchedSection.Occurrence)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("diff 预览第 %d 个章节重复引用了同一基线章节 %q", index, matchedSection.Title)
		}
		seen[key] = struct{}{}

		if strings.TrimSpace(diff.Original) != matchedSection.Body {
			return fmt.Errorf("diff 预览第 %d 个章节 original 与基线正文不一致", index)
		}
	}

	return nil
}

// matchPreviewSection 为 diff preview 定位真实基线 section，确保审批前后使用同一套章节语义。
func matchPreviewSection(parsedSections []sections.Section, diff editor.DiffSection) (sections.Section, error) {
	title := strings.TrimSpace(diff.SectionTitle)
	if title == "" {
		return sections.Section{}, fmt.Errorf("diff 预览章节标题为空")
	}

	candidates := make([]sections.Section, 0, len(parsedSections))
	for _, section := range parsedSections {
		if section.Title != title {
			continue
		}

		candidates = append(candidates, section)
	}

	if len(candidates) == 0 {
		return sections.Section{}, fmt.Errorf("文档中未找到 diff 预览对应章节：%s", title)
	}

	if diff.SectionOccurrence <= 0 {
		if len(candidates) == 1 {
			return candidates[0], nil
		}
		return sections.Section{}, fmt.Errorf("legacy diff_preview 缺少 section_occurrence，章节 %q 存在重复标题", title)
	}

	for _, section := range candidates {
		if section.Occurrence == diff.SectionOccurrence {
			return section, nil
		}
	}

	return sections.Section{}, fmt.Errorf("文档中未找到 diff 预览对应章节：%s#%d", title, diff.SectionOccurrence)
}

// sectionMatchesHint 判断 focus_sections 与真实章节标题是否足够接近，兼容主题词与标题词的双向包含。
func sectionMatchesHint(title string, hint string) bool {
	title = strings.TrimSpace(title)
	hint = strings.TrimSpace(hint)
	if title == "" || hint == "" {
		return false
	}

	return strings.Contains(title, hint) || strings.Contains(hint, title)
}

// buildWorkflowSectionKey 生成工作流内部使用的稳定章节键，避免 map 判重逻辑分散。
func buildWorkflowSectionKey(title string, occurrence int) string {
	return title + "\n" + fmt.Sprintf("%d", occurrence)
}
