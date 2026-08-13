package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/agent/cutover"
	"agent_project/apps/server/internal/agent/identity"
	agentturn "agent_project/apps/server/internal/agent/turn"
	"agent_project/apps/server/internal/assistant"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// TestAssistantStreamAndNonStreamHandlersUseOneTurnPipeline 验证对应场景下的正常路径与失败路径。
func TestAssistantStreamAndNonStreamHandlersUseOneTurnPipeline(t *testing.T) {
	pipeline := &fakeAssistantTurnPipeline{result: cutover.Result{
		DTO: json.RawMessage(`{"session":{"id":"session-1"},"messages":[]}`),
		Events: []cutover.Event{
			{Sequence: 3, Type: "assistant.message", Payload: json.RawMessage(`{"role":"assistant","kind":"text","payload":{"content":"done"}}`)},
			{Sequence: 4, Type: "turn.succeeded", Payload: json.RawMessage(`{"status":"succeeded"}`)},
		},
	}}
	handler := NewAssistantHandlerWithTurnPipeline(
		fakeAssistantService{}, defaultAssistantUploadMaxBytes, textOnlyAssistantUploadPolicy{},
		pipeline, fakeIdentityAdapter{scope: handlerTrustedScope()},
	)
	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages", handler.AppendMessage)
	engine.POST("/api/assistant/sessions/:id/messages/stream", handler.AppendMessageStream)

	body := []byte(`{"message":"revise","resource_id":"22222222-2222-4222-8222-222222222222"}`)
	nonStream := performAssistantTurnRequestWithHeaders(engine, "/api/assistant/sessions/00000000-0000-0000-0000-000000000001/messages", body,
		ut.Header{Key: identity.HeaderSignature, Value: strings.Repeat("0", 64)})
	if nonStream.StatusCode() != consts.StatusOK || !bytes.Equal(nonStream.Body(), pipeline.result.DTO) {
		t.Fatalf("unexpected non-stream response: status=%d body=%s", nonStream.StatusCode(), nonStream.Body())
	}
	stream := performAssistantTurnRequestWithHeaders(engine, "/api/assistant/sessions/00000000-0000-0000-0000-000000000001/messages/stream", body,
		ut.Header{Key: identity.HeaderSignature, Value: strings.Repeat("0", 64)})
	streamBody := stream.Body()
	if !strings.Contains(string(streamBody), "id: 3") || !strings.Contains(string(streamBody), "event: message_completed") || !strings.Contains(string(streamBody), "event: done") {
		t.Fatalf("expected persisted sequence SSE projection, got %s", streamBody)
	}
	if pipeline.calls != 2 || pipeline.requests[0].RequestID != "request-1" || pipeline.requests[1].AfterSequence != 2 {
		t.Fatalf("both transports must use one pipeline with request/cursor, calls=%d requests=%#v", pipeline.calls, pipeline.requests)
	}
}

// TestAssistantDurableHandlerFailsClosedWhenIdentityAdapterRejects 验证对应场景下的正常路径与失败路径。
func TestAssistantDurableHandlerFailsClosedWhenIdentityAdapterRejects(t *testing.T) {
	pipeline := &fakeAssistantTurnPipeline{}
	handler := NewAssistantHandlerWithTurnPipeline(
		fakeAssistantService{}, defaultAssistantUploadMaxBytes, textOnlyAssistantUploadPolicy{},
		pipeline, fakeIdentityAdapter{err: identity.ErrUntrustedIdentity},
	)
	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages", handler.AppendMessage)
	response := performAssistantTurnRequestWithHeaders(engine, "/api/assistant/sessions/00000000-0000-0000-0000-000000000001/messages", []byte(`{"message":"revise","resource_id":"22222222-2222-4222-8222-222222222222"}`),
		ut.Header{Key: identity.HeaderSignature, Value: strings.Repeat("0", 64)})
	if response.StatusCode() != consts.StatusUnauthorized || pipeline.calls != 0 {
		t.Fatalf("untrusted durable request reached pipeline: status=%d calls=%d body=%s", response.StatusCode(), pipeline.calls, response.Body())
	}
}

// TestAssistantDurableOnlyRequestRequiresTrustedSignature verifies unsigned
// requests cannot bypass the durable identity boundary through a legacy path.
func TestAssistantDurableOnlyRequestRequiresTrustedSignature(t *testing.T) {
	pipeline := &fakeAssistantTurnPipeline{result: cutover.Result{DTO: json.RawMessage(`{"session":{"id":"session-1"},"messages":[]}`)}}
	handler := NewAssistantHandlerWithTurnPipeline(
		fakeAssistantService{}, defaultAssistantUploadMaxBytes, textOnlyAssistantUploadPolicy{},
		pipeline, fakeIdentityAdapter{err: identity.ErrUntrustedIdentity},
	)
	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages", handler.AppendMessage)
	response := performAssistantTurnRequest(engine, "/api/assistant/sessions/00000000-0000-0000-0000-000000000001/messages", []byte(`{"message":"revise","resource_id":"22222222-2222-4222-8222-222222222222"}`))
	if response.StatusCode() != consts.StatusUnauthorized || pipeline.calls != 0 {
		t.Fatalf("unsigned request reached durable pipeline: status=%d calls=%d body=%s", response.StatusCode(), pipeline.calls, response.Body())
	}
}

