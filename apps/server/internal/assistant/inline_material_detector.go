package assistant

import "strings"

const inlineMaterialSyntheticName = "对话粘贴正文.md"

type InlineMaterialCandidate struct {
	Body          string
	HasMaterial   bool
	SyntheticName string
}

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

func utf8RuneLen(value string) int {
	return len([]rune(strings.TrimSpace(value)))
}
