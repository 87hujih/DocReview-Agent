package router

import (
	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/server/handlers"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/route"
)

func New(cfg appconfig.Config) *server.Hertz {
	h := server.Default(server.WithHostPorts(":" + cfg.ServerPort))
	Register(h.Engine)

	return h
}

func Register(engine *route.Engine) {
	engine.GET("/healthz", handlers.Health)
}
