package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

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

const (
	summaryRefreshMessageThreshold = 12
	summaryRefreshCharThreshold    = 3000
)

type sessionRepository interface {
	ListSessions(ctx context.Context) ([]postgres.AssistantSession, error)
	GetSessionByID(ctx context.Context, id string) (*postgres.AssistantSession, error)
	ListMessages(ctx context.Context, sessionID string) ([]postgres.AssistantMessage, error)
	ListMessagesAfterSequence(ctx context.Context, sessionID string, afterSequenceNo int) ([]postgres.AssistantMessage, error)
	GetMessageByID(ctx context.Context, id string) (*postgres.AssistantMessage, error)
	CreateSessionWithMessages(ctx context.Context, title string, inputs []postgres.AssistantMessageInput) (*postgres.AssistantSession, []postgres.AssistantMessage, error)
	AppendMessages(ctx context.Context, sessionID string, inputs []postgres.AssistantMessageInput) ([]postgres.AssistantMessage, error)
	DeleteSession(ctx context.Context, id string) (bool, error)
}

type documentImporter interface {
	ImportDocument(ctx context.Context, input ImportDocumentInput) (*ImportDocumentResult, error)
}

type taskCreator interface {
	CreateTaskFromAssistantSuggestion(
		ctx context.Context,
		resourceID string,
		instruction string,
		sourceMessageID string,
	) (*postgres.Task, bool, error)
}

type resourceCitationRetriever interface {
	SearchByResource(ctx context.Context, resourceID string, query string, limit int) ([]citation.Citation, error)
}

type summarySnapshotRepository interface {
	GetBySessionID(ctx context.Context, sessionID string) (*postgres.SessionContextSnapshotRecord, error)
	AdvanceRollingSummary(ctx context.Context, sessionID string, summary *string, nextBaseSequenceNo int) (bool, error)
}

type replyContextLoader interface {
	LoadForReply(
		ctx context.Context,
		sessionID string,
		history []postgres.AssistantMessage,
		currentMessage string,
	) (*ReplyContext, error)
}

type uploadedFileStore interface {
	Save(ctx context.Context, originalName string, content []byte) (*filestore.StoredFile, error)
}

type uploadedFileRepository interface {
	Create(ctx context.Context, input postgres.UploadedFileCreateParams) (*postgres.UploadedFile, error)
	UpdateResourceID(ctx context.Context, fileID string, resourceID string) error
}

type sessionContextProjector interface {
	InitSession(ctx context.Context, sessionID string) error
	ProjectSessionFileReady(ctx context.Context, projection SessionFileReadyProjection) error
	ProjectTaskSuggestionCreated(ctx context.Context, projection TaskSuggestionProjection) error
	ProjectTaskCreated(ctx context.Context, projection TaskCreatedProjection) error
}

// ServiceOption 允许按需注入上传原文件存储等扩展能力。
type ServiceOption func(*Service)

