package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"agent_project/apps/server/internal/assistant"
	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"
)

type assistantService interface {
	ListSessions(ctx context.Context) ([]postgres.AssistantSession, error)
	GetConversation(ctx context.Context, sessionID string) (*assistant.ConversationResult, error)
	StartConversation(ctx context.Context, content string) (*assistant.ConversationResult, error)
	StartConversationStream(ctx context.Context, content string, emit func(assistant.StreamEvent) error) error
	AppendMessage(ctx context.Context, sessionID string, content string) (*assistant.ConversationResult, error)
	AppendMessageStream(ctx context.Context, sessionID string, content string, emit func(assistant.StreamEvent) error) error
	UploadFile(ctx context.Context, sessionID string, fileName string, content []byte) (*assistant.UploadFileResult, error)
	ConfirmTaskSuggestion(ctx context.Context, messageID string) (*assistant.ConfirmTaskResult, error)
	DeleteSession(ctx context.Context, sessionID string) (bool, error)
}

type assistantUploadPolicy interface {
	SupportsFileName(fileName string) bool
	SupportedExtensions() []string
	UnsupportedFileMessage(fileName string) string
}

const defaultAssistantUploadMaxBytes int64 = 20 * 1024 * 1024

// AssistantHandler 暴露助手会话列表、消息、文件和任务确认接口。
type AssistantHandler struct {
	service        assistantService
	uploadMaxBytes int64
	uploadPolicy   assistantUploadPolicy
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

type assistantCapabilitiesResponse struct {
	Upload assistantUploadCapabilitiesResponse `json:"upload"`
}

type assistantUploadCapabilitiesResponse struct {
	SupportedExtensions []string `json:"supported_extensions"`
	Accept              string   `json:"accept"`
	Hint                string   `json:"hint"`
}

type confirmTaskSuggestionResponse struct {
	Session      assistantSessionResponse   `json:"session"`
	Task         *assistantTaskResponse     `json:"task"`
	Messages     []assistantMessageResponse `json:"messages"`
	ErrorMessage *string                    `json:"error_message"`
}

type assistantStreamSessionResponse struct {
	Session assistantSessionResponse `json:"session"`
}

type assistantStreamMessageResponse struct {
	Message assistantMessageResponse `json:"message"`
}

type assistantStreamDeltaResponse struct {
	Delta string `json:"delta"`
}

type assistantStreamErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewAssistantHandler 创建助手 HTTP handler。
func NewAssistantHandler(service assistantService) *AssistantHandler {
	return NewAssistantHandlerWithUploadLimit(service, defaultAssistantUploadMaxBytes)
}

// NewAssistantHandlerWithUploadLimit 创建带上传大小限制的助手 HTTP handler。
func NewAssistantHandlerWithUploadLimit(service assistantService, maxBytes int64) *AssistantHandler {
	return NewAssistantHandlerWithUploadLimitAndPolicy(service, maxBytes, nil)
}

// NewAssistantHandlerWithUploadLimitAndPolicy 创建带上传大小限制和格式策略的助手 HTTP handler。
func NewAssistantHandlerWithUploadLimitAndPolicy(service assistantService, maxBytes int64, policy assistantUploadPolicy) *AssistantHandler {
	if maxBytes <= 0 {
		maxBytes = defaultAssistantUploadMaxBytes
	}
	if policy == nil {
		policy = defaultAssistantUploadPolicy()
	}

	return &AssistantHandler{
		service:        service,
		uploadMaxBytes: maxBytes,
		uploadPolicy:   policy,
	}
}

func defaultAssistantUploadPolicy() assistantUploadPolicy {
	policy, err := documentparser.New(documentparser.Options{Mode: documentparser.ModeText})
	if err != nil {
		return textOnlyAssistantUploadPolicy{}
	}

	return policy
}

type textOnlyAssistantUploadPolicy struct{}

func (textOnlyAssistantUploadPolicy) SupportsFileName(fileName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(fileName))
	return strings.HasSuffix(normalized, ".md") || strings.HasSuffix(normalized, ".txt")
}

func (textOnlyAssistantUploadPolicy) SupportedExtensions() []string {
	return []string{".md", ".txt"}
}

func (textOnlyAssistantUploadPolicy) UnsupportedFileMessage(string) string {
	return "当前服务仅支持 md、txt；pdf/docx 等文件需要启用 Tika 解析。"
}

func buildAssistantUploadCapabilitiesResponse(policy assistantUploadPolicy) assistantUploadCapabilitiesResponse {
	supportedExtensions := append([]string(nil), policy.SupportedExtensions()...)
	if supportedExtensions == nil {
		supportedExtensions = []string{}
	}

	return assistantUploadCapabilitiesResponse{
		SupportedExtensions: supportedExtensions,
		Accept:              strings.Join(supportedExtensions, ","),
		Hint:                buildAssistantUploadHint(supportedExtensions),
	}
}

