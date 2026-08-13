# DocReview Agent

一个以“助手会话”为主入口的文档审阅与修订工作台。

用户可以在对话里上传文档、直接针对当前文件提问、基于引用生成修订建议，并在需要时把对话转成带审批的异步任务。任务不会直接改文档，而是先经过 `Planner -> Retriever -> Reviewer -> Editor` 多阶段编排，再进入人工审批，最后由确定性执行器生成新版本并提供导出。

> 当前仓库更偏个人项目 / 作品集形态，已经覆盖完整产品闭环，但暂未引入登录、多租户和权限系统。

## 功能亮点

- **助手优先的工作流**：首页就是助手，支持流式回复、会话历史、文件上传和任务建议确认。
- **当前文件原生可读**：上传后的文档会被导入资源库，助手既能做 citation-aware 检索，也能对当前文件做更稳定的章节 / 序号 / 实体读取。
- **Citation-aware RAG**：结构化 section + grounded chunk 入库，检索链路结合 `pgvector` 语义召回、`pg_trgm` 词法召回与 reranker 重排。
- **Human-in-the-loop 修订闭环**：任务建议确认后，进入 `Planner -> Retriever -> Reviewer -> Editor` 编排，产出 `citations`、`review_summary` 和 `diff_preview`，审批通过后异步执行。
- **版本化结果管理**：原始上传文件按内容寻址存储，支持下载原文件、查看修订结果、导出当前版本。
- **可观测与可修复**：任务事件审计、结构化日志、本地联调脚本，以及 `resource-reindex` / `inline-material-repair` 等维护 CLI 已内置。

## 工作流总览

```mermaid
flowchart LR
    U[用户] --> A[助手会话]
    A --> B[上传文件 / 对话提问]
    B --> C[解析与导入资源库]
    C --> D[当前文件直读 + grounded retrieval]
    D --> E[流式回复 / 任务建议]
    E --> F[确认任务]
    F --> G[Planner]
    G --> H[Retriever]
    H --> I[Reviewer]
    I --> J[Editor]
    J --> K[审批中心]
    K --> L[Worker + Executor]
    L --> M[新版本 / 导出结果]
```

## 核心页面

| 路由 | 说明 |
| --- | --- |
| `/` | 助手主工作台，支持流式聊天、上传文件、会话历史、任务建议 |
| `/resources` | 资源库列表 |
| `/resources/:id` | 当前版本查看、资源检索、结果导出 |
| `/tasks/:id` | 任务时间线、引用证据、审阅摘要、diff 预览、执行结果 |
| `/approvals` | 待审批任务队列 |

## 技术栈

- **前端**：Next.js 14、React 18、TypeScript、Vitest
- **后端**：Go 1.26、Hertz、pgx、CloudWeGo Eino
- **检索与 AI**：SiliconFlow（LLM / Embedding / Reranker）、`pgvector`、`pg_trgm`
- **文档处理**：文本直通解析、Apache Tika（可选）
- **存储与运行**：PostgreSQL 16、Docker Compose、本地内容寻址文件存储
- **CI/CD**：GitHub Actions、区域 OCI registry、SSH + Docker Compose 部署

## 快速开始

### 前置条件

- Go `1.26+`
- Node.js `20`（本地至少 `18+`）
- Docker / Docker Compose
- 可访问的 PostgreSQL（本地脚本默认使用仓库自带 `docker-compose.yml`）
- SiliconFlow API Key

### 推荐启动方式

仓库已经内置一套 PowerShell 本地联调脚本，会统一拉起 `postgres + tika + server + web`。

```powershell
git clone https://github.com/87hujih/Agent_Project.git
cd Agent_Project

Copy-Item .env.example .env

cd apps/web
npm ci
cd ../..

pwsh -File scripts/dev/start-local.ps1
```

启动后默认地址：

- Web: `http://127.0.0.1:3000`
- Server: `http://127.0.0.1:18080`
- Tika: `http://127.0.0.1:9998`

常用联调命令：

```powershell
pwsh -File scripts/dev/status-local.ps1
pwsh -File scripts/dev/stop-local.ps1
```

> 后端启动时会尽力导入 `demo-data/documents/` 下的示例文档，缺失示例数据不会阻塞服务启动。

## 环境变量

完整模板见根目录 [`.env.example`](./.env.example)。常用项如下：

