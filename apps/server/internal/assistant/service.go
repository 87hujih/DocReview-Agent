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
	UpdateSessionID(ctx context.Context, fileID string, sessionID string) error
}

type sectionLocator interface {
	Locate(ctx context.Context, input LocateSectionInput) (*LocatedSection, error)
}

type canonicalSectionReader interface {
	Read(ctx context.Context, input CanonicalReadInput) (*CanonicalReadResult, error)
}

type deterministicReadResponder interface {
	Respond(ctx context.Context, input DeterministicReadInput) (*DeterministicReadResult, error)
}

type sessionContextProjector interface {
	InitSession(ctx context.Context, sessionID string) error
	ProjectSessionFileReady(ctx context.Context, projection SessionFileReadyProjection) error
	ProjectTaskSuggestionCreated(ctx context.Context, projection TaskSuggestionProjection) error
	ProjectTaskCreated(ctx context.Context, projection TaskCreatedProjection) error
	ProjectGroundingState(ctx context.Context, projection GroundingStateProjection) error
}

type runtimeEventRecorder interface {
	Record(ctx context.Context, input RuntimeRecordInput) (*postgres.AssistantRuntimeEvent, error)
}

type runtimeLearningProjector interface {
	Project(ctx context.Context, event *postgres.AssistantRuntimeEvent) error
}

// ServiceOption 允许按需注入上传原文件存储等扩展能力。
type ServiceOption func(*Service)

