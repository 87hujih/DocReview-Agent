package router

import (
	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/server/handlers"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/route"
)

type Deps struct {
	ResourceHandler *handlers.ResourceHandler
}

func New(cfg appconfig.Config, deps Deps) *server.Hertz {
	h := server.Default(server.WithHostPorts(":" + cfg.ServerPort))
	Register(h.Engine)
	if deps.ResourceHandler != nil {
		registerResourceRoutes(h.Engine, deps.ResourceHandler)
	}

	return h
}

func Register(engine *route.Engine) {
	engine.GET("/healthz", handlers.Health)
}

func registerResourceRoutes(engine *route.Engine, h *handlers.ResourceHandler) {
	api := engine.Group("/api")
	api.GET("/resources", h.List)
	api.GET("/resources/:id", h.GetByID)
	api.GET("/resources/:id/search", h.Search)
}
