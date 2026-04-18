package normalize

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"agent_project/apps/server/internal/document/parser"
)

var (
	projectDatePattern = regexp.MustCompile(`[（(]\s*20\d{2}[./-]\d{1,2}.*?[)）]`)
)

// SectionType 表示归一化后逻辑 section 的类别。
type SectionType string

const (
	// SectionTypeProject 表示简历 / 项目类 section。
	SectionTypeProject SectionType = "project"
	// SectionTypeSection 表示通用标题分节。
	SectionTypeSection SectionType = "section"
	// SectionTypeDocument 表示无法再细分时的整文档 section。
	SectionTypeDocument SectionType = "document"
)

// NormalizedDocument 表示供后续持久化的逻辑 section 图。
type NormalizedDocument struct {
	Sections []NormalizedSection
}

// NormalizedSection 表示归一化后的单个逻辑 section。
type NormalizedSection struct {
	SectionKey          string
	Type                SectionType
	Order               int
	Title               string
	CanonicalEntityName string
	Aliases             []string
	Summary             string
	Content             string
	TechStack           []string
	PageStart           int
	PageEnd             int
	Metadata            map[string]any
}

// Service 负责把 parser blocks 归一化为逻辑 sections。
type Service struct{}

// NewService 创建归一化服务。
func NewService() *Service {
	return &Service{}
}

// Normalize 把结构化 block 转为逻辑 section 图。
func (s *Service) Normalize(document parser.ParsedDocument) NormalizedDocument {
	if sections := normalizeProjectSections(document.Blocks); len(sections) > 0 {
		return NormalizedDocument{Sections: sections}
	}

	return NormalizedDocument{Sections: normalizeGenericSections(document.Blocks)}
}

// projectAccumulator 承载项目Accumulator相关状态，明确文档归一化链路中的数据边界。
type projectAccumulator struct {
	title     string
	order     int
	techStack []string
	bodyLines []string
	lastLabel string
	pageStart int
	pageEnd   int
}

// normalizeProjectSections 归一化 `项目section`，避免后续流程重复处理边界输入。
func normalizeProjectSections(blocks []parser.Block) []NormalizedSection {
	var (
		sections []NormalizedSection
		current  *projectAccumulator
		order    = 1
	)

	flushCurrent := func() {
		if current == nil {
			return
		}

		title := strings.TrimSpace(current.title)
		if title == "" {
			current = nil
			return
		}

		canonicalName := stripProjectDateSuffix(title)
		content := strings.TrimSpace(strings.Join(current.bodyLines, "\n"))
		metadata := map[string]any{}
		if len(current.techStack) > 0 {
			metadata["tech_stack"] = append([]string(nil), current.techStack...)
		}
		if content == "" {
			metadata["low_confidence"] = true
			metadata["quality_flag"] = "heading_only"
		}

		sections = append(sections, NormalizedSection{
			SectionKey:          fmt.Sprintf("project-%d", current.order),
			Type:                SectionTypeProject,
			Order:               current.order,
			Title:               title,
			CanonicalEntityName: canonicalName,
			Aliases:             buildAliases(title, canonicalName),
			Summary:             firstNonEmptyLine(content),
			Content:             content,
			TechStack:           append([]string(nil), current.techStack...),
			PageStart:           current.pageStart,
			PageEnd:             current.pageEnd,
			Metadata:            metadata,
		})
		current = nil
	}

	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}

		if isProjectTitle(text) {
			flushCurrent()
			current = &projectAccumulator{
				title:     text,
				order:     order,
				pageStart: 0,
				pageEnd:   0,
			}
			order++
			continue
		}

		if current == nil {
			continue
		}

		if label := normalizeLabel(text); label != "" {
			current.lastLabel = label
			continue
		}

		if techTokens := parseTechStack(text); len(techTokens) > 0 {
			current.techStack = appendUniqueStrings(current.techStack, techTokens...)
			current.lastLabel = ""
			continue
		}

		current.bodyLines = append(current.bodyLines, text)
		current.lastLabel = ""
	}

	flushCurrent()
	return sections
}

