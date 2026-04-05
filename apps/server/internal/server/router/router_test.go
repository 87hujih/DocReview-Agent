package router

import (
	"strings"
	"testing"

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
