package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"agent_project/apps/server/internal/assistant"
	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

const (
	testAssistantSessionUUID         = "00000000-0000-0000-0000-000000000001"
	testAssistantMissingSessionUUID  = "00000000-0000-0000-0000-000000000999"
	testAssistantSuggestionUUID      = "00000000-0000-0000-0000-000000000002"
	testAssistantMissingSuggestionID = "00000000-0000-0000-0000-000000000998"
)

// TestListAssistantSessionsHandler 验证`listAssistantSessionsHandler`在特定边界条件下的行为，防止同类回归。
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

// TestGetAssistantCapabilitiesHandlerReturnsUploadCapabilities 验证`getAssistantCapabilitiesHandler`在返回值分支下的行为，防止同类回归。
func TestGetAssistantCapabilitiesHandlerReturnsUploadCapabilities(t *testing.T) {
	handler := NewAssistantHandlerWithUploadLimitAndPolicy(
		fakeAssistantService{},
		defaultAssistantUploadMaxBytes,
		textOnlyAssistantUploadPolicy{},
	)

	engine := server.New()
	engine.GET("/api/assistant/capabilities", handler.GetCapabilities)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/assistant/capabilities", nil).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	var payload struct {
		Upload struct {
			Accept              string   `json:"accept"`
			Hint                string   `json:"hint"`
			SupportedExtensions []string `json:"supported_extensions"`
		} `json:"upload"`
	}
	if err := json.Unmarshal(response.Body(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if payload.Upload.Accept != ".md,.txt" {
		t.Fatalf("expected accept %q, got %q", ".md,.txt", payload.Upload.Accept)
	}
	if payload.Upload.Hint != "支持 md、txt" {
		t.Fatalf("expected hint %q, got %q", "支持 md、txt", payload.Upload.Hint)
	}
	if len(payload.Upload.SupportedExtensions) != 2 || payload.Upload.SupportedExtensions[0] != ".md" || payload.Upload.SupportedExtensions[1] != ".txt" {
		t.Fatalf("expected supported_extensions [.md .txt], got %v", payload.Upload.SupportedExtensions)
	}
}

// TestCreateConversationHandler 验证`createConversationHandler`在特定边界条件下的行为，防止同类回归。
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

// TestAppendAssistantMessageHandler 验证`appendAssistantMessageHandler`在特定边界条件下的行为，防止同类回归。
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
		"/api/assistant/sessions/"+testAssistantSessionUUID+"/messages",
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

// TestCreateConversationStreamHandler 验证`createConversationStreamHandler`在特定边界条件下的行为，防止同类回归。
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

// TestWriteAssistantStreamEventSupportsSessionFile 验证`writeAssistantStreamEvent`在合法输入或兼容路径下的行为，防止同类回归。
func TestWriteAssistantStreamEventSupportsSessionFile(t *testing.T) {
	reader, writer := io.Pipe()
	done := make(chan string, 1)

	go func() {
		body, _ := io.ReadAll(reader)
		done <- string(body)
	}()

	err := writeAssistantStreamEvent(writer, assistant.StreamEvent{
		Type: assistant.StreamEventSessionFile,
		Message: &postgres.AssistantMessage{
			ID:         "message-file",
			SessionID:  "session-1",
			Role:       assistant.RoleAssistant,
			Kind:       assistant.KindSessionFile,
			SequenceNo: 2,
			Payload: mustMarshalHandlerJSON(t, assistant.SessionFilePayload{
				FileName:      "对话粘贴正文.md",
				ResourceID:    "resource-inline",
				ResourceTitle: "对话粘贴正文",
				SourceType:    "inline_text",
				Status:        "ready",
			}),
			CreatedAt: time.Unix(1710000001, 0),
		},
	})
	if err != nil {
		t.Fatalf("write assistant stream event: %v", err)
	}
	_ = writer.Close()

	body := <-done
	if !strings.Contains(body, "event: session_file") {
		t.Fatalf("expected session_file event name, got %q", body)
	}
	if !strings.Contains(body, "\"kind\":\"session_file\"") {
		t.Fatalf("expected session_file message payload, got %q", body)
	}
}

// TestAppendMessageStreamHandler 验证`appendMessageStreamHandler`在特定边界条件下的行为，防止同类回归。
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
		"/api/assistant/sessions/"+testAssistantSessionUUID+"/messages/stream",
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

// TestAssistantHandlerStreamDoesNotEmitTaskSuggestionForReadbackDecision 验证阅读型流式响应不会额外发 task_suggestion 事件。
func TestAssistantHandlerStreamDoesNotEmitTaskSuggestionForReadbackDecision(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		getConversationResult: &assistant.ConversationResult{
			Session: postgres.AssistantSession{ID: "session-1"},
		},
		appendMessageStreamEvents: []assistant.StreamEvent{
			{Type: assistant.StreamEventMessageStarted},
			{Type: assistant.StreamEventMessageDelta, Delta: "我先把第三个项目输出给你"},
			{
				Type: assistant.StreamEventMessageCompleted,
				Message: &postgres.AssistantMessage{
					ID:        "message-readback",
					SessionID: "session-1",
					Role:      assistant.RoleAssistant,
					Kind:      assistant.KindText,
					Payload:   mustMarshalHandlerJSON(t, assistant.TextPayload{Content: "我先把第三个项目输出给你。"}),
				},
			},
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages/stream", handler.AppendMessageStream)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/"+testAssistantSessionUUID+"/messages/stream",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"把第三个项目先输出一遍"}`),
			Len:  len(`{"message":"把第三个项目先输出一遍"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	events := parseSSEEvents(t, string(response.Body()))
	for _, event := range events {
		if event.Type == assistant.StreamEventTaskSuggestion {
			t.Fatalf("expected no task_suggestion event for readback stream, got %#v", events)
		}
	}
}

// TestAssistantHandlerStreamStillEmitsTaskSuggestionForWorkflowDecision 验证任务型流式响应仍会透传 task_suggestion 事件。
func TestAssistantHandlerStreamStillEmitsTaskSuggestionForWorkflowDecision(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		getConversationResult: &assistant.ConversationResult{
			Session: postgres.AssistantSession{ID: "session-1"},
		},
		appendMessageStreamEvents: []assistant.StreamEvent{
			{Type: assistant.StreamEventMessageStarted},
			{Type: assistant.StreamEventMessageDelta, Delta: "这件事适合进入任务流"},
			{
				Type: assistant.StreamEventMessageCompleted,
				Message: &postgres.AssistantMessage{
					ID:        "message-workflow",
					SessionID: "session-1",
					Role:      assistant.RoleAssistant,
					Kind:      assistant.KindText,
					Payload:   mustMarshalHandlerJSON(t, assistant.TextPayload{Content: "这件事适合进入任务流。"}),
				},
			},
			{
				Type: assistant.StreamEventTaskSuggestion,
				Message: &postgres.AssistantMessage{
					ID:        "message-suggestion",
					SessionID: "session-1",
					Role:      assistant.RoleAssistant,
					Kind:      assistant.KindTaskSuggestion,
					Payload: mustMarshalHandlerJSON(t, assistant.TaskSuggestionPayload{
						ActionLabel:   "确认创建任务",
						CanCreate:     true,
						Instruction:   "请把第三个项目改成产品经理版本",
						ResourceLabel: "简历 · upload",
						StatusMessage: "资源已明确，可以创建任务。",
						Title:         "建议创建任务",
					}),
				},
			},
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages/stream", handler.AppendMessageStream)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/"+testAssistantSessionUUID+"/messages/stream",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"直接开始改第三个项目，创建任务"}`),
			Len:  len(`{"message":"直接开始改第三个项目，创建任务"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	events := parseSSEEvents(t, string(response.Body()))
	foundTaskSuggestion := false
	for _, event := range events {
		if event.Type == assistant.StreamEventTaskSuggestion {
			foundTaskSuggestion = true
			break
		}
	}
	if !foundTaskSuggestion {
		t.Fatalf("expected task_suggestion event for workflow stream, got %#v", events)
	}
}

// TestAssistantHandlerStreamKeepsProtocolStableWithVerifier 验证接入 verifier 后 SSE 协议顺序保持不变。
func TestAssistantHandlerStreamKeepsProtocolStableWithVerifier(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		getConversationResult: &assistant.ConversationResult{
			Session: postgres.AssistantSession{ID: "session-1"},
		},
		appendMessageStreamEvents: []assistant.StreamEvent{
			{Type: assistant.StreamEventMessageStarted},
			{Type: assistant.StreamEventMessageDelta, Delta: "这件事适合进入任务流"},
			{
				Type: assistant.StreamEventMessageCompleted,
				Message: &postgres.AssistantMessage{
					ID:        "message-workflow",
					SessionID: "session-1",
					Role:      assistant.RoleAssistant,
					Kind:      assistant.KindText,
					Payload:   mustMarshalHandlerJSON(t, assistant.TextPayload{Content: "这件事适合进入任务流。"}),
				},
			},
			{
				Type: assistant.StreamEventTaskSuggestion,
				Message: &postgres.AssistantMessage{
					ID:        "message-suggestion",
					SessionID: "session-1",
					Role:      assistant.RoleAssistant,
					Kind:      assistant.KindTaskSuggestion,
					Payload: mustMarshalHandlerJSON(t, assistant.TaskSuggestionPayload{
						ActionLabel:   "确认创建任务",
						CanCreate:     true,
						Instruction:   "请把第三个项目改成产品经理版本",
						ResourceLabel: "简历 · upload",
						StatusMessage: "资源已明确，可以创建任务。",
						Title:         "建议创建任务",
					}),
				},
			},
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages/stream", handler.AppendMessageStream)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/"+testAssistantSessionUUID+"/messages/stream",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"直接开始改第三个项目，创建任务"}`),
			Len:  len(`{"message":"直接开始改第三个项目，创建任务"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	events := parseSSEEvents(t, string(response.Body()))
	expectedTypes := []string{
		assistant.StreamEventMessageStarted,
		assistant.StreamEventMessageDelta,
		assistant.StreamEventMessageCompleted,
		assistant.StreamEventTaskSuggestion,
		assistant.StreamEventDone,
	}
	if len(events) != len(expectedTypes) {
		t.Fatalf("expected %d sse events, got %d", len(expectedTypes), len(events))
	}
	for index, expected := range expectedTypes {
		if events[index].Type != expected {
			t.Fatalf("expected event %d type %q, got %q", index, expected, events[index].Type)
		}
	}
}

// TestAssistantHandlerStreamProtocolRemainsStableWithLearningInstrumentation 验证接入 runtime learning 后 SSE 协议顺序保持不变。
func TestAssistantHandlerStreamProtocolRemainsStableWithLearningInstrumentation(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		getConversationResult: &assistant.ConversationResult{
			Session: postgres.AssistantSession{ID: "session-1"},
		},
		appendMessageStreamEvents: []assistant.StreamEvent{
			{Type: assistant.StreamEventMessageStarted},
			{Type: assistant.StreamEventMessageDelta, Delta: "这件事适合进入任务流"},
			{
				Type: assistant.StreamEventMessageCompleted,
				Message: &postgres.AssistantMessage{
					ID:        "message-workflow",
					SessionID: "session-1",
					Role:      assistant.RoleAssistant,
					Kind:      assistant.KindText,
					Payload:   mustMarshalHandlerJSON(t, assistant.TextPayload{Content: "这件事适合进入任务流。"}),
				},
			},
			{
				Type: assistant.StreamEventTaskSuggestion,
				Message: &postgres.AssistantMessage{
					ID:        "message-suggestion",
					SessionID: "session-1",
					Role:      assistant.RoleAssistant,
					Kind:      assistant.KindTaskSuggestion,
					Payload: mustMarshalHandlerJSON(t, assistant.TaskSuggestionPayload{
						ActionLabel:   "确认创建任务",
						CanCreate:     true,
						Instruction:   "请把第三个项目改成产品经理版本",
						ResourceLabel: "简历 · upload",
						StatusMessage: "资源已明确，可以创建任务。",
						Title:         "建议创建任务",
					}),
				},
			},
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages/stream", handler.AppendMessageStream)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/"+testAssistantSessionUUID+"/messages/stream",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"直接开始改第三个项目，创建任务"}`),
			Len:  len(`{"message":"直接开始改第三个项目，创建任务"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()

	events := parseSSEEvents(t, string(response.Body()))
	expectedTypes := []string{
		assistant.StreamEventMessageStarted,
		assistant.StreamEventMessageDelta,
		assistant.StreamEventMessageCompleted,
		assistant.StreamEventTaskSuggestion,
		assistant.StreamEventDone,
	}
	if len(events) != len(expectedTypes) {
		t.Fatalf("expected %d sse events, got %d", len(expectedTypes), len(events))
	}
	for index, expected := range expectedTypes {
		if events[index].Type != expected {
			t.Fatalf("expected event %d type %q, got %q", index, expected, events[index].Type)
		}
	}
}

// TestAppendMessageStreamHandlerReturnsJSON404BeforeOpeningStream 验证`appendMessageStreamHandler`在返回值分支下的行为，防止同类回归。
func TestAppendMessageStreamHandlerReturnsJSON404BeforeOpeningStream(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		getConversationErr: assistant.ErrSessionNotFound,
	})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages/stream", handler.AppendMessageStream)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/"+testAssistantMissingSessionUUID+"/messages/stream",
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

// TestCreateConversationStreamHandlerWritesErrorEventAfterStreamStarts 验证`createConversationStreamHandler`在写入或副作用路径下的行为，防止同类回归。
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

// TestUploadAssistantFileHandler 验证`uploadAssistantFileHandler`在特定边界条件下的行为，防止同类回归。
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
		"/api/assistant/sessions/"+testAssistantSessionUUID+"/files",
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

// TestUploadAssistantConversationFileHandler 验证空白草稿会话也可以直接上传文件。
func TestUploadAssistantConversationFileHandler(t *testing.T) {
	var uploadedFileName string
	handler := NewAssistantHandler(fakeAssistantService{
		startConversationWithFileResult: &assistant.UploadFileResult{
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
		startConversationWithFileHook: func(_ context.Context, fileName string, _ []byte) {
			uploadedFileName = fileName
		},
	})

	engine := server.New()
	engine.POST("/api/assistant/conversations/files", handler.UploadConversationFile)

	response := performUploadRequestToPath(
		t,
		engine,
		"/api/assistant/conversations/files",
		"学生守则.md",
		"text/markdown",
		[]byte("# 学生守则\n内容"),
	)

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", consts.StatusOK, response.StatusCode(), string(response.Body()))
	}
	if uploadedFileName != "学生守则.md" {
		t.Fatalf("expected uploaded file name %q, got %q", "学生守则.md", uploadedFileName)
	}
}

// TestUploadAssistantFileHandlerRejectsTikaDocumentInTextMode 验证`uploadAssistantFileHandler`在非法输入或失败路径下的行为，防止同类回归。
func TestUploadAssistantFileHandlerRejectsTikaDocumentInTextMode(t *testing.T) {
	var uploadCalled bool
	policy := mustDocumentParser(t, documentparser.Options{Mode: documentparser.ModeText})
	handler := NewAssistantHandlerWithUploadLimitAndPolicy(fakeAssistantService{
		uploadFileResult: &assistant.UploadFileResult{},
		uploadFileHook: func(context.Context, string, string, []byte) {
			uploadCalled = true
		},
	}, defaultAssistantUploadMaxBytes, policy)

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/files", handler.UploadFile)
	response := performUploadRequest(t, engine, "合同.pdf", "application/pdf", []byte("pdf-binary"))

	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
	if uploadCalled {
		t.Fatal("expected pdf to be rejected before service upload in text mode")
	}
	if !strings.Contains(string(response.Body()), "Tika") {
		t.Fatalf("expected response to mention Tika, got %s", string(response.Body()))
	}
}

// TestUploadAssistantFileHandlerAllowsDocumentInTikaMode 验证`uploadAssistantFileHandler`在合法输入或兼容路径下的行为，防止同类回归。
func TestUploadAssistantFileHandlerAllowsDocumentInTikaMode(t *testing.T) {
	var uploadedFileName string
	policy := mustDocumentParser(t, documentparser.Options{
		Mode:    documentparser.ModeTika,
		TikaURL: "http://127.0.0.1:9998",
	})
	handler := NewAssistantHandlerWithUploadLimitAndPolicy(fakeAssistantService{
		uploadFileResult: &assistant.UploadFileResult{Session: postgres.AssistantSession{ID: "session-1"}},
		uploadFileHook: func(_ context.Context, _ string, fileName string, _ []byte) {
			uploadedFileName = fileName
		},
	}, defaultAssistantUploadMaxBytes, policy)

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/files", handler.UploadFile)
	response := performUploadRequest(t, engine, "合同.pdf", "application/pdf", []byte("pdf-binary"))

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", consts.StatusOK, response.StatusCode(), string(response.Body()))
	}
	if uploadedFileName != "合同.pdf" {
		t.Fatalf("expected service upload to receive file name, got %q", uploadedFileName)
	}
}

// TestUploadAssistantFileHandlerRejectsUnsupportedExtension 验证`uploadAssistantFileHandler`在非法输入或失败路径下的行为，防止同类回归。
func TestUploadAssistantFileHandlerRejectsUnsupportedExtension(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options documentparser.Options
	}{
		{name: "text", options: documentparser.Options{Mode: documentparser.ModeText}},
		{name: "tika", options: documentparser.Options{Mode: documentparser.ModeTika, TikaURL: "http://127.0.0.1:9998"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var uploadCalled bool

			handler := NewAssistantHandlerWithUploadLimitAndPolicy(fakeAssistantService{
				uploadFileResult: &assistant.UploadFileResult{},
				uploadFileHook: func(context.Context, string, string, []byte) {
					uploadCalled = true
				},
			}, defaultAssistantUploadMaxBytes, mustDocumentParser(t, tc.options))

			engine := server.New()
			engine.POST("/api/assistant/sessions/:id/files", handler.UploadFile)
			response := performUploadRequest(t, engine, "资料包.zip", "application/zip", []byte("zip-binary"))

			if response.StatusCode() != consts.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
			}

			if uploadCalled {
				t.Fatal("expected unsupported file to be rejected before service upload")
			}
		})
	}
}

// TestUploadAssistantFileHandlerRejectsTooLargeFile 验证`uploadAssistantFileHandler`在非法输入或失败路径下的行为，防止同类回归。
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
		"/api/assistant/sessions/"+testAssistantSessionUUID+"/files",
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

// TestConfirmTaskSuggestionHandlerReturnsHandledFailurePayload 验证`confirmTaskSuggestionHandler`在返回值分支下的行为，防止同类回归。
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

	response := ut.PerformRequest(engine.Engine, "POST", "/api/assistant/task-suggestions/"+testAssistantSuggestionUUID+"/confirm", nil).Result()
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

// TestDeleteAssistantSessionHandler 验证`deleteAssistantSessionHandler`在特定边界条件下的行为，防止同类回归。
func TestDeleteAssistantSessionHandler(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		deleteSessionResult: true,
	})

	engine := server.New()
	engine.DELETE("/api/assistant/sessions/:id", handler.DeleteSession)

	response := ut.PerformRequest(engine.Engine, "DELETE", "/api/assistant/sessions/"+testAssistantSessionUUID, nil).Result()
	if response.StatusCode() != consts.StatusNoContent {
		t.Fatalf("expected status %d, got %d", consts.StatusNoContent, response.StatusCode())
	}
}

