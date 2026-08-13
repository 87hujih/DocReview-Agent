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

// TestNewDoesNotExposeLegacyTaskApprovalOrJobRoutes verifies the old execution
// surface cannot be re-enabled through router dependency injection.
func TestNewDoesNotExposeLegacyTaskApprovalOrJobRoutes(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{})
	for _, path := range []string{"/api/tasks", "/api/approvals", "/api/jobs/00000000-0000-0000-0000-000000000000"} {
		response := ut.PerformRequest(h.Engine, "GET", path, nil).Result()
		if response.StatusCode() != consts.StatusNotFound {
			t.Fatalf("legacy route %s must stay disabled, got %d", path, response.StatusCode())
		}
	}
}

func TestNewRegistersAgentRuntimeQueryRoutesWhenHandlerProvided(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		AgentRuntimeQueryHandler: handlers.NewAgentRuntimeQueryHandler(nil, nil),
	})

	for _, path := range []string{"/api/agent/runs", "/api/agent/approvals"} {
		response := ut.PerformRequest(h.Engine, "GET", path, nil).Result()
		if response.StatusCode() != consts.StatusServiceUnavailable {
			t.Fatalf("new agent route %s was not registered, got %d", path, response.StatusCode())
		}
	}
}

// TestNewRegistersFileRoutesWhenHandlerProvided 验证`newRegistersFileRoutesWhenHandlerProvided`在特定边界条件下的行为，防止同类回归。
func TestNewRegistersFileRoutesWhenHandlerProvided(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		FileHandler: handlers.NewFileHandler(nil, nil),
	})

	response := ut.PerformRequest(h.Engine, "GET", "/api/files/file-1/download", nil).Result()
	if response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d when file route is registered without storage, got %d", consts.StatusInternalServerError, response.StatusCode())
	}
}

// TestNewRegistersResourceExportRouteWhenHandlerProvided 验证`newRegistersResourceExportRouteWhenHandlerProvided`在特定边界条件下的行为，防止同类回归。
func TestNewRegistersResourceExportRouteWhenHandlerProvided(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		ResourceHandler: handlers.NewResourceHandler(nil, nil),
	})

	response := ut.PerformRequest(h.Engine, "GET", "/api/resources/resource-1/export", nil).Result()
	if response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d when resource export route is registered without repo, got %d", consts.StatusInternalServerError, response.StatusCode())
	}
}

// TestNewRegistersResourceListRouteWhenHandlerProvided 验证`newRegistersResourceListRouteWhenHandlerProvided`在特定边界条件下的行为，防止同类回归。
func TestNewRegistersResourceListRouteWhenHandlerProvided(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		ResourceHandler: handlers.NewResourceHandler(nil, nil),
	})

	response := ut.PerformRequest(h.Engine, "GET", "/api/resources", nil).Result()
	if response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d when resource list route is registered without repo, got %d", consts.StatusInternalServerError, response.StatusCode())
	}
	if body := string(response.Body()); !strings.Contains(body, "资源存储未配置") {
		t.Fatalf("expected resource list route to return configured storage error, got %q", body)
	}
}

// TestNewDoesNotExposeLegacyResourceTaskContext verifies task-oriented resource
// projection is no longer part of the production HTTP surface.
func TestNewDoesNotExposeLegacyResourceTaskContext(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		ResourceHandler: handlers.NewResourceHandler(nil, nil),
	})

	response := ut.PerformRequest(h.Engine, "GET", "/api/resources/resource-1/task-context", nil).Result()
	if response.StatusCode() != consts.StatusNotFound {
		t.Fatalf("legacy task-context route must stay disabled, got %d", response.StatusCode())
	}
}

// TestNewAddsCORSHeadersToAPIResponses 验证`new`在写入或副作用路径下的行为，防止同类回归。
func TestNewAddsCORSHeadersToAPIResponses(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0", CORSAllowedOrigins: []string{"http://127.0.0.1:3000"}}, nil, Deps{})

	response := ut.PerformRequest(
		h.Engine,
		"GET",
		"/api/not-found",
		nil,
		ut.Header{Key: "Origin", Value: "http://127.0.0.1:3000"},
	).Result()

	if value := string(response.Header.Peek("Access-Control-Allow-Origin")); value != "http://127.0.0.1:3000" {
		t.Fatalf("expected allowlisted Access-Control-Allow-Origin, got %q", value)
	}
}

