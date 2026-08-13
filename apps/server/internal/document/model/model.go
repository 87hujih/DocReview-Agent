// Package 模型 defines the 格式-neutral Canonical 文档 AST.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const SchemaVersion = "1.0"

type NodeType string

const (
	NodeDocument  NodeType = "document"
	NodeHeading   NodeType = "heading"
	NodeParagraph NodeType = "paragraph"
	NodePage      NodeType = "page"
	NodeList      NodeType = "list"
	NodeListItem  NodeType = "list_item"
	NodeTable     NodeType = "table"
)

type SourceLocation struct {
	FileName    string `json:"file_name"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
}

type PageMapping struct {
	Page        int `json:"page"`
	StartOffset int `json:"start_offset"`
	EndOffset   int `json:"end_offset"`
}

type Node struct {
	NodeID         string         `json:"node_id"`
	Type           NodeType       `json:"type"`
	Attributes     map[string]any `json:"attributes"`
	Content        string         `json:"content"`
	Children       []*Node        `json:"children"`
	SourceLocation SourceLocation `json:"source_location"`
	PageMapping    []PageMapping  `json:"page_mapping"`
	Metadata       map[string]any `json:"metadata"`
	ContentHash    string         `json:"content_hash"`
}

type Document struct {
	DocumentID    string         `json:"document_id"`
	VersionID     string         `json:"version_id"`
	Root          *Node          `json:"root"`
	SourceFormat  string         `json:"source_format"`
	Metadata      map[string]any `json:"metadata"`
	ContentHash   string         `json:"content_hash"`
	SchemaVersion string         `json:"schema_version"`
}

// StableNodeID derives 标识 来自 the 资源 和 structural 来源 path,
// never 来自 editable text. Re-importing the same 来源 structure 为 stable.
func StableNodeID(documentID, structuralPath string, nodeType NodeType) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(documentID) + "\x00" + structuralPath + "\x00" + string(nodeType)))
	return "node_" + hex.EncodeToString(digest[:16])
}

// HashNode 执行该函数负责的核心处理逻辑。
func HashNode(node *Node) (string, error) {
	if node == nil {
		return "", fmt.Errorf("节点不能为空")
	}
	type hashShape struct {
		Type           NodeType       `json:"type"`
		Attributes     map[string]any `json:"attributes"`
		Content        string         `json:"content"`
		ChildIDs       []string       `json:"child_ids"`
		SourceLocation SourceLocation `json:"source_location"`
		PageMapping    []PageMapping  `json:"page_mapping"`
		Metadata       map[string]any `json:"metadata"`
	}
	childIDs := make([]string, 0, len(node.Children))
	for _, child := range node.Children {
		if child == nil {
			return "", fmt.Errorf("处理失败：节点 %s 具有一个 nil child", node.NodeID)
		}
		childIDs = append(childIDs, child.NodeID)
	}
	payload, err := json.Marshal(hashShape{
		Type: node.Type, Attributes: nonNilMap(node.Attributes), Content: node.Content,
		ChildIDs: childIDs, SourceLocation: node.SourceLocation,
		PageMapping: nonNilPages(node.PageMapping), Metadata: nonNilMap(node.Metadata),
	})
	if err != nil {
		return "", fmt.Errorf("处理失败：哈希节点 %s：%w", node.NodeID, err)
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// Rehash updates every 节点 哈希 和 the 文档 哈希 以确定性方式.
func Rehash(document *Document) error {
	if document == nil || document.Root == nil {
		return fmt.Errorf("文档和 root 不能为空")
	}
	seen := make(map[*Node]struct{})
	var visit func(*Node) error
	visit = func(node *Node) error {
		if node == nil {
			return fmt.Errorf("处理失败：nil 节点")
		}
		if _, exists := seen[node]; exists {
			return fmt.Errorf("cycle 或 multiply-parented 节点 %s", node.NodeID)
		}
		seen[node] = struct{}{}
		for _, child := range node.Children {
			if child == nil {
				return fmt.Errorf("处理失败：nil child")
			}
			if err := visit(child); err != nil {
				return err
			}
		}
		hash, err := HashNode(node)
		if err != nil {
			return err
		}
		node.ContentHash = hash
		return nil
	}
	if err := visit(document.Root); err != nil {
		return err
	}
	clone := *document
	clone.ContentHash = ""
	payload, err := json.Marshal(clone)
	if err != nil {
		return fmt.Errorf("哈希文档：%w", err)
	}
	digest := sha256.Sum256(payload)
	document.ContentHash = "sha256:" + hex.EncodeToString(digest[:])
	return nil
}

// Validate 校验输入及领域约束。
func Validate(document *Document) error {
	if document == nil || strings.TrimSpace(document.DocumentID) == "" || strings.TrimSpace(document.VersionID) == "" {
		return fmt.Errorf("文档_id 和 version_id 不能为空")
	}
	if document.SchemaVersion != SchemaVersion || document.Root == nil || document.Root.Type != NodeDocument {
		return fmt.Errorf("canonical 模式版本和文档 root 不能为空")
	}
	if err := validateJSONValue(document.Metadata, "document metadata"); err != nil {
		return err
	}
	seenIDs := make(map[string]struct{})
	seenPointers := make(map[*Node]struct{})
	sourceFile := strings.TrimSpace(document.Root.SourceLocation.FileName)
	sourceEnd := document.Root.SourceLocation.EndOffset
	if sourceFile == "" {
		return fmt.Errorf("文档来源 file 不能为空")
	}
	var walk func(*Node) error
	walk = func(node *Node) error {
		if node == nil || strings.TrimSpace(node.NodeID) == "" || strings.TrimSpace(string(node.Type)) == "" {
			return fmt.Errorf("every 节点需要 node_id 和类型")
		}
		if _, exists := seenPointers[node]; exists {
			return fmt.Errorf("cycle 或 multiply-parented 节点 %s", node.NodeID)
		}
		seenPointers[node] = struct{}{}
		if _, exists := seenIDs[node.NodeID]; exists {
			return fmt.Errorf("重复的 node_id %s", node.NodeID)
		}
		seenIDs[node.NodeID] = struct{}{}
		if strings.TrimSpace(node.SourceLocation.FileName) != sourceFile || node.SourceLocation.StartOffset < 0 || node.SourceLocation.EndOffset < node.SourceLocation.StartOffset ||
			node.SourceLocation.EndOffset > sourceEnd ||
			node.SourceLocation.StartLine < 0 || node.SourceLocation.EndLine < 0 || (node.SourceLocation.EndLine > 0 && node.SourceLocation.EndLine < node.SourceLocation.StartLine) {
			return fmt.Errorf("节点 %s 具有无效的来源 location", node.NodeID)
		}
		for _, mapping := range node.PageMapping {
			if mapping.Page < 1 || mapping.StartOffset < node.SourceLocation.StartOffset || mapping.EndOffset < mapping.StartOffset || mapping.EndOffset > node.SourceLocation.EndOffset {
				return fmt.Errorf("节点 %s 具有无效的 page mapping", node.NodeID)
			}
		}
		if err := validateJSONValue(node.Attributes, "node attributes"); err != nil {
			return err
		}
		if err := validateJSONValue(node.Metadata, "node metadata"); err != nil {
			return err
		}
		expected, err := HashNode(node)
		if err != nil {
			return err
		}
		if node.ContentHash != expected {
			return fmt.Errorf("处理失败：节点 %s content_hash 为 stale", node.NodeID)
		}
		for _, child := range node.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(document.Root); err != nil {
		return err
	}
	existingHash := document.ContentHash
	clone, err := Clone(document)
	if err != nil {
		return err
	}
	if err := Rehash(clone); err != nil {
		return err
	}
	if existingHash == "" || existingHash != clone.ContentHash {
		return fmt.Errorf("文档 content_hash 为 stale")
	}
	return nil
}

// Clone 执行该函数负责的核心处理逻辑。
func Clone(document *Document) (*Document, error) {
	if document == nil {
		return nil, fmt.Errorf("文档不能为空")
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var clone Document
	if err := json.Unmarshal(payload, &clone); err != nil {
		return nil, err
	}
	return &clone, nil
}

// Flatten 执行该函数负责的核心处理逻辑。
func Flatten(root *Node) []*Node {
	result := make([]*Node, 0)
	seen := make(map[*Node]struct{})
	var walk func(*Node)
	walk = func(node *Node) {
		if node == nil {
			return
		}
		if _, exists := seen[node]; exists {
			return
		}
		seen[node] = struct{}{}
		result = append(result, node)
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return result
}

// validateJSONValue 校验输入及领域约束。
func validateJSONValue(value any, name string) error {
	var check func(any) error
	check = func(candidate any) error {
		// 根据当前状态或类型选择对应的处理分支。
		switch typed := candidate.(type) {
		case nil, string, bool, json.Number:
			return nil
		case float64:
			if math.IsInf(typed, 0) || math.IsNaN(typed) {
				return fmt.Errorf("处理失败：%s 包含一个 non-finite number", name)
			}
			return nil
		case float32:
			if math.IsInf(float64(typed), 0) || math.IsNaN(float64(typed)) {
				return fmt.Errorf("处理失败：%s 包含一个 non-finite number", name)
			}
			return nil
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return nil
		case []any:
			for _, item := range typed {
				if err := check(item); err != nil {
					return err
				}
			}
			return nil
		case map[string]any:
			for key, item := range typed {
				if strings.TrimSpace(key) == "" {
					return fmt.Errorf("%s 包含一个空键", name)
				}
				if err := check(item); err != nil {
					return err
				}
			}
			return nil
		default:
			return fmt.Errorf("%s 包含不支持的值 %T", name, candidate)
		}
	}
	return check(value)
}

// nonNilMap 执行该函数负责的核心处理逻辑。
func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

// nonNilPages 执行该函数负责的核心处理逻辑。
func nonNilPages(value []PageMapping) []PageMapping {
	if value == nil {
		return []PageMapping{}
	}
	return value
}
