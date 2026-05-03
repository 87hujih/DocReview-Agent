package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"agent_project/apps/server/internal/assistant/websearch"
	"agent_project/apps/server/internal/knowledge/citation"
	"agent_project/apps/server/internal/storage/filestore"
	"agent_project/apps/server/internal/storage/postgres"
)

var (
	// ErrFileContentRequired 表示上传文件内容为空。
	ErrFileContentRequired = errors.New("文件内容不能为空")
	// ErrFileNameRequired 表示上传文件缺少文件名。
	ErrFileNameRequired = errors.New("文件名不能为空")
	// ErrMessageRequired 表示用户消息为空。
	ErrMessageRequired = errors.New("消息内容不能为空")
	// ErrSessionNotFound 表示目标会话不存在。
	ErrSessionNotFound = errors.New("会话不存在")
	// ErrTaskSuggestionNotFound 表示目标任务建议不存在。
	ErrTaskSuggestionNotFound = errors.New("任务建议不存在")
)

type sessionRepository interface {
	ListSessions(ctx context.Context) ([]postgres.AssistantSession, error)
	GetSessionByID(ctx context.Context, id string) (*postgres.AssistantSession, error)
	ListMessages(ctx context.Context, sessionID string) ([]postgres.AssistantMessage, error)
	GetMessageByID(ctx context.Context, id string) (*postgres.AssistantMessage, error)
	CreateSessionWithMessages(ctx context.Context, title string, inputs []postgres.AssistantMessageInput) (*postgres.AssistantSession, []postgres.AssistantMessage, error)
	AppendMessages(ctx context.Context, sessionID string, inputs []postgres.AssistantMessageInput) ([]postgres.AssistantMessage, error)
	DeleteSession(ctx context.Context, id string) (bool, error)
	UpdateSessionWebSearchEnabled(ctx context.Context, sessionID string, enabled bool) error
}

type documentImporter interface {
	ImportDocument(ctx context.Context, input ImportDocumentInput) (*ImportDocumentResult, error)
}

type taskCreator interface {
	CreateTask(ctx context.Context, resourceID string, instruction string) (*postgres.Task, error)
}

type resourceCitationRetriever interface {
	SearchByResource(ctx context.Context, resourceID string, query string, limit int) ([]citation.Citation, error)
}

type uploadedFileStore interface {
	Save(ctx context.Context, originalName string, content []byte) (*filestore.StoredFile, error)
}

type uploadedFileRepository interface {
	Create(ctx context.Context, input postgres.UploadedFileCreateParams) (*postgres.UploadedFile, error)
	UpdateResourceID(ctx context.Context, fileID string, resourceID string) error
}

// taskArtifactAppender 允许 assistant 服务在任务确认后写入产物（如联网证据溯源）。
type taskArtifactAppender interface {
	AddArtifact(ctx context.Context, taskID string, artifactType string, content []byte) (*postgres.TaskArtifact, error)
}

// ServiceOption 允许按需注入上传原文件存储等扩展能力。
type ServiceOption func(*Service)

// Service 负责会话消息流、文件导入和任务确认。
type Service struct {
	importer         documentImporter
	repo             sessionRepository
	retriever        resourceCitationRetriever
	responder        chatResponder
	tasks            taskCreator
	fileStore        uploadedFileStore
	uploadedFiles    uploadedFileRepository
	webSearch        websearch.WebSearchProvider
	artifactAppender taskArtifactAppender
}

// NewService 构造助手领域服务。
func NewService(
	repo sessionRepository,
	importer documentImporter,
	tasks taskCreator,
	responder chatResponder,
	retriever resourceCitationRetriever,
	options ...ServiceOption,
) *Service {
	service := &Service{
		importer:  importer,
		repo:      repo,
		retriever: retriever,
		responder: responder,
		tasks:     tasks,
	}

	for _, option := range options {
		if option != nil {
			option(service)
		}
	}

	return service
}

// WithUploadedFileStorage 为上传接口启用原文件落盘和元数据持久化。
func WithUploadedFileStorage(store uploadedFileStore, repo uploadedFileRepository) ServiceOption {
	return func(service *Service) {
		service.fileStore = store
		service.uploadedFiles = repo
	}
}

// WithWebSearchProvider 注入联网搜索 provider，默认为 nil（即禁用联网搜索）。
func WithWebSearchProvider(provider websearch.WebSearchProvider) ServiceOption {
	return func(service *Service) {
		service.webSearch = provider
	}
}

