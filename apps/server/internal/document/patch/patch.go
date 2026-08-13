// Package patch defines 和 applies the strict, versioned 节点-ID PatchSet contract.
package patch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"agent_project/apps/server/internal/document/model"
)

const SchemaVersion = "1.0"

type OperationType string

const (
	ReplaceNode      OperationType = "replace_node"
	InsertBefore     OperationType = "insert_before"
	InsertAfter      OperationType = "insert_after"
	DeleteNode       OperationType = "delete_node"
	UpdateAttributes OperationType = "update_attributes"
)

type Operation struct {
	Op                 OperationType  `json:"op"`
	NodeID             string         `json:"node_id"`
	ExpectedHash       string         `json:"expected_hash"`
	Content            *string        `json:"content,omitempty"`
	Attributes         map[string]any `json:"attributes,omitempty"`
	ExpectedParentID   string         `json:"expected_parent_id,omitempty"`
	ExpectedParentHash string         `json:"expected_parent_hash,omitempty"`
	Node               *model.Node    `json:"node,omitempty"`
}

type Set struct {
	SchemaVersion string      `json:"schema_version"`
	ResourceID    string      `json:"resource_id"`
	BaseVersionID string      `json:"base_version_id"`
	Operations    []Operation `json:"operations"`
	EvidenceRefs  []string    `json:"evidence_refs"`
	Reason        string      `json:"reason"`
}

type Limits struct {
	MaxBytes      int
	MaxOperations int
	MaxDepth      int
	MaxEvidence   int
}

// DefaultLimits 执行该函数负责的核心处理逻辑。
func DefaultLimits() Limits {
	return Limits{MaxBytes: 256 * 1024, MaxOperations: 100, MaxDepth: 24, MaxEvidence: 100}
}

