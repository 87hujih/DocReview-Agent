// Package validation 以确定性方式 validates PatchSets against 可信的 状态.
package validation

import (
	"context"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/document/model"
	"agent_project/apps/server/internal/document/patch"
)

type ErrorCategory string

const (
	InvalidPatch        ErrorCategory = "invalid_patch"
	VersionConflict     ErrorCategory = "version_conflict"
	HashConflict        ErrorCategory = "hash_conflict"
	UnauthorizedNode    ErrorCategory = "unauthorized_node"
	ReferenceMissing    ErrorCategory = "reference_missing"
	StructuralConflict  ErrorCategory = "structural_conflict"
	ResourceScopeDenied ErrorCategory = "resource_scope_denied"
	PolicyBlocked       ErrorCategory = "policy_blocked"
)

type Violation struct {
	Category       ErrorCategory `json:"category"`
	OperationIndex *int          `json:"operation_index,omitempty"`
	NodeID         string        `json:"node_id,omitempty"`
	Message        string        `json:"message"`
}

type Result struct {
	Valid    bool            `json:"valid"`
	Errors   []Violation     `json:"errors"`
	Document *model.Document `json:"-"`
}

type Snapshot struct {
	WorkspaceID       string
	ResourceID        string
	CurrentVersionID  string
	Document          *model.Document
	AuthorizedNodeIDs map[string]struct{}
	EvidenceRefs      map[string]struct{}
}

type Request struct {
	WorkspaceID         string
	ResourceID          string
	Patch               patch.Set
	Snapshot            Snapshot
	PolicyBlockedReason string
}

type Validator struct{}

// New 校验依赖并创建对应实例。
func New() *Validator { return &Validator{} }

// Validate 校验输入及领域约束。
func (v *Validator) Validate(ctx context.Context, request Request) Result {
	result := Result{Errors: []Violation{}}
	if err := ctx.Err(); err != nil {
		return add(result, PolicyBlocked, -1, "", err.Error())
	}
	if err := patch.ValidateSet(request.Patch); err != nil {
		return add(result, InvalidPatch, -1, "", err.Error())
	}
	if strings.TrimSpace(request.PolicyBlockedReason) != "" {
		result = add(result, PolicyBlocked, -1, "", request.PolicyBlockedReason)
	}
	if request.WorkspaceID == "" || request.WorkspaceID != request.Snapshot.WorkspaceID || request.ResourceID == "" || request.ResourceID != request.Snapshot.ResourceID || request.Patch.ResourceID != request.ResourceID {
		result = add(result, ResourceScopeDenied, -1, "", "workspace or resource scope does not match trusted snapshot")
	}
	if request.Snapshot.Document == nil {
		return add(result, ReferenceMissing, -1, "", "canonical document is missing")
	}
	if err := model.Validate(request.Snapshot.Document); err != nil {
		return add(result, StructuralConflict, -1, "", "stored canonical document is invalid: "+err.Error())
	}
	if request.Patch.BaseVersionID != request.Snapshot.CurrentVersionID || request.Snapshot.Document.VersionID != request.Snapshot.CurrentVersionID {
		result = add(result, VersionConflict, -1, "", "base_version_id is not current")
	}
	for _, reference := range request.Patch.EvidenceRefs {
		if _, exists := request.Snapshot.EvidenceRefs[reference]; !exists {
			result = add(result, ReferenceMissing, -1, "", "evidence_ref is missing: "+reference)
		}
	}
	nodes, parents := index(request.Snapshot.Document.Root)
	deleted := map[string]struct{}{}
	newIDs := map[string]struct{}{}
	mutatedTargets := map[string]struct{}{}
	for operationIndex, operation := range request.Patch.Operations {
		node, exists := nodes[operation.NodeID]
		if !exists {
			result = add(result, ReferenceMissing, operationIndex, operation.NodeID, "node_id does not exist")
			continue
		}
		if _, authorized := request.Snapshot.AuthorizedNodeIDs[operation.NodeID]; !authorized {
			result = add(result, UnauthorizedNode, operationIndex, operation.NodeID, "node is outside authorized scope")
		}
		if node.ContentHash != operation.ExpectedHash {
			result = add(result, HashConflict, operationIndex, operation.NodeID, "expected_hash does not match current node")
		}
		if _, alreadyDeleted := deleted[operation.NodeID]; alreadyDeleted {
			result = add(result, StructuralConflict, operationIndex, operation.NodeID, "operation targets a node deleted earlier in the PatchSet")
		}
		if _, alreadyMutated := mutatedTargets[operation.NodeID]; alreadyMutated {
			result = add(result, StructuralConflict, operationIndex, operation.NodeID, "multiple operations target the same base node")
		}
		mutatedTargets[operation.NodeID] = struct{}{}
		if operation.Op == patch.DeleteNode {
			if operation.NodeID == request.Snapshot.Document.Root.NodeID {
				result = add(result, StructuralConflict, operationIndex, operation.NodeID, "document root is required and cannot be deleted")
			}
			deleted[operation.NodeID] = struct{}{}
		}
		if operation.Op == patch.InsertBefore || operation.Op == patch.InsertAfter {
			parent := parents[operation.NodeID]
			if parent == nil || parent.NodeID != operation.ExpectedParentID {
				result = add(result, StructuralConflict, operationIndex, operation.NodeID, "expected parent reference is missing or changed")
			} else if parent.ContentHash != operation.ExpectedParentHash {
				result = add(result, HashConflict, operationIndex, parent.NodeID, "expected parent hash does not match")
			}
			if _, authorized := request.Snapshot.AuthorizedNodeIDs[operation.ExpectedParentID]; !authorized {
				result = add(result, UnauthorizedNode, operationIndex, operation.ExpectedParentID, "parent node is outside authorized scope")
			}
			for _, inserted := range model.Flatten(operation.Node) {
				if _, exists := nodes[inserted.NodeID]; exists {
					result = add(result, StructuralConflict, operationIndex, inserted.NodeID, "new node_id conflicts with the document")
				}
				if _, exists := newIDs[inserted.NodeID]; exists {
					result = add(result, StructuralConflict, operationIndex, inserted.NodeID, "new node_id conflicts with another insertion")
				}
				newIDs[inserted.NodeID] = struct{}{}
			}
		}
	}
	if len(result.Errors) > 0 {
		return result
	}
	applied, err := patch.Apply(request.Snapshot.Document, request.Patch)
	if err != nil {
		return add(result, StructuralConflict, -1, "", fmt.Sprintf("apply PatchSet: %v", err))
	}
	result.Valid, result.Document = true, applied
	return result
}

// index 执行该函数负责的核心处理逻辑。
func index(root *model.Node) (map[string]*model.Node, map[string]*model.Node) {
	nodes := map[string]*model.Node{}
	parents := map[string]*model.Node{}
	seen := map[*model.Node]struct{}{}
	var walk func(*model.Node)
	walk = func(node *model.Node) {
		if node == nil {
			return
		}
		if _, exists := seen[node]; exists {
			return
		}
		seen[node] = struct{}{}
		nodes[node.NodeID] = node
		for _, child := range node.Children {
			if child != nil {
				parents[child.NodeID] = node
			}
			walk(child)
		}
	}
	walk(root)
	return nodes, parents
}

// add 执行该函数负责的核心处理逻辑。
func add(result Result, category ErrorCategory, operationIndex int, nodeID, message string) Result {
	violation := Violation{Category: category, NodeID: nodeID, Message: message}
	if operationIndex >= 0 {
		index := operationIndex
		violation.OperationIndex = &index
	}
	result.Errors = append(result.Errors, violation)
	result.Valid = false
	return result
}
