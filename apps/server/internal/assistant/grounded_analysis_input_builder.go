package assistant

import (
	"fmt"
	"strings"
)

// GroundedAnalysisInput 描述基于 canonical read 构造分析上下文所需的输入。
type GroundedAnalysisInput struct {
	Message      string
	ResolvedNode *ResolvedNode
	ReadResult   CanonicalReadResult
}

// GroundedAnalysisBuildResult 承载分析链可直接消费的 canonical 上下文。
type GroundedAnalysisBuildResult struct {
	AnalysisContext string
}

// BuildGroundedAnalysisInput 把 canonical read 结果转换成分析型请求可直接消费的上下文文本。
func BuildGroundedAnalysisInput(input GroundedAnalysisInput) (*GroundedAnalysisBuildResult, error) {
	switch input.ReadResult.Mode {
	case CanonicalReadModeSection:
		content := strings.TrimSpace(input.ReadResult.Content)
		if content == "" {
			return nil, ErrCanonicalReadUnavailable
		}

		targetLabel := "section"
		idLabel := "section_id"
		kindLabel := "section_type"
		if input.ResolvedNode != nil {
			targetLabel = "node"
			idLabel = "node_id"
			kindLabel = "node_kind"
		}
		lines := []string{
			fmt.Sprintf("当前文件已直接读取到目标 %s：title=%s；%s=%s；%s=%s。",
				targetLabel,
				strings.TrimSpace(input.ReadResult.SectionTitle),
				idLabel,
				strings.TrimSpace(input.ReadResult.SectionID),
				kindLabel,
				strings.TrimSpace(input.ReadResult.SectionType),
			),
			"以下是 canonical 正文：",
			content,
		}
		if message := strings.TrimSpace(input.Message); message != "" {
			lines = append(lines, "用户当前问题："+message)
		}

		return &GroundedAnalysisBuildResult{
			AnalysisContext: strings.Join(lines, "\n"),
		}, nil
	case CanonicalReadModeDocument:
		content := strings.TrimSpace(input.ReadResult.Content)
		if content == "" {
			return nil, ErrCanonicalReadUnavailable
		}

		lines := []string{
			"当前文件缺少稳定 section，但已直接读取到当前版本全文。",
			"以下是 canonical 正文：",
			content,
		}
		if message := strings.TrimSpace(input.Message); message != "" {
			lines = append(lines, "用户当前问题："+message)
		}

		return &GroundedAnalysisBuildResult{
			AnalysisContext: strings.Join(lines, "\n"),
		}, nil
	default:
		return nil, ErrCanonicalReadUnavailable
	}
}