func TestDurableHandlerConstructorNeverFallsBackWhenPipelineIsMissing(t *testing.T) {
	service := &countingLegacyAssistantService{}
	handler := NewAssistantHandlerWithTurnPipeline(
		service, defaultAssistantUploadMaxBytes, textOnlyAssistantUploadPolicy{},
		nil, fakeIdentityAdapter{scope: handlerTrustedScope()},
	)
	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages", handler.AppendMessage)
	response := performAssistantTurnRequestWithHeaders(
		engine,
		"/api/assistant/sessions/00000000-0000-0000-0000-000000000001/messages",
		[]byte(`{"message":"revise","resource_id":"22222222-2222-4222-8222-222222222222"}`),
		ut.Header{Key: identity.HeaderSignature, Value: strings.Repeat("0", 64)},
	)
	if response.StatusCode() != consts.StatusServiceUnavailable || service.appendDirectCalls != 0 || service.appendStreamCalls != 0 {
		t.Fatalf("missing durable pipeline fell back to legacy: status=%d direct=%d stream=%d body=%s", response.StatusCode(), service.appendDirectCalls, service.appendStreamCalls, response.Body())
	}
}

// TestLegacyAssistantRunnerUsesStreamingOrchestrationForBothTransports 验证对应场景下的正常路径与失败路径。
func TestLegacyAssistantRunnerUsesStreamingOrchestrationForBothTransports(t *testing.T) {
	now := time.Now().UTC()
	conversation := &assistant.ConversationResult{
		Session:  postgres.AssistantSession{ID: testAssistantSessionUUID, Title: "session", CreatedAt: now, UpdatedAt: now, LastMessageAt: now},
		Messages: []postgres.AssistantMessage{{ID: "message-1", SessionID: testAssistantSessionUUID, Role: "assistant", Kind: "text", SequenceNo: 2, Payload: []byte(`{"content":"done"}`), CreatedAt: now}},
	}
	service := &countingLegacyAssistantService{fakeAssistantService: fakeAssistantService{
		getConversationResult:     conversation,
		appendMessageStreamEvents: []assistant.StreamEvent{{Type: assistant.StreamEventMessageDelta, Delta: "done"}},
	}}
	runner := NewLegacyAssistantRunner(service)
	request := cutover.Request{RequestID: "request-1", SessionID: testAssistantSessionUUID, Message: "revise"}

	if _, err := runner.Execute(context.Background(), request, nil); err != nil {
		t.Fatalf("legacy non-stream through pipeline: %v", err)
	}
	var observed []cutover.Event
	if _, err := runner.Execute(context.Background(), request, func(event cutover.Event) error { observed = append(observed, event); return nil }); err != nil {
		t.Fatalf("legacy stream through pipeline: %v", err)
	}
	if service.appendStreamCalls != 2 || service.appendDirectCalls != 0 || service.getCalls != 2 {
		t.Fatalf("legacy transports diverged: stream=%d direct=%d get=%d", service.appendStreamCalls, service.appendDirectCalls, service.getCalls)
	}
	if len(observed) != 2 || observed[1].Type != assistant.StreamEventDone {
		t.Fatalf("legacy runner must expose one recoverable event projection, got %#v", observed)
	}
}

// TestConversationPublicProjectorReturnsExistingDTOOnlyForDeterministicTurnStatus 验证对应场景下的正常路径与失败路径。
func TestConversationPublicProjectorReturnsExistingDTOOnlyForDeterministicTurnStatus(t *testing.T) {
	now := time.Now().UTC()
	reader := &fakeConversationProjectionReader{
		session: &postgres.AssistantSession{ID: testAssistantSessionUUID, CreatedAt: now, UpdatedAt: now, LastMessageAt: now},
	}
	projector := NewConversationPublicProjector(reader)
	turn := agentturn.Turn{ID: "turn-1", SessionID: testAssistantSessionUUID}
	if _, err := projector.Project(context.Background(), turn, agentturn.StatusRunning, nil); err == nil {
		t.Fatal("running turn must not produce an ambiguous public DTO")
	}
	dto, err := projector.Project(context.Background(), turn, agentturn.StatusWaitingApproval, nil)
	if err != nil || !json.Valid(dto) || reader.sessionCalls != 1 || reader.messageCalls != 1 {
		t.Fatalf("project deterministic conversation DTO: dto=%s session_calls=%d message_calls=%d err=%v", dto, reader.sessionCalls, reader.messageCalls, err)
	}
}

