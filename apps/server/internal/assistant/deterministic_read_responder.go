package assistant

import (
	"context"
	"fmt"
	"strings"
)

// DeterministicReadInput 归拢 deterministic read 所需的输入字段。
type DeterministicReadInput struct {
	Message        string
	Intent         ReadIntent
	Resource       *resourceContext
	Located        *LocatedSection
	ResolvedNode   *ResolvedNode
	NodeReadResult *NodeReadResult
	ReadResult     *CanonicalReadResult
}

// DeterministicReadResult 承载 deterministic read 返回给会话层的最终展示文本。
type DeterministicReadResult struct {
	Content string
}

// DeterministicReadResponder 负责把 canonical read 结果转换成最终可展示文本。
type DeterministicReadResponder struct{}

// NewDeterministicReadResponder 构造 deterministic read responder。
func NewDeterministicReadResponder() *DeterministicReadResponder {
	return &DeterministicReadResponder{}
}

// Respond 根据 canonical read 结果生成最终展示文本。
func (r *DeterministicReadResponder) Respond(_ context.Context, input DeterministicReadInput) (*DeterministicReadResult, error) {
	if input.NodeReadResult != nil {
		return respondDeterministicNodeRead(input.NodeReadResult), nil
	}
	if input.ReadResult == nil {
		return nil, nil
	}

	switch input.ReadResult.Mode {
	case CanonicalReadModeSection, CanonicalReadModeDocument:
		content := strings.TrimSpace(input.ReadResult.Content)
		if content == "" {
			return nil, nil
		}

		return &DeterministicReadResult{Content: content}, nil
	case CanonicalReadModeSectionList:
		if len(input.ReadResult.Sections) == 0 {
			return nil, nil
		}

		lines := make([]string, 0, len(input.ReadResult.Sections))
		for index, section := range input.ReadResult.Sections {
			title := strings.TrimSpace(section.SectionTitle)
			if title == "" {
				title = fmt.Sprintf("第%d项", index+1)
			}
			lines = append(lines, fmt.Sprintf("%d. %s", index+1, title))
		}

		return &DeterministicReadResult{Content: strings.Join(lines, "\n")}, nil
	default:
		return nil, nil
	}
}

func respondDeterministicNodeRead(result *NodeReadResult) *DeterministicReadResult {
	if result == nil {
		return nil
	}

	switch result.Mode {
	case NodeReadModeNode, NodeReadModeDocument:
		content := strings.TrimSpace(result.Content)
		if content == "" {
			return nil
		}

		return &DeterministicReadResult{Content: content}
	case NodeReadModeList:
		if len(result.Nodes) == 0 {
			return nil
		}

		lines := make([]string, 0, len(result.Nodes))
		for index, node := range result.Nodes {
			title := strings.TrimSpace(node.Title)
			if title == "" {
				title = fmt.Sprintf("第%d项", index+1)
			}
			lines = append(lines, fmt.Sprintf("%d. %s", index+1, title))
		}

		return &DeterministicReadResult{Content: strings.Join(lines, "\n")}
	default:
		return nil
	}
}