| 变量 | 说明 | 默认 / 示例 |
| --- | --- | --- |
| `DATABASE_URL` | PostgreSQL 连接串，服务启动必填 | `postgres://.../agent_project?sslmode=disable` |
| `SILICONFLOW_API_KEY` | SiliconFlow API Key，真实模型调用必填 | `sk-...` |
| `LLM_MODEL` | 对话与任务编排模型 | `.env.example` 默认 `Pro/deepseek-ai/DeepSeek-V3.2` |
| `EMBEDDING_MODEL` | Embedding 模型 | `Qwen/Qwen3-Embedding-8B` |
| `RERANKER_MODEL` | Reranker 模型 | `Qwen/Qwen3-Reranker-8B` |
| `DOCUMENT_PARSER` | 文档解析模式，支持 `text` / `tika` | `text` |
| `TIKA_URL` | 启用 `tika` 时的服务地址 | `http://127.0.0.1:9998` |
| `UPLOAD_STORAGE_DIR` | 原始上传文件保存目录 | `data/uploads` |
| `UPLOAD_MAX_BYTES` | 单文件大小上限 | `20971520`（20 MB） |
| `NEXT_PUBLIC_API_URL` | 前端访问后端的基地址；本地脚本会自动注入 | `http://127.0.0.1:18080` |

补充说明：

- 配置优先级为：`环境变量 > .env > config/default.yaml > 代码内默认值`
- 默认 `DOCUMENT_PARSER=text` 时，仅支持上传 `.md` / `.txt`
- 当 `DOCUMENT_PARSER=tika` 且配置了 `TIKA_URL` 后，才会开放 `.doc` / `.docx` / `.pdf` / `.rtf` / `.odt`

## 一次完整演示

1. 打开首页助手，直接上传一份文档或先发一条描述需求的消息。
2. 对当前文件提问，例如“列出这份简历的项目经历”或“第一个项目做了什么”。
3. 当助手识别到明确的修订意图后，会在消息流里插入任务建议卡片。
4. 确认任务建议后，进入任务详情页，查看 `citations`、`review_summary` 和 `diff_preview`。
5. 在审批中心批准或拒绝任务。
6. 批准后由异步执行器生成新版本，并在资源详情页查看或导出修订结果。

## 常用开发命令

### 测试与构建

```bash
go test ./apps/server/...

cd apps/web
npm test -- --run
npm run lint
npm run build
```

### 维护命令

重建当前版本索引：

```bash
go run ./apps/server/cmd/resource-reindex --resource-id <resource_uuid>
go run ./apps/server/cmd/resource-reindex --missing-current
```

修复历史内联正文脏数据（默认 `dry-run`）：

```bash
go run ./apps/server/cmd/inline-material-repair --resource-id <resource_uuid>
go run ./apps/server/cmd/inline-material-repair --resource-id <resource_uuid> --apply
```

## 项目结构

```text
Agent_Project/
├── apps/
│   ├── server/                 # Go 后端：助手、任务编排、审批、执行、检索
│   │   ├── cmd/server/         # 服务入口
│   │   ├── cmd/resource-reindex/
│   │   └── internal/
│   │       ├── assistant/      # 助手会话、上下文、direct read、流式回复
│   │       ├── agent/          # planner / reviewer / editor / executor
│   │       ├── approval/       # 审批服务
│   │       ├── document/       # 文档解析与归一化
│   │       ├── job/            # 异步 worker
│   │       ├── knowledge/      # chunk、embedding、检索、reranker、indexer
│   │       ├── server/         # handlers / router / middleware
│   │       ├── storage/        # PostgreSQL + 本地文件存储
│   │       └── task/           # 任务服务、状态机、workflow
│   └── web/                    # Next.js 前端
├── config/default.yaml         # 默认运行配置
├── demo-data/documents/        # 本地演示文档
├── deploy/                     # 生产部署 compose 与 env 模板
├── docs/                       # 设计与链路说明
├── scripts/dev/                # 本地启动 / 状态 / 停止脚本
└── .github/workflows/          # CI / Release Images / Deploy Production
```

## CI / 部署

- `CI`：在 `pull_request` 和 `push main` 上运行
  - `go test ./apps/server/...`
  - Web `lint`
  - Web `build`
  - Server / Web Docker 镜像构建
- `Release Images`：在推送 `v*` tag 时触发
  - 构建 Server / Web 镜像并推送到区域 OCI registry
  - 镜像地址为 `${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/docreview-agent-{server,web}:<tag>`
- `Deploy Production`：手动输入 `image_tag` 触发部署或回滚
  - 通过 SSH 同步部署文件
  - 远端服务器从 registry 拉取指定版本并执行 Docker Compose 更新

生产环境注意事项：

- 前端必须配置 `NEXT_PUBLIC_API_URL` 为**浏览器可访问**的后端公网地址
- 不要把它写成 Docker service name 或服务器本机 `127.0.0.1`
- 部署前必须人工确认服务器 `.env` 中的 `DATABASE_URL` 是真实生产连接，而不是示例占位值
- 生产模板参见 [`deploy/docker-compose.prod.yml`](./deploy/docker-compose.prod.yml) 和 [`deploy/prod.env.example`](./deploy/prod.env.example)
