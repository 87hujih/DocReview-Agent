package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/knowledge/citation"
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
}

type documentImporter interface {
	ImportDocument(ctx context.Context, input ImportDocumentInput) (*ImportDocumentResult, error)
}

type taskCreator interface {
	CreateTask(ctx context.Context, resourceID string, instruction string) (*postgres.Task, error)
}

type resourceSearcher interface {
	SearchChunksLexicalByResource(ctx context.Context, query string, limit int, resourceID string) ([]postgres.ResourceChunk, error)
}

// Service 负责会话消息流、文件导入和任务确认。
type Service struct {
	importer  documentImporter
	repo      sessionRepository
	resources resourceSearcher
	responder chatResponder
	tasks     taskCreator
}

// NewService 构造助手领域服务。
func NewService(
	repo sessionRepository,
	importer documentImporter,
	tasks taskCreator,
	responder chatResponder,
	resources resourceSearcher,
) *Service {
	return &Service{
		importer:  importer,
		repo:      repo,
		resources: resources,
		responder: responder,
		tasks:     tasks,
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

	inputs, err := s.buildReplyInputs(ctx, nil, trimmed, nil)
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

	inputs, err := s.buildReplyInputs(ctx, history, trimmed, resourceContext)
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

	resource, appendInputs, errorMessage, err := s.buildUploadMessages(ctx, fileName, content)
	if err != nil {
		return nil, err
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

// DeleteSession 删除一个会话。
func (s *Service) DeleteSession(ctx context.Context, sessionID string) (bool, error) {
	return s.repo.DeleteSession(ctx, sessionID)
}

func (s *Service) buildReplyInputs(
	ctx context.Context,
	history []postgres.AssistantMessage,
	content string,
	resource *resourceContext,
) ([]postgres.AssistantMessageInput, error) {
	userInput, err := buildMessageInput(RoleUser, KindText, TextPayload{Content: content})
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

	reply, err := s.responder.Reply(ctx, ChatCompletionInput{
		Citations: citations,
		History:   history,
		Message:   content,
		Resource:  resource,
	})
	if err != nil {
		return nil, err
	}

	replyText := strings.TrimSpace(reply.Reply)
	if replyText == "" {
		return nil, errors.New("助手模型返回了空回复")
	}

	assistantInput, err := buildMessageInput(RoleAssistant, KindText, TextPayload{Content: replyText})
	if err != nil {
		return nil, err
	}

	inputs := []postgres.AssistantMessageInput{userInput, assistantInput}

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

func (s *Service) buildUploadMessages(ctx context.Context, fileName string, content []byte) (*postgres.Resource, []postgres.AssistantMessageInput, *string, error) {
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

	fileInput, err := buildMessageInput(RoleAssistant, KindSessionFile, SessionFilePayload{
		FileName:      fileName,
		ResourceID:    result.Resource.ID,
		ResourceTitle: result.Resource.Title,
		SourceType:    result.Resource.SourceType,
		Status:        "ready",
	})
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
	if s.resources == nil || resource == nil {
		return nil, nil
	}

	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil, nil
	}

	chunks, err := s.resources.SearchChunksLexicalByResource(ctx, trimmedQuery, 4, resource.ID)
	if err != nil {
		return nil, err
	}

	return citation.BuildFromChunks(chunks), nil
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
