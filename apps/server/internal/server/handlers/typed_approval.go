package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/identity"
	agenttools "agent_project/apps/server/internal/agent/tools"
	"agent_project/apps/server/internal/agent/tools/builtin"
	"agent_project/apps/server/internal/storage/postgres/agentpolicy"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"
)

type TypedApprovalDecider interface {
	DecideApproval(context.Context, agentpolicy.DecisionParams) (builtin.Approval, error)
}

type TypedApprovalHandler struct {
	decider  TypedApprovalDecider
	identity identity.Adapter
}

// NewTypedApprovalHandler 校验依赖并创建对应实例。
func NewTypedApprovalHandler(decider TypedApprovalDecider, adapter identity.Adapter) *TypedApprovalHandler {
	return &TypedApprovalHandler{decider: decider, identity: adapter}
}

// Approve 执行该函数负责的核心处理逻辑。
func (handler *TypedApprovalHandler) Approve(ctx context.Context, request *app.RequestContext) {
	handler.decide(ctx, request, "approved")
}

// Reject 执行该函数负责的核心处理逻辑。
func (handler *TypedApprovalHandler) Reject(ctx context.Context, request *app.RequestContext) {
	handler.decide(ctx, request, "rejected")
}

// decide 执行该函数负责的核心处理逻辑。
func (handler *TypedApprovalHandler) decide(ctx context.Context, request *app.RequestContext, status string) {
	approvalID := strings.TrimSpace(request.Param("id"))
	if _, err := uuid.Parse(approvalID); err != nil {
		request.JSON(consts.StatusBadRequest, map[string]string{"error": "审批 ID 非法"})
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(request.Request.Body(), &body); err != nil || strings.TrimSpace(body.Reason) == "" {
		request.JSON(consts.StatusBadRequest, map[string]string{"error": "审批理由不能为空"})
		return
	}
	if handler == nil || handler.decider == nil || handler.identity == nil {
		request.JSON(consts.StatusServiceUnavailable, map[string]string{"error": "持久化审批服务未配置"})
		return
	}
	headers := make(http.Header)
	request.Request.Header.VisitAll(func(key, value []byte) { headers.Add(string(key), string(value)) })
	requestID := strings.TrimSpace(string(request.Request.Header.Peek("X-Request-ID")))
	if value, exists := request.Get("request_id"); exists {
		if persisted, ok := value.(string); ok && strings.TrimSpace(persisted) != "" {
			requestID = strings.TrimSpace(persisted)
		}
	}
	workspaceID := strings.TrimSpace(string(request.Request.Header.Peek(identity.HeaderWorkspaceID)))
	scope, err := handler.identity.Authenticate(ctx, identity.Request{
		Method: string(request.Method()), Path: string(request.Path()), RequestID: requestID, Header: headers,
	}, workspaceID)
	if err != nil || !scope.Trusted || scope.Principal.Type != "user" || scope.WorkspaceID != workspaceID {
		request.JSON(consts.StatusUnauthorized, map[string]string{"error": "审批身份不可信"})
		return
	}
	approval, err := handler.decider.DecideApproval(ctx, agentpolicy.DecisionParams{
		ApprovalID: approvalID,
		Security: agenttools.SecurityContext{
			PrincipalType: scope.Principal.Type, PrincipalID: scope.Principal.ID,
			WorkspaceID: scope.WorkspaceID, RequestID: requestID,
		},
		Status: status, Reason: strings.TrimSpace(body.Reason), DecidedAt: time.Now().UTC(),
	})
	if err != nil {
		handler.writeDecisionError(request, err)
		return
	}
	request.JSON(consts.StatusOK, map[string]any{"approval": approval})
}

// writeDecisionError 执行该函数负责的核心处理逻辑。
func (*TypedApprovalHandler) writeDecisionError(request *app.RequestContext, err error) {
	var toolErr *agenttools.ToolError
	if errors.As(err, &toolErr) {
		// 根据当前状态或类型选择对应的处理分支。
		switch toolErr.Category {
		case agenttools.ErrorPermissionDenied:
			request.JSON(consts.StatusForbidden, map[string]string{"error": "审批权限不足"})
		case agenttools.ErrorNotFound:
			request.JSON(consts.StatusNotFound, map[string]string{"error": "审批不存在"})
		case agenttools.ErrorConflict:
			request.JSON(consts.StatusConflict, map[string]string{"error": "审批状态冲突"})
		default:
			request.JSON(consts.StatusBadRequest, map[string]string{"error": "审批请求无效"})
		}
		return
	}
	request.JSON(consts.StatusInternalServerError, map[string]string{"error": "审批决策失败"})
}
