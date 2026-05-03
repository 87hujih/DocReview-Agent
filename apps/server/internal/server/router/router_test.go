package router

import (
	"context"
	"strings"
	"testing"

	"agent_project/apps/server/internal/assistant"
	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/server/handlers"
	"agent_project/apps/server/internal/storage/postgres"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// TestRegisterExposesHealthEndpoint 验证基础路由注册后会暴露健康检查接口。
func TestRegisterExposesHealthEndpoint(t *testing.T) {
	h := server.New()
	Register(h.Engine)

	response := ut.PerformRequest(h.Engine, "GET", "/healthz", nil).Result()

	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d, got %d", consts.StatusOK, response.StatusCode())
	}

	body := string(response.Body())
	if !strings.Contains(body, "\"status\":\"ok\"") {
		t.Fatalf("expected body to contain status field, got %q", body)
	}

	if !strings.Contains(body, "\"service\":\"server\"") {
		t.Fatalf("expected body to contain service field, got %q", body)
	}
}

func TestNewRegistersApprovalRoutesWhenHandlerProvided(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		ApprovalHandler: handlers.NewApprovalHandler(nil),
	})

	response := ut.PerformRequest(h.Engine, "GET", "/api/approvals", nil).Result()

	if response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d when approval route is registered without service, got %d", consts.StatusInternalServerError, response.StatusCode())
	}

	approvalDetailResponse := ut.PerformRequest(h.Engine, "GET", "/api/approvals/00000000-0000-0000-0000-000000000000", nil).Result()
	if approvalDetailResponse.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d for registered approval detail route, got %d", consts.StatusInternalServerError, approvalDetailResponse.StatusCode())
	}

	jobDetailResponse := ut.PerformRequest(h.Engine, "GET", "/api/jobs/00000000-0000-0000-0000-000000000000", nil).Result()
	if jobDetailResponse.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d for registered job detail route, got %d", consts.StatusInternalServerError, jobDetailResponse.StatusCode())
	}
}

func TestNewRegistersFileRoutesWhenHandlerProvided(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		FileHandler: handlers.NewFileHandler(nil, nil),
	})

	response := ut.PerformRequest(h.Engine, "GET", "/api/files/file-1/download", nil).Result()
	if response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d when file route is registered without storage, got %d", consts.StatusInternalServerError, response.StatusCode())
	}
}

func TestNewRegistersResourceExportRouteWhenHandlerProvided(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		ResourceHandler: handlers.NewResourceHandler(nil, nil),
	})

	response := ut.PerformRequest(h.Engine, "GET", "/api/resources/resource-1/export", nil).Result()
	if response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d when resource export route is registered without repo, got %d", consts.StatusInternalServerError, response.StatusCode())
	}
}

func TestNewAddsCORSHeadersToAPIResponses(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		ApprovalHandler: handlers.NewApprovalHandler(nil),
	})

	response := ut.PerformRequest(
		h.Engine,
		"GET",
		"/api/approvals",
		nil,
		ut.Header{Key: "Origin", Value: "http://127.0.0.1:3000"},
	).Result()

	if value := string(response.Header.Peek("Access-Control-Allow-Origin")); value != "*" {
		t.Fatalf("expected Access-Control-Allow-Origin header '*', got %q", value)
	}
}

func TestNewHandlesAssistantPreflightOPTIONS(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		ApprovalHandler: handlers.NewApprovalHandler(nil),
	})

	response := ut.PerformRequest(
		h.Engine,
		"OPTIONS",
		"/api/approvals/test-approval/approve",
		nil,
		ut.Header{Key: "Origin", Value: "http://127.0.0.1:3000"},
		ut.Header{Key: "Access-Control-Request-Method", Value: "POST"},
		ut.Header{Key: "Access-Control-Request-Headers", Value: "content-type"},
	).Result()

	if response.StatusCode() != consts.StatusNoContent {
		t.Fatalf("expected status %d, got %d", consts.StatusNoContent, response.StatusCode())
	}

	if value := string(response.Header.Peek("Access-Control-Allow-Origin")); value != "*" {
		t.Fatalf("expected Access-Control-Allow-Origin header '*', got %q", value)
	}

	if value := string(response.Header.Peek("Access-Control-Allow-Methods")); !strings.Contains(value, "POST") {
		t.Fatalf("expected Access-Control-Allow-Methods to contain POST, got %q", value)
	}
}

