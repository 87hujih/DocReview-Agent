package datarepair

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// RepairInlineMaterialBody 修复历史内联正文里的尾部执行句，保持正文主体尽量不变。
func RepairInlineMaterialBody(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", false
	}

	lines := strings.Split(trimmed, "\n")
	lastIndex := lastNonEmptyLineIndex(lines)
	if lastIndex < 0 {
		return trimmed, false
	}

	lastLine := strings.TrimSpace(lines[lastIndex])
	if utf8RuneLen(lastLine) <= 32 && looksLikeExecutionRequest(lastLine) {
		lines[lastIndex] = ""
		return strings.TrimSpace(strings.Join(lines, "\n")), true
	}

	repairedLine, changed := stripTrailingExecutionSuffix(lastLine)
	if !changed {
		return trimmed, false
	}

	lines[lastIndex] = repairedLine
	return strings.TrimSpace(strings.Join(lines, "\n")), true
}

// RepairDiffPreviewArtifact 只修复 diff_preview.sections[*].original 中的历史脏尾巴，避免改动 revised 等字段。
func RepairDiffPreviewArtifact(content []byte) ([]byte, bool, error) {
	var payload diffPreviewArtifact
	if err := json.Unmarshal(content, &payload); err != nil {
		return nil, false, err
	}

	changed := false
	for index := range payload.Sections {
		repairedOriginal, sectionChanged := RepairInlineMaterialBody(payload.Sections[index].Original)
		if !sectionChanged {
			continue
		}

		payload.Sections[index].Original = repairedOriginal
		changed = true
	}

	if !changed {
		return append([]byte(nil), content...), false, nil
	}

	repaired, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}

	return repaired, true, nil
}

type diffPreviewArtifact struct {
	NoChange bool                 `json:"no_change"`
	Sections []diffPreviewSection `json:"sections"`
}

type diffPreviewSection struct {
	SectionTitle      string   `json:"section_title"`
	SectionOccurrence int      `json:"section_occurrence,omitempty"`
	Original          string   `json:"original"`
	Revised           string   `json:"revised"`
	Reason            string   `json:"reason"`
	CitationIDs       []string `json:"citation_ids,omitempty"`
}

// lastNonEmptyLineIndex 返回最后一个非空行的下标，避免尾部空白扰动修复判定。
func lastNonEmptyLineIndex(lines []string) int {
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.TrimSpace(lines[index]) != "" {
			return index
		}
	}

	return -1
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
		if suffix == "" || utf8RuneLen(suffix) > 32 || !looksLikeExecutionRequest(suffix) {
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

// looksLikeExecutionRequest 用和线上识别接近的轻量关键词判断“这段文本更像尾部执行句而不是正文”。
func looksLikeExecutionRequest(message string) bool {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return false
	}

	if containsAny(trimmed, []string{
		"整理成任务",
		"生成任务",
		"创建任务",
		"任务建议",
		"任务卡",
		"开始执行",
	}) {
		return true
	}

	if !containsAny(trimmed, []string{
		"请直接",
		"直接帮我",
		"直接把",
		"请帮我",
		"帮我",
		"请把",
		"开始处理",
		"开始修改",
		"开始执行",
		"执行吧",
		"生成新版本",
		"按这个方向开始修改",
		"按这个方向开始处理",
		"按照这个",
		"按这个",
		"现在就改",
		"马上改",
	}) {
		return false
	}

	return containsAny(trimmed, []string{
		"改成",
		"改写",
		"重写",
		"润色",
		"检查并修订",
		"修订",
		"改为",
		"修改",
		"处理",
		"新版本",
		"补充",
		"创建任务",
	})
}

func containsAny(message string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(message, keyword) {
			return true
		}
	}

	return false
}

func isSentenceBoundary(r rune) bool {
	switch r {
	case '。', '！', '？', '!', '?', '；', ';':
		return true
	default:
		return false
	}
}

func utf8RuneLen(value string) int {
	return len([]rune(strings.TrimSpace(value)))
}