// Service 负责会话消息流、文件导入和任务确认。
type Service struct {
	importer          documentImporter
	repo              sessionRepository
	retriever         resourceCitationRetriever
	responder         chatResponder
	deliberator       deliberationAgent
	workflowPlanner   workflowPlanner
	workflowVerifier  workflowVerifier
	tasks             taskCreator
	summarizer        ConversationSummarizer
	fileStore         uploadedFileStore
	uploadedFiles     uploadedFileRepository
	contextLoader     replyContextLoader
	projector         sessionContextProjector
	summaryRepo       summarySnapshotRepository
	stateBuilder      *RuntimeStateBuilder
	runtimeEvents     runtimeEventRecorder
	learningProjector runtimeLearningProjector
	asyncRunner       func(task func())
	sectionLocator    sectionLocator
	sectionReader     canonicalSectionReader
	directReader      deterministicReadResponder
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
		importer:     importer,
		repo:         repo,
		retriever:    retriever,
		responder:    responder,
		tasks:        tasks,
		stateBuilder: &RuntimeStateBuilder{},
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
	if service.deliberator == nil {
		service.deliberator = heuristicDeliberationAgent{}
	}
	if service.workflowPlanner == nil {
		service.workflowPlanner = passthroughWorkflowPlanner{}
	}
	if service.workflowVerifier == nil {
		service.workflowVerifier = passthroughWorkflowVerifier{}
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

// WithDeliberationAgent 为 assistant service 注入 deliberation agent。
func WithDeliberationAgent(agent deliberationAgent) ServiceOption {
	return func(service *Service) {
		service.deliberator = agent
	}
}

// WithWorkflowPlanner 为 assistant service 注入 workflow planner。
func WithWorkflowPlanner(planner workflowPlanner) ServiceOption {
	return func(service *Service) {
		service.workflowPlanner = planner
	}
}

// WithWorkflowVerifier 为 assistant service 注入 workflow verifier。
func WithWorkflowVerifier(verifier workflowVerifier) ServiceOption {
	return func(service *Service) {
		service.workflowVerifier = verifier
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

// WithSectionLocator 为 assistant service 注入当前文件目标定位器。
func WithSectionLocator(locator sectionLocator) ServiceOption {
	return func(service *Service) {
		service.sectionLocator = locator
	}
}

// WithSectionReader 为 assistant service 注入当前文件 canonical 读取器。
func WithSectionReader(reader canonicalSectionReader) ServiceOption {
	return func(service *Service) {
		service.sectionReader = reader
	}
}

// WithDeterministicReadResponder 为 assistant service 注入 deterministic read responder。
func WithDeterministicReadResponder(responder deterministicReadResponder) ServiceOption {
	return func(service *Service) {
		service.directReader = responder
	}
}

// WithRuntimeLearning 为 assistant service 注入 runtime 事件记录与样本投影能力。
func WithRuntimeLearning(recorder runtimeEventRecorder, projector runtimeLearningProjector) ServiceOption {
	return func(service *Service) {
		service.runtimeEvents = recorder
		service.learningProjector = projector
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

	replyBuild, err := s.buildReplyInputs(ctx, "", nil, trimmed)
	if err != nil {
		return nil, err
	}

	session, messages, err := s.repo.CreateSessionWithMessages(ctx, buildSessionTitle(trimmed), replyBuild.Inputs)
	if err != nil {
		return nil, err
	}
	if err := s.initSessionSnapshot(ctx, session.ID); err != nil {
		return nil, err
	}
	if err := s.projectPersistedMessages(ctx, session.ID, messages); err != nil {
		return nil, err
	}
	if err := s.projectReplyGrounding(ctx, session.ID, replyBuild.ReplyContext); err != nil {
		return nil, err
	}

	return &ConversationResult{
		Session:  *session,
		Messages: messages,
	}, nil
}

// StartConversationWithFile 用首个上传文件创建空会话，并只写入结构化文件消息。
func (s *Service) StartConversationWithFile(ctx context.Context, fileName string, content []byte) (*UploadFileResult, error) {
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return nil, ErrFileNameRequired
	}
	if len(content) == 0 {
		return nil, ErrFileContentRequired
	}

	uploadedFile, err := s.persistUploadedFile(ctx, nil, fileName, content)
	if err != nil {
		return nil, err
	}

	resource, createInputs, errorMessage, err := s.buildUploadMessages(ctx, fileName, content, uploadedFile)
	if err != nil {
		return nil, err
	}

	session, messages, err := s.repo.CreateSessionWithMessages(ctx, buildUploadSessionTitle(fileName, resource), createInputs)
	if err != nil {
		return nil, err
	}
	if err := s.initSessionSnapshot(ctx, session.ID); err != nil {
		return nil, err
	}
	if uploadedFile != nil {
		if err := s.uploadedFiles.UpdateSessionID(ctx, uploadedFile.ID, session.ID); err != nil {
			return nil, err
		}
		if resource != nil {
			if err := s.uploadedFiles.UpdateResourceID(ctx, uploadedFile.ID, resource.ID); err != nil {
				return nil, err
			}
		}
	}
	if err := s.projectPersistedMessages(ctx, session.ID, messages); err != nil {
		return nil, err
	}

	return &UploadFileResult{
		Session:      *session,
		Resource:     resource,
		Messages:     messages,
		ErrorMessage: errorMessage,
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

	replyBuild, err := s.buildReplyInputs(ctx, sessionID, history, trimmed)
	if err != nil {
		return nil, err
	}

	messages, err := s.repo.AppendMessages(ctx, sessionID, replyBuild.Inputs)
	if err != nil {
		return nil, err
	}
	if err := s.projectPersistedMessages(ctx, sessionID, messages); err != nil {
		return nil, err
	}
	if err := s.projectReplyGrounding(ctx, sessionID, replyBuild.ReplyContext); err != nil {
		return nil, err
	}
	if err := s.recordRuntimeLearningForTurn(ctx, replyBuild, messages); err != nil {
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

// persistUploadedFile 持久化 `Uploaded文件`，把写库细节收口在接收者内部。
func (s *Service) persistUploadedFile(ctx context.Context, sessionID *string, fileName string, content []byte) (*postgres.UploadedFile, error) {
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
		SessionID:        sessionID,
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
	if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
		SessionID: message.SessionID,
		MessageID: &message.ID,
		Source:    "task_suggestion",
		EventType: RuntimeEventTypeTaskSuggestionConfirmed,
		Payload: map[string]any{
			"task_id": task.ID,
		},
	}); err != nil {
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

// buildReplyInputs 组装 `Reply输入`，统一接收者返回结果的结构形态。
type replyBuildResult struct {
	Inputs                         []postgres.AssistantMessageInput
	ReplyContext                   *ReplyContext
	RuntimeState                   RuntimeState
	Decision                       *DeliberationDecision
	Policy                         PolicyDecision
	PlanDecision                   *WorkflowPlanDecision
	VerificationDecision           *WorkflowVerificationDecision
	ReplyDecision                  *DeliberationDecision
	PendingSuggestion              *SnapshotPendingTaskSuggestion
	PreviousClarificationMessageID *string
	CorrectionSignal               *RuntimeCorrectionSignal
}

func (s *Service) buildReplyInputs(
	ctx context.Context,
	sessionID string,
	history []postgres.AssistantMessage,
	content string,
) (*replyBuildResult, error) {
	userInput, err := buildUserTextInput(content)
	if err != nil {
		return nil, err
	}

	prepared, err := s.prepareTurnContext(ctx, history, content)
	if err != nil {
		return nil, err
	}
	if s.responder == nil {
		return nil, errors.New("助手对话模型未配置")
	}

	replyHistory := history
	if prepared != nil && len(prepared.History) > 0 {
		replyHistory = prepared.History
	}

	replyContext, err := s.loadReplyContext(ctx, sessionID, replyHistory, content)
	if err != nil {
		return nil, err
	}
	replyContext, err = s.repairReplyContext(ctx, content, replyContext)
	if err != nil {
		return nil, err
	}

	runtimeState := s.stateBuilder.Build(content, replyContext)
	decision, policy, err := s.deliberateTurn(ctx, runtimeState)
	if err != nil {
		return nil, err
	}
	planDecision, err := s.planWorkflow(ctx, runtimeState, decision, policy)
	if err != nil {
		return nil, err
	}
	verificationDecision, err := s.verifyWorkflow(ctx, runtimeState, decision, policy, planDecision)
	if err != nil {
		return nil, err
	}
	replyDecision := decisionForReply(decision, policy, planDecision, verificationDecision)

	deterministicInputs, usedDeterministicRead, err := s.tryBuildDeterministicReadInputs(ctx, content, replyContext)
	if err != nil {
		return nil, err
	}
	if usedDeterministicRead {
		inputs := []postgres.AssistantMessageInput{userInput}
		if prepared != nil && len(prepared.AdditionalInputs) > 0 {
			inputs = append(inputs, prepared.AdditionalInputs...)
		}
		inputs = append(inputs, deterministicInputs...)

		var correctionSignal *RuntimeCorrectionSignal
		if runtimeState.PendingTaskSuggestion != nil {
			correctionSignal = DetectExplicitWorkflowCorrection(content)
		}

		return &replyBuildResult{
			Inputs:                         inputs,
			ReplyContext:                   replyContext,
			RuntimeState:                   runtimeState,
			Decision:                       decision,
			Policy:                         policy,
			PlanDecision:                   planDecision,
			VerificationDecision:           verificationDecision,
			ReplyDecision:                  replyDecision,
			PendingSuggestion:              clonePendingTaskSuggestion(runtimeState.PendingTaskSuggestion),
			PreviousClarificationMessageID: latestClarificationPromptMessageID(replyHistory),
			CorrectionSignal:               correctionSignal,
		}, nil
	}
	if failureReply := strings.TrimSpace(replyContext.CurrentFileFailureReply); failureReply != "" {
		assistantInput, err := buildMessageInput(RoleAssistant, KindText, TextPayload{Content: failureReply})
		if err != nil {
			return nil, err
		}

		inputs := []postgres.AssistantMessageInput{userInput}
		if prepared != nil && len(prepared.AdditionalInputs) > 0 {
			inputs = append(inputs, prepared.AdditionalInputs...)
		}
		inputs = append(inputs, assistantInput)

		var correctionSignal *RuntimeCorrectionSignal
		if runtimeState.PendingTaskSuggestion != nil {
			correctionSignal = DetectExplicitWorkflowCorrection(content)
		}

		return &replyBuildResult{
			Inputs:                         inputs,
			ReplyContext:                   replyContext,
			RuntimeState:                   runtimeState,
			Decision:                       decision,
			Policy:                         policy,
			PlanDecision:                   planDecision,
			VerificationDecision:           verificationDecision,
			ReplyDecision:                  replyDecision,
			PendingSuggestion:              clonePendingTaskSuggestion(runtimeState.PendingTaskSuggestion),
			PreviousClarificationMessageID: latestClarificationPromptMessageID(replyHistory),
			CorrectionSignal:               correctionSignal,
		}, nil
	}

	reply, err := s.responder.Reply(ctx, ChatCompletionInput{
		RuntimeState:             runtimeState,
		Snapshot:                 replyContext.Snapshot,
		Citations:                replyContext.Citations,
		GroundedTarget:           replyContext.GroundedTarget,
		CanonicalAnalysisContext: replyContext.CanonicalAnalysisContext,
		History:                  replyContext.History,
		Message:                  content,
		Resource:                 replyContext.ActiveResource,
		Decision:                 replyDecision,
	})
	if err != nil {
		return nil, err
	}

	assistantInputs, err := buildAssistantReplyInputsFromPolicy(reply, replyDecision, policy, runtimeState.ActiveResource)
	if err != nil {
		return nil, err
	}

	inputs := []postgres.AssistantMessageInput{userInput}
	if prepared != nil && len(prepared.AdditionalInputs) > 0 {
		inputs = append(inputs, prepared.AdditionalInputs...)
	}
	inputs = append(inputs, assistantInputs...)

	var correctionSignal *RuntimeCorrectionSignal
	if runtimeState.PendingTaskSuggestion != nil {
		correctionSignal = DetectExplicitWorkflowCorrection(content)
	}

	return &replyBuildResult{
		Inputs:                         inputs,
		ReplyContext:                   replyContext,
		RuntimeState:                   runtimeState,
		Decision:                       decision,
		Policy:                         policy,
		PlanDecision:                   planDecision,
		VerificationDecision:           verificationDecision,
		ReplyDecision:                  replyDecision,
		PendingSuggestion:              clonePendingTaskSuggestion(runtimeState.PendingTaskSuggestion),
		PreviousClarificationMessageID: latestClarificationPromptMessageID(replyHistory),
		CorrectionSignal:               correctionSignal,
	}, nil
}

// tryBuildDeterministicReadInputs 尝试把当前文件读取型请求改写成 deterministic read 的持久化输入。
func (s *Service) tryBuildDeterministicReadInputs(
	ctx context.Context,
	content string,
	replyContext *ReplyContext,
) ([]postgres.AssistantMessageInput, bool, error) {
	if s == nil || s.sectionLocator == nil || s.sectionReader == nil || s.directReader == nil || replyContext == nil || replyContext.ActiveResource == nil {
		return nil, false, nil
	}

	intent := ClassifyReadIntent(content)
	if !isDeterministicReadIntent(intent) {
		return nil, false, nil
	}

	located, err := s.sectionLocator.Locate(ctx, LocateSectionInput{
		ResourceID:      replyContext.ActiveResource.ID,
		Message:         content,
		Intent:          intent,
		ActiveSectionID: activeSectionIDFromSnapshot(replyContext.Snapshot),
	})
	if err != nil {
		return nil, false, err
	}
	if located == nil {
		return nil, false, nil
	}

	readResult, err := s.sectionReader.Read(ctx, CanonicalReadInput{
		ResourceID: replyContext.ActiveResource.ID,
		VersionID:  located.VersionID,
		Located:    located,
	})
	if err != nil {
		if errors.Is(err, ErrCanonicalReadUnavailable) {
			return nil, false, nil
		}

		return nil, false, err
	}
	if readResult == nil {
		return nil, false, nil
	}

	directReply, err := s.directReader.Respond(ctx, DeterministicReadInput{
		Message:    content,
		Intent:     intent,
		Resource:   replyContext.ActiveResource,
		Located:    located,
		ReadResult: readResult,
	})
	if err != nil {
		return nil, false, err
	}
	if directReply == nil || strings.TrimSpace(directReply.Content) == "" {
		return nil, false, nil
	}

	assistantInput, err := buildMessageInput(RoleAssistant, KindText, TextPayload{
		Content: strings.TrimSpace(directReply.Content),
	})
	if err != nil {
		return nil, false, err
	}

	return []postgres.AssistantMessageInput{assistantInput}, true, nil
}

// isDeterministicReadIntent 判断当前 read intent 是否应该优先走 direct access 的 deterministic read 主路。
func isDeterministicReadIntent(intent ReadIntent) bool {
	switch intent.Kind {
	case ReadIntentListSections, ReadIntentLocateOrdinal, ReadIntentExcerptSection:
		return true
	default:
		return false
	}
}

// activeSectionIDFromSnapshot 提取快照里的 active section id，避免调用方重复判空。
func activeSectionIDFromSnapshot(snapshot *SessionContextSnapshot) string {
	if snapshot == nil || snapshot.ActiveSection == nil {
		return ""
	}

	return strings.TrimSpace(snapshot.ActiveSection.ID)
}

// recordRuntimeLearningForTurn 记录当前轮的 runtime 事件，并把事件投影为学习样本。
func (s *Service) recordRuntimeLearningForTurn(
	ctx context.Context,
	build *replyBuildResult,
	messages []postgres.AssistantMessage,
) error {
	if s == nil || s.runtimeEvents == nil || build == nil {
		return nil
	}

	decisionMessageID := runtimeDecisionMessageID(messages)
	if decisionMessageID == nil {
		return nil
	}

	sessionID := runtimeSessionID(build, messages)
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}

	if build.PendingSuggestion != nil && strings.TrimSpace(build.PendingSuggestion.MessageID) != "" {
		pendingMessageID := build.PendingSuggestion.MessageID
		if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
			SessionID: sessionID,
			MessageID: &pendingMessageID,
			Source:    "task_suggestion",
			EventType: RuntimeEventTypeTaskSuggestionIgnored,
			Payload: map[string]any{
				"instruction": strings.TrimSpace(build.PendingSuggestion.Instruction),
			},
		}); err != nil {
			return err
		}
		if build.CorrectionSignal != nil {
			if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
				SessionID: sessionID,
				MessageID: &pendingMessageID,
				Source:    "user",
				EventType: RuntimeEventTypeUserCorrected,
				Payload: map[string]any{
					"reason": build.CorrectionSignal.Reason,
				},
			}); err != nil {
				return err
			}
		}
	}

	if build.Decision != nil {
		if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
			SessionID: sessionID,
			MessageID: decisionMessageID,
			Source:    "deliberation",
			EventType: RuntimeEventTypeDeliberationDecided,
			Payload: map[string]any{
				"request_kind":         build.Decision.RequestKind,
				"response_mode":        build.Decision.ResponseMode,
				"chat_fulfillable":     build.Decision.ChatFulfillable,
				"workflow_commitment":  build.Decision.WorkflowCommitment,
				"needs_clarification":  build.Decision.NeedsClarification,
				"evidence_sufficiency": build.Decision.EvidenceSufficiency,
				"confidence":           build.Decision.Confidence,
				"reasons":              build.Decision.Reasons,
			},
		}); err != nil {
			return err
		}
	}

	if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
		SessionID: sessionID,
		MessageID: decisionMessageID,
		Source:    "policy",
		EventType: RuntimeEventTypePolicyApplied,
		Payload: map[string]any{
			"allow_answer":            build.Policy.AllowAnswer,
			"allow_clarification":     build.Policy.AllowClarification,
			"allow_task_suggestion":   build.Policy.AllowTaskSuggestion,
			"allow_workflow_planning": build.Policy.AllowWorkflowPlanning,
			"require_verifier":        build.Policy.RequireVerifier,
			"blocked_reason":          build.Policy.BlockedReason,
		},
	}); err != nil {
		return err
	}

	if build.PlanDecision != nil {
		if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
			SessionID: sessionID,
			MessageID: decisionMessageID,
			Source:    "planner",
			EventType: RuntimeEventTypePlannerUsed,
			Payload: map[string]any{
				"should_enter_workflow": build.PlanDecision.ShouldEnterWorkflow,
				"chat_fulfillable":      build.PlanDecision.ChatFulfillable,
				"needs_clarification":   build.PlanDecision.NeedsClarification,
				"candidate_instruction": derefOptionalString(build.PlanDecision.CandidateInstruction),
				"candidate_plan_goal":   derefOptionalString(build.PlanDecision.CandidatePlanGoal),
				"confidence":            build.PlanDecision.Confidence,
				"reasons":               build.PlanDecision.Reasons,
			},
		}); err != nil {
			return err
		}
	}

	if build.VerificationDecision != nil {
		if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
			SessionID: sessionID,
			MessageID: decisionMessageID,
			Source:    "verifier",
			EventType: RuntimeEventTypeVerifierUsed,
			Payload: map[string]any{
				"approve_workflow":    build.VerificationDecision.ApproveWorkflow,
				"downgrade_to_chat":   build.VerificationDecision.DowngradeToChat,
				"needs_clarification": build.VerificationDecision.NeedsClarification,
				"revised_instruction": derefOptionalString(build.VerificationDecision.RevisedInstruction),
				"confidence":          build.VerificationDecision.Confidence,
				"reasons":             build.VerificationDecision.Reasons,
			},
		}); err != nil {
			return err
		}
	}

	if build.ReplyDecision != nil && build.ReplyDecision.NeedsClarification && build.ReplyDecision.ClarificationQuestion != nil {
		if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
			SessionID: sessionID,
			MessageID: decisionMessageID,
			Source:    "clarification",
			EventType: RuntimeEventTypeClarificationPrompted,
			Payload: map[string]any{
				"question": *build.ReplyDecision.ClarificationQuestion,
			},
		}); err != nil {
			return err
		}
	}
	if build.PreviousClarificationMessageID != nil && build.ReplyDecision != nil && !build.ReplyDecision.NeedsClarification {
		switch build.ReplyDecision.ResponseMode {
		case ResponseModeAnswerThenTaskCard:
			if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
				SessionID: sessionID,
				MessageID: build.PreviousClarificationMessageID,
				Source:    "clarification",
				EventType: RuntimeEventTypeClarificationResolvedFlow,
				Payload: map[string]any{
					"outcome":       "resolved_to_workflow",
					"response_mode": build.ReplyDecision.ResponseMode,
				},
			}); err != nil {
				return err
			}
		case ResponseModeAnswerOnly, ResponseModeAnswerWithGrounding:
			if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
				SessionID: sessionID,
				MessageID: build.PreviousClarificationMessageID,
				Source:    "clarification",
				EventType: RuntimeEventTypeClarificationResolvedChat,
				Payload: map[string]any{
					"outcome":       "resolved_to_chat",
					"response_mode": build.ReplyDecision.ResponseMode,
				},
			}); err != nil {
				return err
			}
		}
	}

	if build.Decision != nil && build.Decision.WorkflowCommitment && build.ReplyDecision != nil {
		switch build.ReplyDecision.ResponseMode {
		case ResponseModeAnswerThenTaskCard:
			if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
				SessionID: sessionID,
				MessageID: decisionMessageID,
				Source:    "workflow",
				EventType: RuntimeEventTypeWorkflowPromoted,
				Payload: map[string]any{
					"response_mode": build.ReplyDecision.ResponseMode,
				},
			}); err != nil {
				return err
			}
		case ResponseModeAnswerOnly, ResponseModeAnswerWithGrounding, ResponseModeClarifyFirst:
			if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
				SessionID: sessionID,
				MessageID: decisionMessageID,
				Source:    "workflow",
				EventType: RuntimeEventTypeWorkflowDowngraded,
				Payload: map[string]any{
					"response_mode": build.ReplyDecision.ResponseMode,
				},
			}); err != nil {
				return err
			}
		}
	}

	if build.ReplyDecision != nil && build.ReplyDecision.ResponseMode == ResponseModeAnswerThenTaskCard && build.ReplyDecision.CandidateTaskInstruction != nil {
		if err := s.recordRuntimeEvent(ctx, RuntimeRecordInput{
			SessionID: sessionID,
			MessageID: decisionMessageID,
			Source:    "task_suggestion",
			EventType: RuntimeEventTypeTaskSuggestionCreated,
			Payload: map[string]any{
				"instruction": strings.TrimSpace(*build.ReplyDecision.CandidateTaskInstruction),
			},
		}); err != nil {
			return err
		}
	}

	return nil
}

