package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/assistant"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func TestListAssistantSessionsHandler(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		listSessionsResult: []postgres.AssistantSession{
			{
				ID:            "session-1",
				Title:         "学生手册修订",
				LastMessageAt: time.Unix(1710000000, 0),
				CreatedAt:     time.Unix(1710000000, 0),
				UpdatedAt:     time.Unix(1710000000, 0),
			},
		},
	})

	engine := server.New()
	engine.GET("/api/assistant/sessions", handler.ListSessions)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/assistant/sessions", nil).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	var payload struct {
		Sessions []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(response.Body(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(payload.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(payload.Sessions))
	}

	if payload.Sessions[0].ID != "session-1" {
		t.Fatalf("expected session id %q, got %q", "session-1", payload.Sessions[0].ID)
	}
}

func TestCreateConversationHandler(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		startConversationResult: &assistant.ConversationResult{
			Session: postgres.AssistantSession{
				ID:            "session-1",
				Title:         "请帮我整理学生守则",
				LastMessageAt: time.Unix(1710000000, 0),
				CreatedAt:     time.Unix(1710000000, 0),
				UpdatedAt:     time.Unix(1710000000, 0),
			},
			Messages: []postgres.AssistantMessage{
				{
					ID:         "message-1",
					SessionID:  "session-1",
					Role:       assistant.RoleUser,
					Kind:       assistant.KindText,
					SequenceNo: 1,
					Payload:    mustMarshalHandlerJSON(t, assistant.TextPayload{Content: "请帮我整理学生守则"}),
					CreatedAt:  time.Unix(1710000000, 0),
				},
			},
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/conversations", handler.CreateConversation)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/conversations",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"请帮我整理学生守则"}`),
			Len:  len(`{"message":"请帮我整理学生守则"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusCreated {
		t.Fatalf("expected status %d, got %d", consts.StatusCreated, response.StatusCode())
	}

	var payload struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
		Messages []struct {
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(response.Body(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if payload.Session.ID != "session-1" {
		t.Fatalf("expected session id %q, got %q", "session-1", payload.Session.ID)
	}

	if len(payload.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(payload.Messages))
	}
}

func TestAppendAssistantMessageHandler(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		appendMessageResult: &assistant.ConversationResult{
			Session: postgres.AssistantSession{
				ID:            "session-1",
				Title:         "学生守则",
				LastMessageAt: time.Unix(1710000000, 0),
				CreatedAt:     time.Unix(1710000000, 0),
				UpdatedAt:     time.Unix(1710000000, 0),
			},
			Messages: []postgres.AssistantMessage{
				{
					ID:         "message-2",
					SessionID:  "session-1",
					Role:       assistant.RoleAssistant,
					Kind:       assistant.KindTaskSuggestion,
					SequenceNo: 2,
					Payload: mustMarshalHandlerJSON(t, assistant.TaskSuggestionPayload{
						ActionLabel:   "确认创建任务",
						CanCreate:     true,
						Instruction:   "请修订第二章",
						ResourceLabel: "学生守则 · upload",
						StatusMessage: "资源已明确，可以创建任务。",
						Title:         "建议创建任务",
					}),
					CreatedAt: time.Unix(1710000000, 0),
				},
			},
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages", handler.AppendMessage)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/session-1/messages",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"请修订第二章"}`),
			Len:  len(`{"message":"请修订第二章"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}
}

func TestCreateConversationStreamHandler(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		startConversationStreamEvents: []assistant.StreamEvent{
			{
				Type: assistant.StreamEventSessionCreated,
				Session: &postgres.AssistantSession{
					ID:            "session-stream",
					Title:         "流式会话",
					LastMessageAt: time.Unix(1710000000, 0),
					CreatedAt:     time.Unix(1710000000, 0),
					UpdatedAt:     time.Unix(1710000000, 0),
				},
			},
			{Type: assistant.StreamEventMessageStarted},
			{Type: assistant.StreamEventMessageDelta, Delta: "当然可以"},
			{
				Type: assistant.StreamEventMessageCompleted,
				Message: &postgres.AssistantMessage{
					ID:         "message-stream",
					SessionID:  "session-stream",
					Role:       assistant.RoleAssistant,
					Kind:       assistant.KindText,
					SequenceNo: 2,
					Payload: mustMarshalHandlerJSON(t, assistant.TextPayload{
						Content: "当然可以，我们先梳理目标。",
					}),
					CreatedAt: time.Unix(1710000001, 0),
				},
			},
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/conversations/stream", handler.CreateConversationStream)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/conversations/stream",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"帮我梳理本周安排"}`),
			Len:  len(`{"message":"帮我梳理本周安排"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	if value := string(response.Header.ContentType()); !strings.HasPrefix(value, "text/event-stream") {
		t.Fatalf("expected text/event-stream content type, got %q", value)
	}

	events := parseSSEEvents(t, string(response.Body()))
	if len(events) != 5 {
		t.Fatalf("expected 5 sse events, got %d", len(events))
	}

	expectedTypes := []string{
		assistant.StreamEventSessionCreated,
		assistant.StreamEventMessageStarted,
		assistant.StreamEventMessageDelta,
		assistant.StreamEventMessageCompleted,
		assistant.StreamEventDone,
	}
	for index, expected := range expectedTypes {
		if events[index].Type != expected {
			t.Fatalf("expected event %d type %q, got %q", index, expected, events[index].Type)
		}
	}

	var sessionPayload struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(events[0].Data, &sessionPayload); err != nil {
		t.Fatalf("unmarshal session payload: %v", err)
	}
	if sessionPayload.Session.ID != "session-stream" {
		t.Fatalf("expected session id %q, got %q", "session-stream", sessionPayload.Session.ID)
	}
}

func TestAppendMessageStreamHandler(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		getConversationResult: &assistant.ConversationResult{
			Session: postgres.AssistantSession{
				ID:            "session-1",
				Title:         "学生手册",
				LastMessageAt: time.Unix(1710000000, 0),
				CreatedAt:     time.Unix(1710000000, 0),
				UpdatedAt:     time.Unix(1710000000, 0),
			},
		},
		appendMessageStreamEvents: []assistant.StreamEvent{
			{Type: assistant.StreamEventMessageStarted},
			{Type: assistant.StreamEventMessageDelta, Delta: "我先看第二章"},
			{
				Type: assistant.StreamEventMessageCompleted,
				Message: &postgres.AssistantMessage{
					ID:         "message-stream-append",
					SessionID:  "session-1",
					Role:       assistant.RoleAssistant,
					Kind:       assistant.KindText,
					SequenceNo: 3,
					Payload: mustMarshalHandlerJSON(t, assistant.TextPayload{
						Content: "我先看第二章，再给你建议。",
					}),
					CreatedAt: time.Unix(1710000001, 0),
				},
			},
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages/stream", handler.AppendMessageStream)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/session-1/messages/stream",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"继续看看第二章"}`),
			Len:  len(`{"message":"继续看看第二章"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	if value := string(response.Header.ContentType()); !strings.HasPrefix(value, "text/event-stream") {
		t.Fatalf("expected text/event-stream content type, got %q", value)
	}

	events := parseSSEEvents(t, string(response.Body()))
	if len(events) != 4 {
		t.Fatalf("expected 4 sse events, got %d", len(events))
	}

	expectedTypes := []string{
		assistant.StreamEventMessageStarted,
		assistant.StreamEventMessageDelta,
		assistant.StreamEventMessageCompleted,
		assistant.StreamEventDone,
	}
	for index, expected := range expectedTypes {
		if events[index].Type != expected {
			t.Fatalf("expected event %d type %q, got %q", index, expected, events[index].Type)
		}
	}
}

func TestAppendMessageStreamHandlerReturnsJSON404BeforeOpeningStream(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		getConversationErr: assistant.ErrSessionNotFound,
	})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages/stream", handler.AppendMessageStream)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/missing/messages/stream",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"继续看看第二章"}`),
			Len:  len(`{"message":"继续看看第二章"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}

	if value := string(response.Header.ContentType()); strings.HasPrefix(value, "text/event-stream") {
		t.Fatalf("expected json error response before stream starts, got %q", value)
	}
}

func TestCreateConversationStreamHandlerWritesErrorEventAfterStreamStarts(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		startConversationStreamErr: errors.New("stream failed"),
	})

	engine := server.New()
	engine.POST("/api/assistant/conversations/stream", handler.CreateConversationStream)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/conversations/stream",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"帮我梳理本周安排"}`),
			Len:  len(`{"message":"帮我梳理本周安排"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	events := parseSSEEvents(t, string(response.Body()))
	if len(events) != 1 {
		t.Fatalf("expected 1 sse event, got %d", len(events))
	}
	if events[0].Type != assistant.StreamEventError {
		t.Fatalf("expected error event, got %q", events[0].Type)
	}

	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(events[0].Data, &payload); err != nil {
		t.Fatalf("unmarshal error payload: %v", err)
	}
	if payload.Code != assistant.StreamErrorCodeInternal {
		t.Fatalf("expected error code %q, got %q", assistant.StreamErrorCodeInternal, payload.Code)
	}
}

func TestUploadAssistantFileHandler(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		uploadFileResult: &assistant.UploadFileResult{
			Session: postgres.AssistantSession{
				ID:            "session-1",
				Title:         "学生守则",
				LastMessageAt: time.Unix(1710000000, 0),
				CreatedAt:     time.Unix(1710000000, 0),
				UpdatedAt:     time.Unix(1710000000, 0),
			},
			Resource: &postgres.Resource{
				ID:         "resource-1",
				Title:      "学生守则",
				SourceType: "upload",
			},
			Messages: []postgres.AssistantMessage{
				{
					ID:         "message-file",
					SessionID:  "session-1",
					Role:       assistant.RoleAssistant,
					Kind:       assistant.KindSessionFile,
					SequenceNo: 1,
					Payload: mustMarshalHandlerJSON(t, assistant.SessionFilePayload{
						FileName:      "学生守则.md",
						FileID:        "file-1",
						ResourceID:    "resource-1",
						ResourceTitle: "学生守则",
						SourceType:    "upload",
						Status:        "ready",
					}),
					CreatedAt: time.Unix(1710000000, 0),
				},
			},
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/files", handler.UploadFile)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fileHeader := textproto.MIMEHeader{}
	fileHeader.Set("Content-Disposition", `form-data; name="file"; filename="学生守则.md"`)
	fileHeader.Set("Content-Type", "text/markdown")
	part, err := writer.CreatePart(fileHeader)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write([]byte("# 学生守则\n内容")); err != nil {
		t.Fatalf("write multipart body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/session-1/files",
		&ut.Body{
			Body: body,
			Len:  body.Len(),
		},
		ut.Header{Key: "Content-Type", Value: writer.FormDataContentType()},
	).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}
}

func TestUploadAssistantFileHandlerRejectsTooLargeFile(t *testing.T) {
	handler := NewAssistantHandlerWithUploadLimit(fakeAssistantService{}, 4)
	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/files", handler.UploadFile)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "too-large.md")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write([]byte("12345")); err != nil {
		t.Fatalf("write multipart body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/session-1/files",
		&ut.Body{
			Body: body,
			Len:  body.Len(),
		},
		ut.Header{Key: "Content-Type", Value: writer.FormDataContentType()},
	).Result()

	if response.StatusCode() != consts.StatusRequestEntityTooLarge {
		t.Fatalf("expected status %d, got %d", consts.StatusRequestEntityTooLarge, response.StatusCode())
	}
}

func TestConfirmTaskSuggestionHandlerReturnsHandledFailurePayload(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		confirmTaskResult: &assistant.ConfirmTaskResult{
			Session: postgres.AssistantSession{
				ID:            "session-1",
				Title:         "学生守则",
				LastMessageAt: time.Unix(1710000000, 0),
				CreatedAt:     time.Unix(1710000000, 0),
				UpdatedAt:     time.Unix(1710000000, 0),
			},
			Messages: []postgres.AssistantMessage{
				{
					ID:         "message-system",
					SessionID:  "session-1",
					Role:       assistant.RoleAssistant,
					Kind:       assistant.KindSystem,
					SequenceNo: 2,
					Payload: mustMarshalHandlerJSON(t, assistant.SystemPayload{
						Content: "任务创建失败：task create failed",
						Level:   "error",
					}),
					CreatedAt: time.Unix(1710000000, 0),
				},
			},
			ErrorMessage: stringPointer("任务创建失败：task create failed"),
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/task-suggestions/:id/confirm", handler.ConfirmTaskSuggestion)

	response := ut.PerformRequest(engine.Engine, "POST", "/api/assistant/task-suggestions/message-1/confirm", nil).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	var payload struct {
		ErrorMessage *string `json:"error_message"`
	}
	if err := json.Unmarshal(response.Body(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if payload.ErrorMessage == nil {
		t.Fatal("expected handled failure payload to contain error_message")
	}
}

func TestDeleteAssistantSessionHandler(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		deleteSessionResult: true,
	})

	engine := server.New()
	engine.DELETE("/api/assistant/sessions/:id", handler.DeleteSession)

	response := ut.PerformRequest(engine.Engine, "DELETE", "/api/assistant/sessions/session-1", nil).Result()
	if response.StatusCode() != consts.StatusNoContent {
		t.Fatalf("expected status %d, got %d", consts.StatusNoContent, response.StatusCode())
	}
}

type fakeAssistantService struct {
	listSessionsResult            []postgres.AssistantSession
	getConversationResult         *assistant.ConversationResult
	startConversationResult       *assistant.ConversationResult
	startConversationStreamEvents []assistant.StreamEvent
	appendMessageResult           *assistant.ConversationResult
	appendMessageStreamEvents     []assistant.StreamEvent
	uploadFileResult              *assistant.UploadFileResult
	confirmTaskResult             *assistant.ConfirmTaskResult
	deleteSessionResult           bool

	listSessionsErr            error
	getConversationErr         error
	startConversationErr       error
	startConversationStreamErr error
	appendMessageErr           error
	appendMessageStreamErr     error
	uploadFileErr              error
	confirmTaskErr             error
	deleteSessionErr           error
}

func (f fakeAssistantService) ListSessions(context.Context) ([]postgres.AssistantSession, error) {
	return f.listSessionsResult, f.listSessionsErr
}

func (f fakeAssistantService) GetConversation(context.Context, string) (*assistant.ConversationResult, error) {
	return f.getConversationResult, f.getConversationErr
}

func (f fakeAssistantService) StartConversation(context.Context, string) (*assistant.ConversationResult, error) {
	return f.startConversationResult, f.startConversationErr
}

func (f fakeAssistantService) StartConversationStream(_ context.Context, _ string, emit func(assistant.StreamEvent) error) error {
	for _, event := range f.startConversationStreamEvents {
		if err := emit(event); err != nil {
			return err
		}
	}

	return f.startConversationStreamErr
}

func (f fakeAssistantService) AppendMessage(context.Context, string, string) (*assistant.ConversationResult, error) {
	return f.appendMessageResult, f.appendMessageErr
}

func (f fakeAssistantService) AppendMessageStream(_ context.Context, _ string, _ string, emit func(assistant.StreamEvent) error) error {
	for _, event := range f.appendMessageStreamEvents {
		if err := emit(event); err != nil {
			return err
		}
	}

	return f.appendMessageStreamErr
}

func (f fakeAssistantService) UploadFile(context.Context, string, string, []byte) (*assistant.UploadFileResult, error) {
	return f.uploadFileResult, f.uploadFileErr
}

func (f fakeAssistantService) ConfirmTaskSuggestion(context.Context, string) (*assistant.ConfirmTaskResult, error) {
	return f.confirmTaskResult, f.confirmTaskErr
}

func (f fakeAssistantService) DeleteSession(context.Context, string) (bool, error) {
	return f.deleteSessionResult, f.deleteSessionErr
}

func mustMarshalHandlerJSON(t *testing.T, value any) []byte {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	return payload
}

func stringPointer(value string) *string {
	return &value
}

type parsedSSEEvent struct {
	Type string
	Data json.RawMessage
}

func parseSSEEvents(t *testing.T, body string) []parsedSSEEvent {
	t.Helper()

	blocks := strings.Split(strings.TrimSpace(body), "\n\n")
	events := make([]parsedSSEEvent, 0, len(blocks))
	for _, block := range blocks {
		trimmed := strings.TrimSpace(block)
		if trimmed == "" {
			continue
		}

		var event parsedSSEEvent
		for _, line := range strings.Split(trimmed, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				event.Type = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
			case strings.HasPrefix(line, "data: "):
				event.Data = append(event.Data, strings.TrimSpace(strings.TrimPrefix(line, "data: "))...)
			}
		}

		events = append(events, event)
	}

	return events
}

func TestGetAssistantConversationNotFound(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		getConversationErr: assistant.ErrSessionNotFound,
	})

	engine := server.New()
	engine.GET("/api/assistant/sessions/:id", handler.GetConversation)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/assistant/sessions/missing", nil).Result()
	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}

func TestCreateConversationBadRequest(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		startConversationErr: assistant.ErrMessageRequired,
	})

	engine := server.New()
	engine.POST("/api/assistant/conversations", handler.CreateConversation)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/conversations",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":""}`),
			Len:  len(`{"message":""}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
}

func TestConfirmTaskSuggestionNotFound(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		confirmTaskErr: assistant.ErrTaskSuggestionNotFound,
	})

	engine := server.New()
	engine.POST("/api/assistant/task-suggestions/:id/confirm", handler.ConfirmTaskSuggestion)

	response := ut.PerformRequest(engine.Engine, "POST", "/api/assistant/task-suggestions/missing/confirm", nil).Result()
	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}

func TestDeleteAssistantSessionInternalError(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		deleteSessionErr: errors.New("delete failed"),
	})

	engine := server.New()
	engine.DELETE("/api/assistant/sessions/:id", handler.DeleteSession)

	response := ut.PerformRequest(engine.Engine, "DELETE", "/api/assistant/sessions/session-1", nil).Result()
	if response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", consts.StatusInternalServerError, response.StatusCode())
	}
}
