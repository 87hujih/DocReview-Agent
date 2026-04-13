package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"agent_project/apps/server/internal/assistant"
	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

type assistantService interface {
	ListSessions(ctx context.Context) ([]postgres.AssistantSession, error)
	GetConversation(ctx context.Context, sessionID string) (*assistant.ConversationResult, error)
	StartConversation(ctx context.Context, content string) (*assistant.ConversationResult, error)
	AppendMessage(ctx context.Context, sessionID string, content string) (*assistant.ConversationResult, error)
	UploadFile(ctx context.Context, sessionID string, fileName string, content []byte) (*assistant.UploadFileResult, error)
	ConfirmTaskSuggestion(ctx context.Context, messageID string) (*assistant.ConfirmTaskResult, error)
	DeleteSession(ctx context.Context, sessionID string) (bool, error)
}

// AssistantHandler 暴露助手会话列表、消息、文件和任务确认接口。
type AssistantHandler struct {
	service assistantService
}

type assistantMessageRequest struct {
	Message string `json:"message"`
}

type assistantSessionResponse struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	LastMessageAt time.Time `json:"last_message_at"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type assistantMessageResponse struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Kind       string          `json:"kind"`
	SequenceNo int             `json:"sequence_no"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

type assistantResourceResponse struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	SourceType string `json:"source_type"`
}

