package assistant

import (
	"strings"
	"unicode"

	"agent_project/apps/server/internal/knowledge/citation"
)

const (
	minStandaloneSnippetRunes = 14
	minWindowLineRunes        = 4
	minWindowTotalRunes       = 20
	minFallbackSnippetRunes   = 4
)

type evidenceGate interface {
	Evaluate(citations []citation.Citation) (bool, string)
}

// EvidenceGate 负责判断当前检索证据是否足以支撑 assistant 直接回答。
type EvidenceGate struct{}

// NewEvidenceGate 构造默认的证据质量门控器。
func NewEvidenceGate() *EvidenceGate {
	return &EvidenceGate{}
}

// Evaluate 基于 citation 的 snippet 和 window 判断证据是否可直接使用。
func (g *EvidenceGate) Evaluate(citations []citation.Citation) (bool, string) {
	return EvaluateEvidenceQuality(citations)
}

// EvaluateEvidenceQuality 只做纯判断，不直接触发检索回退。
func EvaluateEvidenceQuality(citations []citation.Citation) (bool, string) {
	if len(citations) == 0 {
		return false, "当前没有命中可直接引用的证据片段"
	}

	needsFallback := false
	for _, item := range citations {
		if citationHasUsableWindow(item) || citationHasStandaloneSnippet(item) {
			return true, ""
		}
		if citationCouldBenefitFromFallback(item) {
			needsFallback = true
		}
	}

	if needsFallback {
		return false, "当前片段缺少足够的相邻 window，需要扩大检索范围后再判断"
	}

	return false, "当前只命中短标题或标签块，证据质量不足"
}

func citationHasUsableWindow(item citation.Citation) bool {
	lines := normalizeEvidenceWindow(item.Window)
	if len(lines) < 2 {
		return false
	}

	total := 0
	for _, line := range lines {
		total += meaningfulRuneCount(line)
	}

	return total >= minWindowTotalRunes
}

func citationHasStandaloneSnippet(item citation.Citation) bool {
	snippet := normalizeEvidenceText(item.Snippet)
	if isLabelLikeText(snippet) {
		return false
	}

	return meaningfulRuneCount(snippet) >= minStandaloneSnippetRunes
}

func citationCouldBenefitFromFallback(item citation.Citation) bool {
	if len(normalizeEvidenceWindow(item.Window)) > 0 {
		return true
	}

	return meaningfulRuneCount(normalizeEvidenceText(item.Snippet)) >= minFallbackSnippetRunes
}

func normalizeEvidenceWindow(window []string) []string {
	seen := make(map[string]struct{})
	lines := make([]string, 0, len(window))
	for _, item := range window {
		normalized := normalizeEvidenceText(item)
		if normalized == "" {
			continue
		}
		if meaningfulRuneCount(normalized) < minWindowLineRunes {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		lines = append(lines, normalized)
	}

	return lines
}

func normalizeEvidenceText(text string) string {
	parts := strings.Fields(strings.TrimSpace(text))
	return strings.Join(parts, " ")
}

func meaningfulRuneCount(text string) int {
	count := 0
	for _, r := range normalizeEvidenceText(text) {
		switch {
		case unicode.IsSpace(r):
			continue
		case unicode.IsLetter(r), unicode.IsDigit(r):
			count++
		case unicode.In(r, unicode.Han):
			count++
		}
	}

	return count
}

func isLabelLikeText(text string) bool {
	normalized := normalizeEvidenceText(text)
	if normalized == "" {
		return true
	}
	if strings.HasSuffix(normalized, ":") || strings.HasSuffix(normalized, "：") {
		return true
	}

	return meaningfulRuneCount(normalized) < minStandaloneSnippetRunes
}
