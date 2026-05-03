package assistant

import (
	"errors"
	"strings"
)

var (
	// ErrTargetNodeNotFound 表示当前文件中不存在用户要求的稳定目标节点。
	ErrTargetNodeNotFound = errors.New("当前文件中未找到稳定目标节点")
)

// ResolvedNodeMode 表示 resolver 解析出的目标读取模式。
type ResolvedNodeMode string

const (
	// ResolvedNodeModeNode 表示解析到了单个 outline 节点。
	ResolvedNodeModeNode ResolvedNodeMode = "node"
	// ResolvedNodeModeList 表示解析到了某类节点的列表聚合视图。
	ResolvedNodeModeList ResolvedNodeMode = "list"
	// ResolvedNodeModeDocument 表示解析到了整份文档。
	ResolvedNodeModeDocument ResolvedNodeMode = "document"
)

// ResolveTargetInput 描述目标解析所需的稳定输入。
type ResolveTargetInput struct {
	Message  string
	Document *CurrentDocument
	Snapshot *SessionContextSnapshot
}

// ResolvedNode 表示消息在当前文件 outline 中稳定解析出的目标。
type ResolvedNode struct {
	Mode       ResolvedNodeMode
	NodeID     string
	SectionID  string
	NodeKind   OutlineNodeKind
	Title      string
	Reason     string
	Confidence float64
	EntityName string
}

// TargetResolver 负责把用户说法稳定映射到当前文件 outline 节点。
type TargetResolver struct{}

// NewTargetResolver 创建 node-aware 目标解析器。
func NewTargetResolver() *TargetResolver {
	return &TargetResolver{}
}

// Resolve 按显式实体 -> ordinal -> 指代承接 -> 列表聚合 -> 整文档的顺序解析当前目标。
func (r *TargetResolver) Resolve(input ResolveTargetInput) (*ResolvedNode, error) {
	message := strings.TrimSpace(input.Message)
	if message == "" || input.Document == nil || !input.Document.Ready {
		return nil, nil
	}

	nodes := cloneOutlineNodes(input.Document.Outline)
	if len(nodes) == 0 {
		return nil, nil
	}

	if resolved := resolveExplicitNode(message, nodes); resolved != nil {
		return resolved, nil
	}
	if resolved, err := resolveOrdinalNode(message, nodes); resolved != nil || err != nil {
		return resolved, err
	}
	if resolved := resolveAnaphoraNode(message, input.Document, input.Snapshot); resolved != nil {
		return resolved, nil
	}
	if resolved := resolveListNode(message, nodes); resolved != nil {
		return resolved, nil
	}
	if resolved := resolveDocumentNode(message, input.Document); resolved != nil {
		return resolved, nil
	}

	return nil, nil
}

func resolveExplicitNode(message string, nodes []OutlineNode) *ResolvedNode {
	for _, node := range nodes {
		if node.NodeKind == OutlineNodeDocument {
			continue
		}
		for _, token := range append([]string{node.Title}, node.Aliases...) {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if strings.Contains(message, token) {
				return resolvedNodeFromOutlineNode(node, ResolvedNodeModeNode, "explicit_entity", 0.95)
			}
		}
	}

	return nil
}

func resolveOrdinalNode(message string, nodes []OutlineNode) (*ResolvedNode, error) {
	ordinal := extractOrdinal(message)
	if ordinal == 0 {
		return nil, nil
	}

	targetKind := inferNodeKindForMessage(message)
	candidates := filterOutlineNodesForResolve(nodes, targetKind)
	if len(candidates) == 0 || ordinal > len(candidates) {
		return nil, ErrTargetNodeNotFound
	}

	return resolvedNodeFromOutlineNode(candidates[ordinal-1], ResolvedNodeModeNode, "ordinal_reference", 0.97), nil
}

