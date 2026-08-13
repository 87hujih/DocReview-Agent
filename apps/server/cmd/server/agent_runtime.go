package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	agentcontext "agent_project/apps/server/internal/agent/context"
	"agent_project/apps/server/internal/agent/cutover"
	agentevidence "agent_project/apps/server/internal/agent/evidence"
	"agent_project/apps/server/internal/agent/identity"
	"agent_project/apps/server/internal/agent/orchestration"
	"agent_project/apps/server/internal/agent/policy"
	"agent_project/apps/server/internal/agent/projection"
	agentruntime "agent_project/apps/server/internal/agent/runtime"
	agenttools "agent_project/apps/server/internal/agent/tools"
	"agent_project/apps/server/internal/agent/tools/builtin"
	agentturn "agent_project/apps/server/internal/agent/turn"
	"agent_project/apps/server/internal/assistant/websearch"
	appconfig "agent_project/apps/server/internal/config"
	documentcommit "agent_project/apps/server/internal/document/commit"
	documentmodel "agent_project/apps/server/internal/document/model"
	documentvalidation "agent_project/apps/server/internal/document/validation"
	"agent_project/apps/server/internal/knowledge/embedder"
	"agent_project/apps/server/internal/knowledge/reranker"
	"agent_project/apps/server/internal/storage/postgres/agentartifact"
	"agent_project/apps/server/internal/storage/postgres/agentpolicy"
	"agent_project/apps/server/internal/storage/postgres/agentprojection"
	"agent_project/apps/server/internal/storage/postgres/agentrun"
	postgresagentturn "agent_project/apps/server/internal/storage/postgres/agentturn"
	postgresdocumentcommit "agent_project/apps/server/internal/storage/postgres/documentcommit"
	"agent_project/apps/server/internal/storage/postgres/documentruntime"
	postgresevidence "agent_project/apps/server/internal/storage/postgres/evidence"
	"agent_project/apps/server/internal/storage/postgres/outbox"

	"github.com/jackc/pgx/v5/pgxpool"
)

type agentRuntimeCore struct {
	coordinator      *agentturn.Coordinator
	runtimeWorker    *agentruntime.Worker
	projectionWorker *outbox.ProjectionWorker
	identity         identity.Adapter
	approvals        *agentpolicy.ApprovalStore
}

func buildDurableOnlyTurnPipeline(durable cutover.Runner) (*cutover.DurableOnlyPipeline, error) {
	return cutover.NewDurableOnlyPipeline(durable)
}

// BuildTurnPipeline 执行该函数负责的核心处理逻辑。
func (core *agentRuntimeCore) BuildTurnPipeline(projector cutover.PublicProjector) (*cutover.DurableOnlyPipeline, error) {
	if core == nil || core.coordinator == nil || projector == nil {
		return nil, fmt.Errorf("Agent 运行时核心和公开投影器不能为空")
	}
	durable, err := cutover.NewDurableRunner(cutover.DurableRunnerConfig{
		PollInterval: 200 * time.Millisecond, MaxWait: 90 * time.Second,
	}, core.coordinator, projector)
	if err != nil {
		return nil, err
	}
	return buildDurableOnlyTurnPipeline(durable)
}

