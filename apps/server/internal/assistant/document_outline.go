package assistant

import "strings"

// OutlineNodeKind 表示当前文件 outline 里可被稳定引用的节点类型。
type OutlineNodeKind string

const (
	// OutlineNodeDocument 表示整份文档。
	OutlineNodeDocument OutlineNodeKind = "document"
	// OutlineNodeHeadingSection 表示普通 heading section。
	OutlineNodeHeadingSection OutlineNodeKind = "heading_section"
	// OutlineNodeProjectItem 表示用户可按“第几个项目”稳定引用的项目节点。
	OutlineNodeProjectItem OutlineNodeKind = "project_item"
	// OutlineNodeListItem 预留给后续列表项寻址。
	OutlineNodeListItem OutlineNodeKind = "list_item"
)

// OutlineNodeSource 表示 outline 节点的构建来源。
type OutlineNodeSource string

const (
	// OutlineSourceSemantic 表示节点来自语义归一化 section。
	OutlineSourceSemantic OutlineNodeSource = "normalized_semantic"
	// OutlineSourceHeadingStructure 表示节点来自 heading 结构。
	OutlineSourceHeadingStructure OutlineNodeSource = "heading_structure"
	// OutlineSourceHybrid 表示节点由语义 section 和 heading 结构合并而成。
	OutlineSourceHybrid OutlineNodeSource = "hybrid"
)

// OutlineNode 表示当前文件里一个稳定可寻址的逻辑节点。
type OutlineNode struct {
	NodeID           string
	NodeKind         OutlineNodeKind
	Title            string
	Ordinal          int
	Aliases          []string
	ParentNodeID     string
	SectionID        string
	CanonicalContent string
	Confidence       float64
	Source           OutlineNodeSource
}

func cloneOutlineNodes(nodes []OutlineNode) []OutlineNode {
	if len(nodes) == 0 {
		return nil
	}

	cloned := make([]OutlineNode, 0, len(nodes))
	for _, node := range nodes {
		copied := node
		copied.NodeID = strings.TrimSpace(node.NodeID)
		copied.Title = strings.TrimSpace(node.Title)
		copied.ParentNodeID = strings.TrimSpace(node.ParentNodeID)
		copied.SectionID = strings.TrimSpace(node.SectionID)
		copied.CanonicalContent = strings.TrimSpace(node.CanonicalContent)
		if len(node.Aliases) > 0 {
			copied.Aliases = append([]string(nil), node.Aliases...)
		}
		cloned = append(cloned, copied)
	}

	return cloned
}