// WithTaskArtifactAppender 注入任务产物写入器，用于在任务确认后保存联网证据摘要。
func WithTaskArtifactAppender(appender taskArtifactAppender) ServiceOption {
	return func(service *Service) {
		service.artifactAppender = appender
	}
}

// ListSessions 返回会话列表。
func (s *Service) ListSessions(ctx context.Context) ([]postgres.AssistantSession, error) {
	return s.repo.ListSessions(ctx)
}

// GetConversation 返回单个会话及其消息流。
func (s *Service) GetConversation(ctx context.Context, sessionID string) (*ConversationResult, error) {
	session, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrSessionNotFound
	}

	messages, err := s.repo.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	return &ConversationResult{
		Session:  *session,
		Messages: messages,
	}, nil
}

// StartConversation 用首条用户消息创建真实会话。
func (s *Service) StartConversation(ctx context.Context, content string) (*ConversationResult, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, ErrMessageRequired
	}

	inputs, err := s.buildReplyInputs(ctx, nil, trimmed, nil, false)
	if err != nil {
		return nil, err
	}

	session, messages, err := s.repo.CreateSessionWithMessages(ctx, buildSessionTitle(trimmed), inputs)
	if err != nil {
		return nil, err
	}

	return &ConversationResult{
		Session:  *session,
		Messages: messages,
	}, nil
}

// AppendMessage 向已有会话追加一轮用户消息及回复。
func (s *Service) AppendMessage(ctx context.Context, sessionID string, content string) (*ConversationResult, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, ErrMessageRequired
	}

	session, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrSessionNotFound
	}

	history, err := s.repo.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	resourceContext, err := latestResourceFromMessages(history)
	if err != nil {
		return nil, err
	}

	inputs, err := s.buildReplyInputs(ctx, history, trimmed, resourceContext, session.WebSearchEnabled)
	if err != nil {
		return nil, err
	}

	messages, err := s.repo.AppendMessages(ctx, sessionID, inputs)
	if err != nil {
		return nil, err
	}

	updatedSession, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if updatedSession == nil {
		return nil, ErrSessionNotFound
	}

	return &ConversationResult{
		Session:  *updatedSession,
		Messages: messages,
	}, nil
}

// StartConversationStream 用首条用户消息创建真实会话，并以流式方式返回 assistant 回复。
func (s *Service) StartConversationStream(ctx context.Context, content string, emit func(StreamEvent) error) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ErrMessageRequired
	}

	userInput, err := buildUserTextInput(trimmed)
	if err != nil {
		return err
	}

	session, _, err := s.repo.CreateSessionWithMessages(ctx, buildSessionTitle(trimmed), []postgres.AssistantMessageInput{userInput})
	if err != nil {
		return err
	}

	if err := emit(StreamEvent{
		Type:    StreamEventSessionCreated,
		Session: session,
	}); err != nil {
		return err
	}

	return s.streamAssistantReply(ctx, streamAssistantReplyInput{
		content:   trimmed,
		emit:      emit,
		sessionID: session.ID,
	})
}

// AppendMessageStream 向已有会话追加一条用户消息，并以流式方式返回 assistant 回复。
func (s *Service) AppendMessageStream(ctx context.Context, sessionID string, content string, emit func(StreamEvent) error) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ErrMessageRequired
	}

	session, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if session == nil {
		return ErrSessionNotFound
	}

	history, err := s.repo.ListMessages(ctx, sessionID)
	if err != nil {
		return err
	}

	resourceContext, err := latestResourceFromMessages(history)
	if err != nil {
		return err
	}

	userInput, err := buildUserTextInput(trimmed)
	if err != nil {
		return err
	}

	if _, err := s.repo.AppendMessages(ctx, sessionID, []postgres.AssistantMessageInput{userInput}); err != nil {
		return err
	}

	return s.streamAssistantReply(ctx, streamAssistantReplyInput{
		content:          trimmed,
		emit:             emit,
		history:          history,
		resource:         resourceContext,
		sessionID:        sessionID,
		webSearchEnabled: session.WebSearchEnabled,
	})
}