func buildAssistantUploadHint(supportedExtensions []string) string {
	if len(supportedExtensions) == 0 {
		return "当前服务未开放文件上传"
	}

	labels := make([]string, 0, len(supportedExtensions))
	for _, extension := range supportedExtensions {
		normalized := strings.TrimSpace(strings.TrimPrefix(extension, "."))
		if normalized == "" {
			continue
		}
		labels = append(labels, normalized)
	}
	if len(labels) == 0 {
		return "当前服务未开放文件上传"
	}

	return "支持 " + strings.Join(labels, "、")
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

// GetCapabilities 返回助手页需要的上传能力声明。
func (h *AssistantHandler) GetCapabilities(_ context.Context, ctx *app.RequestContext) {
	ctx.JSON(consts.StatusOK, assistantCapabilitiesResponse{
		Upload: buildAssistantUploadCapabilitiesResponse(h.uploadPolicy),
	})
}

// GetConversation 返回一个会话及完整消息流。
func (h *AssistantHandler) GetConversation(requestCtx context.Context, ctx *app.RequestContext) {
	sessionID, ok := parseAssistantUUIDParam(ctx, "会话 ID")
	if !ok {
		return
	}

	result, err := h.service.GetConversation(requestCtx, sessionID)
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

// CreateConversationStream 用首条消息创建会话并返回 SSE 流。
func (h *AssistantHandler) CreateConversationStream(requestCtx context.Context, ctx *app.RequestContext) {
	var request assistantMessageRequest
	if err := json.Unmarshal(ctx.Request.Body(), &request); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "消息内容不能为空"})
		return
	}
	if strings.TrimSpace(request.Message) == "" {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": assistant.ErrMessageRequired.Error()})
		return
	}

	h.streamAssistantEvents(ctx, func(emit func(assistant.StreamEvent) error) error {
		return h.service.StartConversationStream(requestCtx, request.Message, emit)
	})
}

// AppendMessage 向已有会话追加消息。
func (h *AssistantHandler) AppendMessage(requestCtx context.Context, ctx *app.RequestContext) {
	var request assistantMessageRequest
	if err := json.Unmarshal(ctx.Request.Body(), &request); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "消息内容不能为空"})
		return
	}

	sessionID, ok := parseAssistantUUIDParam(ctx, "会话 ID")
	if !ok {
		return
	}

	result, err := h.service.AppendMessage(requestCtx, sessionID, request.Message)
	if err != nil {
		h.writeAssistantError(ctx, err, "追加消息失败")
		return
	}

	ctx.JSON(consts.StatusOK, assistantConversationResponse{
		Session:  toAssistantSessionResponse(result.Session),
		Messages: toAssistantMessageResponses(result.Messages),
	})
}

// AppendMessageStream 向已有会话追加消息并返回 SSE 流。
func (h *AssistantHandler) AppendMessageStream(requestCtx context.Context, ctx *app.RequestContext) {
	var request assistantMessageRequest
	if err := json.Unmarshal(ctx.Request.Body(), &request); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "消息内容不能为空"})
		return
	}
	if strings.TrimSpace(request.Message) == "" {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": assistant.ErrMessageRequired.Error()})
		return
	}

	sessionID, ok := parseAssistantUUIDParam(ctx, "会话 ID")
	if !ok {
		return
	}

	if _, err := h.service.GetConversation(requestCtx, sessionID); err != nil {
		h.writeAssistantError(ctx, err, "查询会话失败")
		return
	}

	h.streamAssistantEvents(ctx, func(emit func(assistant.StreamEvent) error) error {
		return h.service.AppendMessageStream(requestCtx, sessionID, request.Message, emit)
	})
}

// UploadFile 接收一个文件并写入资源库与当前会话。
func (h *AssistantHandler) UploadFile(requestCtx context.Context, ctx *app.RequestContext) {
	file, err := ctx.FormFile("file")
	if err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "必须上传文件"})
		return
	}
	if !h.uploadPolicy.SupportsFileName(file.Filename) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": h.uploadPolicy.UnsupportedFileMessage(file.Filename)})
		return
	}

	reader, err := file.Open()
	if err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "读取上传文件失败"})
		return
	}
	defer reader.Close()

	content, err := io.ReadAll(io.LimitReader(reader, h.uploadMaxBytes+1))
	if err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "读取上传文件失败"})
		return
	}
	if int64(len(content)) > h.uploadMaxBytes {
		ctx.JSON(consts.StatusRequestEntityTooLarge, map[string]string{"error": "上传文件过大"})
		return
	}

	sessionID, ok := parseAssistantUUIDParam(ctx, "会话 ID")
	if !ok {
		return
	}

	result, err := h.service.UploadFile(requestCtx, sessionID, file.Filename, content)
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
	messageID, ok := parseAssistantUUIDParam(ctx, "任务建议 ID")
	if !ok {
		return
	}

	result, err := h.service.ConfirmTaskSuggestion(requestCtx, messageID)
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
	sessionID, ok := parseAssistantUUIDParam(ctx, "会话 ID")
	if !ok {
		return
	}

	deleted, err := h.service.DeleteSession(requestCtx, sessionID)
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