// buildAgentRuntimeCore 执行该函数负责的核心处理逻辑。
func buildAgentRuntimeCore(ctx context.Context, cfg appconfig.Config, pool *pgxpool.Pool, embeddings *embedder.Embedder, rerankClient *reranker.Client) (*agentRuntimeCore, error) {
	if pool == nil || embeddings == nil || rerankClient == nil {
		return nil, fmt.Errorf("Agent 运行时数据库和检索客户端不能为空")
	}
	workerID := fmt.Sprintf("agent-runtime-%d", os.Getpid())
	tokenizer := agentcontext.ModelEstimator{Profile: "docreview-estimator-v1"}
	runRepo := agentrun.NewRepository(pool)
	turnCoordinator := agentturn.NewCoordinator(postgresagentturn.NewRepository(pool))

	contextAssembler, err := agentcontext.NewAssembler(agentcontext.Config{
		Tokenizer: tokenizer, TokenBudget: 32768, ReservedOutputTokens: 4096,
		LayerBudgets: map[agentcontext.Layer]int{
			agentcontext.LayerControl: 1500, agentcontext.LayerTask: 4000,
			agentcontext.LayerWorkingMemory: 8000, agentcontext.LayerEvidence: 12000,
			agentcontext.LayerConversation: 5000, agentcontext.LayerArtifactReference: 2000,
		},
	}, runRepo)
	if err != nil {
		return nil, err
	}
	managedContext, err := orchestration.NewManagedContextAssembler(
		contextAssembler, runRepo, agentrun.NewPostgresRuntimeContextSource(pool),
	)
	if err != nil {
		return nil, err
	}

	generator, err := orchestration.NewOpenAIChatGenerator(ctx, orchestration.OpenAIChatConfig{
		APIKey: cfg.SiliconFlowAPIKey, BaseURL: cfg.SiliconFlowBaseURL, Model: cfg.LLMModel,
		Timeout: time.Duration(cfg.LLMTimeoutMS) * time.Millisecond, Temperature: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化类型化的模型生成器：%w", err)
	}
	temperature := 0.0
	modelGateway, err := orchestration.NewProductionModelGateway(orchestration.ModelGatewayConfig{
		Provider: "siliconflow-openai-compatible", Model: cfg.LLMModel,
		PromptVersion: "typed-runtime-v1", Temperature: &temperature, Tokenizer: tokenizer,
	}, generator)
	if err != nil {
		return nil, err
	}

	evidenceService, err := agentevidence.NewService(agentevidence.Config{
		ProfileVersion: "retrieval-v1", LexicalEnabled: true, SemanticEnabled: true,
		LexicalIndexVersion: "pg-trgm-v1", SemanticIndexVersion: "hnsw-cosine-v1",
		CandidateLimit: 50, FusionAlgorithm: agentevidence.FusionWeightedSum,
		LexicalWeight: 0.45, VectorWeight: 0.55, MinimumFusedScore: 0.05,
		Embedding: agentevidence.EmbeddingProfile{
			Version: "embedding-v1", Model: cfg.EmbeddingModel, Dimensions: cfg.EmbeddingDim,
			VectorType: fmt.Sprintf("vector(%d)", cfg.EmbeddingDim), IndexVersion: "hnsw-cosine-v1",
		},
		RerankEnabled: true, RerankProfileVersion: "rerank-v1", RerankModel: cfg.RerankerModel,
	}, postgresevidence.NewRepository(pool), embeddings, evidenceReranker{client: rerankClient})
	if err != nil {
		return nil, fmt.Errorf("初始化类型化的证据Set 检索：%w", err)
	}
	retrievalBackend, err := builtin.NewEvidenceRetrievalBackend(evidenceService)
	if err != nil {
		return nil, err
	}

	documentRepo := documentruntime.New(pool)
	committer, err := documentcommit.New(postgresdocumentcommit.New(pool), documentvalidation.New(), documentcommit.Options{
		ProjectionProfile: documentmodel.ProjectionProfile{SchemaVersion: "1.0", ChunkProfile: "node-v1", EmbeddingProfile: "embedding-v1"},
	})
	if err != nil {
		return nil, err
	}
	patchBackend, err := documentcommit.NewToolBackend(committer, documentRepo)
	if err != nil {
		return nil, err
	}
	artifactRepo := agentartifact.NewRepository(pool)
	approvalStore := agentpolicy.NewApprovalStore(pool)
	registry := agenttools.NewRegistry()
	webBackend := builtin.ProviderWebBackend{}
	webConfig := builtin.WebConfig{ProviderKind: builtin.WebProviderMock, ProviderName: "disabled"}
	if cfg.WebSearchEnabled {
		webBackend.Provider = websearch.NewMCPWebSearchProvider(websearch.MCPConfig{
			URL: cfg.WebSearchMCPURL, AuthToken: cfg.WebSearchMCPAuthToken,
		})
		webConfig = builtin.WebConfig{ProviderKind: builtin.WebProviderProduction, ProviderName: "mcp"}
	}
	if err := builtin.RegisterCore(registry, builtin.Backends{
		Documents: documentRepo, Retrieval: retrievalBackend,
		Web: webBackend, Artifacts: agentartifact.NewBuiltinBackend(artifactRepo),
		Patches: patchBackend, Approvals: approvalStore,
	}, webConfig); err != nil {
		return nil, fmt.Errorf("注册类型化的工具：%w", err)
	}
	resolver := agentpolicy.NewResolver(pool)
	audit, err := agentrun.NewToolAuditStore(runRepo, workerID+"-tool", 45*time.Second)
	if err != nil {
		return nil, err
	}
	toolRuntime, err := agenttools.NewRuntime(agenttools.RuntimeConfig{
		Registry: registry, Authorizer: policy.NewEngine(resolver, resolver, resolver), Audit: audit,
		Limiter: agentpolicy.NewPostgresRateLimiter(pool, agentpolicy.StaticRateLimitRules{
			ByRisk: map[agenttools.RiskLevel]agentpolicy.RateLimitRule{
				agenttools.RiskHigh: {Limit: 10, Window: time.Minute}, agenttools.RiskCritical: {Limit: 5, Window: time.Minute},
			},
			Default: agentpolicy.RateLimitRule{Limit: 60, Window: time.Minute},
		}),
		Counter: agentcontext.JSONTokenCounter{Tokenizer: tokenizer}, Artifacts: agentartifact.NewRuntimeStore(artifactRepo),
	})
	if err != nil {
		return nil, err
	}
	toolExecutor, err := orchestration.NewRuntimeToolExecutor(toolRuntime, agentrun.NewSecurityScopeStore(pool))
	if err != nil {
		return nil, err
	}
	supervisor, err := orchestration.NewSupervisor(orchestration.SupervisorConfig{
		MaxNoProgress: 3, MaxStateObservations: 32,
	}, modelGateway, managedContext, toolExecutor, orchestration.NewActionValidator())
	if err != nil {
		return nil, err
	}
	engine, err := agentruntime.NewEngine(agentruntime.Config{
		WorkerID: workerID, LeaseDuration: 30 * time.Second, HeartbeatInterval: 10 * time.Second,
		AttemptTimeout: time.Duration(cfg.LLMTimeoutMS+15000) * time.Millisecond,
		StepTimeout:    3 * time.Minute, RetryBase: time.Second, RetryMax: 30 * time.Second,
	}, agentrun.NewEngineStore(runRepo), supervisor, nil)
	if err != nil {
		return nil, err
	}
	runtimeWorker, err := agentruntime.NewWorker(agentruntime.WorkerConfig{
		PollInterval: 500 * time.Millisecond, ErrorBackoff: 2 * time.Second, RecoveryInterval: 30 * time.Second,
		OnError: func(err error) { log.Printf("警告：durable Agent Runtime worker：%v", err) },
	}, engine)
	if err != nil {
		return nil, err
	}

	projectionRepo := agentprojection.NewRepository(pool)
	runtimeProjector, err := projection.NewRuntimeProjector(projectionRepo, turnCoordinator, projectionRepo)
	if err != nil {
		return nil, err
	}
	projectionWorker, err := outbox.NewProjectionWorker(outbox.ProjectionWorkerConfig{
		WorkerID: workerID + "-projection", LeaseDuration: 30 * time.Second,
		PollInterval: 500 * time.Millisecond, ErrorBackoff: 2 * time.Second, RecoveryInterval: 30 * time.Second,
		BatchSize: 20, MaxAttempts: 8, RetryBase: time.Second, RetryMax: time.Minute,
		EventTypes: []string{"agent.step.outcome_committed", "agent.tool_approval.rejected"},
	}, outbox.NewRepository(pool), runtimeProjector, nil)
	if err != nil {
		return nil, err
	}

	identityAdapter, err := identity.NewTrustedIngressAdapter(identity.TrustedIngressConfig{
		Secret: cfg.AgentRuntimeTrustedIngressSecret, TrustSource: cfg.AgentRuntimeTrustedIngressSource,
		MaxAge: time.Duration(cfg.AgentRuntimeTrustedIngressMaxAgeMS) * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	return &agentRuntimeCore{
		coordinator: turnCoordinator, runtimeWorker: runtimeWorker, projectionWorker: projectionWorker,
		identity: identityAdapter, approvals: approvalStore,
	}, nil
}

// 启动执行该函数负责的核心处理逻辑。
func (core *agentRuntimeCore) Start(ctx context.Context) {
	// 启动并发任务，并由外围同步机制负责回收。
	go func() {
		if err := core.runtimeWorker.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("警告：durable Agent Runtime worker 已停止：%v", err)
		}
	}()
	// 启动并发任务，并由外围同步机制负责回收。
	go func() {
		if err := core.projectionWorker.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("警告：Agent Runtime projection worker 已停止：%v", err)
		}
	}()
}

type evidenceReranker struct{ client *reranker.Client }

// 重排执行该函数负责的核心处理逻辑。
func (adapter evidenceReranker) Rerank(ctx context.Context, query string, documents []string, topN int) ([]agentevidence.RerankResult, error) {
	results, err := adapter.client.Rerank(ctx, query, documents, topN)
	if err != nil {
		return nil, err
	}
	converted := make([]agentevidence.RerankResult, 0, len(results))
	for _, result := range results {
		converted = append(converted, agentevidence.RerankResult{Index: result.Index, Score: result.RelevanceScore})
	}
	return converted, nil
}
