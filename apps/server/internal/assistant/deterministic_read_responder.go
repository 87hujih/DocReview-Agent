package assistant

import (
	"fmt"
	"strings"

	"agent_project/apps/server/internal/storage/postgres"
)

// DeterministicReadInput 描述确定性阅读回复所需的原始 section 数据。
type DeterministicReadInput struct {
	SectionType    string
	Sections       []postgres.ResourceSection
	LocatedSection *LocatedSection
	SectionRead    *SectionReadResult
}

// DeterministicReadResponder 负责把直读结果格式化成无需 LLM 的回复。
type DeterministicReadResponder struct{}

// Reply 根据阅读意图把 section 列表或 section 正文转成稳定回复。
func (r DeterministicReadResponder) Reply(intent ReadIntent, input DeterministicReadInput) *ChatCompletionResult {
	var reply string

	switch intent.Kind {
	case ReadIntentListSections:
		reply = buildSectionListReply(input.SectionType, input.Sections)
	case ReadIntentAggregateAttribute:
		reply = buildAggregateAttributeReply(input.SectionType, input.Sections)
	case ReadIntentExcerptSection, ReadIntentLocateOrdinal:
		reply = buildSectionReadReply(input.LocatedSection, input.SectionRead)
	}

	reply = strings.TrimSpace(reply)
	if reply == "" {
		return nil
	}

	return &ChatCompletionResult{Reply: reply}
}

func buildSectionListReply(sectionType string, sections []postgres.ResourceSection) string {
	label := readingSectionLabel(sectionType)
	if len(sections) == 0 {
		return fmt.Sprintf("当前文件里没有可读取的%s。", label)
	}

	lines := []string{fmt.Sprintf("当前文件中的%s如下：", label)}
	for _, section := range sections {
		title := strings.TrimSpace(section.Title)
		if title == "" {
			title = fmt.Sprintf("未命名%s %d", label, section.SectionOrder)
		}

		lines = append(lines, fmt.Sprintf("%d. %s", section.SectionOrder, title))
	}

	return strings.Join(lines, "\n")
}

func buildAggregateAttributeReply(sectionType string, sections []postgres.ResourceSection) string {
	tokens := collectAggregateTokens(sections)
	if len(tokens) == 0 {
		return fmt.Sprintf("当前文件里没有可聚合的%s信息。", readingSectionLabel(sectionType))
	}

	return "当前文件中提到的技术栈如下：\n" + strings.Join(tokens, "、")
}

func buildSectionReadReply(located *LocatedSection, sectionRead *SectionReadResult) string {
	if sectionRead == nil {
		return ""
	}

	title := strings.TrimSpace(sectionRead.Title)
	if title == "" && located != nil {
		title = strings.TrimSpace(located.Title)
	}

	header := "原文："
	if title != "" {
		header = fmt.Sprintf("《%s》原文：", title)
	}

	body := strings.TrimSpace(sectionRead.Content)
	if body == "" {
		return ""
	}

	reply := header + "\n" + body
	if sectionRead.IsExcerpt && sectionRead.HasMore {
		reply += "\n\n这是原文摘录，可继续输出剩余部分。"
	}

	return reply
}

func collectAggregateTokens(sections []postgres.ResourceSection) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)

	for _, section := range sections {
		for _, raw := range splitAggregateText(section.Content) {
			token := strings.TrimSpace(raw)
			if token == "" {
				continue
			}
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			result = append(result, token)
		}
	}

	return result
}

func splitAggregateText(content string) []string {
	return strings.FieldsFunc(content, func(r rune) bool {
		switch r {
		case '\n', '\r', '\t', ' ', ',', '，', '、', '/', '｜', '|', ';', '；':
			return true
		default:
			return false
		}
	})
}

func readingSectionLabel(sectionType string) string {
	switch strings.TrimSpace(sectionType) {
	case "project":
		return "项目"
	case "experience":
		return "经历"
	case "skills":
		return "技术栈"
	default:
		return "章节"
	}
}