// fakeAssistantService 作为助手服务的测试替身，用于在用例里提供可控的依赖行为。
type fakeAssistantService struct {
	listSessionsResult              []postgres.AssistantSession
	getConversationResult           *assistant.ConversationResult
	startConversationResult         *assistant.ConversationResult
	startConversationWithFileResult *assistant.UploadFileResult
	startConversationStreamEvents   []assistant.StreamEvent
	appendMessageResult             *assistant.ConversationResult
	appendMessageStreamEvents       []assistant.StreamEvent
	uploadFileResult                *assistant.UploadFileResult
	confirmTaskResult               *assistant.ConfirmTaskResult
	deleteSessionResult             bool

	listSessionsErr              error
	getConversationErr           error
	startConversationErr         error
	startConversationWithFileErr error
	startConversationStreamErr   error
	appendMessageErr             error
	appendMessageStreamErr       error
	uploadFileErr                error
	confirmTaskErr               error
	deleteSessionErr             error

	startConversationWithFileHook func(context.Context, string, []byte)
	uploadFileHook                func(context.Context, string, string, []byte)
}

// ListSessions 实现测试替身需要的 `ListSessions` 接口方法，为用例分支提供可控返回。
func (f fakeAssistantService) ListSessions(context.Context) ([]postgres.AssistantSession, error) {
	return f.listSessionsResult, f.listSessionsErr
}

