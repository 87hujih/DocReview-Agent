package assistant

import (
	"fmt"
	"sort"
	"strings"
)

// NodeReadMode 表示 node reader 返回的读取模式。
type NodeReadMode string

const (
	// NodeReadModeNode 表示返回单个节点内容。
	NodeReadModeNode NodeReadMode = "node"
	// NodeReadModeList 表示返回某类节点的列表视图。
	NodeReadModeList NodeReadMode = "list"
	// NodeReadModeDocument 表示返回整份文档。
	NodeReadModeDocument NodeReadMode = "document"
)

// NodeReadInput 描述 node reader 所需的输入。
type NodeReadInput struct {
	Document *CurrentDocument
	Resolved *ResolvedNode
}

// NodeReadItem 表示列表型读取结果里的单个节点。
type NodeReadItem struct {
	NodeID   string
	NodeKind OutlineNodeKind
	Title    string
	Ordinal  int
}

// NodeReadResult 表示 node reader 产出的稳定 canonical 读取结果。
type NodeReadResult struct {
	Mode     NodeReadMode
	NodeID   string
	NodeKind OutlineNodeKind
	Title    string
	Content  string
	Nodes    []NodeReadItem
}

// NodeReader 负责从 current document outline 中读取稳定 canonical 内容。
type NodeReader struct{}

// NewNodeReader 创建 node-aware canonical 读取器。
func NewNodeReader() *NodeReader {
	return &NodeReader{}
}

// Read 按 resolved node 返回单节点、列表或整文档视图。
func (r *NodeReader) Read(input NodeReadInput) (*NodeReadResult, error) {
	if input.Document == nil || input.Resolved == nil {
		return nil, nil
	}

	switch input.Resolved.Mode {
	case ResolvedNodeModeNode:
		return r.readNode(input)
	case ResolvedNodeModeList:
		return r.readNodeList(input)
	case ResolvedNodeModeDocument:
		return r.readDocument(input)
	default:
		return nil, nil
	}
}

func (r *NodeReader) readNode(input NodeReadInput) (*NodeReadResult, error) {
	node := findOutlineNodeByID(input.Document.Outline, input.Resolved.NodeID)
	if node == nil {
		return nil, fmt.Errorf("read node: %w", ErrCanonicalReadUnavailable)
	}

	content := strings.TrimSpace(node.CanonicalContent)
	if node.NodeKind == OutlineNodeDocument && content == "" {
		content = strings.TrimSpace(input.Document.FullText)
	}
	if content == "" {
		return nil, fmt.Errorf("read node: %w", ErrCanonicalReadUnavailable)
	}

	return &NodeReadResult{
		Mode:     NodeReadModeNode,
		NodeID:   node.NodeID,
		NodeKind: node.NodeKind,
		Title:    node.Title,
		Content:  content,
	}, nil
}

func (r *NodeReader) readNodeList(input NodeReadInput) (*NodeReadResult, error) {
	candidates := filterOutlineNodesForResolve(input.Document.Outline, input.Resolved.NodeKind)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("read node list: %w", ErrCanonicalReadUnavailable)
	}

	sort.SliceStable(candidates, func(left int, right int) bool {
		if candidates[left].Ordinal == candidates[right].Ordinal {
			return candidates[left].Title < candidates[right].Title
		}
		if candidates[left].Ordinal == 0 {
			return false
		}
		if candidates[right].Ordinal == 0 {
			return true
		}
		return candidates[left].Ordinal < candidates[right].Ordinal
	})

	items := make([]NodeReadItem, 0, len(candidates))
	for _, node := range candidates {
		items = append(items, NodeReadItem{
			NodeID:   node.NodeID,
			NodeKind: node.NodeKind,
			Title:    node.Title,
			Ordinal:  node.Ordinal,
		})
	}

	return &NodeReadResult{
		Mode:     NodeReadModeList,
		NodeKind: input.Resolved.NodeKind,
		Nodes:    items,
	}, nil
}

func (r *NodeReader) readDocument(input NodeReadInput) (*NodeReadResult, error) {
	content := strings.TrimSpace(input.Document.FullText)
	if content == "" {
		documentNode := findOutlineDocumentNode(input.Document.Outline)
		if documentNode != nil {
			content = strings.TrimSpace(documentNode.CanonicalContent)
		}
	}
	if content == "" {
		return nil, fmt.Errorf("read document: %w", ErrCanonicalReadUnavailable)
	}

	return &NodeReadResult{
		Mode:     NodeReadModeDocument,
		NodeID:   documentOutlineNodeID(input.Document.VersionID),
		NodeKind: OutlineNodeDocument,
		Title:    "全文",
		Content:  content,
	}, nil
}
