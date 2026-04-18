package workflow

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/knowledge/sections"
)

// TestBuildEditorContentUsesWholeDocumentWhenNoSecondaryHeadings 验证无二级标题正文必须把整篇内容交给 editor，避免只编辑局部段落。
func TestBuildEditorContentUsesWholeDocumentWhenNoSecondaryHeadings(t *testing.T) {
	content := strings.Join([]string{
		"马华恩",
		"后端开发",
		"",
		"项目经历",
		"慢跑 (2025.7-2025.10)",
		"负责后端接口开发与上线。",
		"",
		"专业技能",
		"Go / PostgreSQL / Redis",
	}, "\n")

	result := buildEditorContent(content, []string{"项目经历"}, []citation.Citation{{
		CitationID: "cite_1",
		Snippet:    "慢跑 (2025.7-2025.10)",
	}})

	if result != content {
		t.Fatalf("期望无二级标题正文传给 editor 的是整篇内容，实际得到 %q", result)
	}
}

// TestBuildEditorContentExtractsMatchedMarkdownSections 验证有二级标题时应提取完整 section，而不是只给 editor 片段段落。
func TestBuildEditorContentExtractsMatchedMarkdownSections(t *testing.T) {
	content := strings.Join([]string{
		"# 简历",
		"",
		"## 项目经历",
		"慢跑 (2025.7-2025.10)",
		"负责后端接口开发与上线。",
		"",
		"## 专业技能",
		"Go / PostgreSQL / Redis",
	}, "\n")

	result := buildEditorContent(content, []string{"项目经历"}, nil)

	if !strings.Contains(result, "## 项目经历") {
		t.Fatalf("期望 editor 输入保留完整章节标题，实际得到 %q", result)
	}
	if !strings.Contains(result, "负责后端接口开发与上线。") {
		t.Fatalf("期望 editor 输入包含完整章节正文，实际得到 %q", result)
	}
	if strings.Contains(result, "## 专业技能") {
		t.Fatalf("期望 editor 输入只包含命中的章节，实际得到 %q", result)
	}
}

// TestValidateDiffPreviewAgainstBaseContentRejectsUnmappedWholeDocumentSection 验证无二级标题正文不能接受伪造章节标题。
func TestValidateDiffPreviewAgainstBaseContentRejectsUnmappedWholeDocumentSection(t *testing.T) {
	content := strings.Join([]string{
		"马华恩",
		"后端开发",
		"",
		"项目经历",
		"慢跑 (2025.7-2025.10)",
		"负责后端接口开发与上线。",
	}, "\n")

	preview := &editor.DiffPreview{
		Sections: []editor.DiffSection{{
			SectionTitle:      "项目",
			SectionOccurrence: 1,
			Original:          "慢跑 (2025.7-2025.10)\n负责后端接口开发与上线。",
			Revised:           "慢跑 (2025.7-2025.10)\n负责后端接口设计、开发与上线。",
			Reason:            "补充说明",
			CitationIDs:       []string{"cite_1"},
		}},
	}

	err := validateDiffPreviewAgainstBaseContent(preview, content)
	if err == nil {
		t.Fatal("期望无二级标题正文上的伪造章节标题被拒绝")
	}
	if !strings.Contains(err.Error(), "未找到 diff 预览对应章节：项目") {
		t.Fatalf("期望错误包含未找到章节提示，实际得到 %v", err)
	}
}

// TestValidateDiffPreviewAgainstBaseContentRejectsWholeDocumentOriginalMismatch 验证全文模式下 original 必须与基线正文精确一致。
func TestValidateDiffPreviewAgainstBaseContentRejectsWholeDocumentOriginalMismatch(t *testing.T) {
	content := strings.Join([]string{
		"马华恩",
		"后端开发",
		"",
		"项目经历",
		"慢跑 (2025.7-2025.10)",
		"负责后端接口开发与上线。",
	}, "\n")

	preview := &editor.DiffPreview{
		Sections: []editor.DiffSection{{
			SectionTitle:      sections.WholeDocumentTitle,
			SectionOccurrence: 1,
			Original:          "慢跑 (2025.7-2025.10)\n负责后端接口开发与上线。",
			Revised:           content + "\n\n补充：熟悉高并发场景。",
			Reason:            "补充说明",
			CitationIDs:       []string{"cite_1"},
		}},
	}

	err := validateDiffPreviewAgainstBaseContent(preview, content)
	if err == nil {
		t.Fatal("期望全文模式下 original 与基线正文不一致时返回错误")
	}
	if !strings.Contains(err.Error(), "original 与基线正文不一致") {
		t.Fatalf("期望错误包含 original 校验提示，实际得到 %v", err)
	}
}