func resolveAnaphoraNode(message string, document *CurrentDocument, snapshot *SessionContextSnapshot) *ResolvedNode {
	if snapshot == nil || !containsAnaphora(message) {
		return nil
	}

	if snapshot.ActiveNode != nil {
		node := findOutlineNodeByID(document.Outline, snapshot.ActiveNode.ID)
		if node == nil {
			node = findOutlineNodeBySectionID(document.Outline, snapshot.ActiveNode.ID)
		}
		if node != nil {
			return resolvedNodeFromOutlineNode(*node, ResolvedNodeModeNode, "anaphora", 0.9)
		}
	}

	if len(snapshot.NodeReferenceFrame) != 1 {
		return nil
	}

	node := findOutlineNodeByID(document.Outline, snapshot.NodeReferenceFrame[0].NodeID)
	if node == nil {
		return nil
	}

	return resolvedNodeFromOutlineNode(*node, ResolvedNodeModeNode, "anaphora", 0.88)
}

func resolveListNode(message string, nodes []OutlineNode) *ResolvedNode {
	if !referencesNodeList(message) {
		return nil
	}

	nodeKind := inferNodeKindForMessage(message)
	if nodeKind == "" {
		nodeKind = OutlineNodeHeadingSection
	}
	if len(filterOutlineNodesForResolve(nodes, nodeKind)) == 0 {
		return nil
	}

	return &ResolvedNode{
		Mode:       ResolvedNodeModeList,
		NodeKind:   nodeKind,
		Reason:     "list_reference",
		Confidence: 0.9,
	}
}

func resolveDocumentNode(message string, document *CurrentDocument) *ResolvedNode {
	if !referencesWholeDocument(message) {
		return nil
	}

	node := findOutlineDocumentNode(document.Outline)
	if node == nil && strings.TrimSpace(document.FullText) == "" {
		return nil
	}
	if node == nil {
		return &ResolvedNode{
			Mode:       ResolvedNodeModeDocument,
			NodeID:     documentOutlineNodeID(document.VersionID),
			NodeKind:   OutlineNodeDocument,
			Title:      "全文",
			Reason:     "document_fallback",
			Confidence: 1,
		}
	}

	return resolvedNodeFromOutlineNode(*node, ResolvedNodeModeDocument, "document_fallback", 1)
}

func resolvedNodeFromOutlineNode(node OutlineNode, mode ResolvedNodeMode, reason string, confidence float64) *ResolvedNode {
	return &ResolvedNode{
		Mode:       mode,
		NodeID:     node.NodeID,
		SectionID:  strings.TrimSpace(node.SectionID),
		NodeKind:   node.NodeKind,
		Title:      node.Title,
		Reason:     reason,
		Confidence: confidence,
		EntityName: node.Title,
	}
}

func inferNodeKindForMessage(message string) OutlineNodeKind {
	switch {
	case containsAny(message, []string{"项目", "project", "projects"}):
		return OutlineNodeProjectItem
	case containsAny(message, []string{"章节", "一节", "一段", "section"}):
		return OutlineNodeHeadingSection
	default:
		return ""
	}
}

func filterOutlineNodesForResolve(nodes []OutlineNode, kind OutlineNodeKind) []OutlineNode {
	filtered := make([]OutlineNode, 0, len(nodes))
	for _, node := range nodes {
		if node.NodeKind == OutlineNodeDocument {
			continue
		}
		if kind != "" && node.NodeKind != kind {
			continue
		}
		filtered = append(filtered, node)
	}

	return filtered
}

func referencesNodeList(message string) bool {
	return containsAny(message, []string{"有哪些", "列出", "都有什么", "有哪些项目", "哪些项目"})
}

func findOutlineNodeByID(nodes []OutlineNode, nodeID string) *OutlineNode {
	trimmedID := strings.TrimSpace(nodeID)
	if trimmedID == "" {
		return nil
	}
	for index := range nodes {
		if strings.TrimSpace(nodes[index].NodeID) == trimmedID {
			node := nodes[index]
			return &node
		}
	}

	return nil
}

func findOutlineDocumentNode(nodes []OutlineNode) *OutlineNode {
	for index := range nodes {
		if nodes[index].NodeKind == OutlineNodeDocument {
			node := nodes[index]
			return &node
		}
	}

	return nil
}

func findOutlineNodeBySectionID(nodes []OutlineNode, sectionID string) *OutlineNode {
	trimmedID := strings.TrimSpace(sectionID)
	if trimmedID == "" {
		return nil
	}
	for index := range nodes {
		if strings.TrimSpace(nodes[index].SectionID) == trimmedID {
			node := nodes[index]
			return &node
		}
	}

	return nil
}