// TestNewHandlesAssistantPreflightOPTIONS 验证`new`在格式处理路径下的行为，防止同类回归。
func TestNewHandlesAssistantPreflightOPTIONS(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0", CORSAllowedOrigins: []string{"http://127.0.0.1:3000"}}, nil, Deps{})

	response := ut.PerformRequest(
		h.Engine,
		"OPTIONS",
		"/api/agent/approvals/test-approval/approve",
		nil,
		ut.Header{Key: "Origin", Value: "http://127.0.0.1:3000"},
		ut.Header{Key: "Access-Control-Request-Method", Value: "POST"},
		ut.Header{Key: "Access-Control-Request-Headers", Value: "content-type"},
	).Result()

	if response.StatusCode() != consts.StatusNoContent {
		t.Fatalf("expected status %d, got %d", consts.StatusNoContent, response.StatusCode())
	}

	if value := string(response.Header.Peek("Access-Control-Allow-Origin")); value != "http://127.0.0.1:3000" {
		t.Fatalf("expected allowlisted Access-Control-Allow-Origin, got %q", value)
	}

	if value := string(response.Header.Peek("Access-Control-Allow-Methods")); !strings.Contains(value, "POST") {
		t.Fatalf("expected Access-Control-Allow-Methods to contain POST, got %q", value)
	}
}

// TestNewAddsRequestIDToPreflightOPTIONS 验证`new`在写入或副作用路径下的行为，防止同类回归。
func TestNewAddsRequestIDToPreflightOPTIONS(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0", CORSAllowedOrigins: []string{"http://127.0.0.1:3000"}}, nil, Deps{})

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

	if value := string(response.Header.Peek("Access-Control-Allow-Origin")); value != "http://127.0.0.1:3000" {
		t.Fatalf("expected allowlisted Access-Control-Allow-Origin, got %q", value)
	}
}

// TestNewRejectsDisallowedCORSPreflight 验证对应场景下的正常路径与失败路径。
func TestNewRejectsDisallowedCORSPreflight(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0", CORSAllowedOrigins: []string{"https://app.example.com"}}, nil, Deps{})

	response := ut.PerformRequest(
		h.Engine,
		"OPTIONS",
		"/api/resources",
		nil,
		ut.Header{Key: "Origin", Value: "https://evil.example.com"},
		ut.Header{Key: "Access-Control-Request-Method", Value: "GET"},
	).Result()

	if response.StatusCode() != consts.StatusForbidden {
		t.Fatalf("expected status %d, got %d", consts.StatusForbidden, response.StatusCode())
	}
	if value := string(response.Header.Peek("Access-Control-Allow-Origin")); value != "" {
		t.Fatalf("expected no allow-origin header for rejected origin, got %q", value)
	}
}

// TestNewAllowsPatchCORSPreflight 验证对应场景下的正常路径与失败路径。
func TestNewAllowsPatchCORSPreflight(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0", CORSAllowedOrigins: []string{"https://app.example.com"}}, nil, Deps{})

	response := ut.PerformRequest(
		h.Engine,
		"OPTIONS",
		"/api/assistant/sessions/session-1/web-search",
		nil,
		ut.Header{Key: "Origin", Value: "https://app.example.com"},
		ut.Header{Key: "Access-Control-Request-Method", Value: "PATCH"},
	).Result()

	if response.StatusCode() != consts.StatusNoContent {
		t.Fatalf("expected status %d, got %d", consts.StatusNoContent, response.StatusCode())
	}
	if value := string(response.Header.Peek("Access-Control-Allow-Methods")); !strings.Contains(value, "PATCH") {
		t.Fatalf("expected Access-Control-Allow-Methods to contain PATCH, got %q", value)
	}
}

// TestNewRegistersAssistantStreamingRoutesWhenHandlerProvided 验证`newRegistersAssistantStreamingRoutesWhenHandlerProvided`在特定边界条件下的行为，防止同类回归。
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

	uploadConversationResponse := ut.PerformRequest(
		h.Engine,
		"POST",
		"/api/assistant/conversations/files",
		nil,
	).Result()
	if uploadConversationResponse.StatusCode() != consts.StatusBadRequest {
		t.Fatalf("expected status %d for registered conversation upload route, got %d", consts.StatusBadRequest, uploadConversationResponse.StatusCode())
	}
}

