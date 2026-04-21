package assistant

import (
	"strings"
	"testing"
)

// TestDetectInlineMaterialRecognizesStructuredResumeText 验证`detectInlineMaterialRecognizesStructuredResumeText`在特定边界条件下的行为，防止同类回归。
func TestDetectInlineMaterialRecognizesStructuredResumeText(t *testing.T) {
	candidate := DetectInlineMaterial(strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析

教育经历
- XX 大学
`))

	if !candidate.HasMaterial {
		t.Fatal("expected structured resume text to be detected as material")
	}
	if candidate.Body == "" {
		t.Fatal("expected detected material body to be retained")
	}
	if candidate.SyntheticName != "对话粘贴正文.md" {
		t.Fatalf("expected stable synthetic name, got %q", candidate.SyntheticName)
	}
}

// TestDetectInlineMaterialDoesNotTreatShortQuestionAsMaterial 验证`detectInlineMaterialDoesNotTreatShortQuestionAsMaterial`在特定边界条件下的行为，防止同类回归。
func TestDetectInlineMaterialDoesNotTreatShortQuestionAsMaterial(t *testing.T) {
	candidate := DetectInlineMaterial("这份简历哪里还需要优化？")
	if candidate.HasMaterial {
		t.Fatalf("expected short question not to be treated as material, got %#v", candidate)
	}
}

// TestDetectInlineMaterialSplitsTrailingExecutionSentenceFromBody 验证`detectInlineMaterialSplitsTrailingExecutionSentenceFromBody`在特定边界条件下的行为，防止同类回归。
func TestDetectInlineMaterialSplitsTrailingExecutionSentenceFromBody(t *testing.T) {
	candidate := DetectInlineMaterial(strings.TrimSpace(`
项目经历
- 负责增长策略
- 负责数据分析

请直接帮我改成产品经理版本
`))

	if !candidate.HasMaterial {
		t.Fatal("expected mixed body and execution sentence to be detected as material")
	}
	if strings.Contains(candidate.Body, "请直接帮我改成产品经理版本") {
		t.Fatalf("expected trailing execution sentence to be stripped from body, got %q", candidate.Body)
	}
	if !strings.Contains(candidate.Body, "负责增长策略") {
		t.Fatalf("expected body content to stay intact, got %q", candidate.Body)
	}
}

// TestDetectInlineMaterialStripsInlineExecutionSuffix 验证尾部执行句与正文处于同一行时也会被剥离。
func TestDetectInlineMaterialStripsInlineExecutionSuffix(t *testing.T) {
	candidate := DetectInlineMaterial(strings.TrimSpace(`
流程与时间线：原文提到“试用期内应完成基础制度学习”，但未明确完成周期与责任主体。
资源与路径：原文提到“进阶课程、项目历练、外部培训机会”，但缺少申请路径说明。
反馈机制：原文说“直属主管与导师应定期跟进学习效果并给予反馈”，但“定期”较为模糊。可以细化，例如“导师需在入职第1个月、第3个月进行正式学习回顾与反馈”。直接按照这个补充，创建任务吧
`))

	if !candidate.HasMaterial {
		t.Fatal("expected mixed inline body and execution suffix to be detected as material")
	}
	if strings.Contains(candidate.Body, "直接按照这个补充，创建任务吧") {
		t.Fatalf("expected inline execution suffix to be stripped from body, got %q", candidate.Body)
	}
	if !strings.Contains(candidate.Body, "反馈机制") {
		t.Fatalf("expected body content to stay intact, got %q", candidate.Body)
	}
}
