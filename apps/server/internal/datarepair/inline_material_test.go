package datarepair

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRepairInlineMaterialBodyRemovesTrailingExecutionLine 验证单独成行的执行句会从历史正文中剥离。
func TestRepairInlineMaterialBodyRemovesTrailingExecutionLine(t *testing.T) {
	content := strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析

请直接帮我改成产品经理版本
`)

	repaired, changed := RepairInlineMaterialBody(content)

	if !changed {
		t.Fatal("期望识别出尾部执行句并返回 changed=true")
	}
	if strings.Contains(repaired, "请直接帮我改成产品经理版本") {
		t.Fatalf("期望修复后正文不再包含尾部执行句，实际得到 %q", repaired)
	}
}

// TestRepairInlineMaterialBodyRemovesInlineExecutionSuffix 验证与正文处于同一行的执行尾巴也会被剥离。
func TestRepairInlineMaterialBodyRemovesInlineExecutionSuffix(t *testing.T) {
	content := strings.TrimSpace(`
流程与时间线：原文提到“试用期内应完成基础制度学习”，但未明确完成周期与责任主体。
资源与路径：原文提到“进阶课程、项目历练、外部培训机会”，但缺少申请路径说明。
反馈机制：原文说“直属主管与导师应定期跟进学习效果并给予反馈”，但“定期”较为模糊。可以细化，例如“导师需在入职第1个月、第3个月进行正式学习回顾与反馈”。直接按照这个补充，创建任务吧
`)

	repaired, changed := RepairInlineMaterialBody(content)

	if !changed {
		t.Fatal("期望识别出同一行尾部执行句并返回 changed=true")
	}
	if strings.Contains(repaired, "直接按照这个补充，创建任务吧") {
		t.Fatalf("期望修复后正文不再包含同一行尾部执行句，实际得到 %q", repaired)
	}
	if !strings.Contains(repaired, "反馈机制") {
		t.Fatalf("期望正文主体内容保留，实际得到 %q", repaired)
	}
}

// TestRepairInlineMaterialBodyKeepsNormalTail 验证普通正文结尾不会被误删。
func TestRepairInlineMaterialBodyKeepsNormalTail(t *testing.T) {
	content := strings.TrimSpace(`
流程与时间线：补充入职第一周完成基础制度学习。
资源与路径：补充培训课程目录入口。
反馈机制：导师需在入职第1个月、第3个月进行正式学习回顾与反馈。
`)

	repaired, changed := RepairInlineMaterialBody(content)

	if changed {
		t.Fatalf("期望普通正文不触发修复，实际得到 %q", repaired)
	}
	if repaired != content {
		t.Fatalf("期望普通正文保持不变，实际得到 %q", repaired)
	}
}

// TestRepairDiffPreviewArtifactRepairsOriginalOnly 验证 diff_preview 只修复 original 字段里的历史脏尾巴。
func TestRepairDiffPreviewArtifactRepairsOriginalOnly(t *testing.T) {
	artifact := []byte(`{
  "no_change": false,
  "sections": [
    {
      "section_title": "全文",
      "section_occurrence": 1,
      "original": "反馈机制：原文说直属主管与导师应定期跟进学习效果并给予反馈，但定期较为模糊。可以细化，例如导师需在入职第1个月、第3个月进行正式学习回顾与反馈。直接按照这个补充，创建任务吧",
      "revised": "反馈机制：补充后的正文，同时保留 task 提示文本以展示历史原样。",
      "reason": "clarify",
      "citation_ids": ["cite_1"]
    }
  ]
}`)

	repaired, changed, err := RepairDiffPreviewArtifact(artifact)
	if err != nil {
		t.Fatalf("期望 diff_preview 修复成功，实际报错：%v", err)
	}
	if !changed {
		t.Fatal("期望识别出 diff_preview.original 里的历史脏尾巴")
	}

	var payload struct {
		Sections []struct {
			Original string `json:"original"`
			Revised  string `json:"revised"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(repaired, &payload); err != nil {
		t.Fatalf("解析修复后的 artifact 失败：%v", err)
	}
	if strings.Contains(payload.Sections[0].Original, "直接按照这个补充，创建任务吧") {
		t.Fatalf("期望修复后 original 不再包含尾部执行句，实际得到 %q", payload.Sections[0].Original)
	}
	if !strings.Contains(payload.Sections[0].Revised, "task 提示文本") {
		t.Fatalf("期望 revised 保持原样，实际得到 %q", payload.Sections[0].Revised)
	}
}
