package assistant

import (
	"strings"

	"agent_project/apps/server/internal/knowledge/citation"
)

// EvidenceEvaluationInput 描述一轮 grounded 回答前的证据质量判断输入。
type EvidenceEvaluationInput struct {
	QueryIntent    string
	ResolvedTarget *ResolvedReference
	Citations      []citation.Citation
	CanonicalRead  *CanonicalReadResult
}

// EvaluateEvidenceQuality 只负责纯判断，不触发检索或修复动作。
func EvaluateEvidenceQuality(input EvidenceEvaluationInput) (bool, string) {
	if canonicalReadSatisfiesIntent(input) {
		return true, ""
	}

	if len(input.Citations) == 0 {
		return false, "missing_citations"
	}

	switch strings.TrimSpace(input.QueryIntent) {
	case "detail_by_ordinal", "detail_by_entity":
		if input.ResolvedTarget == nil || strings.TrimSpace(input.ResolvedTarget.SectionID) == "" {
			return false, "missing_resolved_section"
		}
		if !citationsContainSection(input.Citations, input.ResolvedTarget.SectionID) {
			return false, "missing_concrete_section"
		}
		if citationsAreHeadingOnly(input.Citations) {
			return false, "heading_only"
		}
	case "aggregate_attribute":
		if citationsAreHeadingOnly(input.Citations) {
			return false, "heading_only"
		}
	case "list_sections":
		if !citationsContainGroundedSection(input.Citations) {
			return false, "missing_grounded_sections"
		}
	default:
		if citationsAreHeadingOnly(input.Citations) {
			return false, "heading_only"
		}
	}

	return true, ""
}

// canonicalReadSatisfiesIntent 判断当前是否已经拿到足够稳定的 canonical 内容，可直接跳过 citation 级证据门控。
func canonicalReadSatisfiesIntent(input EvidenceEvaluationInput) bool {
	if input.CanonicalRead == nil {
		return false
	}

	switch strings.TrimSpace(input.QueryIntent) {
	case "list_sections":
		return input.CanonicalRead.Mode == CanonicalReadModeSectionList && len(input.CanonicalRead.Sections) > 0
	case "detail_by_ordinal", "detail_by_entity":
		if input.CanonicalRead.Mode != CanonicalReadModeSection || strings.TrimSpace(input.CanonicalRead.Content) == "" {
			return false
		}
		if input.ResolvedTarget == nil || strings.TrimSpace(input.ResolvedTarget.SectionID) == "" {
			return true
		}

		return strings.TrimSpace(input.CanonicalRead.SectionID) == strings.TrimSpace(input.ResolvedTarget.SectionID)
	default:
		return strings.TrimSpace(input.CanonicalRead.Content) != ""
	}
}

// citationsContainSection 判断引用列表里是否已经出现 section 级定位，避免证据门控误把标题级引用当作正文证据。
func citationsContainSection(citations []citation.Citation, sectionID string) bool {
	trimmedSectionID := strings.TrimSpace(sectionID)
	for _, item := range citations {
		if strings.TrimSpace(item.SectionID) == trimmedSectionID {
			return true
		}
	}

	return false
}

// citationsContainGroundedSection 判断引用列表里是否包含 grounded section 证据，供证据门控区分高质量引用。
func citationsContainGroundedSection(citations []citation.Citation) bool {
	for _, item := range citations {
		if strings.TrimSpace(item.SectionID) != "" {
			return true
		}
	}

	return false
}

// citationsAreHeadingOnly 判断引用是否全部停留在标题级命中，用于拦截缺少正文支撑的回答。
func citationsAreHeadingOnly(citations []citation.Citation) bool {
	for _, item := range citations {
		if !isHeadingOnlyCitation(item) {
			return false
		}
	}

	return true
}

// isHeadingOnlyCitation 判断单条引用是否只有标题命中而没有正文窗口。
func isHeadingOnlyCitation(item citation.Citation) bool {
	snippet := strings.TrimSpace(item.Snippet)
	sectionTitle := strings.TrimSpace(item.SectionTitle)
	if snippet == "" {
		return true
	}
	if snippet == sectionTitle {
		return true
	}
	if strings.HasSuffix(snippet, "：") || strings.HasSuffix(snippet, ":") {
		return true
	}

	compact := strings.ReplaceAll(snippet, " ", "")
	for _, marker := range []string{"项目描述：", "工作内容：", "职责描述：", "岗位职责：", "技术栈："} {
		if compact == marker {
			return true
		}
	}

	return false
}
