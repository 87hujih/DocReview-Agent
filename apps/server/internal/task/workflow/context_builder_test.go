package workflow

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/knowledge/citation"
)

func TestBuildFocusSectionPriority(t *testing.T) {
	content := "第一章 总则\n总则内容\n\n第二章 考勤\n考勤规定内容\n\n第三章 薪酬\n薪酬计算方式"
	focusSections := []string{"考勤"}
	citations := []citation.Citation{}
	maxRunes := 50 // 足够小，无法包含全部内容

	result := (ContextBuilder{}).Build(content, focusSections, citations, maxRunes)

	if !strings.Contains(result.Content, "考勤") {
		t.Fatalf("期望结果包含重点章节【考勤】，实际得到 %q", result.Content)
	}
	if len(result.UsedSections) == 0 || result.UsedSections[0] != "考勤" {
		t.Fatalf("期望 UsedSections 包含【考勤】，实际得到 %v", result.UsedSections)
	}
}

func TestBuildCitationSnippetExtracted(t *testing.T) {
	content := "导言\n一般描述\n\n核心条款\n数据分类规定\n\n附录\n参考文献"
	focusSections := []string{}
	citations := []citation.Citation{
		{CitationID: "cite_1", Snippet: "数据分类规定"},
	}
	maxRunes := 40

	result := (ContextBuilder{}).Build(content, focusSections, citations, maxRunes)

	if !strings.Contains(result.Content, "数据分类规定") {
		t.Fatalf("期望结果包含引用片段【数据分类规定】，实际得到 %q", result.Content)
	}
}

func TestBuildTrimmedRunesWhenOverBudget(t *testing.T) {
	// 构造明显超出预算的文档
	content := strings.Repeat("这是很长的一段文字内容。", 200) // 约 2400 个字符
	maxRunes := 100

	result := (ContextBuilder{}).Build(content, nil, nil, maxRunes)

	if result.TrimmedRunes <= 0 {
		t.Fatalf("期望 TrimmedRunes > 0，实际得到 %d", result.TrimmedRunes)
	}
	if result.TrimReason == "" {
		t.Fatal("内容被裁剪时期望 TrimReason 非空")
	}
	if len([]rune(result.Content)) > maxRunes {
		t.Fatalf("结果字符数超出 maxRunes：实际 %d 个字符", len([]rune(result.Content)))
	}
}

func TestBuildFallbackToHeadWhenNoFocusOrCitations(t *testing.T) {
	content := "第一段内容是文档开头\n\n第二段内容\n\n第三段内容"
	maxRunes := 30 // 足够小，强制触发裁剪

	result := (ContextBuilder{}).Build(content, nil, nil, maxRunes)

	if !strings.Contains(result.Content, "第一段") {
		t.Fatalf("无重点章节和引用时期望兜底返回首段，实际得到 %q", result.Content)
	}
}

func TestBuildReturnsFullContentWhenUnderBudget(t *testing.T) {
	content := "短文档"
	result := (ContextBuilder{}).Build(content, []string{"短"}, nil, 24000)

	if result.Content != content {
		t.Fatalf("内容未超预算时期望原样返回，实际得到 %q", result.Content)
	}
	if result.TrimmedRunes != 0 {
		t.Fatalf("期望 TrimmedRunes == 0，实际得到 %d", result.TrimmedRunes)
	}
}