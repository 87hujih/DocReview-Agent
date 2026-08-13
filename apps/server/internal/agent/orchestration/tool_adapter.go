package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentruntime "agent_project/apps/server/internal/agent/runtime"
	agenttools "agent_project/apps/server/internal/agent/tools"
)

type SecurityScopeResolver interface {
	ResolveToolScope(ctx context.Context, runID string) (agenttools.SecurityContext, error)
}

type RuntimeToolExecutor struct {
	runtime *agenttools.Runtime
	scopes  SecurityScopeResolver
}

// NewRuntimeToolExecutor 校验依赖并创建对应实例。
func NewRuntimeToolExecutor(runtime *agenttools.Runtime, scopes SecurityScopeResolver) (*RuntimeToolExecutor, error) {
	if runtime == nil || scopes == nil {
		return nil, fmt.Errorf("工具运行时和可信的安全作用域解析器不能为空")
	}
	return &RuntimeToolExecutor{runtime: runtime, scopes: scopes}, nil
}

// Execute 执行该函数负责的核心处理逻辑。
func (executor *RuntimeToolExecutor) Execute(ctx context.Context, request ToolRequest) (ToolObservation, error) {
	security, err := executor.scopes.ResolveToolScope(ctx, request.RunID)
	if err != nil {
		return ToolObservation{}, &ToolFailure{Category: agentruntime.ErrorCategoryPolicyBlocked, Message: "解析可信工具作用域失败", Cause: err}
	}
	if strings.TrimSpace(security.PrincipalType) == "" || strings.TrimSpace(security.PrincipalID) == "" ||
		strings.TrimSpace(security.WorkspaceID) == "" || strings.TrimSpace(security.ResourceID) == "" {
		return ToolObservation{}, &ToolFailure{Category: agentruntime.ErrorCategoryPolicyBlocked, Message: "可信工具作用域不完整"}
	}
	// 运行/步骤 authority comes 来自 the 工作进程's claimed WorkItem. Persisted
	// 主体作用域不能 supply 或 override 执行溯源信息.
	security.RunID = request.RunID
	security.StepID = request.StepID
	execution, err := executor.runtime.Execute(ctx, agenttools.Call{
		RunID: request.RunID, StepID: request.StepID,
		ToolName: request.ToolName, ToolVersion: request.ToolVersion, Input: request.Input,
		IdempotencyKey: request.IdempotencyKey, ApprovalID: request.ApprovalID,
		TraceID: request.TraceID, Security: security,
	})
	if err != nil {
		return ToolObservation{}, &ToolFailure{Category: agentruntime.ErrorCategoryTerminalUpstream, Message: "ToolRuntime 基础设施故障", Cause: err}
	}
	if execution.Error != nil {
		return ToolObservation{}, &ToolFailure{
			Category: runtimeCategory(execution.Error.Category), Message: execution.Error.Message, Cause: execution.Error,
		}
	}
	if execution.Status == agenttools.AuditRunning {
		return ToolObservation{}, &ToolFailure{Category: agentruntime.ErrorCategoryRetryableUpstream, Message: "工具调用已在有效租约下运行"}
	}
	if execution.Status != agenttools.AuditSucceeded || execution.Result == nil {
		return ToolObservation{}, &ToolFailure{Category: agentruntime.ErrorCategoryTerminalUpstream, Message: "ToolRuntime 未返回终态结果"}
	}
	payload, err := json.Marshal(execution.Result)
	if err != nil {
		return ToolObservation{}, &ToolFailure{Category: agentruntime.ErrorCategoryTerminalUpstream, Message: "编码 ToolRuntime 结果失败", Cause: err}
	}
	return ToolObservation{ToolCallID: execution.CallID, Output: payload}, nil
}

// runtimeCategory 执行该函数负责的核心处理逻辑。
func runtimeCategory(category agenttools.ErrorCategory) agentruntime.ErrorCategory {
	// 根据当前状态或类型选择对应的处理分支。
	switch category {
	case agenttools.ErrorInvalidInput:
		return agentruntime.ErrorCategoryInvalidInput
	case agenttools.ErrorPermissionDenied:
		return agentruntime.ErrorCategoryPermissionDenied
	case agenttools.ErrorNotFound:
		return agentruntime.ErrorCategoryNotFound
	case agenttools.ErrorConflict:
		return agentruntime.ErrorCategoryConflict
	case agenttools.ErrorRateLimited:
		return agentruntime.ErrorCategoryRateLimited
	case agenttools.ErrorTimeout:
		return agentruntime.ErrorCategoryTimeout
	case agenttools.ErrorRetryableUpstream:
		return agentruntime.ErrorCategoryRetryableUpstream
	case agenttools.ErrorPolicyBlocked:
		return agentruntime.ErrorCategoryPolicyBlocked
	case agenttools.ErrorCancelled:
		return agentruntime.ErrorCategoryCancelled
	default:
		return agentruntime.ErrorCategoryTerminalUpstream
	}
}

var _ ToolExecutor = (*RuntimeToolExecutor)(nil)