// TestNewAddsCORSHeadersToAssistantStreamingResponses 验证`new`在写入或副作用路径下的行为，防止同类回归。
func TestNewAddsCORSHeadersToAssistantStreamingResponses(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0", CORSAllowedOrigins: []string{"http://127.0.0.1:3000"}}, nil, Deps{
		AssistantHandler: handlers.NewAssistantHandler(fakeAssistantRouterService{}),
	})

	response := ut.PerformRequest(
		h.Engine,
		"POST",
		"/api/assistant/conversations/stream",
		nil,
		ut.Header{Key: "Origin", Value: "http://127.0.0.1:3000"},
	).Result()

	if value := string(response.Header.Peek("Access-Control-Allow-Origin")); value != "http://127.0.0.1:3000" {
		t.Fatalf("expected allowlisted Access-Control-Allow-Origin, got %q", value)
	}
}

// TestNewRegistersAssistantCapabilitiesRouteWhenHandlerProvided 验证`newRegistersAssistantCapabilitiesRouteWhenHandlerProvided`在特定边界条件下的行为，防止同类回归。
func TestNewRegistersAssistantCapabilitiesRouteWhenHandlerProvided(t *testing.T) {
	h := New(appconfig.Config{ServerPort: "0"}, nil, Deps{
		AssistantHandler: handlers.NewAssistantHandler(fakeAssistantRouterService{}),
	})

	response := ut.PerformRequest(
		h.Engine,
		"GET",
		"/api/assistant/capabilities",
		nil,
	).Result()
	if response.StatusCode() != consts.StatusOK {
		t.Fatalf("expected status %d for registered capabilities route, got %d", consts.StatusOK, response.StatusCode())
	}
}

// fakeAssistantRouterService 作为助手路由器服务的测试替身，用于在用例里提供可控的依赖行为。
type fakeAssistantRouterService struct{}

// ListSessions 实现测试替身需要的 `ListSessions` 接口方法，为用例分支提供可控返回。
func (fakeAssistantRouterService) ListSessions(context.Context) ([]postgres.AssistantSession, error) {
	return nil, nil
}

// GetConversation 实现测试替身需要的 `GetConversation` 接口方法，为用例分支提供可控返回。
func (fakeAssistantRouterService) GetConversation(context.Context, string) (*assistant.ConversationResult, error) {
	return nil, nil
}

// StartConversation 实现测试替身需要的 `StartConversation` 接口方法，为用例分支提供可控返回。
func (fakeAssistantRouterService) StartConversation(context.Context, string) (*assistant.ConversationResult, error) {
	return nil, nil
}

// StartConversationWithFile 实现测试替身需要的空会话上传接口，为用例分支提供可控返回。
func (fakeAssistantRouterService) StartConversationWithFile(context.Context, string, []byte) (*assistant.UploadFileResult, error) {
	return nil, nil
}

// StartConversationStream 实现测试替身需要的 `StartConversationStream` 接口方法，为用例分支提供可控返回。
func (fakeAssistantRouterService) StartConversationStream(context.Context, string, func(assistant.StreamEvent) error) error {
	return nil
}

// AppendMessage 实现测试替身需要的 `AppendMessage` 接口方法，为用例分支提供可控返回。
func (fakeAssistantRouterService) AppendMessage(context.Context, string, string) (*assistant.ConversationResult, error) {
	return nil, nil
}

// AppendMessageStream 实现测试替身需要的 `AppendMessageStream` 接口方法，为用例分支提供可控返回。
func (fakeAssistantRouterService) AppendMessageStream(context.Context, string, string, func(assistant.StreamEvent) error) error {
	return nil
}

// UploadFile 实现测试替身需要的 `UploadFile` 接口方法，为用例分支提供可控返回。
func (fakeAssistantRouterService) UploadFile(context.Context, string, string, []byte) (*assistant.UploadFileResult, error) {
	return nil, nil
}

// ConfirmTaskSuggestion 实现测试替身需要的 `ConfirmTaskSuggestion` 接口方法，为用例分支提供可控返回。
func (fakeAssistantRouterService) ConfirmTaskSuggestion(context.Context, string) (*assistant.ConfirmTaskResult, error) {
	return nil, nil
}

// DeleteSession 实现测试替身需要的 `DeleteSession` 接口方法，为用例分支提供可控返回。
func (fakeAssistantRouterService) DeleteSession(context.Context, string) (bool, error) {
	return false, nil
}

// ToggleWebSearch 执行该函数负责的核心处理逻辑。
func (fakeAssistantRouterService) ToggleWebSearch(context.Context, string, bool) (*postgres.AssistantSession, error) {
	return nil, nil
}
