package router

import (
	"strings"
	"testing"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/server/handlers"

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
	h := New(appconfig.Config{ServerPort: "0"}, Deps{
		ApprovalHandler: handlers.NewApprovalHandler(nil),
	})

	response := ut.PerformRequest(h.Engine, "GET", "/api/approvals", nil).Result()

	if response.StatusCode() != consts.StatusInternalServerError {
		t.Fatalf("expected status %d when approval route is registered without service, got %d", consts.StatusInternalServerError, response.StatusCode())
	}
}