// GetConversation 实现测试替身需要的 `GetConversation` 接口方法，为用例分支提供可控返回。
func (f fakeAssistantService) GetConversation(context.Context, string) (*assistant.ConversationResult, error) {
	return f.getConversationResult, f.getConversationErr
}

// StartConversation 实现测试替身需要的 `StartConversation` 接口方法，为用例分支提供可控返回。
func (f fakeAssistantService) StartConversation(context.Context, string) (*assistant.ConversationResult, error) {
	return f.startConversationResult, f.startConversationErr
}

// StartConversationWithFile 实现测试替身需要的空会话上传接口，为用例分支提供可控返回。
func (f fakeAssistantService) StartConversationWithFile(ctx context.Context, fileName string, content []byte) (*assistant.UploadFileResult, error) {
	if f.startConversationWithFileHook != nil {
		f.startConversationWithFileHook(ctx, fileName, content)
	}
	return f.startConversationWithFileResult, f.startConversationWithFileErr
}

// StartConversationStream 实现测试替身需要的 `StartConversationStream` 接口方法，为用例分支提供可控返回。
func (f fakeAssistantService) StartConversationStream(_ context.Context, _ string, emit func(assistant.StreamEvent) error) error {
	for _, event := range f.startConversationStreamEvents {
		if err := emit(event); err != nil {
			return err
		}
	}

	return f.startConversationStreamErr
}

