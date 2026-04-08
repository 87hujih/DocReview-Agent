package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"agent_project/apps/server/internal/agent/editor"
	"agent_project/apps/server/internal/agent/planner"
	"agent_project/apps/server/internal/agent/reviewer"
	appconfig "agent_project/apps/server/internal/config"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/knowledge/ingest"
	"agent_project/apps/server/internal/knowledge/reranker"
	"agent_project/apps/server/internal/knowledge/retriever"
	"agent_project/apps/server/internal/server/handlers"
	"agent_project/apps/server/internal/server/router"
	"agent_project/apps/server/internal/storage/postgres"
	taskservice "agent_project/apps/server/internal/task/service"
	"agent_project/apps/server/internal/task/workflow"
)

// main 负责装配配置、数据库、检索能力和 HTTP 服务入口。
func main() {
	cfg := appconfig.Load()
	if err := cfg.ValidateForServer(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer pool.Close()

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		log.Fatalf("database migration failed: %v", err)
	}

	resourceRepo := postgres.NewResourceRepo(pool)
	taskRepo := postgres.NewTaskRepo(pool)
	approvalRepo := postgres.NewApprovalRepo(pool)

	emb, err := embedder.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDim)
	if err != nil {
		log.Fatalf("embedder init failed: %v", err)
	}

	rerankerClient := reranker.New(cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.RerankerModel)
	retrieverService := retriever.NewService(resourceRepo, emb, rerankerClient)
	ingestService := ingest.NewService(resourceRepo, emb)

	plannerAgent, err := planner.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.LLMModel)
	if err != nil {
		log.Fatalf("planner agent init failed: %v", err)
	}

	reviewerAgent, err := reviewer.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.LLMModel)
	if err != nil {
		log.Fatalf("reviewer agent init failed: %v", err)
	}

	editorAgent, err := editor.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.LLMModel)
	if err != nil {
		log.Fatalf("editor agent init failed: %v", err)
	}

	// 演示数据导入采用尽力而为策略，样例目录缺失不应阻塞本地启动。
	if err := ingestService.ImportDirectory(ctx, demoDataDir()); err != nil {
		log.Printf("WARN: demo data import failed: %v", err)
	}

	resourceHandler := handlers.NewResourceHandler(resourceRepo, retrieverService)
	orchestrator := workflow.New(taskRepo, resourceRepo, approvalRepo, plannerAgent, reviewerAgent, editorAgent, retrieverService)
	taskService := taskservice.New(taskRepo, resourceRepo, orchestrator)
	taskHandler := handlers.NewTaskHandler(taskService, taskRepo)
	h := router.New(cfg, router.Deps{
		ResourceHandler: resourceHandler,
		TaskHandler:     taskHandler,
	})

	if err := h.Run(); err != nil {
		log.Fatalf("server exited: %v", err)
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
