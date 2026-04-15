package sections

import "strings"

// WholeDocumentTitle 是无二级标题文档在共享 section 模型中的稳定标题。
const WholeDocumentTitle = "全文"

// Section 表示一段可被 chunker / executor 共同消费的文档 section。
type Section struct {
	Title      string
	Occurrence int
	Heading    string
	Body       string
	StartLine  int
	EndLine    int
}

// ParseMarkdown 解析 Markdown 文本中的二级标题 section；若不存在二级标题，则退化为整篇文档 section。
func ParseMarkdown(content string) []Section {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if strings.TrimSpace(normalized) == "" {
		return nil
	}

	lines := strings.Split(normalized, "\n")
	headings := make([]int, 0)
	for index, line := range lines {
		if !strings.HasPrefix(line, "## ") {
			continue
		}

		headings = append(headings, index)
	}

	if len(headings) == 0 {
		return []Section{
			{
				Title:      WholeDocumentTitle,
				Occurrence: 1,
				Heading:    "",
				Body:       strings.TrimSpace(normalized),
				StartLine:  0,
				EndLine:    len(lines),
			},
		}
	}

	occurrences := make(map[string]int)
	parsed := make([]Section, 0, len(headings))
	for index, headingLine := range headings {
		endLine := len(lines)
		if index+1 < len(headings) {
			endLine = headings[index+1]
		}

		title := strings.TrimSpace(strings.TrimPrefix(lines[headingLine], "## "))
		occurrences[title]++
		parsed = append(parsed, Section{
			Title:      title,
			Occurrence: occurrences[title],
			Heading:    lines[headingLine],
			Body:       strings.TrimSpace(strings.Join(lines[headingLine+1:endLine], "\n")),
			StartLine:  headingLine,
			EndLine:    endLine,
		})
	}

	return parsed
}
