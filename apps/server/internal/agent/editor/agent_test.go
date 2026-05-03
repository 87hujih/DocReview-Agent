package editor

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/knowledge/sections"
)

// TestNormalizePreviewForResourceContentUsesWholeDocumentFallback 验证无二级标题正文会把模型输出归一化到“全文”章节。
func TestNormalizePreviewForResourceContentUsesWholeDocumentFallback(t *testing.T) {
	preview := &DiffPreview{
		Sections: []DiffSection{{
			SectionTitle:      "项目",
			SectionOccurrence: 3,
			Original:          "原文",
			Revised:           "改写后原文",
			Reason:            "补充说明",
			CitationIDs:       []string{"cite_1"},
		}},
	}

	normalizePreviewForResourceContent(preview, "马华恩\n\n项目经历\n慢跑")

	if preview.Sections[0].SectionTitle != sections.WholeDocumentTitle {
		t.Fatalf("期望 section_title 被归一化为 %q，实际得到 %q", sections.WholeDocumentTitle, preview.Sections[0].SectionTitle)
	}
	if preview.Sections[0].SectionOccurrence != 1 {
		t.Fatalf("期望 section_occurrence 被归一化为 1，实际得到 %d", preview.Sections[0].SectionOccurrence)
	}
}

// TestNormalizePreviewForResourceContentKeepsMarkdownSections 验证存在二级标题时不应篡改模型返回的章节信息。
func TestNormalizePreviewForResourceContentKeepsMarkdownSections(t *testing.T) {
	preview := &DiffPreview{
		Sections: []DiffSection{{
			SectionTitle:      "项目经历",
			SectionOccurrence: 2,
			Original:          "原文",
			Revised:           "改写后原文",
			Reason:            "补充说明",
			CitationIDs:       []string{"cite_1"},
		}},
	}

	normalizePreviewForResourceContent(preview, "## 项目经历\n原文")

	if preview.Sections[0].SectionTitle != "项目经历" {
		t.Fatalf("期望保留原始 section_title，实际得到 %q", preview.Sections[0].SectionTitle)
	}
	if preview.Sections[0].SectionOccurrence != 2 {
		t.Fatalf("期望保留原始 section_occurrence，实际得到 %d", preview.Sections[0].SectionOccurrence)
	}
}

// TestNormalizePreviewForResourceContentStripsRepeatedMarkdownHeading 验证模型把章节标题重复写进 original/revised 时会被归一化掉。
func TestNormalizePreviewForResourceContentStripsRepeatedMarkdownHeading(t *testing.T) {
	preview := &DiffPreview{
		Sections: []DiffSection{{
			SectionTitle:      "项目经历",
			SectionOccurrence: 1,
			Original: strings.Join([]string{
				"## 项目经历",
				"慢跑 (2025.7-2025.10)",
				"负责后端接口开发与上线。",
			}, "\n"),
			Revised: strings.Join([]string{
				"## 项目经历",
				"慢跑 (2025.7-2025.10)",
				"负责后端接口设计、开发与上线。",
			}, "\n"),
			Reason:      "补充说明",
			CitationIDs: []string{"cite_1"},
		}},
	}

	resourceContent := strings.Join([]string{
		"## 项目经历",
		"慢跑 (2025.7-2025.10)",
		"负责后端接口开发与上线。",
		"",
		"## 专业技能",
		"Go / PostgreSQL / Redis",
	}, "\n")

	normalizePreviewForResourceContent(preview, resourceContent)

	if strings.Contains(preview.Sections[0].Original, "## 项目经历") {
		t.Fatalf("期望 original 去掉重复标题，实际得到 %q", preview.Sections[0].Original)
	}
	if strings.Contains(preview.Sections[0].Revised, "## 项目经历") {
		t.Fatalf("期望 revised 去掉重复标题，实际得到 %q", preview.Sections[0].Revised)
	}
	if preview.Sections[0].Original != "慢跑 (2025.7-2025.10)\n负责后端接口开发与上线。" {
		t.Fatalf("期望 original 归一化为 section body，实际得到 %q", preview.Sections[0].Original)
	}
	if preview.Sections[0].Revised != "慢跑 (2025.7-2025.10)\n负责后端接口设计、开发与上线。" {
		t.Fatalf("期望 revised 归一化为 section body，实际得到 %q", preview.Sections[0].Revised)
	}
}