// AppendMessage 实现测试替身需要的 `AppendMessage` 接口方法，为用例分支提供可控返回。
func (f fakeAssistantService) AppendMessage(context.Context, string, string) (*assistant.ConversationResult, error) {
	return f.appendMessageResult, f.appendMessageErr
}

// AppendMessageStream 实现测试替身需要的 `AppendMessageStream` 接口方法，为用例分支提供可控返回。
func (f fakeAssistantService) AppendMessageStream(_ context.Context, _ string, _ string, emit func(assistant.StreamEvent) error) error {
	for _, event := range f.appendMessageStreamEvents {
		if err := emit(event); err != nil {
			return err
		}
	}

	return f.appendMessageStreamErr
}

// UploadFile 实现测试替身需要的 `UploadFile` 接口方法，为用例分支提供可控返回。
func (f fakeAssistantService) UploadFile(ctx context.Context, sessionID string, fileName string, content []byte) (*assistant.UploadFileResult, error) {
	if f.uploadFileHook != nil {
		f.uploadFileHook(ctx, sessionID, fileName, content)
	}
	return f.uploadFileResult, f.uploadFileErr
}

// ConfirmTaskSuggestion 实现测试替身需要的 `ConfirmTaskSuggestion` 接口方法，为用例分支提供可控返回。
func (f fakeAssistantService) ConfirmTaskSuggestion(context.Context, string) (*assistant.ConfirmTaskResult, error) {
	return f.confirmTaskResult, f.confirmTaskErr
}

