package router

import (
	"log/slog"

	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/server/handlers"
	servermiddleware "agent_project/apps/server/internal/server/middleware"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/route"
)

// Deps 收集接入 HTTP 服务时需要注入的可选 handler。
type Deps struct {
	ResourceHandler  *handlers.ResourceHandler
	TaskHandler      *handlers.TaskHandler
	ApprovalHandler  *handlers.ApprovalHandler
	AssistantHandler *handlers.AssistantHandler
	FileHandler      *handlers.FileHandler
}

// New 构建 Hertz 服务，并只注册当前依赖已经就绪的路由。
func New(cfg appconfig.Config, logger *slog.Logger, deps Deps) *server.Hertz {
	h := server.Default(server.WithHostPorts(":" + cfg.ServerPort))
	h.Use(servermiddleware.RequestContext("server", logger))
	h.Use(servermiddleware.CORS())
	Register(h.Engine)
	if deps.ResourceHandler != nil {
		registerResourceRoutes(h.Engine, deps.ResourceHandler)
	}
	if deps.TaskHandler != nil {
		registerTaskRoutes(h.Engine, deps.TaskHandler)
	}
	if deps.ApprovalHandler != nil {
		registerApprovalRoutes(h.Engine, deps.ApprovalHandler)
	}
	if deps.AssistantHandler != nil {
		registerAssistantRoutes(h.Engine, deps.AssistantHandler)
	}
	if deps.FileHandler != nil {
		registerFileRoutes(h.Engine, deps.FileHandler)
	}

	return h
}

// Register 注册不依赖业务服务的基础路由。
func Register(engine *route.Engine) {
	engine.GET("/healthz", handlers.Health)
	engine.OPTIONS("/api/*path", handlers.Preflight)
}

// registerResourceRoutes 注册依赖资源 handler 的 API 路由。
func registerResourceRoutes(engine *route.Engine, h *handlers.ResourceHandler) {
	api := engine.Group("/api")
	api.GET("/resources", h.List)
	api.GET("/resources/:id", h.GetByID)
	api.GET("/resources/:id/task-context", h.GetTaskContext)
	api.GET("/resources/:id/export", h.ExportCurrentVersion)
	api.GET("/resources/:id/search", h.Search)
}

// registerTaskRoutes 注册依赖任务 handler 的 API 路由。
func registerTaskRoutes(engine *route.Engine, h *handlers.TaskHandler) {
	api := engine.Group("/api")
	api.POST("/tasks", h.Create)
	api.GET("/tasks", h.List)
	api.GET("/tasks/:id", h.GetByID)
	api.GET("/tasks/:id/artifacts", h.GetArtifacts)
	api.GET("/tasks/:id/events", h.GetEvents)
}

// registerApprovalRoutes 注册依赖审批 handler 的 API 路由。
func registerApprovalRoutes(engine *route.Engine, h *handlers.ApprovalHandler) {
	api := engine.Group("/api")
	api.GET("/approvals", h.List)
	api.GET("/approvals/:id", h.GetByID)
	api.GET("/jobs/:id", h.GetJobByID)
	api.POST("/approvals/:id/approve", h.Approve)
	api.POST("/approvals/:id/reject", h.Reject)
}

// registerAssistantRoutes 注册助手会话相关 API 路由。
func registerAssistantRoutes(engine *route.Engine, h *handlers.AssistantHandler) {
	api := engine.Group("/api/assistant")
	api.GET("/capabilities", h.GetCapabilities)
	api.GET("/sessions", h.ListSessions)
	api.GET("/sessions/:id", h.GetConversation)
	api.DELETE("/sessions/:id", h.DeleteSession)
	api.POST("/conversations", h.CreateConversation)
	api.POST("/conversations/files", h.UploadConversationFile)
	api.POST("/conversations/stream", h.CreateConversationStream)
	api.POST("/sessions/:id/messages", h.AppendMessage)
	api.POST("/sessions/:id/messages/stream", h.AppendMessageStream)
	api.POST("/sessions/:id/files", h.UploadFile)
	api.POST("/task-suggestions/:id/confirm", h.ConfirmTaskSuggestion)
}

// registerFileRoutes 注册原始上传文件下载 API 路由。
func registerFileRoutes(engine *route.Engine, h *handlers.FileHandler) {
	api := engine.Group("/api")
	api.GET("/files/:id/download", h.Download)
}