// Service 负责会话消息流、文件导入和任务确认。
type Service struct {
	importer      documentImporter
	repo          sessionRepository
	retriever     resourceCitationRetriever
	responder     chatResponder
	tasks         taskCreator
	summarizer    ConversationSummarizer
	fileStore     uploadedFileStore
	uploadedFiles uploadedFileRepository
	contextLoader replyContextLoader
	projector     sessionContextProjector
	summaryRepo   summarySnapshotRepository
	asyncRunner   func(task func())
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
		asyncRunner: func(task func()) {
			go task()
		},
	}

	for _, option := range options {
		if option != nil {
			option(service)
		}
	}

	if service.contextLoader == nil {
		service.contextLoader = NewContextLoader(snapshotReaderFromProjector(service.projector), service.retriever, service.repo)
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

// WithReplyContextLoader 为 assistant service 注入回复上下文装载器。
func WithReplyContextLoader(loader replyContextLoader) ServiceOption {
	return func(service *Service) {
		service.contextLoader = loader
	}
}

// WithSessionContextProjector 为 assistant service 注入上下文快照投影器。
func WithSessionContextProjector(projector sessionContextProjector) ServiceOption {
	return func(service *Service) {
		service.projector = projector
	}
}

// WithConversationSummarizer 为 assistant service 注入滚动摘要器与快照仓储。
func WithConversationSummarizer(summarizer ConversationSummarizer, snapshotRepo summarySnapshotRepository) ServiceOption {
	return func(service *Service) {
		service.summarizer = summarizer
		service.summaryRepo = snapshotRepo
	}
}

// WithSummaryAsyncRunner 为摘要刷新注入异步执行器，便于测试控制触发时机。
func WithSummaryAsyncRunner(runner func(task func())) ServiceOption {
	return func(service *Service) {
		service.asyncRunner = runner
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

	inputs, err := s.buildReplyInputs(ctx, "", nil, trimmed)
	if err != nil {
		return nil, err
	}

	session, messages, err := s.repo.CreateSessionWithMessages(ctx, buildSessionTitle(trimmed), inputs)
	if err != nil {
		return nil, err
	}
	if err := s.initSessionSnapshot(ctx, session.ID); err != nil {
		return nil, err
	}
	if err := s.projectPersistedMessages(ctx, session.ID, messages); err != nil {
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

	inputs, err := s.buildReplyInputs(ctx, sessionID, history, trimmed)
	if err != nil {
		return nil, err
	}

	messages, err := s.repo.AppendMessages(ctx, sessionID, inputs)
	if err != nil {
		return nil, err
	}
	if err := s.projectPersistedMessages(ctx, sessionID, messages); err != nil {
		return nil, err
	}
	s.triggerSummaryRefresh(sessionID)

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
	if err := s.initSessionSnapshot(ctx, session.ID); err != nil {
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

	userInput, err := buildUserTextInput(trimmed)
	if err != nil {
		return err
	}

	if _, err := s.repo.AppendMessages(ctx, sessionID, []postgres.AssistantMessageInput{userInput}); err != nil {
		return err
	}

	return s.streamAssistantReply(ctx, streamAssistantReplyInput{
		content:   trimmed,
		emit:      emit,
		history:   history,
		sessionID: sessionID,
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
	if err := s.projectPersistedMessages(ctx, sessionID, messages); err != nil {
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

	task, created, taskErr := s.tasks.CreateTaskFromAssistantSuggestion(
		ctx,
		*suggestion.ResourceID,
		suggestion.Instruction,
		message.ID,
	)
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
	if !created {
		return &ConfirmTaskResult{
			Session:  *session,
			Task:     task,
			Messages: []postgres.AssistantMessage{},
		}, nil
	}

	input, err := buildMessageInput(RoleAssistant, KindTaskCreated, TaskCreatedPayload{
		DetailURL:           buildTaskDetailURL(task.ID, message.SessionID),
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
	if err := s.projectPersistedMessages(ctx, message.SessionID, messages); err != nil {
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

// DeleteSession 删除一个会话。
func (s *Service) DeleteSession(ctx context.Context, sessionID string) (bool, error) {
	return s.repo.DeleteSession(ctx, sessionID)
}

func (s *Service) buildReplyInputs(
	ctx context.Context,
	sessionID string,
	history []postgres.AssistantMessage,
	content string,
) ([]postgres.AssistantMessageInput, error) {
	userInput, err := buildUserTextInput(content)
	if err != nil {
		return nil, err
	}

	if s.responder == nil {
		return nil, errors.New("助手对话模型未配置")
	}

	replyContext, err := s.loadReplyContext(ctx, sessionID, history, content)
	if err != nil {
		return nil, err
	}

	reply, err := s.responder.Reply(ctx, ChatCompletionInput{
		Snapshot:  replyContext.Snapshot,
		Citations: replyContext.Citations,
		History:   replyContext.History,
		Message:   content,
		Resource:  replyContext.ActiveResource,
	})
	if err != nil {
		return nil, err
	}

	assistantInputs, err := buildAssistantReplyInputs(content, reply, replyContext.ActiveResource)
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
) ([]postgres.AssistantMessageInput, error) {
	if reply == nil || strings.TrimSpace(reply.Reply) == "" {
		return nil, errors.New("助手模型返回了空回复")
	}

	assistantInput, err := buildMessageInput(RoleAssistant, KindText, TextPayload{
		Content: strings.TrimSpace(reply.Reply),
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

func buildTaskDetailURL(taskID string, sessionID string) string {
	return buildSessionAwareURL("/tasks/"+taskID, sessionID)
}

func buildResourceDetailURL(resourceID string, sessionID string) string {
	return buildSessionAwareURL("/resources/"+resourceID, sessionID)
}

func buildSessionAwareURL(path string, sessionID string) string {
	values := url.Values{}
	if strings.TrimSpace(sessionID) != "" {
		values.Set("session", sessionID)
	}

	if encoded := values.Encode(); encoded != "" {
		return path + "?" + encoded
	}

	return path
}

type streamAssistantReplyInput struct {
	content   string
	emit      func(StreamEvent) error
	history   []postgres.AssistantMessage
	sessionID string
}

func (s *Service) streamAssistantReply(ctx context.Context, input streamAssistantReplyInput) error {
	if s.responder == nil {
		return errors.New("助手对话模型未配置")
	}

	replyContext, err := s.loadReplyContext(ctx, input.sessionID, input.history, input.content)
	if err != nil {
		return err
	}

	stream, err := s.responder.Stream(ctx, ChatCompletionInput{
		Snapshot:  replyContext.Snapshot,
		Citations: replyContext.Citations,
		History:   replyContext.History,
		Message:   input.content,
		Resource:  replyContext.ActiveResource,
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

	assistantInputs, err := buildAssistantReplyInputs(input.content, result, replyContext.ActiveResource)
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
	if err := s.projectPersistedMessages(ctx, input.sessionID, messages); err != nil {
		return err
	}
	s.triggerSummaryRefresh(input.sessionID)

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

func (s *Service) triggerSummaryRefresh(sessionID string) {
	if s == nil || s.summarizer == nil || s.summaryRepo == nil || strings.TrimSpace(sessionID) == "" {
		return
	}

	runner := s.asyncRunner
	if runner == nil {
		runner = func(task func()) {
			go task()
		}
	}

	runner(func() {
		if err := s.refreshRollingSummary(context.Background(), sessionID); err != nil {
			log.Printf("警告：assistant rolling summary refresh failed: %v", err)
		}
	})
}

func (s *Service) refreshRollingSummary(ctx context.Context, sessionID string) error {
	snapshotRecord, err := s.summaryRepo.GetBySessionID(ctx, sessionID)
	if err != nil {
		return err
	}

	summaryBaseSequenceNo := 0
	var previousSummary *string
	var snapshot *SessionContextSnapshot
	if snapshotRecord != nil {
		summaryBaseSequenceNo = snapshotRecord.SummaryBaseSequenceNo
		previousSummary = cloneOptionalString(snapshotRecord.RollingSummary)
		snapshot, err = SessionContextSnapshotFromRecord(snapshotRecord)
		if err != nil {
			return err
		}
	}

	messages, err := s.repo.ListMessagesAfterSequence(ctx, sessionID, summaryBaseSequenceNo)
	if err != nil {
		return err
	}

	transcript, totalChars := selectSummaryTranscript(messages)
	if len(transcript) < summaryRefreshMessageThreshold && totalChars < summaryRefreshCharThreshold {
		return nil
	}

	result, err := s.summarizer.Summarize(ctx, SummaryInput{
		PreviousSummary: previousSummary,
		Transcript:      transcript,
		Snapshot:        snapshot,
	})
	if err != nil {
		return err
	}
	if result == nil || strings.TrimSpace(result.Summary) == "" {
		return nil
	}

	summary := strings.TrimSpace(result.Summary)
	_, err = s.summaryRepo.AdvanceRollingSummary(ctx, sessionID, &summary, transcript[len(transcript)-1].SequenceNo)
	return err
}

func (s *Service) initSessionSnapshot(ctx context.Context, sessionID string) error {
	if s.projector == nil {
		return nil
	}

	return s.projector.InitSession(ctx, sessionID)
}

func (s *Service) projectPersistedMessages(ctx context.Context, sessionID string, messages []postgres.AssistantMessage) error {
	if s.projector == nil {
		return nil
	}

	for _, message := range messages {
		switch message.Kind {
		case KindSessionFile:
			payload, err := decodeSessionFile(message.Payload)
			if err != nil {
				return err
			}
			if strings.TrimSpace(payload.ResourceID) == "" || strings.TrimSpace(payload.Status) != "ready" {
				continue
			}
			if err := s.projector.ProjectSessionFileReady(ctx, SessionFileReadyProjection{
				SessionID:       sessionID,
				ResourceID:      payload.ResourceID,
				ResourceTitle:   payload.ResourceTitle,
				ResourceSource:  payload.SourceType,
				SourceMessageID: message.ID,
			}); err != nil {
				return err
			}
		case KindTaskSuggestion:
			payload, err := decodeTaskSuggestion(message.Payload)
			if err != nil {
				return err
			}
			if err := s.projector.ProjectTaskSuggestionCreated(ctx, TaskSuggestionProjection{
				SessionID:   sessionID,
				MessageID:   message.ID,
				Instruction: payload.Instruction,
			}); err != nil {
				return err
			}
		case KindTaskCreated:
			payload, err := unmarshalTaskCreatedPayload(message.Payload)
			if err != nil {
				return err
			}
			if strings.TrimSpace(payload.TaskID) == "" {
				continue
			}
			if err := s.projector.ProjectTaskCreated(ctx, TaskCreatedProjection{
				SessionID:       sessionID,
				TaskID:          payload.TaskID,
				Status:          payload.Status,
				SourceMessageID: payload.SuggestionMessageID,
			}); err != nil {
				return err
			}
		}
	}

	return nil
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

func (s *Service) loadReplyContext(
	ctx context.Context,
	sessionID string,
	history []postgres.AssistantMessage,
	currentMessage string,
) (*ReplyContext, error) {
	if s.contextLoader == nil {
		s.contextLoader = NewContextLoader(snapshotReaderFromProjector(s.projector), s.retriever, s.repo)
	}

	return s.contextLoader.LoadForReply(ctx, sessionID, history, currentMessage)
}

func snapshotReaderFromProjector(projector sessionContextProjector) sessionContextSnapshotReader {
	concrete, ok := projector.(*SessionContextProjector)
	if !ok || concrete == nil {
		return nil
	}

	return concrete.repo
}

func selectSummaryTranscript(messages []postgres.AssistantMessage) ([]postgres.AssistantMessage, int) {
	if len(messages) == 0 {
		return nil, 0
	}

	transcript := make([]postgres.AssistantMessage, 0, len(messages))
	totalChars := 0
	for _, message := range messages {
		if message.Kind != KindText {
			continue
		}

		payload, err := unmarshalTextPayload(message.Payload)
		if err != nil {
			continue
		}

		content := strings.TrimSpace(payload.Content)
		if content == "" {
			continue
		}

		transcript = append(transcript, message)
		totalChars += utf8.RuneCountInString(content)
	}

	return transcript, totalChars
}
