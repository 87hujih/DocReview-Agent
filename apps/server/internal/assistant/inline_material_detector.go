package assistant

import "strings"

const inlineMaterialSyntheticName = "对话粘贴正文.md"

// InlineMaterialCandidate 保存判定内联材料所需的正文、指令和摘要片段，供会话入口决定是否转入资源导入链路。
type InlineMaterialCandidate struct {
	Body          string
	HasMaterial   bool
	SyntheticName string
}

// DetectInlineMaterial 从用户输入里识别可直接导入为会话资源的结构化正文，并在尾部夹带执行指令时先把指令句剥离出来。
func DetectInlineMaterial(content string) InlineMaterialCandidate {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return InlineMaterialCandidate{}
	}

	lines := nonEmptyLines(trimmed)
	if len(lines) < 2 {
		return InlineMaterialCandidate{}
	}

	bodyLines := append([]string(nil), lines...)
	lastLine := bodyLines[len(bodyLines)-1]
	if len(bodyLines) >= 3 && utf8RuneLen(lastLine) <= 32 && isExecutionRequest(lastLine) {
		bodyLines = bodyLines[:len(bodyLines)-1]
	}

	body := strings.TrimSpace(strings.Join(bodyLines, "\n"))
	if !looksLikeInlineMaterial(bodyLines, body) {
		return InlineMaterialCandidate{}
	}

	return InlineMaterialCandidate{
		Body:          body,
		HasMaterial:   true,
		SyntheticName: inlineMaterialSyntheticName,
	}
}

// looksLikeInlineMaterial 用轻量启发式区分“多行材料”与“普通提问”，避免把短问题误判成待导入正文。
func looksLikeInlineMaterial(lines []string, body string) bool {
	if len(lines) < 2 || strings.TrimSpace(body) == "" {
		return false
	}

	structuredLines := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "-") ||
			strings.HasPrefix(trimmed, "*") ||
			strings.HasPrefix(trimmed, "•") ||
			strings.Contains(trimmed, "：") ||
			strings.Contains(trimmed, ":") {
			structuredLines++
			continue
		}
		if utf8RuneLen(trimmed) <= 12 && !strings.ContainsAny(trimmed, "，。！？?") {
			structuredLines++
		}
	}

	return structuredLines >= 2
}

// nonEmptyLines 按行裁剪空白并过滤空行，为后续正文结构识别提供稳定输入。
func nonEmptyLines(content string) []string {
	rawLines := strings.Split(content, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed)
	}

	return lines
}

// utf8RuneLen 按 rune 统计裁剪后文本长度，避免中文被按字节数错误放大。
func utf8RuneLen(value string) int {
	return len([]rune(strings.TrimSpace(value)))
}