// DeleteSession 实现测试替身需要的 `DeleteSession` 接口方法，为用例分支提供可控返回。
func (f fakeAssistantService) DeleteSession(context.Context, string) (bool, error) {
	return f.deleteSessionResult, f.deleteSessionErr
}

// fakeAssistantUploadPolicy 作为助手上传策略的测试替身，用于在用例里提供可控的依赖行为。
type fakeAssistantUploadPolicy struct {
	supportedExtensions []string
}

// SupportsFileName 返回接收者对指定文件名的兼容性判断，供上层尽早拦截不支持的输入。
func (f fakeAssistantUploadPolicy) SupportsFileName(string) bool {
	return true
}

// SupportedExtensions 返回接收者允许的扩展名集合，避免调用方重复维护能力清单。
func (f fakeAssistantUploadPolicy) SupportedExtensions() []string {
	return append([]string(nil), f.supportedExtensions...)
}

// UnsupportedFileMessage 为不支持的文件生成对外提示文案，避免各入口分散拼接错误消息。
func (f fakeAssistantUploadPolicy) UnsupportedFileMessage(string) string {
	return "unsupported"
}

// mustMarshalHandlerJSON 在测试里强制构造 `MarshalhandlerJSON`，失败时立即终止当前用例。
func mustMarshalHandlerJSON(t *testing.T, value any) []byte {
	t.Helper()

	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}

	return payload
}