// normalizeGenericSections 归一化 `通用section`，避免后续流程重复处理边界输入。
func normalizeGenericSections(blocks []parser.Block) []NormalizedSection {
	sections := make([]NormalizedSection, 0)
	var (
		currentTitle string
		currentLines []string
		order        = 1
	)

	flushCurrent := func() {
		content := strings.TrimSpace(strings.Join(currentLines, "\n"))
		if strings.TrimSpace(currentTitle) == "" && content == "" {
			return
		}

		title := strings.TrimSpace(currentTitle)
		if title == "" {
			title = "全文"
		}

		sections = append(sections, NormalizedSection{
			SectionKey: fmt.Sprintf("section-%d", order),
			Type:       SectionTypeSection,
			Order:      order,
			Title:      title,
			Summary:    firstNonEmptyLine(content),
			Content:    content,
			Metadata:   map[string]any{},
		})
		order++
		currentTitle = ""
		currentLines = nil
	}

	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}

		if block.Type == parser.BlockHeading {
			if currentTitle != "" || len(currentLines) > 0 {
				flushCurrent()
			}
			currentTitle = text
			continue
		}

		currentLines = append(currentLines, text)
	}

	flushCurrent()
	if len(sections) > 0 {
		return sections
	}

	contentLines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text != "" {
			contentLines = append(contentLines, text)
		}
	}

	content := strings.TrimSpace(strings.Join(contentLines, "\n"))
	if content == "" {
		return nil
	}

	return []NormalizedSection{{
		SectionKey: "document-1",
		Type:       SectionTypeDocument,
		Order:      1,
		Title:      "全文",
		Summary:    firstNonEmptyLine(content),
		Content:    content,
		Metadata:   map[string]any{},
	}}
}

// isProjectTitle 判断 `项目标题` 是否满足当前流程的条件，避免同一谓词在多处分散实现。
func isProjectTitle(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || normalizeLabel(trimmed) != "" {
		return false
	}

	return projectDatePattern.MatchString(trimmed)
}

// stripProjectDateSuffix 去掉 `项目日期后缀` 中不需要的后缀或噪声，统一后续处理看到的文本。
func stripProjectDateSuffix(title string) string {
	return strings.TrimSpace(projectDatePattern.ReplaceAllString(title, ""))
}

// normalizeLabel 归一化 `Label`，避免后续流程重复处理边界输入。
func normalizeLabel(text string) string {
	switch strings.TrimSuffix(strings.TrimSpace(text), "：") {
	case "项目", "项目描述":
		return "project_description"
	case "工作内容", "职责", "负责内容":
		return "project_work"
	default:
		return ""
	}
}

// parseTechStack 解析 `技术栈`，把格式和参数错误收口到文档归一化边界。
func parseTechStack(text string) []string {
	if strings.Contains(text, "：") || strings.Contains(text, ":") {
		return nil
	}

	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '、' || r == '/' || r == ',' || r == '，'
	})
	if len(fields) < 2 {
		return nil
	}

	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		token := strings.TrimSpace(field)
		if token == "" {
			continue
		}
		if strings.ContainsAny(token, "。；;") {
			return nil
		}
		if !looksLikeTechToken(token) {
			return nil
		}
		tokens = append(tokens, token)
	}
	if len(tokens) < 2 {
		return nil
	}

	return tokens
}

// buildAliases 组装 `Aliases`，统一名称去重和空值过滤规则。
func buildAliases(title string, canonical string) []string {
	return appendUniqueStrings(nil, strings.TrimSpace(title), strings.TrimSpace(canonical))
}

// appendUniqueStrings 追加 `UniqueStrings`，保持消息和副作用写入顺序一致。
func appendUniqueStrings(existing []string, values ...string) []string {
	result := append([]string(nil), existing...)
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}

		duplicate := false
		for _, candidate := range result {
			if candidate == trimmed {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, trimmed)
		}
	}

	return result
}

// firstNonEmptyLine 返回文本里的首个非空行，供标题推断和摘要回退逻辑复用。
func firstNonEmptyLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}

	return ""
}

// looksLikeTechToken 用轻量规则判断 token 是否像技术栈条目，避免把普通短语误判成技术名。
func looksLikeTechToken(token string) bool {
	for _, r := range token {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if unicode.In(r, unicode.Han) {
				return false
			}
		case strings.ContainsRune("+-_#.", r):
		default:
			return false
		}
	}

	return true
}