func parseAssistantUUIDParam(ctx *app.RequestContext, field string) (string, bool) {
	id := strings.TrimSpace(ctx.Param("id"))
	if _, err := uuid.Parse(id); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": field + " 非法"})
		return "", false
	}

	return id, true
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

func (h *AssistantHandler) streamAssistantEvents(
	ctx *app.RequestContext,
	run func(emit func(assistant.StreamEvent) error) error,
) {
	reader, writer := io.Pipe()
	ctx.SetStatusCode(consts.StatusOK)
	ctx.SetBodyStream(reader, -1)
	ctx.Response.ImmediateHeaderFlush = true
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Connection", "keep-alive")
	ctx.SetContentType("text/event-stream; charset=utf-8")

	go func() {
		defer writer.Close()

		emit := func(event assistant.StreamEvent) error {
			if err := writeAssistantStreamEvent(writer, event); err != nil {
				if errors.Is(err, io.ErrClosedPipe) {
					return context.Canceled
				}
				return err
			}
			return nil
		}

		if err := run(emit); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			_ = writeAssistantStreamError(writer, assistant.NormalizeStreamError(err))
			return
		}

		_ = writeAssistantSSEEvent(writer, assistant.StreamEventDone, struct{}{})
	}()
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
		response = append(response, toAssistantMessageResponse(message))
	}

	return response
}

func toAssistantMessageResponse(message postgres.AssistantMessage) assistantMessageResponse {
	return assistantMessageResponse{
		ID:         message.ID,
		Role:       message.Role,
		Kind:       message.Kind,
		SequenceNo: message.SequenceNo,
		Payload:    json.RawMessage(message.Payload),
		CreatedAt:  message.CreatedAt,
	}
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

func writeAssistantStreamEvent(writer *io.PipeWriter, event assistant.StreamEvent) error {
	switch event.Type {
	case assistant.StreamEventSessionCreated:
		if event.Session == nil {
			return writeAssistantSSEEvent(writer, event.Type, struct{}{})
		}
		return writeAssistantSSEEvent(writer, event.Type, assistantStreamSessionResponse{
			Session: toAssistantSessionResponse(*event.Session),
		})
	case assistant.StreamEventMessageDelta:
		return writeAssistantSSEEvent(writer, event.Type, assistantStreamDeltaResponse{
			Delta: event.Delta,
		})
	case assistant.StreamEventSessionFile, assistant.StreamEventMessageCompleted, assistant.StreamEventTaskSuggestion:
		if event.Message == nil {
			return writeAssistantSSEEvent(writer, event.Type, struct{}{})
		}
		return writeAssistantSSEEvent(writer, event.Type, assistantStreamMessageResponse{
			Message: toAssistantMessageResponse(*event.Message),
		})
	case assistant.StreamEventMessageStarted:
		return writeAssistantSSEEvent(writer, event.Type, struct{}{})
	default:
		return writeAssistantSSEEvent(writer, event.Type, struct{}{})
	}
}

func writeAssistantStreamError(writer *io.PipeWriter, streamErr *assistant.StreamError) error {
	if streamErr == nil {
		streamErr = assistant.NewStreamError(
			assistant.StreamErrorCodeInternal,
			"助手暂时不可用，请稍后重试。",
			nil,
		)
	}

	return writeAssistantSSEEvent(writer, assistant.StreamEventError, assistantStreamErrorResponse{
		Code:    streamErr.Code,
		Message: streamErr.Message,
	})
}

func writeAssistantSSEEvent(writer *io.PipeWriter, eventType string, payload any) error {
	body := []byte("{}")
	if payload != nil {
		marshaled, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = marshaled
	}

	if _, err := io.WriteString(writer, "event: "+eventType+"\n"); err != nil {
		return err
	}
	if _, err := io.WriteString(writer, "data: "); err != nil {
		return err
	}
	if _, err := writer.Write(body); err != nil {
		return err
	}
	_, err := io.WriteString(writer, "\n\n")
	return err
}