// UploadFile 把文件导入资源库，并在会话中写入一条结构化文件消息。
func (s *Service) UploadFile(ctx context.Context, sessionID string, fileName string, content []byte) (*UploadFileResult, error) {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return nil, ErrFileNameRequired
	}
	if len(content) == 0 {
		return nil, ErrFileContentRequired
	}

	session, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrSessionNotFound
	}

	uploadedFile, err := s.persistUploadedFile(ctx, session.ID, fileName, content)
	if err != nil {
		return nil, err
	}

	resource, appendInputs, errorMessage, err := s.buildUploadMessages(ctx, fileName, content, uploadedFile)
	if err != nil {
		return nil, err
	}
	if resource != nil && uploadedFile != nil {
		if err := s.uploadedFiles.UpdateResourceID(ctx, uploadedFile.ID, resource.ID); err != nil {
			return nil, err
		}
	}

	messages, err := s.repo.AppendMessages(ctx, sessionID, appendInputs)
	if err != nil {
		return nil, err
	}

	updatedSession, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if updatedSession == nil {
		return nil, ErrSessionNotFound
	}

	return &UploadFileResult{
		Session:      *updatedSession,
		Resource:     resource,
		Messages:     messages,
		ErrorMessage: errorMessage,
	}, nil
}

func (s *Service) persistUploadedFile(ctx context.Context, sessionID string, fileName string, content []byte) (*postgres.UploadedFile, error) {
	if s.fileStore == nil && s.uploadedFiles == nil {
		return nil, nil
	}
	if s.fileStore == nil || s.uploadedFiles == nil {
		return nil, errors.New("原文件存储未完整配置")
	}

	stored, err := s.fileStore.Save(ctx, fileName, content)
	if err != nil {
		return nil, err
	}

	return s.uploadedFiles.Create(ctx, postgres.UploadedFileCreateParams{
		SessionID:        stringPointer(sessionID),
		OriginalFilename: fileName,
		ContentType:      http.DetectContentType(content),
		SizeBytes:        stored.SizeBytes,
		SHA256:           stored.SHA256,
		StorageKey:       stored.StorageKey,
	})
}

// ConfirmTaskSuggestion 根据任务建议创建真实任务，并把结果回写到会话。
func (s *Service) ConfirmTaskSuggestion(ctx context.Context, messageID string) (*ConfirmTaskResult, error) {
	message, err := s.repo.GetMessageByID(ctx, messageID)
	if err != nil {
		return nil, err
	}
	if message == nil || message.Kind != KindTaskSuggestion {
		return nil, ErrTaskSuggestionNotFound
	}

	session, err := s.repo.GetSessionByID(ctx, message.SessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrSessionNotFound
	}

	suggestion, err := decodeTaskSuggestion(message.Payload)
	if err != nil {
		return nil, err
	}

	if !suggestion.CanCreate || suggestion.ResourceID == nil || strings.TrimSpace(*suggestion.ResourceID) == "" {
		errorMessage := "当前建议缺少可执行资源，请先上传文件后再创建任务。"
		messages, appendErr := s.appendSystemMessage(ctx, message.SessionID, errorMessage)
		if appendErr != nil {
			return nil, appendErr
		}

		updatedSession, sessionErr := s.repo.GetSessionByID(ctx, message.SessionID)
		if sessionErr != nil {
			return nil, sessionErr
		}
		if updatedSession == nil {
			return nil, ErrSessionNotFound
		}

		return &ConfirmTaskResult{
			Session:      *updatedSession,
			Messages:     messages,
			ErrorMessage: stringPointer(errorMessage),
		}, nil
	}

	task, taskErr := s.tasks.CreateTask(ctx, *suggestion.ResourceID, suggestion.Instruction)
	if taskErr != nil {
		errorMessage := fmt.Sprintf("任务创建失败：%v", taskErr)
		messages, appendErr := s.appendSystemMessage(ctx, message.SessionID, errorMessage)
		if appendErr != nil {
			return nil, appendErr
		}

		updatedSession, sessionErr := s.repo.GetSessionByID(ctx, message.SessionID)
		if sessionErr != nil {
			return nil, sessionErr
		}
		if updatedSession == nil {
			return nil, ErrSessionNotFound
		}

		return &ConfirmTaskResult{
			Session:      *updatedSession,
			Messages:     messages,
			ErrorMessage: stringPointer(errorMessage),
		}, nil
	}

	// 尽力而为：把本会话中最近搜索过的联网证据写入任务产物，供审批溯源。
	s.saveWebEvidenceArtifact(ctx, task.ID, message.SessionID)

	input, err := buildMessageInput(RoleAssistant, KindTaskCreated, TaskCreatedPayload{
		DetailURL:           "/tasks/" + task.ID,
		Instruction:         task.Instruction,
		ResourceID:          task.ResourceID,
		Status:              task.Status,
		SuggestionMessageID: message.ID,
		TaskID:              task.ID,
	})
	if err != nil {
		return nil, err
	}

	messages, err := s.repo.AppendMessages(ctx, message.SessionID, []postgres.AssistantMessageInput{input})
	if err != nil {
		return nil, err
	}

	updatedSession, err := s.repo.GetSessionByID(ctx, message.SessionID)
	if err != nil {
		return nil, err
	}
	if updatedSession == nil {
		return nil, ErrSessionNotFound
	}

	return &ConfirmTaskResult{
		Session:  *updatedSession,
		Task:     task,
		Messages: messages,
	}, nil
}

