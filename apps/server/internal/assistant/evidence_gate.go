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
}

// EvaluateEvidenceQuality 只负责纯判断，不触发检索或修复动作。
func EvaluateEvidenceQuality(input EvidenceEvaluationInput) (bool, string) {
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

func citationsContainSection(citations []citation.Citation, sectionID string) bool {
	trimmedSectionID := strings.TrimSpace(sectionID)
	for _, item := range citations {
		if strings.TrimSpace(item.SectionID) == trimmedSectionID {
			return true
		}
	}

	return false
}

func citationsContainGroundedSection(citations []citation.Citation) bool {
	for _, item := range citations {
		if strings.TrimSpace(item.SectionID) != "" {
			return true
		}
	}

	return false
}

func citationsAreHeadingOnly(citations []citation.Citation) bool {
	for _, item := range citations {
		if !isHeadingOnlyCitation(item) {
			return false
		}
	}

	return true
}

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
