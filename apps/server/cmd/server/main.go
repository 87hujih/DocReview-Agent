package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"agent_project/apps/server/internal/assistant"
	appconfig "agent_project/apps/server/internal/config"
	documentnormalize "agent_project/apps/server/internal/document/normalize"
	documentparser "agent_project/apps/server/internal/document/parser"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/knowledge/indexer"
	"agent_project/apps/server/internal/knowledge/ingest"
	"agent_project/apps/server/internal/knowledge/reranker"
	"agent_project/apps/server/internal/knowledge/retriever"
	"agent_project/apps/server/internal/observability/logging"
	"agent_project/apps/server/internal/server/handlers"
	"agent_project/apps/server/internal/server/router"
	"agent_project/apps/server/internal/storage/filestore"
	"agent_project/apps/server/internal/storage/postgres"
	"agent_project/apps/server/internal/storage/postgres/agentops"
)

// main 负责装配配置、数据库、检索能力和 HTTP 服务入口
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

	resourceRepo := postgres.NewResourceRepo(pool)
	resourceStructureRepo := postgres.NewResourceStructureRepo(pool)
	assistantRepo := postgres.NewAssistantRepo(pool)
	uploadedFileRepo := postgres.NewUploadedFileRepo(pool)

	emb, err := embedder.New(ctx, cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.EmbeddingModel, cfg.EmbeddingDim)
	if err != nil {
		log.Fatalf("向量嵌入器初始化失败：%v", err)
	}

	rerankerClient := reranker.New(cfg.SiliconFlowBaseURL, cfg.SiliconFlowAPIKey, cfg.RerankerModel)
	agentRuntime, err := buildAgentRuntimeCore(ctx, cfg, pool, emb, rerankerClient)
	if err != nil {
		log.Fatalf("Agent Runtime 初始化失败：%v", err)
	}
	agentRuntime.Start(ctx)
	retrieverService := retriever.NewService(resourceRepo, emb, rerankerClient)
	versionIndexer := indexer.NewService(resourceRepo, emb)
	normalizeService := documentnormalize.NewService()
	docParser, err := documentparser.New(documentparser.Options{
		Mode:        cfg.DocumentParser,
		TikaURL:     cfg.TikaURL,
		TikaTimeout: time.Duration(cfg.TikaTimeoutMS) * time.Millisecond,
	})
	if err != nil {
		log.Fatalf("文档解析器初始化失败：%v", err)
	}

	ingestService := ingest.NewService(
		resourceRepo,
		emb,
		ingest.WithParser(docParser),
		ingest.WithIndexer(versionIndexer),
		ingest.WithStructureRepo(resourceStructureRepo),
		ingest.WithNormalizer(normalizeService),
	)

	uploadStore, err := filestore.NewLocalStore(projectPath(cfg.UploadStorageDir))
	if err != nil {
		log.Fatalf("原文件存储初始化失败：%v", err)
	}
	resourceHandler := handlers.NewResourceHandler(resourceRepo, retrieverService)

	assistantOptions := []assistant.ServiceOption{
		assistant.WithUploadedFileStorage(uploadStore, uploadedFileRepo),
	}

	assistantService := assistant.NewService(
		assistantRepo,
		assistant.NewIngestDocumentImporter(ingestService),
		nil,
		nil,
		retrieverService,
		assistantOptions...,
	)
	turnPipeline, err := agentRuntime.BuildTurnPipeline(handlers.NewConversationPublicProjector(assistantRepo))
	if err != nil {
		log.Fatalf("Agent Runtime Turn Pipeline 初始化失败：%v", err)
	}
	assistantHandler := handlers.NewAssistantHandlerWithTurnPipeline(
		assistantService, int64(cfg.UploadMaxBytes), docParser, turnPipeline, agentRuntime.identity,
	)
	typedApprovalHandler := handlers.NewTypedApprovalHandler(agentRuntime.approvals, agentRuntime.identity)
	agentRuntimeQueryHandler := handlers.NewAgentRuntimeQueryHandler(agentops.NewRepository(pool), agentRuntime.identity)
	fileHandler := handlers.NewFileHandler(uploadedFileRepo, uploadStore)
	h := router.New(cfg, logger, router.Deps{
		ResourceHandler:          resourceHandler,
		AgentRuntimeQueryHandler: agentRuntimeQueryHandler,
		TypedApprovalHandler:     typedApprovalHandler,
		AssistantHandler:         assistantHandler,
		FileHandler:              fileHandler,
	})

	if err := h.Run(); err != nil {
		log.Fatalf("服务退出：%v", err)
	}
}

// projectPath 从 section 元数据里提取项目路径，供项目结构切块时复用。
func projectPath(relative string) string {
	if filepath.IsAbs(relative) {
		return relative
	}

	root := findProjectRoot()
	if root == "" {
		return relative
	}

	return filepath.Join(root, relative)
}

// findProjectRoot 在项目 section 树里定位顶层目录节点，作为生成项目级 chunk 的锚点。
func findProjectRoot() string {
	currentDir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		if _, err := os.Stat(filepath.Join(currentDir, ".git")); err == nil {
			return currentDir
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			return ""
		}

		currentDir = parentDir
	}
}
