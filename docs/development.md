# 开发指南

## 依赖版本

| 工具 | 版本 |
|------|------|
| Go | >= 1.26 |
| Node.js | >= 18 |
| Docker | >= 24 |
| PostgreSQL | 16（本地演示可用 `docker compose up -d`） |

## 环境变量

参考项目根目录 `.env.example`，复制为 `.env` 后填入实际值：

| 变量 | 说明 | 默认值 |
|------|------|--------|
| SERVER_PORT | 后端端口 | 8080 |
| DATABASE_URL | PostgreSQL 连接串 | （必填，无默认值） |
| SILICONFLOW_API_KEY | 硅基流动 API Key | （必填） |
| SILICONFLOW_BASE_URL | 硅基流动 API 地址 | https://api.siliconflow.cn/v1 |
| LLM_MODEL | LLM 模型 ID | Qwen/Qwen3.5-35B-A3B |
| EMBEDDING_MODEL | Embedding 模型 ID | Qwen/Qwen3-Embedding-8B |
| EMBEDDING_DIM | Embedding 维度 | 1024 |
| RERANKER_MODEL | Reranker 模型 ID | Qwen/Qwen3-Reranker-8B |
| LOG_LEVEL | 日志级别（debug/info/warn/error） | info |
| LOG_FORMAT | 日志格式（json/text） | json |
| LOG_ADD_SOURCE | 日志是否包含源码位置 | false |

配置优先级：环境变量 > `.env` > `config/default.yaml` > 硬编码默认值。

## 数据库

准备数据库：

```bash
# 使用本地 Docker（pgvector/pgvector:pg16）
docker compose up -d
```

对应的 `.env` 配置：

```
DATABASE_URL=postgres://postgres:postgres@localhost:5432/agent_project?sslmode=disable
```

如果使用远程或已有的 PostgreSQL 实例，确保已安装 `pgvector` 和 `pg_trgm` 扩展，并在 `.env` 中填入正确的连接串即可。

迁移在后端启动时自动执行（`RunMigrations`），无需手动操作。当前包含三个迁移：

| 文件 | 内容 |
|------|------|
| 001_mvp_init.sql | 基础表：resources、resource_versions、tasks、task_steps、task_artifacts、approvals、execution_jobs |
| 002_lexical_search.sql | pg_trgm 扩展与 GIN 索引 |
| 003_task_events_observability.sql | task_events 事件审计表与复合索引 |

## 运行测试

后端：

```bash
cd apps/server && go test ./... -v
```

前端：

```bash
cd apps/web && npm test -- --run
```

## 构建

后端：

```bash
cd apps/server && go build -o bin/server ./cmd/server
```

前端：

```bash
cd apps/web && npm run build
```

## 目录结构

```
Agent_Project/
├── apps/
│   ├── server/                Go 后端（Hertz 框架）
│   │   ├── cmd/server/        入口（main.go）
│   │   └── internal/
│   │       ├── agent/         LLM Agent
│   │       │   ├── planner/   规划代理（分析指令，生成检索查询）
│   │       │   ├── reviewer/  评审代理（基于引用生成审阅摘要）
│   │       │   ├── editor/    编辑代理（生成结构化 diff 提案）
│   │       │   └── executor/  执行代理（应用 diff 到文档，创建新版本）
│   │       ├── approval/      审批服务（approve/reject + 事件记录）
│   │       ├── config/        配置加载（YAML + .env + 环境变量三级联）
│   │       ├── job/           异步执行 worker pool（channel-based）
│   │       ├── knowledge/     知识层
│   │       │   ├── chunker/   Markdown 分块
│   │       │   ├── citation/  引用模型
│   │       │   ├── embedder/  向量嵌入（SiliconFlow OpenAI-compatible）
│   │       │   ├── ingest/    文档导入服务
│   │       │   ├── reranker/  SiliconFlow Reranker 客户端
│   │       │   └── retriever/ 混合检索（向量 + 词法 + rerank）
│   │       ├── observability/ 可观测性
│   │       │   └── logging/   结构化日志（slog）与 context 传播
│   │       ├── server/        HTTP 层
│   │       │   ├── handlers/  资源、任务、审批 handler
│   │       │   ├── middleware/ 请求追踪（X-Request-ID、访问日志、panic 捕获）
│   │       │   └── router/    路由注册
│   │       ├── storage/       数据持久化
│   │       │   └── postgres/  pgx 直连（ResourceRepo、TaskRepo、ApprovalRepo、JobRepo、TaskEventRepo）
│   │       └── task/          任务领域
│   │           ├── models/    状态机与模型
│   │           ├── service/   任务创建与查询
│   │           ├── events/    任务事件记录服务
│   │           └── workflow/  Orchestrator（四步编排）
│   └── web/                   Next.js 14 前端
│       ├── app/               App Router 页面（Dashboard、Resources、Task Create、Task Detail、Approvals）
│       ├── components/        UI 组件（时间线、引用列表、diff 预览、任务创建表单等）
│       └── lib/
│           ├── api/           Typed API client
│           └── terminal.ts    终端 UI 工具函数
├── config/                    默认配置（default.yaml）
├── demo-data/documents/       演示文档（employee-handbook.md、security-policy.md）
├── deploy/                    生产部署（docker-compose.prod.yml、prod.env.example）
└── .github/workflows/         CI（ci.yml）+ CD（release-deploy.yml）
```

## API 端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | /healthz | 健康检查 |
| GET | /api/resources | 资源列表 |
| GET | /api/resources/:id | 资源详情（含当前版本内容） |
| GET | /api/resources/:id/search?q= | 资源内混合检索 |
| POST | /api/tasks | 创建修订任务 |
| GET | /api/tasks | 任务列表 |
| GET | /api/tasks/:id | 任务详情（含步骤） |
| GET | /api/tasks/:id/artifacts | 任务产物（citations、review_summary、diff_preview） |
| GET | /api/approvals | 审批列表（可选 ?status= 过滤） |
| POST | /api/approvals/:id/approve | 批准审批 |
| POST | /api/approvals/:id/reject | 拒绝审批（body: {"reason": "..."})  |