// stringPointer 返回字符串指针，简化构造可选文本字段时的样板代码。
func stringPointer(value string) *string {
	return &value
}

// mustDocumentParser 在测试里强制构造 `文档解析器`，失败时立即终止当前用例。
func mustDocumentParser(t *testing.T, options documentparser.Options) documentparser.Parser {
	t.Helper()

	parser, err := documentparser.New(options)
	if err != nil {
		t.Fatalf("new document parser: %v", err)
	}

	return parser
}

// performUploadRequest 为测试用例执行 `上传请求`，收口常见请求发起步骤。
func performUploadRequest(t *testing.T, engine *server.Hertz, fileName string, contentType string, content []byte) *protocol.Response {
	t.Helper()

	return performUploadRequestToPath(t, engine, "/api/assistant/sessions/"+testAssistantSessionUUID+"/files", fileName, contentType, content)
}

// performUploadRequestToPath 为测试用例执行 `上传请求路径`，收口常见请求发起步骤。
func performUploadRequestToPath(
	t *testing.T,
	engine *server.Hertz,
	path string,
	fileName string,
	contentType string,
	content []byte,
) *protocol.Response {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fileHeader := textproto.MIMEHeader{}
	fileHeader.Set("Content-Disposition", `form-data; name="file"; filename="`+fileName+`"`)
	fileHeader.Set("Content-Type", contentType)
	part, err := writer.CreatePart(fileHeader)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	return ut.PerformRequest(
		engine.Engine,
		"POST",
		path,
		&ut.Body{
			Body: body,
			Len:  body.Len(),
		},
		ut.Header{Key: "Content-Type", Value: writer.FormDataContentType()},
	).Result()
}

