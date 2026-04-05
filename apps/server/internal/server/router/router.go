package router

import (
	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/server/handlers"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/route"
)

// Deps 收集接入 HTTP 服务时需要注入的可选 handler。
type Deps struct {
	ResourceHandler *handlers.ResourceHandler
}

// New 构建 Hertz 服务，并只注册当前依赖已经就绪的路由。
func New(cfg appconfig.Config, deps Deps) *server.Hertz {
	h := server.Default(server.WithHostPorts(":" + cfg.ServerPort))
	Register(h.Engine)
	if deps.ResourceHandler != nil {
		registerResourceRoutes(h.Engine, deps.ResourceHandler)
	}

	return h
}

// Register 注册不依赖业务服务的基础路由。
func Register(engine *route.Engine) {
	engine.GET("/healthz", handlers.Health)
}

// registerResourceRoutes 注册依赖资源 handler 的 API 路由。
func registerResourceRoutes(engine *route.Engine, h *handlers.ResourceHandler) {
	api := engine.Group("/api")
	api.GET("/resources", h.List)
	api.GET("/resources/:id", h.GetByID)
	api.GET("/resources/:id/search", h.Search)
}