// saveWebEvidenceArtifact 从会话历史消息中提取最近的联网搜索证据，写入任务产物。
// 失败时记录日志但不影响主流程。
func (s *Service) saveWebEvidenceArtifact(ctx context.Context, taskID string, sessionID string) {
	if s.artifactAppender == nil {
		return
	}

	sessionMessages, err := s.repo.ListMessages(ctx, sessionID)
	if err != nil {
		slog.WarnContext(ctx, "web evidence: failed to list session messages",
			slog.String("session_id", sessionID),
			slog.String("error", err.Error()),
		)
		return
	}

	// 从最近的助手文本消息中收集联网证据（最多往前扫描 20 条）
	var allSources []WebSearchSource
	var queries []string
	var provider string
	seen := make(map[string]struct{})
	scanLimit := 20
	scanned := 0

	for i := len(sessionMessages) - 1; i >= 0 && scanned < scanLimit; i-- {
		msg := sessionMessages[i]
		if msg.Kind != KindText || msg.Role != RoleAssistant {
			scanned++
			continue
		}
		payload, parseErr := unmarshalTextPayload(msg.Payload)
		if parseErr != nil || payload.WebSearch == nil {
			scanned++
			continue
		}
		ws := payload.WebSearch
		if ws.Status != "searched_sufficient" || len(ws.Sources) == 0 {
			scanned++
			continue
		}
		if provider == "" {
			provider = ws.Provider
		}
		for _, q := range ws.Queries {
			if _, dup := seen["q:"+q]; !dup {
				seen["q:"+q] = struct{}{}
				queries = append(queries, q)
			}
		}
		for _, src := range ws.Sources {
			key := normalizeSourceURL(src.URL)
			if _, dup := seen["u:"+key]; dup {
				continue
			}
			seen["u:"+key] = struct{}{}
			allSources = append(allSources, src)
		}
		scanned++
	}

	if len(allSources) == 0 {
		return
	}

	artifact := WebEvidenceArtifact{
		Queries:  queries,
		Provider: provider,
		Sources:  allSources,
	}
	body, err := json.Marshal(artifact)
	if err != nil {
		return
	}

	if _, err := s.artifactAppender.AddArtifact(ctx, taskID, "web_evidence", body); err != nil {
		slog.WarnContext(ctx, "web evidence: failed to save artifact",
			slog.String("task_id", taskID),
			slog.String("error", err.Error()),
		)
	}
}

func normalizeSourceURL(raw string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(raw), "/"))
}

// DeleteSession 删除一个会话。
func (s *Service) DeleteSession(ctx context.Context, sessionID string) (bool, error) {
	return s.repo.DeleteSession(ctx, sessionID)
}

// ToggleWebSearch 更新会话的联网搜索开关，并返回更新后的会话。
func (s *Service) ToggleWebSearch(ctx context.Context, sessionID string, enabled bool) (*postgres.AssistantSession, error) {
	session, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, ErrSessionNotFound
	}

	if err := s.repo.UpdateSessionWebSearchEnabled(ctx, sessionID, enabled); err != nil {
		return nil, err
	}

	updated, err := s.repo.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, ErrSessionNotFound
	}

	return updated, nil
}

