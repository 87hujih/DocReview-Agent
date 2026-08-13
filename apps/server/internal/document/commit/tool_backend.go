package commit

import (
	"context"
	"fmt"
	"strings"

	agenttools "agent_project/apps/server/internal/agent/tools"
	"agent_project/apps/server/internal/document/patch"
	"agent_project/apps/server/internal/document/validation"
)

type NodeAuthorization struct {
	AuthorizedNodeIDs map[string]struct{}
	EvidenceRefs      map[string]struct{}
}

type NodeAuthorizer interface {
	ResolveDocumentAuthorization(context.Context, agenttools.SecurityContext, string, []string, []string) (NodeAuthorization, error)
}

// 处理失败： ToolBackend 为 the only canonical 适配器 exposed 用于 patch.validate/commit.
// 可信的 policy 作用域 为 resolved again 位于 节点 granularity; PatchSet 内容
// 处理失败： 为 never accepted as 一个 authorization fact.
type ToolBackend struct {
	committer  *Committer
	authorizer NodeAuthorizer
}

type CanonicalToolBackend interface {
	canonicalPatchBackend()
	Validate(context.Context, agenttools.SecurityContext, patch.Set) (validation.Result, error)
	Commit(context.Context, agenttools.SecurityContext, patch.Set, string) (Result, error)
}

// NewToolBackend 校验依赖并创建对应实例。
func NewToolBackend(committer *Committer, authorizer NodeAuthorizer) (*ToolBackend, error) {
	if committer == nil || authorizer == nil {
		return nil, fmt.Errorf("canonical 提交器和节点 authorizer 不能为空")
	}
	return &ToolBackend{committer: committer, authorizer: authorizer}, nil
}

// canonicalPatchBackend 执行该函数负责的核心处理逻辑。
func (*ToolBackend) canonicalPatchBackend() {}

// Validate 校验输入及领域约束。
func (b *ToolBackend) Validate(ctx context.Context, security agenttools.SecurityContext, set patch.Set) (validation.Result, error) {
	input, err := b.input(ctx, security, set, "", false)
	if err != nil {
		return validation.Result{}, err
	}
	return b.committer.Validate(ctx, input)
}

// Commit 执行该函数负责的核心处理逻辑。
func (b *ToolBackend) Commit(ctx context.Context, security agenttools.SecurityContext, set patch.Set, idempotencyKey string) (Result, error) {
	input, err := b.input(ctx, security, set, idempotencyKey, true)
	if err != nil {
		return Result{}, err
	}
	return b.committer.Commit(ctx, input)
}

// 输入 执行该函数负责的核心处理逻辑。
func (b *ToolBackend) input(ctx context.Context, security agenttools.SecurityContext, set patch.Set, idempotencyKey string, writing bool) (Input, error) {
	if err := patch.ValidateSet(set); err != nil {
		return Input{}, err
	}
	if strings.TrimSpace(security.WorkspaceID) == "" || strings.TrimSpace(security.PrincipalID) == "" || strings.TrimSpace(security.PrincipalType) == "" {
		return Input{}, fmt.Errorf("可信的主体/工作区作用域不能为空")
	}
	if writing && strings.TrimSpace(idempotencyKey) == "" {
		return Input{}, fmt.Errorf("commit idempotency 键不能为空")
	}
	requested := make([]string, 0, len(set.Operations)*2)
	seen := map[string]struct{}{}
	for _, operation := range set.Operations {
		for _, nodeID := range []string{operation.NodeID, operation.ExpectedParentID} {
			if nodeID == "" {
				continue
			}
			if _, exists := seen[nodeID]; exists {
				continue
			}
			seen[nodeID] = struct{}{}
			requested = append(requested, nodeID)
		}
	}
	authorization, err := b.authorizer.ResolveDocumentAuthorization(ctx, security, set.ResourceID, requested, set.EvidenceRefs)
	if err != nil {
		return Input{}, err
	}
	return Input{
		WorkspaceID: security.WorkspaceID, ResourceID: set.ResourceID, IdempotencyKey: idempotencyKey,
		ActorID: security.PrincipalType + ":" + security.PrincipalID, Patch: set,
		AuthorizedNodeIDs: authorization.AuthorizedNodeIDs, EvidenceRefs: authorization.EvidenceRefs,
	}, nil
}

var _ CanonicalToolBackend = (*ToolBackend)(nil)