// recordRuntimeEvent 统一处理 runtime 事件写入与样本投影，避免调用点重复编排。
func (s *Service) recordRuntimeEvent(ctx context.Context, input RuntimeRecordInput) error {
	if s == nil || s.runtimeEvents == nil {
		return nil
	}

	event, err := s.runtimeEvents.Record(ctx, input)
	if err != nil {
		return err
	}
	if s.learningProjector == nil {
		return nil
	}

	return s.learningProjector.Project(ctx, event)
}

// runtimeDecisionMessageID 选出当前轮最适合作为学习样本主键的 assistant message id。
func runtimeDecisionMessageID(messages []postgres.AssistantMessage) *string {
	for index := range messages {
		if messages[index].Kind == KindTaskSuggestion {
			return stringPointer(messages[index].ID)
		}
	}
	for index := range messages {
		if messages[index].Role == RoleAssistant && messages[index].Kind == KindText {
			return stringPointer(messages[index].ID)
		}
	}
	for index := range messages {
		if messages[index].Role == RoleAssistant {
			return stringPointer(messages[index].ID)
		}
	}

	return nil
}

// latestClarificationPromptMessageID 识别上一轮 assistant 输出中的最近澄清提示消息。
func latestClarificationPromptMessageID(history []postgres.AssistantMessage) *string {
	sawAssistant := false

	for index := len(history) - 1; index >= 0; index-- {
		message := history[index]
		if message.Role != RoleAssistant {
			if sawAssistant {
				break
			}

			continue
		}

		sawAssistant = true
		if message.Kind != KindText {
			continue
		}

		payload, err := unmarshalTextPayload(message.Payload)
		if err != nil {
			continue
		}
		if isClarificationPromptText(payload.Content) {
			return stringPointer(message.ID)
		}
	}

	return nil
}

