package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agent_project/apps/server/internal/approval"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ApprovalHandler 暴露审批与执行作业相关接口。
type ApprovalHandler struct {
	approvalService *approval.Service
}

type approvalResponse struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"task_id"`
	Status       string     `json:"status"`
	RejectReason *string    `json:"reject_reason"`
	DecidedAt    *time.Time `json:"decided_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

type executionJobResponse struct {
	ID           string     `json:"id"`
	TaskID       string     `json:"task_id"`
	ApprovalID   string     `json:"approval_id"`
	Status       string     `json:"status"`
	ErrorMessage *string    `json:"error_message"`
	NewVersionID *string    `json:"new_version_id"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

type listApprovalsResponse struct {
	Approvals []approvalResponse `json:"approvals"`
}

type getApprovalResponse struct {
	Approval approvalResponse `json:"approval"`
}

type getExecutionJobResponse struct {
	Job executionJobResponse `json:"job"`
}

type rejectApprovalRequest struct {
	Reason string `json:"reason"`
}

// NewApprovalHandler 创建审批 handler。
func NewApprovalHandler(svc *approval.Service) *ApprovalHandler {
	return &ApprovalHandler{approvalService: svc}
}

// List 返回审批列表，可按状态过滤。
func (h *ApprovalHandler) List(requestCtx context.Context, ctx *app.RequestContext) {
	if h.approvalService == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "审批服务未配置"})
		return
	}

	approvals, err := h.approvalService.List(requestCtx, ctx.Query("status"))
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询审批列表失败"})
		return
	}

	response := listApprovalsResponse{
		Approvals: make([]approvalResponse, 0, len(approvals)),
	}
	for _, approvalRecord := range approvals {
		response.Approvals = append(response.Approvals, approvalToResponse(approvalRecord))
	}

	ctx.JSON(consts.StatusOK, response)
}

// GetByID 返回单条审批记录。
func (h *ApprovalHandler) GetByID(requestCtx context.Context, ctx *app.RequestContext) {
	id := ctx.Param("id")
	if !isValidUUID(id) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "无效的审批 ID"})
		return
	}
	if h.approvalService == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "审批服务未配置"})
		return
	}

	approvalRecord, err := h.approvalService.GetApproval(requestCtx, id)
	if err != nil {
		switch {
		case errors.Is(err, approval.ErrApprovalNotFound):
			ctx.JSON(consts.StatusNotFound, map[string]string{"error": "审批不存在"})
		default:
			ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询审批详情失败"})
		}
		return
	}

	ctx.JSON(consts.StatusOK, getApprovalResponse{
		Approval: approvalToResponse(*approvalRecord),
	})
}

// GetJobByID 返回单条执行作业记录。
func (h *ApprovalHandler) GetJobByID(requestCtx context.Context, ctx *app.RequestContext) {
	id := ctx.Param("id")
	if !isValidUUID(id) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "无效的执行作业 ID"})
		return
	}
	if h.approvalService == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "审批服务未配置"})
		return
	}

	job, err := h.approvalService.GetJob(requestCtx, id)
	if err != nil {
		switch {
		case errors.Is(err, approval.ErrJobNotFound):
			ctx.JSON(consts.StatusNotFound, map[string]string{"error": "执行作业不存在"})
		default:
			ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询执行作业详情失败"})
		}
		return
	}

	ctx.JSON(consts.StatusOK, getExecutionJobResponse{
		Job: executionJobToResponse(*job),
	})
}

// Approve 将指定审批切换为 approved。
func (h *ApprovalHandler) Approve(requestCtx context.Context, ctx *app.RequestContext) {
	id := ctx.Param("id")
	if !isValidUUID(id) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "无效的审批 ID"})
		return
	}
	if h.approvalService == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "审批服务未配置"})
		return
	}

	approvalRecord, err := h.approvalService.Approve(requestCtx, id)
	if err != nil {
		switch {
		case errors.Is(err, approval.ErrApprovalNotFound):
			ctx.JSON(consts.StatusNotFound, map[string]string{"error": "审批不存在"})
		case errors.Is(err, approval.ErrApprovalAlreadyDecided):
			ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "审批已处理"})
		default:
			ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "通过审批失败"})
		}
		return
	}

	ctx.JSON(consts.StatusOK, getApprovalResponse{
		Approval: approvalToResponse(*approvalRecord),
	})
}

// Reject 将指定审批切换为 rejected。
func (h *ApprovalHandler) Reject(requestCtx context.Context, ctx *app.RequestContext) {
	var request rejectApprovalRequest
	if err := json.Unmarshal(ctx.Request.Body(), &request); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "必须提供原因"})
		return
	}

	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "必须提供原因"})
		return
	}

	id := ctx.Param("id")
	if !isValidUUID(id) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "无效的审批 ID"})
		return
	}

	if h.approvalService == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "审批服务未配置"})
		return
	}

	approvalRecord, err := h.approvalService.Reject(requestCtx, id, request.Reason)
	if err != nil {
		switch {
		case errors.Is(err, approval.ErrApprovalNotFound):
			ctx.JSON(consts.StatusNotFound, map[string]string{"error": "审批不存在"})
		case errors.Is(err, approval.ErrApprovalAlreadyDecided), errors.Is(err, approval.ErrReasonRequired):
			ctx.JSON(consts.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "拒绝审批失败"})
		}
		return
	}

	ctx.JSON(consts.StatusOK, getApprovalResponse{
		Approval: approvalToResponse(*approvalRecord),
	})
}

func approvalToResponse(approvalRecord postgres.Approval) approvalResponse {
	return approvalResponse{
		ID:           approvalRecord.ID,
		TaskID:       approvalRecord.TaskID,
		Status:       approvalRecord.Status,
		RejectReason: approvalRecord.RejectReason,
		DecidedAt:    approvalRecord.DecidedAt,
		CreatedAt:    approvalRecord.CreatedAt,
	}
}

func executionJobToResponse(job postgres.ExecutionJob) executionJobResponse {
	return executionJobResponse{
		ID:           job.ID,
		TaskID:       job.TaskID,
		ApprovalID:   job.ApprovalID,
		Status:       job.Status,
		ErrorMessage: job.ErrorMessage,
		NewVersionID: job.NewVersionID,
		StartedAt:    job.StartedAt,
		CompletedAt:  job.CompletedAt,
		CreatedAt:    job.CreatedAt,
	}
}
