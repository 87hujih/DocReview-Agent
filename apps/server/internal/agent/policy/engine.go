// Package policy owns deterministic 权限、资源-作用域、和审批
// decisions. 模型输入为 never treated as 一个 authorization fact.
package policy

import (
	"context"
	"fmt"
	"strings"

	agenttools "agent_project/apps/server/internal/agent/tools"
)

type PermissionResolver interface {
	HasPermission(ctx context.Context, principal agenttools.SecurityContext, permission string) (bool, error)
}

type ResourceResolver interface {
	AuthorizeResource(ctx context.Context, principal agenttools.SecurityContext, resource agenttools.ResourceRef) (bool, error)
}

type ApprovalVerifier interface {
	VerifyApproval(ctx context.Context, check agenttools.ApprovalCheck) (bool, error)
}

type Engine struct {
	permissions PermissionResolver
	resources   ResourceResolver
	approvals   ApprovalVerifier
}

var _ agenttools.Authorizer = (*Engine)(nil)

// NewEngine 校验依赖并创建对应实例。
func NewEngine(permissions PermissionResolver, resources ResourceResolver, approvals ApprovalVerifier) *Engine {
	return &Engine{permissions: permissions, resources: resources, approvals: approvals}
}

// 鉴权执行该函数负责的核心处理逻辑。
func (e *Engine) Authorize(ctx context.Context, request agenttools.AuthorizationRequest) (agenttools.PolicyDecision, error) {
	principal := request.Call.Security
	if e == nil || e.permissions == nil || e.resources == nil || e.approvals == nil {
		return deny("policy_engine_unavailable"), fmt.Errorf("策略引擎依赖不能为空")
	}
	if strings.TrimSpace(principal.PrincipalType) == "" || strings.TrimSpace(principal.PrincipalID) == "" || strings.TrimSpace(principal.WorkspaceID) == "" {
		return deny("principal_scope_missing"), nil
	}
	for _, permission := range request.Descriptor.RequiredPermissions {
		allowed, err := e.permissions.HasPermission(ctx, principal, strings.TrimSpace(permission))
		if err != nil {
			return deny("permission_check_failed"), fmt.Errorf("解析权限 %q：%w", permission, err)
		}
		if !allowed {
			return deny("permission_denied"), nil
		}
	}
	for _, resource := range request.Resources {
		if strings.TrimSpace(resource.Type) == "" || strings.TrimSpace(resource.ID) == "" ||
			(resource.Access != agenttools.AccessRead && resource.Access != agenttools.AccessWrite) {
			return deny("resource_scope_invalid"), nil
		}
		allowed, err := e.resources.AuthorizeResource(ctx, principal, resource)
		if err != nil {
			return deny("resource_check_failed"), fmt.Errorf("鉴权资源 %s/%s：%w", resource.Type, resource.ID, err)
		}
		if !allowed {
			return deny("resource_scope_denied"), nil
		}
	}

	if request.Descriptor.RiskLevel == agenttools.RiskHigh || request.Descriptor.RiskLevel == agenttools.RiskCritical {
		if strings.TrimSpace(request.Call.ApprovalID) == "" {
			return agenttools.PolicyDecision{Outcome: agenttools.PolicyRequireApproval, ReasonCode: "approval_required"}, nil
		}
		approved, err := e.approvals.VerifyApproval(ctx, agenttools.ApprovalCheck{
			ApprovalID: request.Call.ApprovalID, Principal: principal,
			RunID: request.Call.RunID, StepID: request.Call.StepID,
			ToolName: request.Descriptor.Name, ToolVersion: request.Descriptor.Version,
			IdempotencyKey: request.Call.IdempotencyKey, Resources: append([]agenttools.ResourceRef(nil), request.Resources...),
		})
		if err != nil {
			return deny("approval_check_failed"), fmt.Errorf("校验审批：%w", err)
		}
		if !approved {
			return deny("approval_invalid"), nil
		}
	}
	return agenttools.PolicyDecision{Outcome: agenttools.PolicyAllow, ReasonCode: "authorized"}, nil
}

// deny 执行该函数负责的核心处理逻辑。
func deny(reason string) agenttools.PolicyDecision {
	return agenttools.PolicyDecision{Outcome: agenttools.PolicyDeny, ReasonCode: reason}
}