// isClarificationPromptText 用保守规则识别“你是想……还是……”这类澄清提示。
func isClarificationPromptText(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	if !strings.HasSuffix(trimmed, "？") && !strings.HasSuffix(trimmed, "?") {
		return false
	}
	if strings.Contains(trimmed, "还是") {
		return true
	}

	return strings.Contains(trimmed, "请确认")
}

// runtimeSessionID 优先从构建结果里提取 session id，必要时退回到持久化消息。
func runtimeSessionID(build *replyBuildResult, messages []postgres.AssistantMessage) string {
	if build != nil && build.ReplyContext != nil && build.ReplyContext.Snapshot != nil {
		if sessionID := strings.TrimSpace(build.ReplyContext.Snapshot.SessionID); sessionID != "" {
			return sessionID
		}
	}
	if len(messages) > 0 {
		return strings.TrimSpace(messages[0].SessionID)
	}

	return ""
}

// deliberateTurn 执行当前轮的 deliberation 与 policy 裁决。
func (s *Service) deliberateTurn(ctx context.Context, state RuntimeState) (*DeliberationDecision, PolicyDecision, error) {
	if s.deliberator == nil {
		s.deliberator = heuristicDeliberationAgent{}
	}

	decision, err := s.deliberator.Deliberate(ctx, state)
	if err != nil {
		return nil, PolicyDecision{}, err
	}

	return decision, ApplyPolicy(state, decision), nil
}

// planWorkflow 在 policy 允许时执行 workflow planner，收敛最终 workflow 入口结论。
func (s *Service) planWorkflow(
	ctx context.Context,
	state RuntimeState,
	decision *DeliberationDecision,
	policy PolicyDecision,
) (*WorkflowPlanDecision, error) {
	if !policy.AllowWorkflowPlanning {
		return nil, nil
	}
	if s.workflowPlanner == nil {
		s.workflowPlanner = passthroughWorkflowPlanner{}
	}

	return s.workflowPlanner.Plan(ctx, state, decision)
}

// verifyWorkflow 在 policy 与 planner 共同允许时执行 verifier，收敛最终 workflow promotion 结论。
func (s *Service) verifyWorkflow(
	ctx context.Context,
	state RuntimeState,
	decision *DeliberationDecision,
	policy PolicyDecision,
	plan *WorkflowPlanDecision,
) (*WorkflowVerificationDecision, error) {
	if !policy.RequireVerifier || plan == nil || !plan.ShouldEnterWorkflow {
		return nil, nil
	}
	if s.workflowVerifier == nil {
		s.workflowVerifier = passthroughWorkflowVerifier{}
	}

	return s.workflowVerifier.Verify(ctx, state, decision, plan)
}

// buildUploadMessages 组装 `上传消息`，统一接收者返回结果的结构形态。
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

// appendSystemMessage 向助手追加 `系统消息`，保持写入顺序和副作用收口在单点。
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

// buildMessageInput 组装 `消息输入`，为后续流程准备可直接消费的输入。
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

// buildUserTextInput 组装 `User文本输入`，为后续流程准备可直接消费的输入。
func buildUserTextInput(content string) (postgres.AssistantMessageInput, error) {
	return buildMessageInput(RoleUser, KindText, TextPayload{Content: content})
}

// buildAssistantReplyInputs 组装 `助手Reply输入`，为后续流程准备可直接消费的输入。
func buildAssistantReplyInputs(
	reply *ChatCompletionResult,
	decision TaskSuggestionDecision,
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
	if decision.ReadinessState != ReadinessStateReadyForTask || strings.TrimSpace(decision.NormalizedInstruction) == "" {
		return inputs, nil
	}

	suggestionInput, err := buildMessageInput(
		RoleAssistant,
		KindTaskSuggestion,
		buildTaskSuggestion(decision.NormalizedInstruction, decision.ActiveResource),
	)
	if err != nil {
		return nil, err
	}

	return append(inputs, suggestionInput), nil
}

// buildAssistantReplyInputsFromPolicy 根据 deliberation 与 policy 结果组装最终 assistant 输出。
func buildAssistantReplyInputsFromPolicy(
	reply *ChatCompletionResult,
	decision *DeliberationDecision,
	policy PolicyDecision,
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
	if !policy.AllowTaskSuggestion || decision == nil || resource == nil {
		return inputs, nil
	}
	if decision.ResponseMode != ResponseModeAnswerThenTaskCard || decision.CandidateTaskInstruction == nil || strings.TrimSpace(*decision.CandidateTaskInstruction) == "" {
		return inputs, nil
	}

	suggestionInput, err := buildMessageInput(
		RoleAssistant,
		KindTaskSuggestion,
		buildTaskSuggestion(strings.TrimSpace(*decision.CandidateTaskInstruction), resource),
	)
	if err != nil {
		return nil, err
	}

	return append(inputs, suggestionInput), nil
}

