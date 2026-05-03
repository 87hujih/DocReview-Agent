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
	StartConversationWithFile(ctx context.Context, fileName string, content []byte) (*assistant.UploadFileResult, error)
	StartConversationStream(ctx context.Context, content string, emit func(assistant.StreamEvent) error) error
	AppendMessage(ctx context.Context, sessionID string, content string) (*assistant.ConversationResult, error)
	AppendMessageStream(ctx context.Context, sessionID string, content string, emit func(assistant.StreamEvent) error) error
	UploadFile(ctx context.Context, sessionID string, fileName string, content []byte) (*assistant.UploadFileResult, error)
	ConfirmTaskSuggestion(ctx context.Context, messageID string) (*assistant.ConfirmTaskResult, error)
	DeleteSession(ctx context.Context, sessionID string) (bool, error)
	ToggleWebSearch(ctx context.Context, sessionID string, enabled bool) (*postgres.AssistantSession, error)
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

// assistantMessageRequest 定义助手接口接收的 JSON 请求体，收口当前接口需要的输入字段。
type assistantMessageRequest struct {
	Message string `json:"message"`
}

// assistantSessionResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantSessionResponse struct {
	ID               string    `json:"id"`
	Title            string    `json:"title"`
	WebSearchEnabled bool      `json:"web_search_enabled"`
	LastMessageAt    time.Time `json:"last_message_at"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// assistantMessageResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantMessageResponse struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Kind       string          `json:"kind"`
	SequenceNo int             `json:"sequence_no"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

// assistantResourceResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantResourceResponse struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	SourceType string `json:"source_type"`
}

// assistantTaskResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantTaskResponse struct {
	ID          string    `json:"id"`
	ResourceID  string    `json:"resource_id"`
	Instruction string    `json:"instruction"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// listAssistantSessionsResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type listAssistantSessionsResponse struct {
	Sessions []assistantSessionResponse `json:"sessions"`
}

// assistantConversationResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantConversationResponse struct {
	Session  assistantSessionResponse   `json:"session"`
	Messages []assistantMessageResponse `json:"messages"`
}

// assistantUploadResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantUploadResponse struct {
	Session      assistantSessionResponse   `json:"session"`
	Resource     *assistantResourceResponse `json:"resource"`
	Messages     []assistantMessageResponse `json:"messages"`
	ErrorMessage *string                    `json:"error_message"`
}

// assistantCapabilitiesResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantCapabilitiesResponse struct {
	Upload assistantUploadCapabilitiesResponse `json:"upload"`
}

// assistantUploadCapabilitiesResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantUploadCapabilitiesResponse struct {
	SupportedExtensions []string `json:"supported_extensions"`
	Accept              string   `json:"accept"`
	Hint                string   `json:"hint"`
}

// assistantUploadInput 收口助手文件上传入口解析后的结果，避免在 handler 间重复传递临时字段。
type assistantUploadInput struct {
	FileName string
	Content  []byte
}

// confirmTaskSuggestionResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type confirmTaskSuggestionResponse struct {
	Session      assistantSessionResponse   `json:"session"`
	Task         *assistantTaskResponse     `json:"task"`
	Messages     []assistantMessageResponse `json:"messages"`
	ErrorMessage *string                    `json:"error_message"`
}

// assistantStreamSessionResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantStreamSessionResponse struct {
	Session assistantSessionResponse `json:"session"`
}

// assistantStreamMessageResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantStreamMessageResponse struct {
	Message assistantMessageResponse `json:"message"`
}

// assistantStreamDeltaResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
type assistantStreamDeltaResponse struct {
	Delta string `json:"delta"`
}

// assistantStreamErrorResponse 定义助手接口返回给前端的 JSON 结构，避免直接暴露内部模型。
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

// defaultAssistantUploadPolicy 根据当前解析能力选择默认上传策略，在未启用 Tika 时自动退回到纯文本上传约束。
func defaultAssistantUploadPolicy() assistantUploadPolicy {
	policy, err := documentparser.New(documentparser.Options{Mode: documentparser.ModeText})
	if err != nil {
		return textOnlyAssistantUploadPolicy{}
	}

	return policy
}

// textOnlyAssistantUploadPolicy 定义仅允许纯文本文件上传的策略实现，用于在未启用富文档解析时限制助手上传入口。
type textOnlyAssistantUploadPolicy struct{}

// SupportsFileName 判断文件名是否属于当前上传策略允许的纯文本扩展名。
func (textOnlyAssistantUploadPolicy) SupportsFileName(fileName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(fileName))
	return strings.HasSuffix(normalized, ".md") || strings.HasSuffix(normalized, ".txt")
}

// SupportedExtensions 返回纯文本上传策略允许的扩展名列表，供接口响应和前端提示直接复用。
func (textOnlyAssistantUploadPolicy) SupportedExtensions() []string {
	return []string{".md", ".txt"}
}

// UnsupportedFileMessage 为纯文本上传策略生成统一错误提示，明确告知用户当前允许的文件类型。
func (textOnlyAssistantUploadPolicy) UnsupportedFileMessage(string) string {
	return "当前服务仅支持 md、txt；pdf/docx 等文件需要启用 Tika 解析。"
}

// buildAssistantUploadCapabilitiesResponse 把上传策略转换成前端可消费的能力描述，确保支持格式和错误提示与后端校验一致。
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

// buildAssistantUploadHint 根据允许上传的扩展名生成前端提示文案，避免页面展示能力和后端校验规则不一致。
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

// UploadConversationFile 在空白草稿会话中直接上传文件，并由服务端创建空会话。
func (h *AssistantHandler) UploadConversationFile(requestCtx context.Context, ctx *app.RequestContext) {
	input, ok := h.readAssistantUploadInput(ctx)
	if !ok {
		return
	}

	result, err := h.service.StartConversationWithFile(requestCtx, input.FileName, input.Content)
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

// UploadFile 接收一个文件并写入资源库与当前会话。
func (h *AssistantHandler) UploadFile(requestCtx context.Context, ctx *app.RequestContext) {
	input, ok := h.readAssistantUploadInput(ctx)
	if !ok {
		return
	}

	sessionID, ok := parseAssistantUUIDParam(ctx, "会话 ID")
	if !ok {
		return
	}

	result, err := h.service.UploadFile(requestCtx, sessionID, input.FileName, input.Content)
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

// readAssistantUploadInput 统一解析助手上传接口的 multipart 文件输入，并执行格式与大小校验。
func (h *AssistantHandler) readAssistantUploadInput(ctx *app.RequestContext) (*assistantUploadInput, bool) {
	file, err := ctx.FormFile("file")
	if err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "必须上传文件"})
		return nil, false
	}
	if !h.uploadPolicy.SupportsFileName(file.Filename) {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": h.uploadPolicy.UnsupportedFileMessage(file.Filename)})
		return nil, false
	}

	reader, err := file.Open()
	if err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "读取上传文件失败"})
		return nil, false
	}
	defer reader.Close()

	content, err := io.ReadAll(io.LimitReader(reader, h.uploadMaxBytes+1))
	if err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "读取上传文件失败"})
		return nil, false
	}
	if int64(len(content)) > h.uploadMaxBytes {
		ctx.JSON(consts.StatusRequestEntityTooLarge, map[string]string{"error": "上传文件过大"})
		return nil, false
	}

	return &assistantUploadInput{
		FileName: file.Filename,
		Content:  content,
	}, true
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

// ToggleWebSearch 更新会话的联网搜索开关状态。
func (h *AssistantHandler) ToggleWebSearch(requestCtx context.Context, ctx *app.RequestContext) {
	var request struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(ctx.Request.Body(), &request); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": "请求体格式错误"})
		return
	}

	session, err := h.service.ToggleWebSearch(requestCtx, ctx.Param("id"), request.Enabled)
	if err != nil {
		h.writeAssistantError(ctx, err, "更新联网状态失败")
		return
	}

	ctx.JSON(consts.StatusOK, toAssistantSessionResponse(*session))
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

// parseAssistantUUIDParam 解析并校验助手相关路由参数中的 UUID，把参数错误收口到 handler 边界。
func parseAssistantUUIDParam(ctx *app.RequestContext, field string) (string, bool) {
	id := strings.TrimSpace(ctx.Param("id"))
	if _, err := uuid.Parse(id); err != nil {
		ctx.JSON(consts.StatusBadRequest, map[string]string{"error": field + " 非法"})
		return "", false
	}

	return id, true
}

// writeAssistantError 把助手领域错误统一转换成 HTTP 响应，避免各个 handler 分散维护错误映射。
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

// streamAssistantEvents 把助手流式事件桥接到 SSE 响应，统一会话创建、增量回复和错误事件的写出顺序。
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

// toAssistantSessionResponse 把 `助手会话响应` 转换成助手接口需要的结构，避免上层直接感知内部模型。
func toAssistantSessionResponse(session postgres.AssistantSession) assistantSessionResponse {
	return assistantSessionResponse{
		ID:               session.ID,
		Title:            session.Title,
		WebSearchEnabled: session.WebSearchEnabled,
		LastMessageAt:    session.LastMessageAt,
		CreatedAt:        session.CreatedAt,
		UpdatedAt:        session.UpdatedAt,
	}
}

// toAssistantMessageResponses 把 `助手消息Responses` 转换成助手接口需要的结构，避免上层直接感知内部模型。
func toAssistantMessageResponses(messages []postgres.AssistantMessage) []assistantMessageResponse {
	response := make([]assistantMessageResponse, 0, len(messages))
	for _, message := range messages {
		response = append(response, toAssistantMessageResponse(message))
	}

	return response
}

// toAssistantMessageResponse 把 `助手消息响应` 转换成助手接口需要的结构，避免上层直接感知内部模型。
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

// toAssistantResourceResponse 把 `助手资源响应` 转换成助手接口需要的结构，避免上层直接感知内部模型。
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

// toAssistantTaskResponse 把 `助手任务响应` 转换成助手接口需要的结构，避免上层直接感知内部模型。
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

// writeAssistantStreamEvent 按事件类型把助手流式消息编码到 SSE，保持前端消费协议稳定。
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

// writeAssistantStreamError 把流式链路中的领域错误转换成 SSE error 事件，避免连接直接以裸错误中断。
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

// writeAssistantSSEEvent 向响应流写出单条 SSE 事件并立即刷新，确保前端能及时收到状态变化。
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