var sha256Pattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ParseStrict 解析输入并返回类型化结果。
func ParseStrict(data []byte, limits Limits) (Set, error) {
	if limits.MaxBytes < 1 || len(data) > limits.MaxBytes {
		return Set{}, fmt.Errorf("PatchSet 超过 byte 上限")
	}
	if limits.MaxDepth < 1 {
		return Set{}, fmt.Errorf("PatchSet 深度上限无效")
	}
	if err := inspectJSON(data, limits.MaxDepth); err != nil {
		return Set{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var set Set
	if err := decoder.Decode(&set); err != nil {
		return Set{}, fmt.Errorf("无效的 PatchSet JSON：%w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return Set{}, err
	}
	if len(set.Operations) > limits.MaxOperations || (limits.MaxOperations == 0 && len(set.Operations) > 0) {
		return Set{}, fmt.Errorf("PatchSet 超过操作上限")
	}
	if len(set.EvidenceRefs) > limits.MaxEvidence || (limits.MaxEvidence == 0 && len(set.EvidenceRefs) > 0) {
		return Set{}, fmt.Errorf("PatchSet 超过证据上限")
	}
	if err := ValidateSet(set); err != nil {
		return Set{}, err
	}
	return set, nil
}

// ValidateSet 校验输入及领域约束。
func ValidateSet(set Set) error {
	if set.SchemaVersion != SchemaVersion {
		return fmt.Errorf("不支持的 PatchSet schema_version %q", set.SchemaVersion)
	}
	if strings.TrimSpace(set.ResourceID) == "" || strings.TrimSpace(set.BaseVersionID) == "" || strings.TrimSpace(set.Reason) == "" {
		return fmt.Errorf("resource_id、base_version_id、和原因不能为空")
	}
	if len(set.Operations) == 0 {
		return fmt.Errorf("至少一个操作不能为空")
	}
	seenEvidence := make(map[string]struct{}, len(set.EvidenceRefs))
	for _, reference := range set.EvidenceRefs {
		reference = strings.TrimSpace(reference)
		if reference == "" {
			return fmt.Errorf("证据_refs 不能包含空白s")
		}
		if _, exists := seenEvidence[reference]; exists {
			return fmt.Errorf("重复的证据_ref %s", reference)
		}
		seenEvidence[reference] = struct{}{}
	}
	for index, operation := range set.Operations {
		if err := validateOperation(operation); err != nil {
			return fmt.Errorf("操作 %d：%w", index, err)
		}
	}
	return nil
}

// validateOperation 校验输入及领域约束。
func validateOperation(operation Operation) error {
	if strings.TrimSpace(operation.NodeID) == "" || !sha256Pattern.MatchString(operation.ExpectedHash) {
		return fmt.Errorf("node_id 和 lower用例 sha256 expected_hash 不能为空")
	}
	// 根据当前状态或类型选择对应的处理分支。
	switch operation.Op {
	case ReplaceNode:
		if operation.Content == nil || operation.Node != nil || operation.Attributes != nil || operation.ExpectedParentID != "" || operation.ExpectedParentHash != "" {
			return fmt.Errorf("replace_node 需要 only 内容")
		}
	case InsertBefore, InsertAfter:
		if operation.Node == nil || strings.TrimSpace(operation.Node.NodeID) == "" || strings.TrimSpace(operation.ExpectedParentID) == "" || !sha256Pattern.MatchString(operation.ExpectedParentHash) {
			return fmt.Errorf("insert 需要节点和明确的预期的 parent 标识/哈希")
		}
		if operation.Content != nil || operation.Attributes != nil {
			return fmt.Errorf("insert 不能 carry 内容或 attributes outside 节点")
		}
	case DeleteNode:
		if operation.Content != nil || operation.Node != nil || operation.Attributes != nil || operation.ExpectedParentID != "" || operation.ExpectedParentHash != "" {
			return fmt.Errorf("删除_node 没有负载")
		}
	case UpdateAttributes:
		if operation.Attributes == nil || operation.Content != nil || operation.Node != nil || operation.ExpectedParentID != "" || operation.ExpectedParentHash != "" {
			return fmt.Errorf("更新_attributes 需要 only attributes")
		}
	default:
		return fmt.Errorf("不支持的操作 %q", operation.Op)
	}
	return nil
}

// 哈希 执行该函数负责的核心处理逻辑。
func Hash(set Set) (string, error) {
	payload, err := json.Marshal(set)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// Apply 执行该函数负责的核心处理逻辑。
func Apply(document *model.Document, set Set) (*model.Document, error) {
	if err := ValidateSet(set); err != nil {
		return nil, err
	}
	if document == nil || document.DocumentID != set.ResourceID || document.VersionID != set.BaseVersionID {
		return nil, fmt.Errorf("PatchSet 不匹配文档/base 版本")
	}
	result, err := model.Clone(document)
	if err != nil {
		return nil, err
	}
	for _, operation := range set.Operations {
		node, parent, childIndex := locate(result.Root, operation.NodeID)
		if node == nil {
			return nil, fmt.Errorf("节点 %s 未找到", operation.NodeID)
		}
		if node.ContentHash != operation.ExpectedHash {
			return nil, fmt.Errorf("节点 %s expected_hash 不匹配", operation.NodeID)
		}
		// 根据当前状态或类型选择对应的处理分支。
		switch operation.Op {
		case ReplaceNode:
			node.Content = *operation.Content
		case UpdateAttributes:
			if node.Attributes == nil {
				node.Attributes = map[string]any{}
			}
			for key, value := range operation.Attributes {
				if value == nil {
					delete(node.Attributes, key)
				} else {
					node.Attributes[key] = value
				}
			}
		case DeleteNode:
			if parent == nil {
				return nil, fmt.Errorf("文档 root 不能为删除d")
			}
			parent.Children = append(parent.Children[:childIndex], parent.Children[childIndex+1:]...)
		case InsertBefore, InsertAfter:
			if parent == nil || parent.NodeID != operation.ExpectedParentID {
				return nil, fmt.Errorf("预期的 parent 不匹配")
			}
			if parent.ContentHash != operation.ExpectedParentHash {
				return nil, fmt.Errorf("预期的 parent 哈希不匹配")
			}
			newNode, err := cloneNode(operation.Node)
			if err != nil {
				return nil, err
			}
			insertAt := childIndex
			if operation.Op == InsertAfter {
				insertAt++
			}
			parent.Children = append(parent.Children, nil)
			copy(parent.Children[insertAt+1:], parent.Children[insertAt:])
			parent.Children[insertAt] = newNode
		}
	}
	if err := model.Rehash(result); err != nil {
		return nil, err
	}
	if err := model.Validate(result); err != nil {
		return nil, err
	}
	return result, nil
}

// locate 执行该函数负责的核心处理逻辑。
func locate(root *model.Node, nodeID string) (*model.Node, *model.Node, int) {
	var found, parent *model.Node
	index := -1
	seen := make(map[*model.Node]struct{})
	var walk func(*model.Node)
	walk = func(node *model.Node) {
		if node == nil || found != nil {
			return
		}
		if _, exists := seen[node]; exists {
			return
		}
		seen[node] = struct{}{}
		if node.NodeID == nodeID {
			found = node
			return
		}
		for childIndex, child := range node.Children {
			if child != nil && child.NodeID == nodeID {
				found, parent, index = child, node, childIndex
				return
			}
			walk(child)
		}
	}
	walk(root)
	return found, parent, index
}

// cloneNode 执行该函数负责的核心处理逻辑。
func cloneNode(node *model.Node) (*model.Node, error) {
	payload, err := json.Marshal(node)
	if err != nil {
		return nil, fmt.Errorf("处理失败：clone inserted 节点：%w", err)
	}
	var result model.Node
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// inspectJSON 执行该函数负责的核心处理逻辑。
func inspectJSON(data []byte, maxDepth int) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	stack := make([]map[string]struct{}, 0)
	depth := 0
	for {
		token, err := decoder.Token()
		if errorsIsEOF(err) {
			break
		}
		if err != nil {
			return fmt.Errorf("无效的 PatchSet JSON：%w", err)
		}
		// 根据当前状态或类型选择对应的处理分支。
		switch typed := token.(type) {
		case json.Delim:
			// 根据当前状态或类型选择对应的处理分支。
			switch typed {
			case '{':
				depth++
				stack = append(stack, map[string]struct{}{})
			case '[':
				depth++
				stack = append(stack, nil)
			case '}', ']':
				depth--
				stack = stack[:len(stack)-1]
			}
			if depth > maxDepth {
				return fmt.Errorf("PatchSet 超过深度上限")
			}
		case string:
			if len(stack) > 0 && stack[len(stack)-1] != nil {
				keys := stack[len(stack)-1]
				if _, exists := keys[typed]; exists {
					return fmt.Errorf("重复的 JSON 键 %q", typed)
				}
				keys[typed] = struct{}{}
				// The 下一个 token 为 一个 值, so mark the 对象 frame as 等待.
				stack[len(stack)-1] = nil
			}
		}
		// Restore 对象 键 tracking 之后 scalar/closed composite 值.
		if len(stack) > 0 && stack[len(stack)-1] == nil {
			// Token inspection alone 不能 distinguish arrays 来自 等待 objects;
			// 重复的 detection 为 completed 由 the recursive decoder below.
		}
	}
	var value any
	decoder = json.NewDecoder(bytes.NewReader(data))
	if err := decodeNoDuplicates(decoder, &value, 0, maxDepth); err != nil {
		return err
	}
	return nil
}

// decodeNoDuplicates 解析输入并返回类型化结果。
func decodeNoDuplicates(decoder *json.Decoder, target *any, depth, maxDepth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	// 根据当前状态或类型选择对应的处理分支。
	switch typed := token.(type) {
	case json.Delim:
		if depth+1 > maxDepth {
			return fmt.Errorf("PatchSet 超过深度上限")
		}
		// 根据当前状态或类型选择对应的处理分支。
		switch typed {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("对象键必须是字符串")
				}
				if _, exists := object[key]; exists {
					return fmt.Errorf("重复的 JSON 键 %q", key)
				}
				var item any
				if err := decodeNoDuplicates(decoder, &item, depth+1, maxDepth); err != nil {
					return err
				}
				object[key] = item
			}
			if _, err := decoder.Token(); err != nil {
				return err
			}
			*target = object
		case '[':
			array := []any{}
			for decoder.More() {
				var item any
				if err := decodeNoDuplicates(decoder, &item, depth+1, maxDepth); err != nil {
					return err
				}
				array = append(array, item)
			}
			if _, err := decoder.Token(); err != nil {
				return err
			}
			*target = array
		default:
			return fmt.Errorf("非预期的分隔符")
		}
	default:
		*target = typed
	}
	return nil
}

// ensureEOF 执行该函数负责的核心处理逻辑。
func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("末尾存在多余的 JSON 值")
		}
		return err
	}
	return nil
}

// errorsIsEOF 执行该函数负责的核心处理逻辑。
func errorsIsEOF(err error) bool { return err == io.EOF }