// performAssistantTurnRequest 执行该函数负责的核心处理逻辑。
func performAssistantTurnRequest(engine *server.Hertz, path string, body []byte) *protocol.Response {
	return performAssistantTurnRequestWithHeaders(engine, path, body)
}

// performAssistantTurnRequestWithHeaders 执行该函数负责的核心处理逻辑。
func performAssistantTurnRequestWithHeaders(engine *server.Hertz, path string, body []byte, extra ...ut.Header) *protocol.Response {
	headers := []ut.Header{
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Request-ID", Value: "request-1"},
		ut.Header{Key: identity.HeaderWorkspaceID, Value: "11111111-1111-4111-8111-111111111111"},
		ut.Header{Key: "Last-Event-ID", Value: "2"},
	}
	headers = append(headers, extra...)
	return ut.PerformRequest(engine.Engine, "POST", path, &ut.Body{Body: bytes.NewReader(body), Len: len(body)}, headers...).Result()
}

type fakeAssistantTurnPipeline struct {
	calls    int
	requests []cutover.Request
	result   cutover.Result
	err      error
}

// Execute 执行该函数负责的核心处理逻辑。
func (pipeline *fakeAssistantTurnPipeline) Execute(_ context.Context, request cutover.Request, observe cutover.Observer) (cutover.Result, error) {
	pipeline.calls++
	pipeline.requests = append(pipeline.requests, request)
	if pipeline.err != nil {
		return cutover.Result{}, pipeline.err
	}
	for _, event := range pipeline.result.Events {
		if observe != nil && event.Sequence > request.AfterSequence {
			if err := observe(event); err != nil {
				return cutover.Result{}, err
			}
		}
	}
	return pipeline.result, nil
}

type fakeIdentityAdapter struct {
	scope identity.WorkspaceScope
	err   error
}

type countingLegacyAssistantService struct {
	fakeAssistantService
	appendStreamCalls int
	appendDirectCalls int
	getCalls          int
}

type fakeConversationProjectionReader struct {
	session      *postgres.AssistantSession
	messages     []postgres.AssistantMessage
	sessionCalls int
	messageCalls int
}

func (reader *fakeConversationProjectionReader) GetSessionByID(context.Context, string) (*postgres.AssistantSession, error) {
	reader.sessionCalls++
	return reader.session, nil
}

func (reader *fakeConversationProjectionReader) ListMessages(context.Context, string) ([]postgres.AssistantMessage, error) {
	reader.messageCalls++
	return reader.messages, nil
}

// GetConversation 按作用域读取并返回所需数据。
func (service *countingLegacyAssistantService) GetConversation(ctx context.Context, sessionID string) (*assistant.ConversationResult, error) {
	service.getCalls++
	return service.fakeAssistantService.GetConversation(ctx, sessionID)
}

// AppendMessage 执行该函数负责的核心处理逻辑。
func (service *countingLegacyAssistantService) AppendMessage(ctx context.Context, sessionID, content string) (*assistant.ConversationResult, error) {
	service.appendDirectCalls++
	return service.fakeAssistantService.AppendMessage(ctx, sessionID, content)
}

// AppendMessageStream 执行该函数负责的核心处理逻辑。
func (service *countingLegacyAssistantService) AppendMessageStream(ctx context.Context, sessionID, content string, emit func(assistant.StreamEvent) error) error {
	service.appendStreamCalls++
	return service.fakeAssistantService.AppendMessageStream(ctx, sessionID, content, emit)
}

// Authenticate 执行该函数负责的核心处理逻辑。
func (adapter fakeIdentityAdapter) Authenticate(context.Context, identity.Request, string) (identity.WorkspaceScope, error) {
	return adapter.scope, adapter.err
}

// handlerTrustedScope 执行该函数负责的核心处理逻辑。
func handlerTrustedScope() identity.WorkspaceScope {
	return identity.WorkspaceScope{
		Principal:   identity.Principal{Type: "user", ID: "44444444-4444-4444-8444-444444444444", OrganizationID: "55555555-5555-4555-8555-555555555555"},
		WorkspaceID: "11111111-1111-4111-8111-111111111111", TrustSource: "edge-hmac-v1", Trusted: true, IssuedAt: time.Now().UTC(),
	}
}

var _ assistantTurnPipeline = (*fakeAssistantTurnPipeline)(nil)
var _ identity.Adapter = fakeIdentityAdapter{}