func TestNewAddsRequestIDToPreflightOPTIONS(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{})

	response := ut.PerformRequest(
		h.Engine,
		"OPTIONS",
		"/api/resources",
		nil,
		ut.Header{Key: "Origin", Value: "http://127.0.0.1:3000"},
		ut.Header{Key: "Access-Control-Request-Method", Value: "GET"},
	).Result()

	if response.StatusCode() != consts.StatusNoContent {
		t.Fatalf("expected status %d, got %d", consts.StatusNoContent, response.StatusCode())
	}

	if value := string(response.Header.Peek("X-Request-ID")); value == "" {
		t.Fatal("expected X-Request-ID on preflight response")
	}

	if value := string(response.Header.Peek("Access-Control-Allow-Origin")); value != "*" {
		t.Fatalf("expected Access-Control-Allow-Origin header '*', got %q", value)
	}
}

func TestNewRegistersAssistantStreamingRoutesWhenHandlerProvided(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		AssistantHandler: handlers.NewAssistantHandler(fakeAssistantRouterService{}),
	})

	createResponse := ut.PerformRequest(
		h.Engine,
		"POST",
		"/api/assistant/conversations/stream",
		nil,
	).Result()
	if createResponse.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for registered create stream route, got %d", consts.StatusBadRequest, createResponse.StatusCode())
	}

	appendResponse := ut.PerformRequest(
		h.Engine,
		"POST",
		"/api/assistant/sessions/session-1/messages/stream",
		nil,
	).Result()
	if appendResponse.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for registered append stream route, got %d", consts.StatusBadRequest, appendResponse.StatusCode())
	}
}

func TestNewAddsCORSHeadersToAssistantStreamingResponses(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		AssistantHandler: handlers.NewAssistantHandler(fakeAssistantRouterService{}),
	})

	response := ut.PerformRequest(
		h.Engine,
		"POST",
		"/api/assistant/conversations/stream",
		nil,
		ut.Header{Key: "Origin", Value: "http://127.0.0.1:3000"},
	).Result()

	if value := string(response.Header.Peek("Access-Control-Allow-Origin")); value != "*" {
		t.Fatalf("expected Access-Control-Allow-Origin header '*', got %q", value)
	}
}

type fakeAssistantRouterService struct{}

func (fakeAssistantRouterService) ListSessions(context.Context) ([]postgres.AssistantSession, error) {
	return nil, nil
}

func (fakeAssistantRouterService) GetConversation(context.Context, string) (*assistant.ConversationResult, error) {
	return nil, nil
}

func (fakeAssistantRouterService) StartConversation(context.Context, string) (*assistant.ConversationResult, error) {
	return nil, nil
}

func (fakeAssistantRouterService) StartConversationStream(context.Context, string, func(assistant.StreamEvent) error) error {
	return nil
}

func (fakeAssistantRouterService) AppendMessage(context.Context, string, string) (*assistant.ConversationResult, error) {
	return nil, nil
}

func (fakeAssistantRouterService) AppendMessageStream(context.Context, string, string, func(assistant.StreamEvent) error) error {
	return nil
}

func (fakeAssistantRouterService) UploadFile(context.Context, string, string, []byte) (*assistant.UploadFileResult, error) {
	return nil, nil
}

func (fakeAssistantRouterService) ConfirmTaskSuggestion(context.Context, string) (*assistant.ConfirmTaskResult, error) {
	return nil, nil
}

func (fakeAssistantRouterService) DeleteSession(context.Context, string) (bool, error) {
	return false, nil
}

func (fakeAssistantRouterService) ToggleWebSearch(context.Context, string, bool) (*postgres.AssistantSession, error) {
	return nil, nil
}
