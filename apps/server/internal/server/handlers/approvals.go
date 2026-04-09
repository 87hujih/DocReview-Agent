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

// ApprovalHandler 暴露审批列表、通过和拒绝接口。
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

type listApprovalsResponse struct {
	Approvals []approvalResponse `json:"approvals"`
}

type getApprovalResponse struct {
	Approval approvalResponse `json:"approval"`
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
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "approval service not configured"})
		return
	}

	approvals, err := h.approvalService.List(requestCtx, ctx.Query("status"))
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "failed to list approvals"})
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

// Approve 将指定审批切换为 approved。
func (h *ApprovalHandler) Approve(requestCtx context.Context, ctx *app.RequestContext) {
	if h.approvalService == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "approval service not configured"})
		return
	}

	approvalRecord, err := h.approvalService.Approve(requestCtx, ctx.Param("id"))
	if err != nil {
		switch {
		case errors.Is(err, approval.ErrApprovalNotFound):
			ctx.JSON(consts.StatusNotFound, map[string]string{"error": "approval not found"})
		case errors.Is(err, approval.ErrApprovalAlreadyDecided):
			ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "approval already decided"})
		default:
			ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "failed to approve task"})
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
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "reason is required"})
		return
	}

	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "reason is required"})
		return
	}
	if h.approvalService == nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "approval service not configured"})
		return
	}

	approvalRecord, err := h.approvalService.Reject(requestCtx, ctx.Param("id"), request.Reason)
	if err != nil {
		switch {
		case errors.Is(err, approval.ErrApprovalNotFound):
			ctx.JSON(consts.StatusNotFound, map[string]string{"error": "approval not found"})
		case errors.Is(err, approval.ErrApprovalAlreadyDecided), errors.Is(err, approval.ErrReasonRequired):
			ctx.JSON(consts.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "failed to reject task"})
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