// parsedSSEEvent 保存解析后的 SSE 事件类型和载荷，便于测试按事件顺序与内容断言流式响应。
type parsedSSEEvent struct {
	Type string
	Data json.RawMessage
}

// parseSSEEvents 为测试场景处理 `parseSSE事件` 的辅助步骤，减少重复搭建逻辑。
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

// TestGetAssistantConversationNotFound 验证`getAssistantConversationNotFound`在特定边界条件下的行为，防止同类回归。
func TestGetAssistantConversationNotFound(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		getConversationErr: assistant.ErrSessionNotFound,
	})

	engine := server.New()
	engine.GET("/api/assistant/sessions/:id", handler.GetConversation)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/assistant/sessions/"+testAssistantMissingSessionUUID, nil).Result()
	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}

// TestGetAssistantConversationInvalidID 验证`getAssistantConversationInvalidID`在特定边界条件下的行为，防止同类回归。
func TestGetAssistantConversationInvalidID(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{})

	engine := server.New()
	engine.GET("/api/assistant/sessions/:id", handler.GetConversation)

	response := ut.PerformRequest(engine.Engine, "GET", "/api/assistant/sessions/not-a-uuid", nil).Result()
	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
	if !strings.Contains(string(response.Body()), "会话 ID 非法") {
		t.Fatalf("expected invalid session id error, got %q", string(response.Body()))
	}
}

// TestCreateConversationBadRequest 验证`createConversationBadRequest`在特定边界条件下的行为，防止同类回归。
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

// TestConfirmTaskSuggestionNotFound 验证`confirmTaskSuggestionNotFound`在特定边界条件下的行为，防止同类回归。
func TestConfirmTaskSuggestionNotFound(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		confirmTaskErr: assistant.ErrTaskSuggestionNotFound,
	})

	engine := server.New()
	engine.POST("/api/assistant/task-suggestions/:id/confirm", handler.ConfirmTaskSuggestion)

	response := ut.PerformRequest(engine.Engine, "POST", "/api/assistant/task-suggestions/"+testAssistantMissingSuggestionID+"/confirm", nil).Result()
	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("expected status %d, got %d", consts.StatusNotFound, response.StatusCode())
	}
}

