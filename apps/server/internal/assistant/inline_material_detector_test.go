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
