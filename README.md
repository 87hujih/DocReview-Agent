# DocReview Agent

[![CI](https://github.com/87hujih/DocReview-Agent/actions/workflows/ci.yml/badge.svg)](https://github.com/87hujih/DocReview-Agent/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/Go-1.26.1-00ADD8?logo=go&logoColor=white)
![Next.js](https://img.shields.io/badge/Next.js-14-000000?logo=next.js&logoColor=white)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white)

一个面向文档审阅与修订的 AI 工作台。用户可以在助手会话中上传文档、针对当前文件提问、基于可追溯证据生成修改方案，并在人工审批后提交新版本。

核心架构采用持久化 Agent Runtime：Turn、Run、Step、Attempt、工具调用、证据、审批、提交和 Outbox 都是 PostgreSQL 中的持久化事实；模型负责理解与决策，权限、预算、重试、审批和写入由确定性边界控制。

> [!IMPORTANT]
> Agent 请求必须经过受保护入口并携带签名身份。可信入口负责认证用户、绑定 Organization / Workspace，并为请求生成 HMAC-SHA256 身份证明；签名密钥只能保存在服务端或部署系统中。

## 核心能力

| 能力 | 实现 |
| --- | --- |
| 助手工作台 | 流式回复、会话历史、文件上传、断线后事件续传 |
| 持久化运行时 | Run / Step / Attempt、lease、超时、重试、取消、预算和崩溃恢复 |
| 类型化编排 | 有界 `Decide -> Act -> Observe` 循环，严格 Decision Schema 与工具注册表 |
| 证据检索 | `pgvector` 语义召回、`pg_trgm` 词法召回、融合、阈值和 reranker 重排 |
| 规范化文档 | Canonical AST、稳定节点 ID、结构化 Patch 校验、原子版本提交 |
| 人工审批 | 审批绑定 Workspace、Run、工具版本、Resource hash 与写入幂等键 |
| 可观测性 | Run 详情、工具审计、结构化日志、事务性 Outbox、运维与离线评测 CLI |
| 租户边界 | Principal / Organization / Workspace 作用域与 fail-closed trusted ingress |

## 架构

```mermaid
flowchart LR
    Client["Web / API Client"] --> Ingress["TLS + Auth + Trusted Ingress"]
    Ingress --> Turn["Turn Coordinator"]
    Turn --> Engine["Durable Run Engine"]
    Engine --> Context["Context Assembler"]
    Context --> Graph["Typed Supervisor"]
    Graph --> Tools["Tool Runtime + Policy Engine"]
    Tools --> Evidence["Evidence Set"]
    Tools --> Document["Canonical Document AST"]
    Evidence --> Patch["Patch Validator"]
    Document --> Patch
    Patch --> Approval["External Approval"]
    Approval --> Commit["Atomic Committer"]
    Commit --> Outbox["Transactional Outbox"]
    Outbox --> Projection["Public Projection + SSE"]
    Projection --> Client
```

核心设计约束：

- **持久化执行**：运行状态、上下文引用、工具结果和事件均写入 PostgreSQL，Worker 可在进程重启后恢复任务。
- **Fail closed**：身份、Workspace、Resource、Evidence、审批或预期 hash 不匹配时终止请求。
- **模型不持有权限**：模型不能授予权限、批准自己的请求、跳过 Patch 校验或直接提交文档。
- **写入可重放**：请求、工具和 commit 使用稳定幂等键；SSE 只是持久化事件的可重建投影。
- **作用域隔离**：每次查询、工具调用、审批和提交都绑定精确的 Organization、Workspace 与 Resource。

核心实现位于 [`apps/server/internal/agent`](./apps/server/internal/agent) 和 [`apps/server/internal/document`](./apps/server/internal/document)。

## 产品页面

| 路由 | 用途 |
| --- | --- |
| `/` | Agent 助手、会话历史、文件上传与流式结果 |
| `/resources` | 文档资源列表 |
| `/resources/:id` | 当前版本查看、检索与导出 |
| `/runs` | Durable Run 列表、状态筛选和轮询 |
| `/runs/:id` | Run、Step、工具调用、审批与告警详情 |
| `/approvals` | 类型化审批列表及批准 / 拒绝操作 |

## 技术栈

- **Web**：Next.js 14、React 18、TypeScript、Vitest、Testing Library
- **Server**：Go 1.26.1、Hertz、pgx、CloudWeGo Eino
- **AI**：SiliconFlow 兼容的 LLM、Embedding 与 Reranker API
- **Data**：PostgreSQL 16、pgvector、pg_trgm、本地内容寻址文件存储
- **Documents**：Markdown / Text 直通解析，可选 Apache Tika
- **Delivery**：Docker Compose、GitHub Actions、区域 OCI registry、SSH 部署

## 快速开始

### 环境要求

- Go `1.26.1+`
- Node.js `20+`
- PowerShell `7+`
- Docker 与 Docker Compose
- SiliconFlow API Key

### 1. 获取代码

```powershell
git clone https://github.com/87hujih/DocReview-Agent.git
cd DocReview-Agent
Copy-Item .env.example .env
```

### 2. 配置本地环境

编辑本地 `.env`，至少设置以下内容：

```dotenv
DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/agent_project?sslmode=disable
SILICONFLOW_API_KEY=<your-api-key>
AGENT_RUNTIME_MODE=durable
AGENT_RUNTIME_TRUSTED_INGRESS_HMAC_SECRET=<at-least-32-random-bytes>
AGENT_RUNTIME_TRUSTED_INGRESS_SOURCE=edge-hmac-v1
```

`.env` 仅用于本地开发，不要提交真实密钥。配置优先级为：进程环境变量 > `.env` > `config/default.yaml` > 代码默认值。

### 3. 安装前端依赖并启动

```powershell
Set-Location apps/web
npm ci
Set-Location ../..

docker compose up -d postgres
pwsh -File scripts/dev/start-local.ps1
```

`docker compose` 会启动本地 PostgreSQL。随后本地脚本会启动 Tika、执行显式数据库迁移，再启动 Go Server 和 Next.js Web：

| 服务 | 地址 |
| --- | --- |
| Web | `http://127.0.0.1:3000` |
| Server | `http://127.0.0.1:18080` |
| Health | `http://127.0.0.1:18080/healthz` |
| Tika | `http://127.0.0.1:9998` |

查看状态或停止环境：

```powershell
pwsh -File scripts/dev/status-local.ps1
pwsh -File scripts/dev/stop-local.ps1
```

停止脚本只管理 Tika、Server 和 Web；如需停止本地 PostgreSQL，请单独运行 `docker compose stop postgres`。

> [!NOTE]
> 本地脚本负责启动基础设施和应用，不负责用户认证与身份签名。要完整调用助手、Run 查询和审批接口，需要在 Server 前配置受信代理：清除客户端传入的身份头，完成认证，再附加与 request ID、HTTP method、path、Principal、Organization、Workspace、签发时间和角色绑定的 HMAC-SHA256 签名。HMAC secret 不能进入浏览器配置。

## 关键配置

完整模板见 [`.env.example`](./.env.example)，生产模板见 [`deploy/prod.env.example`](./deploy/prod.env.example)。

| 变量 | 说明 | 默认值 / 要求 |
| --- | --- | --- |
| `APP_ENV` | 运行环境；生产值会启用额外校验 | `development` |
| `SERVER_PORT` | 后端监听端口 | `8080`；本地脚本使用 `18080` |
| `DATABASE_URL` | PostgreSQL 连接串 | Server 与 migrator 必填 |
| `CORS_ALLOWED_ORIGINS` | 浏览器 Origin 白名单 | 生产必填，禁止 `*`、路径和通配符 |
| `SILICONFLOW_API_KEY` | 模型服务密钥 | Server 启动必填 |
| `LLM_MODEL` | 对话与编排模型 | 见 `config/default.yaml` |
| `EMBEDDING_MODEL` | Embedding 模型 | `Qwen/Qwen3-Embedding-8B` |
| `EMBEDDING_DIM` | 向量维度 | `1024` |
| `RERANKER_MODEL` | Reranker 模型 | `Qwen/Qwen3-Reranker-8B` |
| `DOCUMENT_PARSER` | `text` 或 `tika` | `text` |
| `TIKA_URL` | Tika Server 地址 | `tika` 模式必填 |
| `UPLOAD_STORAGE_DIR` | 原始上传文件目录 | `data/uploads` |
| `UPLOAD_MAX_BYTES` | 单文件上限 | `20971520`（20 MiB） |
| `NEXT_PUBLIC_API_URL` | 浏览器访问 API 的公开基地址 | 本地脚本注入 `http://127.0.0.1:18080` |
| `AGENT_RUNTIME_MODE` | Agent Runtime 模式 | 只接受 `durable` |
| `AGENT_RUNTIME_TRUSTED_INGRESS_HMAC_SECRET` | 入口签名密钥 | 必填，至少 32 字节，禁止进入客户端 |
| `AGENT_RUNTIME_TRUSTED_INGRESS_SOURCE` | 信任来源审计名 | 必填 |
| `AGENT_RUNTIME_TRUSTED_INGRESS_MAX_AGE_MS` | 签名最大时效 | `300000` |

`DOCUMENT_PARSER=text` 仅接受 `.md` / `.txt`。切换到 `tika` 并配置 `TIKA_URL` 后，可处理 `.doc` / `.docx` / `.pdf` / `.rtf` / `.odt`。

## 开发与验证

### 后端

运行数据库无关的配置测试与构建：

```powershell
go test ./apps/server/internal/config
go build ./apps/server/cmd/server
```

全量后端测试包含 PostgreSQL 集成测试。测试连接只允许读取 `TEST_DATABASE_URL`，且必须同时满足：

- `ALLOW_DB_TESTS=1`
- 数据库名以 `_test` 结尾
- 主机位于 `TEST_DATABASE_HOST_ALLOWLIST`

```powershell
$env:ALLOW_DB_TESTS = "1"
$env:TEST_DATABASE_URL = "postgres://postgres:postgres@127.0.0.1:5432/agent_project_test?sslmode=disable"
$env:TEST_DATABASE_HOST_ALLOWLIST = "127.0.0.1,localhost"
go test ./apps/server/... -count=1
```

测试绝不会回退读取 `DATABASE_URL` 或仓库 `.env`。无法确认数据库来源时，不要运行数据库集成测试。

### 前端

```powershell
Set-Location apps/web
npm test -- --run
npm run lint
npm run build
```

### 离线评测

```powershell
go run ./apps/server/cmd/agent-eval `
  -dataset apps/server/internal/agent/evaluation/testdata/agent_runtime_eval_v1.json `
  -candidate apps/server/internal/agent/evaluation/testdata/agent_runtime_candidate_v1.json `
  -report agent-runtime-eval-report.json
```

## 运维命令

数据库迁移必须在 Server 或维护命令启动前显式完成：

```powershell
go run ./apps/server/cmd/migrate
```

常用维护入口：

| 命令 | 用途 |
| --- | --- |
| `cmd/agent-runtime-ops` | Workspace 范围内诊断、指标、取消、受控重试和 Outbox replay |
| `cmd/agent-eval` | 对版本化数据集与候选结果执行离线评测 |
| `cmd/resource-reindex` | 重建 Resource 当前版本索引 |

`agent-runtime-ops` 只读取显式进程环境中的 `DATABASE_URL`，不会加载 `.env` 或自动执行迁移。使用前可运行 `go run ./apps/server/cmd/agent-runtime-ops -help` 查看完整参数。

## 项目结构

```text
DocReview-Agent/
|-- apps/
|   |-- server/
|   |   |-- cmd/
|   |   |   |-- server/                 # HTTP Server 与 Runtime 装配
|   |   |   |-- migrate/                # Checksum migration runner
|   |   |   |-- agent-runtime-ops/      # 诊断与受控恢复
|   |   |   `-- agent-eval/             # 离线评测
|   |   `-- internal/
|   |       |-- agent/                  # identity、turn、runtime、context、tools、policy、orchestration
|   |       |-- document/               # import、canonical model、validation、commit、render
|   |       |-- knowledge/              # ingest、embedding、retrieval、rerank、index
|   |       |-- server/                 # handlers、router、middleware
|   |       `-- storage/                # PostgreSQL repositories 与本地文件存储
|   `-- web/                            # Next.js App Router 前端
|-- config/default.yaml                 # 非敏感默认配置
|-- deploy/                             # 生产 Compose 与环境模板
|-- docs/                               # 架构说明、开发指南与运行手册
|-- scripts/dev/                        # 本地启动、状态与停止脚本
`-- .github/workflows/                  # CI / Release Images / Deploy Production
```

## CI 与部署

`CI` 在 Pull Request 和 `main` 分支 push 时执行：

- Agent Runtime 离线评测
- 带安全测试数据库配置的 Go 测试
- Web 测试、Lint 与生产构建
- Server / Web Docker 镜像构建

推送 `v*` Tag 后，`Release Images` 会构建 Server / Web 镜像并推送到区域 OCI registry：

- `${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/docreview-agent-server:<tag>`
- `${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/docreview-agent-web:<tag>`

生产切换由 `Deploy Production` 手动触发。它通过 SSH 同步部署文件，在远端拉取指定 `image_tag`，显式运行 migration，再启动 Server / Web 并检查健康状态。生产部署模板位于 [`deploy/docker-compose.prod.yml`](./deploy/docker-compose.prod.yml)。

生产环境必须满足以下边界：

- Backend 只绑定主机回环地址，由具备 TLS、认证、限流和身份签名能力的反向代理转发 `/api`。
- 代理必须移除客户端伪造的 `X-DocReview-*` 身份头，再写入可信身份头和签名。
- `NEXT_PUBLIC_API_URL` 指向浏览器可访问的 HTTPS 应用来源，HMAC secret 不得出现在任何 `NEXT_PUBLIC_*` 变量中。
- 迁移只允许由独立 migrator 执行；Server 和运维 CLI 不自动修改 Schema。
- 部署前必须人工确认服务器 `.env` 中的 `DATABASE_URL` 是真实生产连接，而不是示例占位值。
- 发布回滚前应先在入口停止接收新请求，等待已接受的 Run 与 Outbox 事件处理完成，再部署目标版本。

## 代码入口

- [Server 装配](./apps/server/cmd/server)
- [Agent Runtime](./apps/server/internal/agent)
- [文档运行时](./apps/server/internal/document)
- [知识检索](./apps/server/internal/knowledge)
- [HTTP 路由](./apps/server/internal/server/router/router.go)
- [Web 应用](./apps/web/app)
- [生产部署](./deploy/docker-compose.prod.yml)