// TestAppendAssistantMessageInvalidID 验证`appendAssistantMessageInvalidID`在特定边界条件下的行为，防止同类回归。
func TestAppendAssistantMessageInvalidID(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages", handler.AppendMessage)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/not-a-uuid/messages",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"请修订第二章"}`),
			Len:  len(`{"message":"请修订第二章"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
	if !strings.Contains(string(response.Body()), "会话 ID 非法") {
		t.Fatalf("expected invalid session id error, got %q", string(response.Body()))
	}
}

// TestAppendAssistantMessageStreamInvalidID 验证`appendAssistantMessageStreamInvalidID`在特定边界条件下的行为，防止同类回归。
func TestAppendAssistantMessageStreamInvalidID(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/messages/stream", handler.AppendMessageStream)

	response := ut.PerformRequest(
		engine.Engine,
		"POST",
		"/api/assistant/sessions/not-a-uuid/messages/stream",
		&ut.Body{
			Body: bytes.NewBufferString(`{"message":"请修订第二章"}`),
			Len:  len(`{"message":"请修订第二章"}`),
		},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	).Result()
	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
	if !strings.Contains(string(response.Body()), "会话 ID 非法") {
		t.Fatalf("expected invalid session id error, got %q", string(response.Body()))
	}
}

// TestUploadAssistantFileInvalidID 验证`uploadAssistantFileInvalidID`在特定边界条件下的行为，防止同类回归。
func TestUploadAssistantFileInvalidID(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{})

	engine := server.New()
	engine.POST("/api/assistant/sessions/:id/files", handler.UploadFile)

	response := performUploadRequestToPath(
		t,
		engine,
		"/api/assistant/sessions/not-a-uuid/files",
		"test.md",
		"text/markdown",
		[]byte("# test"),
	)
	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
	if !strings.Contains(string(response.Body()), "会话 ID 非法") {
		t.Fatalf("expected invalid session id error, got %q", string(response.Body()))
	}
}

// TestDeleteAssistantSessionInvalidID 验证`deleteAssistantSessionInvalidID`在特定边界条件下的行为，防止同类回归。
func TestDeleteAssistantSessionInvalidID(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{})

	engine := server.New()
	engine.DELETE("/api/assistant/sessions/:id", handler.DeleteSession)

	response := ut.PerformRequest(engine.Engine, "DELETE", "/api/assistant/sessions/not-a-uuid", nil).Result()
	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
	if !strings.Contains(string(response.Body()), "会话 ID 非法") {
		t.Fatalf("expected invalid session id error, got %q", string(response.Body()))
	}
}

// TestConfirmTaskSuggestionInvalidID 验证`confirmTaskSuggestionInvalidID`在特定边界条件下的行为，防止同类回归。
func TestConfirmTaskSuggestionInvalidID(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{})

	engine := server.New()
	engine.POST("/api/assistant/task-suggestions/:id/confirm", handler.ConfirmTaskSuggestion)

	response := ut.PerformRequest(engine.Engine, "POST", "/api/assistant/task-suggestions/not-a-uuid/confirm", nil).Result()
	if response.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", consts.StatusBadRequest, response.StatusCode())
	}
	if !strings.Contains(string(response.Body()), "任务建议 ID 非法") {
		t.Fatalf("expected invalid task suggestion id error, got %q", string(response.Body()))
	}
}

// TestDeleteAssistantSessionInternalError 验证`deleteAssistantSessionInternalError`在特定边界条件下的行为，防止同类回归。
func TestDeleteAssistantSessionInternalError(t *testing.T) {
	handler := NewAssistantHandler(fakeAssistantService{
		deleteSessionErr: errors.New("delete failed"),
	})

	engine := server.New()
	engine.DELETE("/api/assistant/sessions/:id", handler.DeleteSession)

	response := ut.PerformRequest(engine.Engine, "DELETE", "/api/assistant/sessions/"+testAssistantSessionUUID, nil).Result()
	if response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", consts.StatusInternalServerError, response.StatusCode())
	}
}
