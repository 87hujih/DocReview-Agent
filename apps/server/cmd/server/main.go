package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/agent/executor"
	"agent_project/apps/server/internal/agent/planner"
	"agent_project/apps/server/internal/agent/reviewer"
	"agent_project/apps/server/internal/approval"
	"agent_project/apps/server/internal/assistant"
	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/job"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/knowledge/ingest"
	"agent_project/apps/server/internal/knowledge/reranker"
	"agent_project/apps/server/internal/knowledge/retriever"
	"agent_project/apps/server/internal/observability/logging"
	"agent_project/apps/server/internal/server/handlers"
	"agent_project/apps/server/internal/server/router"
	"agent_project/apps/server/internal/storage/postgres"
	taskevents "agent_project/apps/server/internal/task/events"
	taskservice "agent_project/apps/server/internal/task/service"
	"agent_project/apps/server/internal/task/workflow"
)

// main 负责装配配置、数据库、检索能力和 HTTP 服务入口。
func main() {
	cfg := appconfig.Load()
	logger := logging.NewLogger("server", cfg.LogLevel, cfg.LogFormat, cfg.LogAddSource, os.Stdout)
	if err := cfg.ValidateForServer(); err != nil {
		log.Fatalf("配置无效：%v", err)
	}

	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("数据库连接失败：%v", err)
	}
	defer pool.Close()

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("数据库迁移失败：%v", err)
	}

	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)
	jobRepo := postgres.NewJobRepo(pool)
	eventRepo := postgres.NewTaskEventRepo(pool)
	assistantRepo := postgres.NewAssistantRepo(pool)
	eventService := taskevents.New(eventRepo)

	emb, err := embedder.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDim)
	if err != nil {
		log.Fatalf("向量嵌入器初始化失败：%v", err)
	}

	rerankerClient := reranker.New(cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.RerankerModel)
	retrieverService := retriever.NewService(resourceRepo, emb, rerankerClient)
	ingestService := ingest.NewService(resourceRepo, emb)

	plannerAgent, err := planner.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.LLMModel)
	if err != nil {
		log.Fatalf("规划代理初始化失败：%v", err)
	}

	reviewerAgent, err := reviewer.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.LLMModel)
	if err != nil {
		log.Fatalf("评审代理初始化失败：%v", err)
	}

	editorAgent, err := editor.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.LLMModel)
	if err != nil {
		log.Fatalf("编辑代理初始化失败：%v", err)
	}

	// 演示数据导入采用尽力而为策略，样例目录缺失不应阻塞本地启动。
	if err := ingestService.ImportDirectory(ctx, demoDataDir()); err != nil {
		log.Printf("警告：演示数据导入失败：%v", err)
	}

	exec := executor.New(taskRepo, resourceRepo)
	worker := job.New(jobRepo, exec, taskRepo, 100, eventService)
	worker.Start(ctx, 3)

	resourceHandler := handlers.NewResourceHandler(resourceRepo, retrieverService)
	orchestrator := workflow.New(taskRepo, resourceRepo, approvalRepo, plannerAgent, reviewerAgent, editorAgent, retrieverService)
	taskService := taskservice.New(taskRepo, resourceRepo, orchestrator, eventService)
	taskHandler := handlers.NewTaskHandler(taskService, taskRepo)
	approvalService := approval.NewService(approvalRepo, jobRepo, taskRepo, worker.JobCh(), eventService)
	approvalHandler := handlers.NewApprovalHandler(approvalService)
	assistantResponder, err := assistant.NewChatResponder(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.LLMModel)
	if err != nil {
		log.Fatalf("助手对话模型初始化失败：%v", err)
	}

	assistantService := assistant.NewService(
		assistantRepo,
		assistant.NewIngestDocumentImporter(ingestService),
		taskService,
		assistantResponder,
		resourceRepo,
	)
	assistantHandler := handlers.NewAssistantHandler(assistantService)
	h := router.New(cfg, logger, router.Deps{
		ResourceHandler:  resourceHandler,
		TaskHandler:      taskHandler,
		ApprovalHandler:  approvalHandler,
		AssistantHandler: assistantHandler,
	})

	if err := h.Run(); err != nil {
		log.Fatalf("服务退出：%v", err)
	}
}

// demoDataDir 兼容从仓库根目录或 apps/server 目录启动服务。
func demoDataDir() string {
	candidates := []string{
		filepath.Join("demo-data", "documents"),
		filepath.Join("..", "..", "demo-data", "documents"),
	}

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
	}

	return candidates[0]
}