func (s *Service) buildReplyInputs(
	ctx context.Context,
	history []postgres.AssistantMessage,
	content string,
	resource *resourceContext,
	webSearchEnabled bool,
) ([]postgres.AssistantMessageInput, error) {
	userInput, err := buildUserTextInput(content)
	if err != nil {
		return nil, err
	}

	if s.responder == nil {
		return nil, errors.New("助手对话模型未配置")
	}

	citations, err := s.loadResourceCitations(ctx, resource, content)
	if err != nil {
		return nil, err
	}

	webSearchState := s.runWebSearchChain(ctx, content, history, webSearchEnabled)

	reply, err := s.responder.Reply(ctx, ChatCompletionInput{
		Citations: citations,
		History:   history,
		Message:   content,
		Resource:  resource,
		WebSearch: webSearchState,
	})
	if err != nil {
		return nil, err
	}

	assistantInputs, err := buildAssistantReplyInputs(content, reply, resource, webSearchState)
	if err != nil {
		return nil, err
	}

	return append([]postgres.AssistantMessageInput{userInput}, assistantInputs...), nil
}

func (s *Service) buildUploadMessages(
	ctx context.Context,
	fileName string,
	content []byte,
	uploadedFile *postgres.UploadedFile,
) (*postgres.Resource, []postgres.AssistantMessageInput, *string, error) {
	if s.importer == nil {
		return nil, nil, nil, errors.New("文档导入器未配置")
	}

	result, err := s.importer.ImportDocument(ctx, ImportDocumentInput{
		FileName: fileName,
		Content:  content,
	})
	if err != nil {
		message := fmt.Sprintf("文件导入失败：%v", err)
		systemInput, buildErr := buildMessageInput(RoleAssistant, KindSystem, SystemPayload{
			Content: message,
			Level:   "error",
		})
		if buildErr != nil {
			return nil, nil, nil, buildErr
		}

		return nil, []postgres.AssistantMessageInput{systemInput}, stringPointer(message), nil
	}

	payload := SessionFilePayload{
		FileName:      fileName,
		ResourceID:    result.Resource.ID,
		ResourceTitle: result.Resource.Title,
		SourceType:    result.Resource.SourceType,
		Status:        "ready",
	}
	if uploadedFile != nil {
		payload.FileID = uploadedFile.ID
	}

	fileInput, err := buildMessageInput(RoleAssistant, KindSessionFile, payload)
	if err != nil {
		return nil, nil, nil, err
	}

	return result.Resource, []postgres.AssistantMessageInput{fileInput}, nil, nil
}

func (s *Service) appendSystemMessage(ctx context.Context, sessionID string, content string) ([]postgres.AssistantMessage, error) {
	input, err := buildMessageInput(RoleAssistant, KindSystem, SystemPayload{
		Content: content,
		Level:   "error",
	})
	if err != nil {
		return nil, err
	}

	return s.repo.AppendMessages(ctx, sessionID, []postgres.AssistantMessageInput{input})
}

func buildMessageInput(role string, kind string, payload any) (postgres.AssistantMessageInput, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return postgres.AssistantMessageInput{}, err
	}

	return postgres.AssistantMessageInput{
		Role:    role,
		Kind:    kind,
		Payload: body,
	}, nil
}

func buildUserTextInput(content string) (postgres.AssistantMessageInput, error) {
	return buildMessageInput(RoleUser, KindText, TextPayload{Content: content})
}

func buildAssistantReplyInputs(
	content string,
	reply *ChatCompletionResult,
	resource *resourceContext,
	webSearch *WebSearchState,
) ([]postgres.AssistantMessageInput, error) {
	if reply == nil || strings.TrimSpace(reply.Reply) == "" {
		return nil, errors.New("助手模型返回了空回复")
	}

	assistantInput, err := buildMessageInput(RoleAssistant, KindText, TextPayload{
		Content:   strings.TrimSpace(reply.Reply),
		WebSearch: webSearchStateToSummary(webSearch),
	})
	if err != nil {
		return nil, err
	}

	inputs := []postgres.AssistantMessageInput{assistantInput}
	taskInstruction := resolveTaskInstruction(content, reply.TaskInstruction)
	if taskInstruction == "" {
		return inputs, nil
	}

	suggestionInput, err := buildMessageInput(
		RoleAssistant,
		KindTaskSuggestion,
		buildTaskSuggestion(taskInstruction, resource),
	)
	if err != nil {
		return nil, err
	}

	return append(inputs, suggestionInput), nil
}

type resourceContext struct {
	ID     string
	Source string
	Title  string
}

