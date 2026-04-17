package normalize

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"agent_project/apps/server/internal/document/parser"
)

// SectionType 表示归一化后的逻辑 section 类型。
type SectionType string

const (
	// SectionTypeWholeDocument 表示整篇文档兜底 section。
	SectionTypeWholeDocument SectionType = "whole_document"
	// SectionTypeProject 表示简历中的项目 section。
	SectionTypeProject SectionType = "project"
)

// NormalizedDocument 表示 normalize 后的结构化文档。
type NormalizedDocument struct {
	SourceFormat string
	Sections     []NormalizedSection
}

// NormalizedSection 表示可持久化的逻辑 section。
type NormalizedSection struct {
	SectionKey string
	Type       SectionType
	Title      string
	Summary    string
	Content    string
	TechStack  []string
	PageStart  int
	PageEnd    int
	Metadata   map[string]any
}

// Service 负责把 parser block 归并成逻辑 section。
type Service struct{}

var projectDateSuffixPattern = regexp.MustCompile(`[（(][^）)]*\d{4}[^）)]*[）)]$`)

// NewService 构造文档 normalize 服务。
func NewService() *Service {
	return &Service{}
}

// Normalize 把结构化 block 重组成逻辑 section；当前阶段优先识别简历项目。
func (s *Service) Normalize(doc parser.ParsedDocument) NormalizedDocument {
	lines := collectNormalizedLines(doc)
	projectSections := s.buildProjectSections(lines)
	if len(projectSections) > 0 {
		return NormalizedDocument{
			SourceFormat: doc.SourceFormat,
			Sections:     projectSections,
		}
	}

	return NormalizedDocument{
		SourceFormat: doc.SourceFormat,
		Sections: []NormalizedSection{
			{
				SectionKey: "whole-document-1",
				Type:       SectionTypeWholeDocument,
				Title:      "全文",
				Content:    strings.Join(lines, "\n"),
			},
		},
	}
}

func collectNormalizedLines(doc parser.ParsedDocument) []string {
	lines := make([]string, 0, len(doc.Blocks))
	for _, block := range doc.Blocks {
		text := strings.TrimSpace(mergeBrokenCJKLines(block.Text))
		if text == "" {
			continue
		}

		for _, line := range strings.Split(text, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}

			lines = append(lines, trimmed)
		}
	}

	return lines
}

func (s *Service) buildProjectSections(lines []string) []NormalizedSection {
	sections := make([]NormalizedSection, 0)
	var current *NormalizedSection
	expectProjectTitle := false

	flushCurrent := func() {
		if current == nil {
			return
		}

		current.Content = strings.TrimSpace(current.Content)
		if current.Content == "" {
			if current.Metadata == nil {
				current.Metadata = map[string]any{}
			}
			current.Metadata["low_confidence"] = true
		}
		if current.Summary == "" {
			current.Summary = firstContentLine(current.Content)
		}

		sections = append(sections, *current)
		current = nil
	}

	for _, line := range lines {
		switch {
		case isProjectAreaHeading(line):
			expectProjectTitle = true
			continue
		case isSectionBoundary(line):
			flushCurrent()
			expectProjectTitle = false
			continue
		case isNoiseLine(line):
			continue
		}

		if current == nil && expectProjectTitle {
			if isProjectLabelOnlyLine(line) {
				continue
			}

			current = &NormalizedSection{
				SectionKey: fmt.Sprintf("project-%d", len(sections)+1),
				Type:       SectionTypeProject,
				Title:      stripProjectDateSuffix(line),
			}
			expectProjectTitle = false
			continue
		}

		if current == nil && looksLikeProjectTitle(line) {
			current = &NormalizedSection{
				SectionKey: fmt.Sprintf("project-%d", len(sections)+1),
				Type:       SectionTypeProject,
				Title:      stripProjectDateSuffix(line),
			}
			continue
		}

		if current == nil {
			continue
		}

		if looksLikeProjectTitle(line) {
			flushCurrent()
			current = &NormalizedSection{
				SectionKey: fmt.Sprintf("project-%d", len(sections)+1),
				Type:       SectionTypeProject,
				Title:      stripProjectDateSuffix(line),
			}
			continue
		}

		if len(current.TechStack) == 0 && looksLikeTechStack(line) {
			current.TechStack = splitTechStack(line)
			continue
		}

		if isProjectLabelOnlyLine(line) {
			continue
		}

		if current.Content == "" {
			current.Content = line
		} else {
			current.Content += "\n" + line
		}
	}

	flushCurrent()
	return sections
}

func mergeBrokenCJKLines(text string) string {
	runes := []rune(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"))
	var builder strings.Builder
	for index, current := range runes {
		if current != '\n' {
			builder.WriteRune(current)
			continue
		}

		if index == 0 || index == len(runes)-1 {
			builder.WriteRune(current)
			continue
		}

		prev := runes[index-1]
		next := runes[index+1]
		if isCJKRune(prev) && isCJKRune(next) {
			continue
		}

		builder.WriteRune(current)
	}

	return builder.String()
}

func isCJKRune(value rune) bool {
	return unicode.In(value, unicode.Han)
}

func isProjectAreaHeading(line string) bool {
	switch strings.TrimSpace(line) {
	case "项目", "项目经验":
		return true
	default:
		return false
	}
}

func isProjectLabelOnlyLine(line string) bool {
	switch strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "："), ":")) {
	case "项目", "项目经验", "项目描述", "工作内容":
		return true
	default:
		return false
	}
}

func isSectionBoundary(line string) bool {
	switch strings.TrimSpace(line) {
	case "教育经历", "专业技能", "技能", "联系方式", "自我评价", "个人简介":
		return true
	default:
		return false
	}
}

func isNoiseLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	return strings.Contains(lower, "@") ||
		strings.Contains(lower, "http://") ||
		strings.Contains(lower, "https://") ||
		strings.Contains(lower, "github.com") ||
		strings.Contains(lower, "www.")
}

func looksLikeProjectTitle(line string) bool {
	if isProjectLabelOnlyLine(line) || isNoiseLine(line) {
		return false
	}

	return projectDateSuffixPattern.MatchString(line)
}

func looksLikeTechStack(line string) bool {
	if strings.Contains(line, "：") || strings.Contains(line, ":") {
		return false
	}

	return len(splitTechStack(line)) >= 2
}

func splitTechStack(line string) []string {
	replacer := strings.NewReplacer("/", " ", ",", " ", "，", " ", "、", " ", "|", " ")
	tokens := strings.Fields(replacer.Replace(line))
	result := make([]string, 0, len(tokens))
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed == "" {
			continue
		}
		if strings.ContainsAny(trimmed, "：:") {
			continue
		}

		result = append(result, trimmed)
	}

	return result
}

func stripProjectDateSuffix(line string) string {
	trimmed := strings.TrimSpace(line)
	if projectDateSuffixPattern.MatchString(trimmed) {
		return strings.TrimSpace(projectDateSuffixPattern.ReplaceAllString(trimmed, ""))
	}

	return trimmed
}

func firstContentLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}

	return ""
}
