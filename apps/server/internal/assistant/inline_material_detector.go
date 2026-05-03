package assistant

import (
	"strings"
	"unicode/utf8"
)

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
	bodyLines = trimTrailingExecutionRequest(bodyLines)

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

// trimTrailingExecutionRequest 去掉正文尾部夹带的执行句，避免“正文 + 创建任务”混写时把指令入库成原文。
func trimTrailingExecutionRequest(lines []string) []string {
	if len(lines) < 3 {
		return lines
	}

	lastLine := lines[len(lines)-1]
	if utf8RuneLen(lastLine) <= 32 && isExecutionRequest(lastLine) {
		return lines[:len(lines)-1]
	}

	trimmedLastLine, stripped := stripTrailingExecutionSuffix(lastLine)
	if !stripped {
		return lines
	}

	lines[len(lines)-1] = trimmedLastLine
	return lines
}

// stripTrailingExecutionSuffix 处理“正文句子。创建任务吧”这类同一行尾部执行句，保留正文句子本身。
func stripTrailingExecutionSuffix(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", false
	}

	splitPoints := make([]int, 0, 4)
	for index, r := range trimmed {
		if !isSentenceBoundary(r) {
			continue
		}

		splitPoints = append(splitPoints, index+utf8.RuneLen(r))
	}

	for index := len(splitPoints) - 1; index >= 0; index-- {
		suffix := strings.TrimSpace(trimmed[splitPoints[index]:])
		if suffix == "" || utf8RuneLen(suffix) > 32 || !isExecutionRequest(suffix) {
			continue
		}

		prefix := strings.TrimSpace(trimmed[:splitPoints[index]])
		if prefix == "" {
			return trimmed, false
		}

		return prefix, true
	}

	return trimmed, false
}

func isSentenceBoundary(r rune) bool {
	switch r {
	case '。', '！', '？', '!', '?', '；', ';':
		return true
	default:
		return false
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