func latestResourceFromMessages(messages []postgres.AssistantMessage) (*resourceContext, error) {
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Kind != KindSessionFile {
			continue
		}

		payload, err := decodeSessionFile(message.Payload)
		if err != nil {
			return nil, err
		}
		if payload.Status != "ready" {
			continue
		}

		return &resourceContext{
			ID:     payload.ResourceID,
			Source: payload.SourceType,
			Title:  payload.ResourceTitle,
		}, nil
	}

	return nil, nil
}

func buildTaskSuggestion(content string, resource *resourceContext) TaskSuggestionPayload {
	suggestion := TaskSuggestionPayload{
		ActionLabel: "确认创建任务",
		CanCreate:   resource != nil,
		Instruction: content,
		Title:       "建议创建任务",
	}

	if resource == nil {
		suggestion.ResourceLabel = "资源未明确"
		suggestion.StatusMessage = "还没有明确可执行的资源，请先上传文件或继续明确材料。"
		return suggestion
	}

	suggestion.ResourceID = stringPointer(resource.ID)
	suggestion.ResourceLabel = resource.Title + " · " + resource.Source
	suggestion.StatusMessage = "资源已明确，可以创建任务。"
	return suggestion
}

func buildSessionTitle(content string) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= 24 {
		return string(runes)
	}

	return string(runes[:24]) + "..."
}

func decodeTaskSuggestion(payload []byte) (TaskSuggestionPayload, error) {
	var value TaskSuggestionPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return TaskSuggestionPayload{}, err
	}

	return value, nil
}

func decodeSessionFile(payload []byte) (SessionFilePayload, error) {
	var value SessionFilePayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return SessionFilePayload{}, err
	}

	return value, nil
}

func resolveTaskInstruction(content string, modelInstruction *string) string {
	if modelInstruction != nil {
		trimmed := strings.TrimSpace(*modelInstruction)
		if trimmed != "" {
			return trimmed
		}
	}

	if hasTaskIntent(content) {
		return strings.TrimSpace(content)
	}

	return ""
}

func (s *Service) loadResourceCitations(
	ctx context.Context,
	resource *resourceContext,
	query string,
) ([]citation.Citation, error) {
	if s.retriever == nil || resource == nil {
		return nil, nil
	}

	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil, nil
	}

	return s.retriever.SearchByResource(ctx, resource.ID, trimmedQuery, 4)
}

func hasTaskIntent(content string) bool {
	keywords := []string{"审阅", "修改", "检查", "优化", "修订", "总结", "整理"}
	for _, keyword := range keywords {
		if strings.Contains(content, keyword) {
			return true
		}
	}

	return false
}

func stringPointer(value string) *string {
	return &value
}

type streamAssistantReplyInput struct {
	content          string
	emit             func(StreamEvent) error
	history          []postgres.AssistantMessage
	resource         *resourceContext
	sessionID        string
	webSearchEnabled bool
}

func (s *Service) streamAssistantReply(ctx context.Context, input streamAssistantReplyInput) error {
	if s.responder == nil {
		return errors.New("助手对话模型未配置")
	}

	citations, err := s.loadResourceCitations(ctx, input.resource, input.content)
	if err != nil {
		return err
	}

	webSearchState := s.runWebSearchChain(ctx, input.content, input.history, input.webSearchEnabled)

	stream, err := s.responder.Stream(ctx, ChatCompletionInput{
		Citations: citations,
		History:   input.history,
		Message:   input.content,
		Resource:  input.resource,
		WebSearch: webSearchState,
	})
	if err != nil {
		return mapAssistantStreamError(err)
	}
	defer stream.Close()

	if err := input.emit(StreamEvent{Type: StreamEventMessageStarted}); err != nil {
		return err
	}

	var replyBuilder strings.Builder
	for {
		delta, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				break
			}

			return mapAssistantStreamError(recvErr)
		}

		if delta == "" {
			continue
		}

		replyBuilder.WriteString(delta)
		if err := input.emit(StreamEvent{
			Type:  StreamEventMessageDelta,
			Delta: delta,
		}); err != nil {
			return err
		}
	}

	result := &ChatCompletionResult{Reply: replyBuilder.String()}
	if provider, ok := stream.(chatStreamResultProvider); ok {
		finalResult, resultErr := provider.Result()
		if resultErr != nil {
			return mapAssistantStreamError(resultErr)
		}
		if finalResult != nil {
			result = finalResult
		}
	}

	assistantInputs, err := buildAssistantReplyInputs(input.content, result, input.resource, webSearchState)
	if err != nil {
		return NewStreamError(StreamErrorCodeEmptyReply, "本轮没有生成可展示内容。", err)
	}

	messages, err := s.repo.AppendMessages(ctx, input.sessionID, assistantInputs)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		return NewStreamError(StreamErrorCodeInternal, "助手暂时不可用，请稍后重试。", errors.New("assistant stream persisted no messages"))
	}

	if err := input.emit(StreamEvent{
		Type:    StreamEventMessageCompleted,
		Message: &messages[0],
	}); err != nil {
		return err
	}

	for index := 1; index < len(messages); index++ {
		if messages[index].Kind != KindTaskSuggestion {
			continue
		}

		if err := input.emit(StreamEvent{
			Type:    StreamEventTaskSuggestion,
			Message: &messages[index],
		}); err != nil {
			return err
		}
	}

	return nil
}

