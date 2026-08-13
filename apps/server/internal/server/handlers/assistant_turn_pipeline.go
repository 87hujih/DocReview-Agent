package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agent_project/apps/server/internal/agent/cutover"
	agentturn "agent_project/apps/server/internal/agent/turn"
	"agent_project/apps/server/internal/assistant"
	"agent_project/apps/server/internal/storage/postgres"
)

// LegacyAssistantRunner keeps 旧版 as the rollback implementation while
// 处理失败： giving both HTTP transports 一个 Execute seam. It deliberately uses the
// 旧版 streaming orchestration 用于 both projections, then reads the 公开的
// conversation DTO 来自 persisted messages.
type LegacyAssistantRunner struct {
	service assistantService
}

// NewLegacyAssistantRunner 校验依赖并创建对应实例。
func NewLegacyAssistantRunner(service assistantService) *LegacyAssistantRunner {
	return &LegacyAssistantRunner{service: service}
}

// Execute 执行该函数负责的核心处理逻辑。
func (runner *LegacyAssistantRunner) Execute(ctx context.Context, request cutover.Request, observe cutover.Observer) (cutover.Result, error) {
	if runner == nil || runner.service == nil {
		return cutover.Result{}, fmt.Errorf("旧版助手服务不能为空")
	}
	request.Message = strings.TrimSpace(request.Message)
	if request.Message == "" {
		return cutover.Result{}, assistant.ErrMessageRequired
	}
	events := make([]cutover.Event, 0, 8)
	sessionID := strings.TrimSpace(request.SessionID)
	emit := func(event assistant.StreamEvent) error {
		if event.Type == assistant.StreamEventSessionCreated && event.Session != nil {
			sessionID = event.Session.ID
		}
		payload, err := legacyEventPayload(event)
		if err != nil {
			return err
		}
		projected := cutover.Event{Sequence: len(events) + 1, Type: event.Type, Payload: payload}
		events = append(events, projected)
		if observe != nil && projected.Sequence > request.AfterSequence {
			return observe(projected)
		}
		return nil
	}
	var err error
	if sessionID == "" {
		err = runner.service.StartConversationStream(ctx, request.Message, emit)
	} else {
		err = runner.service.AppendMessageStream(ctx, sessionID, request.Message, emit)
	}
	if err != nil {
		return cutover.Result{}, err
	}
	if sessionID == "" {
		return cutover.Result{}, fmt.Errorf("旧版 stream did 未持久化一个 session")
	}
	done := cutover.Event{Sequence: len(events) + 1, Type: assistant.StreamEventDone, Payload: json.RawMessage(`{}`)}
	events = append(events, done)
	if observe != nil && done.Sequence > request.AfterSequence {
		if err := observe(done); err != nil {
			return cutover.Result{}, err
		}
	}
	conversation, err := runner.service.GetConversation(ctx, sessionID)
	if err != nil {
		return cutover.Result{}, err
	}
	if conversation == nil {
		return cutover.Result{}, fmt.Errorf("旧版 conversation 投影不可用")
	}
	dto, err := json.Marshal(assistantConversationResponse{
		Session: toAssistantSessionResponse(conversation.Session), Messages: toAssistantMessageResponses(conversation.Messages),
	})
	if err != nil {
		return cutover.Result{}, err
	}
	return cutover.Result{Mode: cutover.ModeLegacy, DTO: dto, Events: events}, nil
}

// legacyEventPayload 执行该函数负责的核心处理逻辑。
func legacyEventPayload(event assistant.StreamEvent) (json.RawMessage, error) {
	var payload any = struct{}{}
	// 根据当前状态或类型选择对应的处理分支。
	switch event.Type {
	case assistant.StreamEventSessionCreated:
		if event.Session != nil {
			payload = assistantStreamSessionResponse{Session: toAssistantSessionResponse(*event.Session)}
		}
	case assistant.StreamEventMessageDelta:
		payload = assistantStreamDeltaResponse{Delta: event.Delta}
	case assistant.StreamEventSessionFile, assistant.StreamEventMessageCompleted, assistant.StreamEventTaskSuggestion:
		if event.Message != nil {
			payload = assistantStreamMessageResponse{Message: toAssistantMessageResponse(*event.Message)}
		}
	}
	return json.Marshal(payload)
}

type ConversationPublicProjector struct {
	reader conversationProjectionReader
}

type conversationProjectionReader interface {
	GetSessionByID(ctx context.Context, sessionID string) (*postgres.AssistantSession, error)
	ListMessages(ctx context.Context, sessionID string) ([]postgres.AssistantMessage, error)
}

// NewConversationPublicProjector 校验依赖并创建对应实例。
func NewConversationPublicProjector(reader conversationProjectionReader) *ConversationPublicProjector {
	return &ConversationPublicProjector{reader: reader}
}

// 投影 执行该函数负责的核心处理逻辑。
func (projector *ConversationPublicProjector) Project(ctx context.Context, turn agentturn.Turn, status agentturn.Status, _ []agentturn.Event) (json.RawMessage, error) {
	// 根据当前状态或类型选择对应的处理分支。
	switch status {
	case agentturn.StatusWaitingInput, agentturn.StatusWaitingApproval,
		agentturn.StatusSucceeded, agentturn.StatusFailed, agentturn.StatusCancelled:
	default:
		return nil, fmt.Errorf("处理失败：轮次状态 %q 为 not 一个 deterministic 公开的状态", status)
	}
	if projector == nil || projector.reader == nil || strings.TrimSpace(turn.SessionID) == "" {
		return nil, fmt.Errorf("conversation 公开的投影不可用")
	}
	session, err := projector.reader.GetSessionByID(ctx, turn.SessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("conversation 公开的投影未找到")
	}
	messages, err := projector.reader.ListMessages(ctx, turn.SessionID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(assistantConversationResponse{
		Session: toAssistantSessionResponse(*session), Messages: toAssistantMessageResponses(messages),
	})
}

var _ cutover.Runner = (*LegacyAssistantRunner)(nil)
var _ cutover.PublicProjector = (*ConversationPublicProjector)(nil)