// decisionForReply 根据 policy 结果收窄 responder 可见的回复模式，避免 reply 误导后续体验。
func decisionForReply(
	decision *DeliberationDecision,
	policy PolicyDecision,
	plan *WorkflowPlanDecision,
	verification *WorkflowVerificationDecision,
) *DeliberationDecision {
	if decision == nil {
		return nil
	}

	cloned := *decision
	cloned.ClarificationQuestion = normalizeOptionalText(decision.ClarificationQuestion)
	cloned.CandidateTaskInstruction = normalizeOptionalText(decision.CandidateTaskInstruction)
	cloned.CandidatePlanGoal = normalizeOptionalText(decision.CandidatePlanGoal)
	cloned.Reasons = normalizeDecisionReasons(decision.Reasons)

	switch {
	case policy.AllowClarification:
		cloned.ResponseMode = ResponseModeClarifyFirst
		cloned.NeedsClarification = true
	case verification != nil && verification.NeedsClarification:
		cloned.ResponseMode = ResponseModeClarifyFirst
		cloned.ChatFulfillable = false
		cloned.WorkflowCommitment = false
		cloned.NeedsClarification = true
		cloned.ClarificationQuestion = normalizeOptionalText(verification.ClarificationQuestion)
		cloned.CandidateTaskInstruction = nil
		cloned.CandidatePlanGoal = nil
	case verification != nil && verification.DowngradeToChat:
		cloned.ResponseMode = ResponseModeAnswerOnly
		cloned.ChatFulfillable = true
		cloned.WorkflowCommitment = false
		cloned.NeedsClarification = false
		cloned.ClarificationQuestion = nil
		cloned.CandidateTaskInstruction = nil
		cloned.CandidatePlanGoal = nil
	case plan != nil && plan.NeedsClarification:
		cloned.ResponseMode = ResponseModeClarifyFirst
		cloned.ChatFulfillable = false
		cloned.WorkflowCommitment = false
		cloned.NeedsClarification = true
		cloned.ClarificationQuestion = normalizeOptionalText(plan.ClarificationQuestion)
		cloned.CandidateTaskInstruction = nil
		cloned.CandidatePlanGoal = nil
	case plan != nil && plan.ChatFulfillable:
		cloned.ResponseMode = ResponseModeAnswerOnly
		cloned.ChatFulfillable = true
		cloned.WorkflowCommitment = false
		cloned.NeedsClarification = false
		cloned.ClarificationQuestion = nil
		cloned.CandidateTaskInstruction = nil
		cloned.CandidatePlanGoal = nil
	case verification != nil && verification.ApproveWorkflow:
		cloned.ResponseMode = ResponseModeAnswerThenTaskCard
		cloned.ChatFulfillable = false
		cloned.WorkflowCommitment = true
		cloned.NeedsClarification = false
		cloned.ClarificationQuestion = nil
		if verification.RevisedInstruction != nil {
			cloned.CandidateTaskInstruction = normalizeOptionalText(verification.RevisedInstruction)
		} else if plan != nil {
			cloned.CandidateTaskInstruction = normalizeOptionalText(plan.CandidateInstruction)
		}
		if plan != nil {
			cloned.CandidatePlanGoal = normalizeOptionalText(plan.CandidatePlanGoal)
		} else {
			cloned.CandidatePlanGoal = nil
		}
	case plan != nil && plan.ShouldEnterWorkflow:
		cloned.ResponseMode = ResponseModeAnswerThenTaskCard
		cloned.ChatFulfillable = false
		cloned.WorkflowCommitment = true
		cloned.NeedsClarification = false
		cloned.ClarificationQuestion = nil
		cloned.CandidateTaskInstruction = normalizeOptionalText(plan.CandidateInstruction)
		cloned.CandidatePlanGoal = normalizeOptionalText(plan.CandidatePlanGoal)
	case !policy.AllowTaskSuggestion && (cloned.ResponseMode == ResponseModeAnswerThenTaskCard || cloned.ResponseMode == ResponseModePlanThenAnswer):
		cloned.ResponseMode = ResponseModeAnswerOnly
	}

	return &cloned
}

// preparedTurnContext 归拢preparedTurn所需的上下文依赖，避免调用方手工拼装。
type preparedTurnContext struct {
	AdditionalInputs []postgres.AssistantMessageInput
	History          []postgres.AssistantMessage
}

// prepareTurnContext 为 `Turn上下文` 准备后续流程所需上下文，收口前置整理逻辑。
func (s *Service) prepareTurnContext(
	ctx context.Context,
	history []postgres.AssistantMessage,
	content string,
) (*preparedTurnContext, error) {
	candidate := DetectInlineMaterial(content)
	if !candidate.HasMaterial {
		return nil, nil
	}
	if s.importer == nil {
		return nil, errors.New("文档导入器未配置")
	}

	result, err := s.importer.ImportDocument(ctx, ImportDocumentInput{
		FileName:      candidate.SyntheticName,
		Content:       []byte(candidate.Body),
		SourceType:    "inline_text",
		VersionSource: "assistant_inline_text",
	})
	if err != nil {
		return nil, err
	}
	if result == nil || result.Resource == nil {
		return nil, errors.New("内联正文导入后缺少资源")
	}

	fileInput, err := buildMessageInput(RoleAssistant, KindSessionFile, SessionFilePayload{
		FileName:      candidate.SyntheticName,
		ResourceID:    result.Resource.ID,
		ResourceTitle: result.Resource.Title,
		SourceType:    result.Resource.SourceType,
		Status:        "ready",
	})
	if err != nil {
		return nil, err
	}

	preparedHistory := append([]postgres.AssistantMessage(nil), history...)
	preparedHistory = append(preparedHistory, postgres.AssistantMessage{
		Role:       RoleAssistant,
		Kind:       KindSessionFile,
		Payload:    fileInput.Payload,
		SequenceNo: len(preparedHistory) + 1,
	})

	return &preparedTurnContext{
		AdditionalInputs: []postgres.AssistantMessageInput{fileInput},
		History:          preparedHistory,
	}, nil
}

// resourceContext 归拢资源所需的上下文依赖，避免调用方手工拼装。
type resourceContext struct {
	ID     string
	Source string
	Title  string
}

// latestResourceFromMessages 从 `消息` 派生 `latest资源`，避免调用方重复拼装适配层。
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

// buildTaskSuggestion 组装 `任务建议`，统一建议生成后的确认与展示字段。
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

// heuristicDeliberationAgent 在显式 deliberation agent 尚未接线时提供兼容性的临时判定。
type heuristicDeliberationAgent struct{}

// Deliberate 基于现有 readiness 规则生成过渡期 deliberation 决策。
func (heuristicDeliberationAgent) Deliberate(_ context.Context, state RuntimeState) (*DeliberationDecision, error) {
	taskDecision := EvaluateTaskSuggestion(TaskSuggestionEvaluationInput{
		CurrentMessage: state.Message,
		ActiveResource: state.ActiveResource,
	})

	decision := &DeliberationDecision{
		RequestKind:         "discussion",
		ResponseMode:        ResponseModeAnswerOnly,
		ChatFulfillable:     true,
		EvidenceSufficiency: "insufficient",
		Confidence:          0.5,
		Reasons:             []string{"fallback heuristic deliberation"},
	}
	if state.ActiveResource != nil {
		decision.EvidenceSufficiency = "sufficient"
	}

	switch taskDecision.IntentState {
	case IntentStateCapabilityQuery:
		decision.RequestKind = "capability_query"
	case IntentStateExecution:
		decision.RequestKind = "workflow_command"
		decision.ChatFulfillable = false
		if state.ActiveResource != nil {
			decision.ResponseMode = ResponseModeAnswerThenTaskCard
			decision.WorkflowCommitment = true
			decision.CandidateTaskInstruction = stringPointer(strings.TrimSpace(state.Message))
			break
		}
		decision.ResponseMode = ResponseModeClarifyFirst
		decision.ChatFulfillable = true
		decision.NeedsClarification = true
		decision.ClarificationQuestion = stringPointer("要进入任务流还需要可执行的材料，你想先上传文件还是继续说明要修改的内容？")
	default:
		if state.ActiveResource != nil {
			decision.RequestKind = "readback"
			decision.ResponseMode = ResponseModeAnswerWithGrounding
		}
	}

	return decision, nil
}

// buildSessionTitle 生成 `会话标题`，避免上层重复拼接展示文案。
func buildSessionTitle(content string) string {
	runes := []rune(strings.TrimSpace(content))
	if len(runes) <= 24 {
		return string(runes)
	}

	return string(runes[:24]) + "..."
}

// buildUploadSessionTitle 为首屏上传场景生成会话标题，优先使用解析后的资源标题。
func buildUploadSessionTitle(fileName string, resource *postgres.Resource) string {
	if resource != nil {
		if title := strings.TrimSpace(resource.Title); title != "" {
			return buildSessionTitle(title)
		}
	}

	baseName := strings.TrimSpace(fileName)
	if dot := strings.LastIndex(baseName, "."); dot > 0 {
		baseName = baseName[:dot]
	}
	if baseName == "" {
		baseName = "未命名文件"
	}

	return buildSessionTitle(baseName)
}

// decodeTaskSuggestion 解码 `任务建议` 载荷，避免上层直接处理原始 JSON。
func decodeTaskSuggestion(payload []byte) (TaskSuggestionPayload, error) {
	var value TaskSuggestionPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return TaskSuggestionPayload{}, err
	}

	return value, nil
}

// decodeSessionFile 解码 `会话文件` 载荷，避免上层直接处理原始 JSON。
func decodeSessionFile(payload []byte) (SessionFilePayload, error) {
	var value SessionFilePayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return SessionFilePayload{}, err
	}

	return value, nil
}

// decodeTextInputPayload 解码 `文本消息输入` 载荷，供 direct read 流式分支复用。
func decodeTextInputPayload(payload []byte) (TextPayload, error) {
	var value TextPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return TextPayload{}, err
	}

	return value, nil
}

// stringPointer 返回字符串指针，简化构造可选文本字段时的样板代码。
func stringPointer(value string) *string {
	return &value
}