// runWebSearchChain 执行联网搜索决策链：门控 → query 规划 → 隐私检查 → 外部搜索 → 结果排序净化。
func (s *Service) runWebSearchChain(ctx context.Context, content string, history []postgres.AssistantMessage, enabled bool) *WebSearchState {
	if !enabled || s.webSearch == nil {
		return &WebSearchState{Status: "unused"}
	}

	gateResult := websearch.AssessWebSearchNeed(content, history)
	if !gateResult.Needed {
		return &WebSearchState{Allowed: true, Status: "skipped_not_needed"}
	}

	queries := websearch.PlanSearchQuery(content)
	if len(queries) == 0 {
		return &WebSearchState{Allowed: true, Needed: true, Status: "skipped_not_needed"}
	}

	privacyResult := websearch.CheckQueryPrivacy(queries)
	if privacyResult.Blocked {
		slog.WarnContext(ctx, "web search blocked by privacy check",
			slog.String("reason", privacyResult.Reason),
		)
		return &WebSearchState{Allowed: true, Needed: true, Status: "blocked_by_privacy"}
	}

	searchQuery := strings.Join(queries, " ")
	resp, err := s.webSearch.Search(ctx, searchQuery, websearch.SearchOptions{Limit: 5})
	if err != nil {
		slog.WarnContext(ctx, "web search failed",
			slog.String("query", searchQuery),
			slog.String("error", err.Error()),
		)
		return &WebSearchState{
			Allowed: true,
			Needed:  true,
			Queries: queries,
			Status:  "search_failed",
		}
	}

	// 排序去重 → snippet 净化 → 可信度标注
	ranked := websearch.RankAndDedup(resp.Results)
	sources := make([]WebSearchSource, 0, len(ranked))
	for _, r := range ranked {
		sources = append(sources, WebSearchSource{
			Title:           r.Title,
			URL:             r.URL,
			Snippet:         websearch.SanitizeSnippet(r.Snippet),
			ReliabilityHint: websearch.DomainReliabilityHint(r.URL),
		})
	}

	slog.InfoContext(ctx, "web search completed",
		slog.String("provider", resp.Provider),
		slog.String("queries", strings.Join(queries, "|")),
		slog.Int("sources", len(sources)),
	)

	return &WebSearchState{
		Allowed:  true,
		Needed:   true,
		Used:     true,
		Queries:  queries,
		Provider: resp.Provider,
		Status:   "searched_sufficient",
		Sources:  sources,
	}
}

// webSearchStateToSummary 将完整搜索状态转换为持久化摘要；未实际搜索时返回 nil。
func webSearchStateToSummary(state *WebSearchState) *WebSearchSummary {
	if state == nil || !state.Used {
		return nil
	}

	return &WebSearchSummary{
		Queries:  state.Queries,
		Provider: state.Provider,
		Status:   state.Status,
		Sources:  state.Sources,
	}
}

func mapAssistantStreamError(err error) error {
	var streamErr *StreamError

	switch {
	case err == nil:
		return nil
	case errors.As(err, &streamErr):
		return streamErr
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return NewStreamError(StreamErrorCodeTimeout, "助手响应超时，请重试。", err)
	default:
		return NewStreamError(StreamErrorCodeStreamFailed, "助手流式回复失败，请重试。", err)
	}
}
