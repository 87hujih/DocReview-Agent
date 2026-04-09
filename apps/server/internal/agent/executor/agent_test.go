package executor

import (
	"strings"
	"testing"

	"agent_project/apps/server/internal/agent/editor"
)

func TestApplySectionReplacements(t *testing.T) {
	content := strings.Join([]string{
		"# 文档标题",
		"",
		"## 第一章",
		"原始第一章内容",
		"",
		"## 第二章",
		"原始第二章内容",
		"",
	}, "\n")

	updated := applySectionReplacements(content, []editor.DiffSection{
		{
			SectionTitle: "第一章",
			Revised:      "修订后的第一章内容",
		},
	})

	expected := strings.Join([]string{
		"# 文档标题",
		"",
		"## 第一章",
		"修订后的第一章内容",
		"",
		"## 第二章",
		"原始第二章内容",
		"",
	}, "\n")

	if updated != expected {
		t.Fatalf("expected updated content:\n%s\n\ngot:\n%s", expected, updated)
	}
}