func derefOptionalString(value *string) string {
	if value == nil {
		return ""
	}

	return strings.TrimSpace(*value)
}

// buildTaskDetailURL 拼装 `任务详情URL` 链接，统一助手里的页面跳转规则。
func buildTaskDetailURL(taskID string, sessionID string) string {
	return buildSessionAwareURL("/tasks/"+taskID, sessionID)
}

// buildResourceDetailURL 拼装 `资源详情URL` 链接，统一助手里的页面跳转规则。
func buildResourceDetailURL(resourceID string, sessionID string) string {
	return buildSessionAwareURL("/resources/"+resourceID, sessionID)
}

// buildSessionAwareURL 拼装 `会话感知URL` 链接，统一助手里的页面跳转规则。
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

// streamAssistantReplyInput 归拢流式消息助手Reply所需的输入字段，避免调用方散落传参。
type streamAssistantReplyInput struct {
	content   string
	emit      func(StreamEvent) error
	history   []postgres.AssistantMessage
	sessionID string
}

// streamAssistantReply 启动 `助手Reply` 的流式处理，统一增量输出协议。
func (s *Service) streamAssistantReply(ctx context.Context, input streamAssistantReplyInput) error {
	if s.responder == nil {
		return errors.New("助手对话模型未配置")
	}

	replyHistory := input.history
	prepared, err := s.prepareTurnContext(ctx, input.history, input.content)
	if err != nil {
		return err
	}
	if prepared != nil && len(prepared.AdditionalInputs) > 0 {
		preparedMessages, err := s.repo.AppendMessages(ctx, input.sessionID, prepared.AdditionalInputs)
		if err != nil {
			return err
		}
		if err := s.projectPersistedMessages(ctx, input.sessionID, preparedMessages); err != nil {
			return err
		}

		replyHistory = append(append([]postgres.AssistantMessage(nil), input.history...), preparedMessages...)
		for index := range preparedMessages {
			if preparedMessages[index].Kind != KindSessionFile {
				continue
			}
			message := preparedMessages[index]
			if err := input.emit(StreamEvent{
				Type:    StreamEventSessionFile,
				Message: &message,
			}); err != nil {
				return err
			}
		}
	}

	replyContext, err := s.loadReplyContext(ctx, input.sessionID, replyHistory, input.content)
	if err != nil {
		return err
	}
	replyContext, err = s.repairReplyContext(ctx, input.content, replyContext)
	if err != nil {
		return err
	}

	runtimeState := s.stateBuilder.Build(input.content, replyContext)
	decision, policy, err := s.deliberateTurn(ctx, runtimeState)
	if err != nil {
		return err
	}
	planDecision, err := s.planWorkflow(ctx, runtimeState, decision, policy)
	if err != nil {
		return err
	}
	verificationDecision, err := s.verifyWorkflow(ctx, runtimeState, decision, policy, planDecision)
	if err != nil {
		return err
	}
	replyDecision := decisionForReply(decision, policy, planDecision, verificationDecision)
	var correctionSignal *RuntimeCorrectionSignal
	if runtimeState.PendingTaskSuggestion != nil {
		correctionSignal = DetectExplicitWorkflowCorrection(input.content)
	}
	turnBuild := &replyBuildResult{
		ReplyContext:                   replyContext,
		RuntimeState:                   runtimeState,
		Decision:                       decision,
		Policy:                         policy,
		PlanDecision:                   planDecision,
		VerificationDecision:           verificationDecision,
		ReplyDecision:                  replyDecision,
		PendingSuggestion:              clonePendingTaskSuggestion(runtimeState.PendingTaskSuggestion),
		PreviousClarificationMessageID: latestClarificationPromptMessageID(replyHistory),
		CorrectionSignal:               correctionSignal,
	}

	deterministicInputs, usedDeterministicRead, err := s.tryBuildDeterministicReadInputs(ctx, input.content, replyContext)
	if err != nil {
		return err
	}
	if usedDeterministicRead {
		replyPayload, decodeErr := decodeTextInputPayload(deterministicInputs[0].Payload)
		if decodeErr != nil {
			return decodeErr
		}

		if err := input.emit(StreamEvent{Type: StreamEventMessageStarted}); err != nil {
			return err
		}
		if err := input.emit(StreamEvent{
			Type:  StreamEventMessageDelta,
			Delta: replyPayload.Content,
		}); err != nil {
			return err
		}

		messages, appendErr := s.repo.AppendMessages(ctx, input.sessionID, deterministicInputs)
		if appendErr != nil {
			return appendErr
		}
		if len(messages) == 0 {
			return NewStreamError(StreamErrorCodeInternal, "助手暂时不可用，请稍后重试。", errors.New("deterministic read persisted no messages"))
		}
		if err := s.projectPersistedMessages(ctx, input.sessionID, messages); err != nil {
			return err
		}
		if err := s.projectReplyGrounding(ctx, input.sessionID, replyContext); err != nil {
			return err
		}
		if err := s.recordRuntimeLearningForTurn(ctx, turnBuild, messages); err != nil {
			return err
		}
		s.triggerSummaryRefresh(input.sessionID)

		return input.emit(StreamEvent{
			Type:    StreamEventMessageCompleted,
			Message: &messages[0],
		})
	}
	if failureReply := strings.TrimSpace(replyContext.CurrentFileFailureReply); failureReply != "" {
		assistantInput, err := buildMessageInput(RoleAssistant, KindText, TextPayload{Content: failureReply})
		if err != nil {
			return err
		}
		replyPayload, decodeErr := decodeTextInputPayload(assistantInput.Payload)
		if decodeErr != nil {
			return decodeErr
		}

		if err := input.emit(StreamEvent{Type: StreamEventMessageStarted}); err != nil {
			return err
		}
		if err := input.emit(StreamEvent{
			Type:  StreamEventMessageDelta,
			Delta: replyPayload.Content,
		}); err != nil {
			return err
		}

		messages, appendErr := s.repo.AppendMessages(ctx, input.sessionID, []postgres.AssistantMessageInput{assistantInput})
		if appendErr != nil {
			return appendErr
		}
		if len(messages) == 0 {
			return NewStreamError(StreamErrorCodeInternal, "助手暂时不可用，请稍后重试。", errors.New("current-file failure reply persisted no messages"))
		}
		if err := s.projectPersistedMessages(ctx, input.sessionID, messages); err != nil {
			return err
		}
		if err := s.projectReplyGrounding(ctx, input.sessionID, replyContext); err != nil {
			return err
		}
		if err := s.recordRuntimeLearningForTurn(ctx, turnBuild, messages); err != nil {
			return err
		}
		s.triggerSummaryRefresh(input.sessionID)

		return input.emit(StreamEvent{
			Type:    StreamEventMessageCompleted,
			Message: &messages[0],
		})
	}

	stream, err := s.responder.Stream(ctx, ChatCompletionInput{
		RuntimeState:             runtimeState,
		Snapshot:                 replyContext.Snapshot,
		Citations:                replyContext.Citations,
		GroundedTarget:           replyContext.GroundedTarget,
		CanonicalAnalysisContext: replyContext.CanonicalAnalysisContext,
		History:                  replyContext.History,
		Message:                  input.content,
		Resource:                 replyContext.ActiveResource,
		Decision:                 replyDecision,
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

	assistantInputs, err := buildAssistantReplyInputsFromPolicy(result, replyDecision, policy, runtimeState.ActiveResource)
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
	if err := s.projectReplyGrounding(ctx, input.sessionID, replyContext); err != nil {
		return err
	}
	if err := s.recordRuntimeLearningForTurn(ctx, turnBuild, messages); err != nil {
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

// triggerSummaryRefresh 触发 `摘要Refresh`，避免上层直接管理异步副作用。
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

// refreshRollingSummary 刷新 `滚动摘要`，让后续读取看到最新状态。
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

// initSessionSnapshot 初始化 `会话快照`，统一首轮运行时的默认状态。
func (s *Service) initSessionSnapshot(ctx context.Context, sessionID string) error {
	if s.projector == nil {
		return nil
	}

	return s.projector.InitSession(ctx, sessionID)
}

// projectPersistedMessages 把 `Persisted消息` 投影回助手状态，保持后续读取口径一致。
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

// mapAssistantStreamError 把 `助手流式消息Error` 转换成助手接口需要的结构，避免上层直接感知内部模型。
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

// loadReplyContext 加载 `Reply上下文`，为后续助手流程准备输入。
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

// repairReplyContext 修复 `Reply上下文` 中缺失或不一致的部分，避免脏状态继续向后传播。
func (s *Service) repairReplyContext(ctx context.Context, currentMessage string, replyContext *ReplyContext) (*ReplyContext, error) {
	if replyContext == nil {
		return nil, nil
	}
	intent := ClassifyReadIntent(currentMessage)
	directAccessEnabled := s != nil && s.sectionLocator != nil && s.sectionReader != nil

	enriched, err := s.enrichReplyContextWithCanonicalRead(ctx, currentMessage, intent, replyContext)
	if err != nil {
		return nil, err
	}
	replyContext = enriched

	if s == nil || replyContext.ActiveResource == nil {
		return replyContext, nil
	}

	ok, _ := EvaluateEvidenceQuality(EvidenceEvaluationInput{
		QueryIntent:    replyContext.QueryIntent,
		ResolvedTarget: replyContext.GroundedTarget,
		Citations:      replyContext.Citations,
		CanonicalRead:  replyContext.CanonicalRead,
	})
	if ok {
		return replyContext, nil
	}
	if s.retriever == nil {
		if directAccessEnabled && shouldReturnCurrentFileFailure(intent, replyContext) {
			replyContext.CurrentFileFailureReply = buildCurrentFileFailureReply(currentMessage)
		}

		return replyContext, nil
	}
	if !directAccessEnabled {
		repairQuery := buildRepairRetrievalQuery(currentMessage, replyContext)
		if strings.TrimSpace(repairQuery) == "" {
			return replyContext, nil
		}

		repairedCitations, err := s.retriever.SearchByResource(ctx, replyContext.ActiveResource.ID, repairQuery, 4)
		if err != nil {
			return nil, err
		}
		if len(repairedCitations) == 0 {
			return replyContext, nil
		}

		replyContext.Citations = repairedCitations
		return replyContext, nil
	}

	repairQuery := buildCurrentFileFallbackQuery(currentMessage, replyContext)
	if strings.TrimSpace(repairQuery) == "" {
		if shouldReturnCurrentFileFailure(intent, replyContext) {
			replyContext.CurrentFileFailureReply = buildCurrentFileFailureReply(currentMessage)
		}

		return replyContext, nil
	}

	repairedCitations, err := s.retriever.SearchByResource(ctx, replyContext.ActiveResource.ID, repairQuery, 4)
	if err != nil {
		return nil, err
	}
	if len(repairedCitations) == 0 {
		if shouldReturnCurrentFileFailure(intent, replyContext) {
			replyContext.CurrentFileFailureReply = buildCurrentFileFailureReply(currentMessage)
		}

		return replyContext, nil
	}

	replyContext.Citations = repairedCitations
	enriched, err = s.enrichReplyContextFromFallbackCitations(ctx, currentMessage, intent, replyContext)
	if err != nil {
		return nil, err
	}
	replyContext = enriched

	ok, _ = EvaluateEvidenceQuality(EvidenceEvaluationInput{
		QueryIntent:    replyContext.QueryIntent,
		ResolvedTarget: replyContext.GroundedTarget,
		Citations:      replyContext.Citations,
		CanonicalRead:  replyContext.CanonicalRead,
	})
	if !ok && shouldReturnCurrentFileFailure(intent, replyContext) {
		replyContext.CurrentFileFailureReply = buildCurrentFileFailureReply(currentMessage)
	}

	return replyContext, nil
}

// buildRepairRetrievalQuery 组装 `修复Retrieval查询`，把检索与过滤规则收口在单点。
func buildRepairRetrievalQuery(_ string, replyContext *ReplyContext) string {
	if replyContext == nil || replyContext.GroundedTarget == nil {
		return ""
	}

	entityName := strings.TrimSpace(replyContext.GroundedTarget.EntityName)
	if entityName == "" {
		return ""
	}

	switch strings.TrimSpace(replyContext.QueryIntent) {
	case "detail_by_ordinal", "detail_by_entity":
		return entityName + " 项目做了什么"
	case "aggregate_attribute":
		return entityName + " 技术栈"
	default:
		return ""
	}
}

// enrichReplyContextWithCanonicalRead 在分析型或当前文件定向请求下优先补齐 canonical read 结果。
func (s *Service) enrichReplyContextWithCanonicalRead(
	ctx context.Context,
	currentMessage string,
	intent ReadIntent,
	replyContext *ReplyContext,
) (*ReplyContext, error) {
	if s == nil || s.sectionLocator == nil || s.sectionReader == nil || replyContext == nil || replyContext.ActiveResource == nil {
		return replyContext, nil
	}
	if !requiresCanonicalCurrentFileRead(intent) || replyContext.CanonicalRead != nil {
		return replyContext, nil
	}

	located, err := s.sectionLocator.Locate(ctx, LocateSectionInput{
		ResourceID:      replyContext.ActiveResource.ID,
		Message:         currentMessage,
		Intent:          intent,
		ActiveSectionID: activeSectionIDFromSnapshot(replyContext.Snapshot),
	})
	if err != nil {
		return nil, err
	}
	if located == nil {
		return replyContext, nil
	}

	readResult, err := s.sectionReader.Read(ctx, CanonicalReadInput{
		ResourceID: replyContext.ActiveResource.ID,
		VersionID:  located.VersionID,
		Located:    located,
	})
	if err != nil {
		if errors.Is(err, ErrCanonicalReadUnavailable) {
			return replyContext, nil
		}

		return nil, err
	}
	if readResult == nil {
		return replyContext, nil
	}

	return applyCanonicalReadToReplyContext(currentMessage, replyContext, located, readResult)
}

// enrichReplyContextFromFallbackCitations 尝试把当前文件内 fallback citations 重新收敛成 canonical section 正文。
func (s *Service) enrichReplyContextFromFallbackCitations(
	ctx context.Context,
	currentMessage string,
	intent ReadIntent,
	replyContext *ReplyContext,
) (*ReplyContext, error) {
	if s == nil || s.sectionReader == nil || replyContext == nil || !requiresCanonicalCurrentFileRead(intent) {
		return replyContext, nil
	}
	if len(replyContext.Citations) == 0 {
		return replyContext, nil
	}

	first := replyContext.Citations[0]
	if strings.TrimSpace(first.SectionID) == "" {
		return replyContext, nil
	}

	readResult, err := s.sectionReader.Read(ctx, CanonicalReadInput{
		ResourceID: replyContext.ActiveResource.ID,
		Located: &LocatedSection{
			Mode:        LocatedSectionModeSection,
			SectionID:   strings.TrimSpace(first.SectionID),
			SectionType: strings.TrimSpace(first.SectionType),
			Reason:      "current_file_retrieval_fallback",
		},
	})
	if err != nil {
		if errors.Is(err, ErrCanonicalReadUnavailable) {
			return replyContext, nil
		}

		return nil, err
	}
	if readResult == nil {
		return replyContext, nil
	}

	return applyCanonicalReadToReplyContext(currentMessage, replyContext, &LocatedSection{
		Mode:        LocatedSectionModeSection,
		SectionID:   strings.TrimSpace(first.SectionID),
		SectionType: strings.TrimSpace(first.SectionType),
		Reason:      "current_file_retrieval_fallback",
	}, readResult)
}

// applyCanonicalReadToReplyContext 把 canonical read 结果回填到 reply context，供 responder 和 evidence gate 共用。
func applyCanonicalReadToReplyContext(
	currentMessage string,
	replyContext *ReplyContext,
	located *LocatedSection,
	readResult *CanonicalReadResult,
) (*ReplyContext, error) {
	if replyContext == nil || readResult == nil {
		return replyContext, nil
	}

	replyContext.CanonicalRead = readResult
	replyContext.CurrentFileFailureReply = ""
	if located != nil && replyContext.GroundedTarget == nil && readResult.Mode == CanonicalReadModeSection {
		replyContext.GroundedTarget = &ResolvedReference{
			SectionID:   strings.TrimSpace(readResult.SectionID),
			SectionType: strings.TrimSpace(readResult.SectionType),
			Reason:      strings.TrimSpace(located.Reason),
		}
	}
	if shouldBuildCanonicalAnalysisContext(currentMessage) {
		result, err := BuildGroundedAnalysisInput(GroundedAnalysisInput{
			Message:    currentMessage,
			ReadResult: *readResult,
		})
		if err != nil {
			if errors.Is(err, ErrCanonicalReadUnavailable) {
				return replyContext, nil
			}

			return nil, err
		}
		if result != nil {
			replyContext.CanonicalAnalysisContext = strings.TrimSpace(result.AnalysisContext)
		}
	}

	return replyContext, nil
}

// buildCurrentFileFallbackQuery 组装当前文件内的一次 fallback 检索查询，避免退回全局问答模式。
func buildCurrentFileFallbackQuery(currentMessage string, replyContext *ReplyContext) string {
	if query := strings.TrimSpace(buildRepairRetrievalQuery(currentMessage, replyContext)); query != "" {
		return query
	}

	return strings.TrimSpace(currentMessage)
}

// requiresCanonicalCurrentFileRead 判断当前 intent 是否需要“先读 canonical 内容再继续”。
func requiresCanonicalCurrentFileRead(intent ReadIntent) bool {
	switch intent.Kind {
	case ReadIntentListSections, ReadIntentLocateOrdinal, ReadIntentExcerptSection, ReadIntentAggregateAttribute, ReadIntentAnalyzeSection, ReadIntentTransformSection:
		return true
	default:
		return false
	}
}

// shouldBuildCanonicalAnalysisContext 判断当前请求是否需要把 canonical 内容进一步包装成分析提示上下文。
func shouldBuildCanonicalAnalysisContext(currentMessage string) bool {
	intent := ClassifyReadIntent(currentMessage)
	switch intent.Kind {
	case ReadIntentAggregateAttribute, ReadIntentAnalyzeSection, ReadIntentTransformSection:
		return true
	default:
		return false
	}
}

// shouldReturnCurrentFileFailure 判断当前轮是否应在当前文件 direct access 失败后直接给出显式失败语义。
func shouldReturnCurrentFileFailure(intent ReadIntent, replyContext *ReplyContext) bool {
	return replyContext != nil && replyContext.ActiveResource != nil && requiresCanonicalCurrentFileRead(intent)
}

// buildCurrentFileFailureReply 返回当前文件 direct access 失败时的统一显式失败文案。
func buildCurrentFileFailureReply(currentMessage string) string {
	message := strings.TrimSpace(currentMessage)
	if message == "" {
		return "我还没能在当前文件里稳定定位到你要看的内容，请直接指出项目名、章节名，或说明是第几个项目。"
	}

	return "我还没能在当前文件里稳定定位到你说的目标内容，请直接指出项目名、章节名，或说明是第几个项目。"
}

// projectReplyGrounding 把 `Replygrounding` 投影回助手状态，保持后续读取口径一致。
func (s *Service) projectReplyGrounding(ctx context.Context, sessionID string, replyContext *ReplyContext) error {
	if s == nil || s.projector == nil || strings.TrimSpace(sessionID) == "" || replyContext == nil {
		return nil
	}

	return s.projector.ProjectGroundingState(ctx, buildGroundingProjection(sessionID, replyContext))
}

// buildGroundingProjection 组装 `groundingProjection`，保持状态投影结果在不同调用点口径一致。
func buildGroundingProjection(sessionID string, replyContext *ReplyContext) GroundingStateProjection {
	projection := GroundingStateProjection{
		SessionID:              strings.TrimSpace(sessionID),
		LastCitationWindows:    buildCitationWindows(replyContext.Citations),
		LastEnumeratedEntities: buildEnumeratedEntities(replyContext),
		OrdinalReferenceFrame:  buildOrdinalReferenceFrame(replyContext),
	}

	if replyContext != nil && replyContext.GroundedTarget != nil {
		projection.ActiveSectionID = strings.TrimSpace(replyContext.GroundedTarget.SectionID)
		projection.ActiveSectionType = strings.TrimSpace(replyContext.GroundedTarget.SectionType)
		projection.ActiveEntityName = strings.TrimSpace(replyContext.GroundedTarget.EntityName)
	}
	if projection.ActiveSectionID == "" && len(replyContext.Citations) > 0 {
		first := replyContext.Citations[0]
		projection.ActiveSectionID = strings.TrimSpace(first.SectionID)
		projection.ActiveSectionType = strings.TrimSpace(first.SectionType)
		projection.ActiveEntityName = deriveCitationEntityName(first)
	}

	return projection
}

// buildCitationWindows 组装 `引用窗口`，尽量保留命中内容前后的局部上下文。
func buildCitationWindows(citations []citation.Citation) []postgres.CitationWindow {
	windows := make([]postgres.CitationWindow, 0, len(citations))
	seen := make(map[string]struct{}, len(citations))
	for _, item := range citations {
		sectionID := strings.TrimSpace(item.SectionID)
		if sectionID == "" {
			continue
		}

		windowGroupID := ""
		if item.Window != nil {
			windowGroupID = strings.TrimSpace(item.Window.GroupID)
		}
		key := sectionID + "|" + windowGroupID
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		windows = append(windows, postgres.CitationWindow{
			SectionID:     sectionID,
			SectionType:   strings.TrimSpace(item.SectionType),
			WindowGroupID: windowGroupID,
		})
	}

	return windows
}

// buildEnumeratedEntities 组装 `枚举Entities`，统一引用和检索结果里的实体提取口径。
func buildEnumeratedEntities(replyContext *ReplyContext) []postgres.EnumeratedEntity {
	if replyContext == nil {
		return nil
	}

	entities := make([]postgres.EnumeratedEntity, 0, len(replyContext.Citations))
	seen := make(map[string]struct{}, len(replyContext.Citations))
	for _, item := range replyContext.Citations {
		sectionID := strings.TrimSpace(item.SectionID)
		if sectionID == "" {
			continue
		}
		if _, ok := seen[sectionID]; ok {
			continue
		}

		seen[sectionID] = struct{}{}
		entities = append(entities, postgres.EnumeratedEntity{
			SectionID:   sectionID,
			SectionType: strings.TrimSpace(item.SectionType),
			EntityName:  deriveEntityName(replyContext, item),
			Ordinal:     len(entities) + 1,
		})
	}

	return entities
}

// buildOrdinalReferenceFrame 整理回答里出现的第一段、第二段这类序号引用线索，供后续检索按顺序定位 section。
func buildOrdinalReferenceFrame(replyContext *ReplyContext) []postgres.OrdinalReference {
	entities := buildEnumeratedEntities(replyContext)
	frame := make([]postgres.OrdinalReference, 0, len(entities))
	for _, entity := range entities {
		frame = append(frame, postgres.OrdinalReference{
			Ordinal:     entity.Ordinal,
			SectionID:   entity.SectionID,
			SectionType: entity.SectionType,
			EntityName:  entity.EntityName,
		})
	}

	return frame
}

// deriveEntityName 从已有上下文推导 `EntityName`，让后续链路只消费归一化结果。
func deriveEntityName(replyContext *ReplyContext, item citation.Citation) string {
	if replyContext != nil && replyContext.GroundedTarget != nil &&
		strings.TrimSpace(replyContext.GroundedTarget.SectionID) == strings.TrimSpace(item.SectionID) &&
		strings.TrimSpace(replyContext.GroundedTarget.EntityName) != "" {
		return strings.TrimSpace(replyContext.GroundedTarget.EntityName)
	}

	return deriveCitationEntityName(item)
}

// deriveCitationEntityName 从已有上下文推导 `引用EntityName`，让后续链路只消费归一化结果。
func deriveCitationEntityName(item citation.Citation) string {
	if title := strings.TrimSpace(item.SectionTitle); title != "" {
		return title
	}

	return strings.TrimSpace(item.SectionID)
}

// snapshotReaderFromProjector 从 `投影器` 派生 `快照读取器`，避免调用方重复拼装适配层。
func snapshotReaderFromProjector(projector sessionContextProjector) sessionContextSnapshotReader {
	concrete, ok := projector.(*SessionContextProjector)
	if !ok || concrete == nil {
		return nil
	}

	return concrete.repo
}

// selectSummaryTranscript 从候选内容里选择 `摘要转录`，把筛选策略收口到单点。
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
