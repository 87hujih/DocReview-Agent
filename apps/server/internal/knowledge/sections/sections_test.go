package sections

import (
	"strings"
	"testing"
)

func TestParseMarkdownAssignsOccurrenceToDuplicateHeadings(t *testing.T) {
	input := strings.Join([]string{
		"# 手册",
		"",
		"## 注意事项",
		"第一处内容",
		"",
		"## 注意事项",
		"第二处内容",
	}, "\n")

	parsed := ParseMarkdown(input)
	if len(parsed) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(parsed))
	}

	if parsed[0].Title != "注意事项" || parsed[0].Occurrence != 1 {
		t.Fatalf("expected first section occurrence to be 1, got %#v", parsed[0])
	}
	if parsed[1].Title != "注意事项" || parsed[1].Occurrence != 2 {
		t.Fatalf("expected second section occurrence to be 2, got %#v", parsed[1])
	}
	if parsed[0].Body != "第一处内容" {
		t.Fatalf("expected first section body %q, got %q", "第一处内容", parsed[0].Body)
	}
	if parsed[1].Body != "第二处内容" {
		t.Fatalf("expected second section body %q, got %q", "第二处内容", parsed[1].Body)
	}
}

func TestParseMarkdownFallsBackToWholeDocumentSection(t *testing.T) {
	input := "这是没有二级标题的正文。\n\n第二段也应该保留。"

	parsed := ParseMarkdown(input)
	if len(parsed) != 1 {
		t.Fatalf("expected 1 fallback section, got %d", len(parsed))
	}

	if parsed[0].Title != WholeDocumentTitle {
		t.Fatalf("expected whole-document title %q, got %q", WholeDocumentTitle, parsed[0].Title)
	}
	if parsed[0].Occurrence != 1 {
		t.Fatalf("expected fallback occurrence 1, got %d", parsed[0].Occurrence)
	}
	if parsed[0].Body != input {
		t.Fatalf("expected fallback body %q, got %q", input, parsed[0].Body)
	}
}
