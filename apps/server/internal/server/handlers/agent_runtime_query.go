package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent_project/apps/server/internal/agent/identity"
	"agent_project/apps/server/internal/agent/operations"
	"agent_project/apps/server/internal/storage/postgres/agentops"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"
)

type AgentRuntimeQueryReader interface {
	ListRuns(context.Context, operations.RunListRequest) ([]operations.RunSummary, error)
	Diagnose(context.Context, operations.DiagnosticRequest) (operations.Diagnostic, error)
	ListApprovals(context.Context, operations.ApprovalListRequest) ([]operations.ApprovalSummary, error)
	GetApproval(context.Context, string, string) (operations.ApprovalSummary, error)
}

type AgentRuntimeQueryHandler struct {
	reader   AgentRuntimeQueryReader
	identity identity.Adapter
}

type publicRunDetail struct {
	Run       publicRun                 `json:"run"`
	Steps     []publicStep              `json:"steps"`
	ToolCalls []publicToolCall          `json:"tool_calls"`
	Approvals []operations.ApprovalView `json:"approvals"`
	Findings  []operations.Finding      `json:"findings"`
}

type publicRun struct {
	ID                string     `json:"id"`
	ResourceID        string     `json:"resource_id,omitempty"`
	SessionID         string     `json:"session_id,omitempty"`
	RequestID         string     `json:"request_id,omitempty"`
	Status            string     `json:"status"`
	Objective         string     `json:"objective"`
	CurrentStep       string     `json:"current_step,omitempty"`
	DeadlineAt        *time.Time `json:"deadline_at,omitempty"`
	CancelRequestedAt *time.Time `json:"cancel_requested_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type publicStep struct {
	ID           string     `json:"id"`
	StepKey      string     `json:"step_key"`
	StepType     string     `json:"step_type"`
	Status       string     `json:"status"`
	AttemptCount int        `json:"attempt_count"`
	MaxAttempts  int        `json:"max_attempts"`
	NextRetryAt  *time.Time `json:"next_retry_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type publicToolCall struct {
	ID            string     `json:"id"`
	StepID        string     `json:"step_id"`
	ToolName      string     `json:"tool_name"`
	ToolVersion   string     `json:"tool_version"`
	Status        string     `json:"status"`
	ErrorCategory string     `json:"error_category,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

func NewAgentRuntimeQueryHandler(reader AgentRuntimeQueryReader, adapter identity.Adapter) *AgentRuntimeQueryHandler {
	return &AgentRuntimeQueryHandler{reader: reader, identity: adapter}
}

func (handler *AgentRuntimeQueryHandler) ListRuns(ctx context.Context, request *app.RequestContext) {
	scope, ok := handler.authenticate(ctx, request)
	if !ok {
		return
	}
	limit, ok := parseQueryLimit(request)
	if !ok {
		return
	}
	status := strings.TrimSpace(request.Query("status"))
	if status != "" && !isPublicRunStatus(status) {
		request.JSON(consts.StatusBadRequest, map[string]string{"error": "运行状态无效"})
		return
	}
	resourceID := strings.TrimSpace(request.Query("resource_id"))
	if resourceID != "" {
		if _, err := uuid.Parse(resourceID); err != nil {
			request.JSON(consts.StatusBadRequest, map[string]string{"error": "资源 ID 非法"})
			return
		}
	}
	runs, err := handler.reader.ListRuns(ctx, operations.RunListRequest{
		WorkspaceID: scope.WorkspaceID, Status: status, ResourceID: resourceID, Limit: limit,
	})
	if err != nil {
		request.JSON(consts.StatusInternalServerError, map[string]string{"error": "运行记录查询失败"})
		return
	}
	if runs == nil {
		runs = make([]operations.RunSummary, 0)
	}
	request.JSON(consts.StatusOK, map[string]any{"runs": runs})
}

func (handler *AgentRuntimeQueryHandler) GetRun(ctx context.Context, request *app.RequestContext) {
	scope, ok := handler.authenticate(ctx, request)
	if !ok {
		return
	}
	runID := strings.TrimSpace(request.Param("id"))
	if _, err := uuid.Parse(runID); err != nil {
		request.JSON(consts.StatusBadRequest, map[string]string{"error": "运行 ID 非法"})
		return
	}
	diagnostic, err := handler.reader.Diagnose(ctx, operations.DiagnosticRequest{WorkspaceID: scope.WorkspaceID, RunID: runID})
	if err != nil {
		handler.writeQueryError(request, err, "运行记录查询失败")
		return
	}
	request.JSON(consts.StatusOK, publicDiagnostic(diagnostic))
}

func (handler *AgentRuntimeQueryHandler) ListApprovals(ctx context.Context, request *app.RequestContext) {
	scope, ok := handler.authenticate(ctx, request)
	if !ok {
		return
	}
	limit, ok := parseQueryLimit(request)
	if !ok {
		return
	}
	status := strings.TrimSpace(request.Query("status"))
	if status != "" && !isPublicApprovalStatus(status) {
		request.JSON(consts.StatusBadRequest, map[string]string{"error": "审批状态无效"})
		return
	}
	approvals, err := handler.reader.ListApprovals(ctx, operations.ApprovalListRequest{
		WorkspaceID: scope.WorkspaceID, Status: status, Limit: limit,
	})
	if err != nil {
		request.JSON(consts.StatusInternalServerError, map[string]string{"error": "审批记录查询失败"})
		return
	}
	if approvals == nil {
		approvals = make([]operations.ApprovalSummary, 0)
	}
	request.JSON(consts.StatusOK, map[string]any{"approvals": approvals})
}

func (handler *AgentRuntimeQueryHandler) GetApproval(ctx context.Context, request *app.RequestContext) {
	scope, ok := handler.authenticate(ctx, request)
	if !ok {
		return
	}
	approvalID := strings.TrimSpace(request.Param("id"))
	if _, err := uuid.Parse(approvalID); err != nil {
		request.JSON(consts.StatusBadRequest, map[string]string{"error": "审批 ID 非法"})
		return
	}
	approval, err := handler.reader.GetApproval(ctx, scope.WorkspaceID, approvalID)
	if err != nil {
		handler.writeQueryError(request, err, "审批记录查询失败")
		return
	}
	request.JSON(consts.StatusOK, map[string]any{"approval": approval})
}

func (handler *AgentRuntimeQueryHandler) authenticate(ctx context.Context, request *app.RequestContext) (identity.WorkspaceScope, bool) {
	if handler == nil || handler.reader == nil || handler.identity == nil {
		request.JSON(consts.StatusServiceUnavailable, map[string]string{"error": "Agent 运行查询服务未配置"})
		return identity.WorkspaceScope{}, false
	}
	if strings.TrimSpace(string(request.Request.Header.Peek(identity.HeaderSignature))) == "" {
		request.JSON(consts.StatusUnauthorized, map[string]string{"error": "Agent 查询身份不可信"})
		return identity.WorkspaceScope{}, false
	}
	headers := make(http.Header)
	request.Request.Header.VisitAll(func(key, value []byte) { headers.Add(string(key), string(value)) })
	requestID := strings.TrimSpace(string(request.Request.Header.Peek("X-Request-ID")))
	if value, exists := request.Get("request_id"); exists {
		if persisted, isString := value.(string); isString && strings.TrimSpace(persisted) != "" {
			requestID = strings.TrimSpace(persisted)
		}
	}
	workspaceID := strings.TrimSpace(string(request.Request.Header.Peek(identity.HeaderWorkspaceID)))
	scope, err := handler.identity.Authenticate(ctx, identity.Request{
		Method: string(request.Method()), Path: string(request.Path()), RequestID: requestID, Header: headers,
	}, workspaceID)
	if err != nil || !scope.Trusted || scope.Principal.Type != "user" || scope.WorkspaceID != workspaceID {
		request.JSON(consts.StatusUnauthorized, map[string]string{"error": "Agent 查询身份不可信"})
		return identity.WorkspaceScope{}, false
	}
	return scope, true
}

func parseQueryLimit(request *app.RequestContext) (int, bool) {
	raw := strings.TrimSpace(request.Query("limit"))
	if raw == "" {
		return 50, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 100 {
		request.JSON(consts.StatusBadRequest, map[string]string{"error": "limit 必须介于 1 和 100 之间"})
		return 0, false
	}
	return limit, true
}

func (*AgentRuntimeQueryHandler) writeQueryError(request *app.RequestContext, err error, message string) {
	if errors.Is(err, agentops.ErrNotFound) {
		request.JSON(consts.StatusNotFound, map[string]string{"error": "记录不存在"})
		return
	}
	request.JSON(consts.StatusInternalServerError, map[string]string{"error": message})
}

func publicDiagnostic(diagnostic operations.Diagnostic) publicRunDetail {
	steps := make([]publicStep, 0, len(diagnostic.Steps))
	for _, step := range diagnostic.Steps {
		steps = append(steps, publicStep{
			ID: step.ID, StepKey: step.StepKey, StepType: step.StepType, Status: step.Status,
			AttemptCount: step.AttemptCount, MaxAttempts: step.MaxAttempts,
			NextRetryAt: step.NextRetryAt, CreatedAt: step.CreatedAt, UpdatedAt: step.UpdatedAt,
		})
	}
	toolCalls := make([]publicToolCall, 0, len(diagnostic.ToolCalls))
	for _, call := range diagnostic.ToolCalls {
		toolCalls = append(toolCalls, publicToolCall{
			ID: call.ID, StepID: call.StepID, ToolName: call.ToolName, ToolVersion: call.ToolVersion,
			Status: call.Status, ErrorCategory: call.ErrorCategory,
			StartedAt: call.StartedAt, CompletedAt: call.CompletedAt,
		})
	}
	approvals := diagnostic.Approvals
	if approvals == nil {
		approvals = make([]operations.ApprovalView, 0)
	}
	findings := diagnostic.Findings
	if findings == nil {
		findings = make([]operations.Finding, 0)
	}
	return publicRunDetail{
		Run: publicRun{
			ID: diagnostic.Run.ID, ResourceID: diagnostic.Run.ResourceID, SessionID: diagnostic.Run.SessionID,
			RequestID: diagnostic.Run.RequestID, Status: diagnostic.Run.Status, Objective: diagnostic.Run.Objective,
			CurrentStep: diagnostic.Run.CurrentStep, DeadlineAt: diagnostic.Run.DeadlineAt,
			CancelRequestedAt: diagnostic.Run.CancelRequestedAt,
			CreatedAt:         diagnostic.Run.CreatedAt, UpdatedAt: diagnostic.Run.UpdatedAt,
		},
		Steps: steps, ToolCalls: toolCalls, Approvals: approvals, Findings: findings,
	}
}

func isPublicRunStatus(status string) bool {
	switch status {
	case "queued", "running", "waiting_input", "waiting_approval", "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func isPublicApprovalStatus(status string) bool {
	switch status {
	case "pending", "approved", "rejected", "cancelled":
		return true
	default:
		return false
	}
}
