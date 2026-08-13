package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	agentevidence "agent_project/apps/server/internal/agent/evidence"
	agenttools "agent_project/apps/server/internal/agent/tools"
)

type EvidenceSearcher interface {
	Search(ctx context.Context, request agentevidence.SearchRequest) (agentevidence.EvidenceSet, error)
}

type EvidenceRetrievalBackend struct {
	searcher EvidenceSearcher
}

// NewEvidenceRetrievalBackend 校验依赖并创建对应实例。
func NewEvidenceRetrievalBackend(searcher EvidenceSearcher) (*EvidenceRetrievalBackend, error) {
	if searcher == nil {
		return nil, errors.New("证据Set 搜索服务不能为空")
	}
	return &EvidenceRetrievalBackend{searcher: searcher}, nil
}

// Search 执行该函数负责的核心处理逻辑。
func (backend *EvidenceRetrievalBackend) Search(ctx context.Context, security agenttools.SecurityContext, input RetrievalInput) (EvidenceSet, error) {
	if backend == nil || backend.searcher == nil || strings.TrimSpace(security.WorkspaceID) == "" ||
		strings.TrimSpace(security.PrincipalType) == "" || strings.TrimSpace(security.PrincipalID) == "" {
		return EvidenceSet{}, &agenttools.ToolError{Category: agenttools.ErrorPolicyBlocked, Message: "可信检索安全作用域不完整"}
	}
	set, err := backend.searcher.Search(ctx, agentevidence.SearchRequest{
		WorkspaceID: security.WorkspaceID, ResourceID: input.ResourceID, VersionID: input.VersionID,
		IncludeHistory: input.IncludeHistory, Query: input.Query, Limit: input.Limit,
	})
	if err == nil {
		return set, nil
	}
	// 根据当前状态或类型选择对应的处理分支。
	switch {
	case errors.Is(err, agentevidence.ErrInvalidSearchRequest):
		return EvidenceSet{}, &agenttools.ToolError{Category: agenttools.ErrorInvalidInput, Message: err.Error(), Cause: err}
	case errors.Is(err, agentevidence.ErrScopeNotFound):
		return EvidenceSet{}, &agenttools.ToolError{Category: agenttools.ErrorNotFound, Message: "未找到已授权的检索作用域", Cause: err}
	case errors.Is(err, agentevidence.ErrRetrievalUnavailable):
		return EvidenceSet{}, &agenttools.ToolError{Category: agenttools.ErrorRetryableUpstream, Message: err.Error(), Cause: err}
	case errors.Is(err, agentevidence.ErrEmbeddingProfileMismatch):
		return EvidenceSet{}, &agenttools.ToolError{
			Category: agenttools.ErrorTerminalUpstream,
			Message:  err.Error(),
			Details:  json.RawMessage(`{"reason_code":"embedding_profile_mismatch"}`),
			Cause:    err,
		}
	default:
		return EvidenceSet{}, &agenttools.ToolError{Category: agenttools.ErrorTerminalUpstream, Message: "检索失败", Cause: err}
	}
}

var _ RetrievalBackend = (*EvidenceRetrievalBackend)(nil)