type assistantTaskResponse struct {
	ID          string    `json:"id"`
	ResourceID  string    `json:"resource_id"`
	Instruction string    `json:"instruction"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type listAssistantSessionsResponse struct {
	Sessions []assistantSessionResponse `json:"sessions"`
}

type assistantConversationResponse struct {
	Session  assistantSessionResponse   `json:"session"`
	Messages []assistantMessageResponse `json:"messages"`
}

type assistantUploadResponse struct {
	Session      assistantSessionResponse   `json:"session"`
	Resource     *assistantResourceResponse `json:"resource"`
	Messages     []assistantMessageResponse `json:"messages"`
	ErrorMessage *string                    `json:"error_message"`
}

type confirmTaskSuggestionResponse struct {
	Session      assistantSessionResponse   `json:"session"`
	Task         *assistantTaskResponse     `json:"task"`
	Messages     []assistantMessageResponse `json:"messages"`
	ErrorMessage *string                    `json:"error_message"`
}

// NewAssistantHandler 创建助手 HTTP handler。
func NewAssistantHandler(service assistantService) *AssistantHandler {
	return &AssistantHandler{service: service}
}

// ListSessions 返回左侧历史栏需要的会话摘要。
func (h *AssistantHandler) ListSessions(requestCtx context.Context, ctx *app.RequestContext) {
	sessions, err := h.service.ListSessions(requestCtx)
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "查询会话列表失败"})
		return
	}

	response := listAssistantSessionsResponse{
		Sessions: make([]assistantSessionResponse, 0, len(sessions)),
	}
	for _, session := range sessions {
		response.Sessions = append(response.Sessions, toAssistantSessionResponse(session))
	}

	ctx.JSON(consts.StatusOK, response)
}

// GetConversation 返回一个会话及完整消息流。
func (h *AssistantHandler) GetConversation(requestCtx context.Context, ctx *app.RequestContext) {
	result, err := h.service.GetConversation(requestCtx, ctx.Param("id"))
	if err != nil {
		h.writeAssistantError(ctx, err, "查询会话失败")
		return
	}

	ctx.JSON(consts.StatusOK, assistantConversationResponse{
		Session:  toAssistantSessionResponse(result.Session),
		Messages: toAssistantMessageResponses(result.Messages),
	})
}

// CreateConversation 用首条消息创建真实会话。
func (h *AssistantHandler) CreateConversation(requestCtx context.Context, ctx *app.RequestContext) {
	var request assistantMessageRequest
	if err := json.Unmarshal(ctx.Request.Body(), &request); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "消息内容不能为空"})
		return
	}

	result, err := h.service.StartConversation(requestCtx, request.Message)
	if err != nil {
		h.writeAssistantError(ctx, err, "创建会话失败")
		return
	}

	ctx.JSON(consts.StatusCreated, assistantConversationResponse{
		Session:  toAssistantSessionResponse(result.Session),
		Messages: toAssistantMessageResponses(result.Messages),
	})
}

// AppendMessage 向已有会话追加消息。
func (h *AssistantHandler) AppendMessage(requestCtx context.Context, ctx *app.RequestContext) {
	var request assistantMessageRequest
	if err := json.Unmarshal(ctx.Request.Body(), &request); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "消息内容不能为空"})
		return
	}

	result, err := h.service.AppendMessage(requestCtx, ctx.Param("id"), request.Message)
	if err != nil {
		h.writeAssistantError(ctx, err, "追加消息失败")
		return
	}

	ctx.JSON(consts.StatusOK, assistantConversationResponse{
		Session:  toAssistantSessionResponse(result.Session),
		Messages: toAssistantMessageResponses(result.Messages),
	})
}

// UploadFile 接收一个文件并写入资源库与当前会话。
func (h *AssistantHandler) UploadFile(requestCtx context.Context, ctx *app.RequestContext) {
	file, err := ctx.FormFile("file")
	if err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "必须上传文件"})
		return
	}
	if !documentparser.IsSupportedFileName(file.Filename) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "当前仅支持 md、txt、doc、docx、pdf、rtf、odt 文件"})
		return
	}

	reader, err := file.Open()
	if err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "读取上传文件失败"})
		return
	}
	defer reader.Close()

	content, err := io.ReadAll(reader)
	if err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "读取上传文件失败"})
		return
	}

	result, err := h.service.UploadFile(requestCtx, ctx.Param("id"), file.Filename, content)
	if err != nil {
		h.writeAssistantError(ctx, err, "上传文件失败")
		return
	}

	ctx.JSON(consts.StatusOK, assistantUploadResponse{
		Session:      toAssistantSessionResponse(result.Session),
		Resource:     toAssistantResourceResponse(result.Resource),
		Messages:     toAssistantMessageResponses(result.Messages),
		ErrorMessage: result.ErrorMessage,
	})
}

// ConfirmTaskSuggestion 把一条任务建议落为真实任务。
func (h *AssistantHandler) ConfirmTaskSuggestion(requestCtx context.Context, ctx *app.RequestContext) {
	result, err := h.service.ConfirmTaskSuggestion(requestCtx, ctx.Param("id"))
	if err != nil {
		h.writeAssistantError(ctx, err, "确认任务建议失败")
		return
	}

	ctx.JSON(consts.StatusOK, confirmTaskSuggestionResponse{
		Session:      toAssistantSessionResponse(result.Session),
		Task:         toAssistantTaskResponse(result.Task),
		Messages:     toAssistantMessageResponses(result.Messages),
		ErrorMessage: result.ErrorMessage,
	})
}

// DeleteSession 删除一个会话。
func (h *AssistantHandler) DeleteSession(requestCtx context.Context, ctx *app.RequestContext) {
	deleted, err := h.service.DeleteSession(requestCtx, ctx.Param("id"))
	if err != nil {
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": "删除会话失败"})
		return
	}
	if !deleted {
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": "会话不存在"})
		return
	}

	ctx.Status(consts.StatusNoContent)
}

func (h *AssistantHandler) writeAssistantError(ctx *app.RequestContext, err error, defaultMessage string) {
	switch {
	case errors.Is(err, assistant.ErrMessageRequired),
		errors.Is(err, assistant.ErrFileNameRequired),
		errors.Is(err, assistant.ErrFileContentRequired):
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, assistant.ErrSessionNotFound),
		errors.Is(err, assistant.ErrTaskSuggestionNotFound):
		ctx.JSON(consts.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		ctx.JSON(consts.StatusInternalServerError, map[string]string{"error": defaultMessage})
	}
}

func toAssistantSessionResponse(session postgres.AssistantSession) assistantSessionResponse {
	return assistantSessionResponse{
		ID:            session.ID,
		Title:         session.Title,
		LastMessageAt: session.LastMessageAt,
		CreatedAt:     session.CreatedAt,
		UpdatedAt:     session.UpdatedAt,
	}
}

func toAssistantMessageResponses(messages []postgres.AssistantMessage) []assistantMessageResponse {
	response := make([]assistantMessageResponse, 0, len(messages))
	for _, message := range messages {
		response = append(response, assistantMessageResponse{
			ID:         message.ID,
			Role:       message.Role,
			Kind:       message.Kind,
			SequenceNo: message.SequenceNo,
			Payload:    json.RawMessage(message.Payload),
			CreatedAt:  message.CreatedAt,
		})
	}

	return response
}

func toAssistantResourceResponse(resource *postgres.Resource) *assistantResourceResponse {
	if resource == nil {
		return nil
	}

	return &assistantResourceResponse{
		ID:         resource.ID,
		Title:      resource.Title,
		SourceType: resource.SourceType,
	}
}

func toAssistantTaskResponse(task *postgres.Task) *assistantTaskResponse {
	if task == nil {
		return nil
	}

	return &assistantTaskResponse{
		ID:          task.ID,
		ResourceID:  task.ResourceID,
		Instruction: task.Instruction,
		Status:      task.Status,
		CreatedAt:   task.CreatedAt,
	}
}
