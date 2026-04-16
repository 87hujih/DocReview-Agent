# DocReview Agent

一个集成检索问答、修订建议生成、人工审批和异步执行的任务型 AI 应用。

## 核心功能

- **Citation-aware RAG**：基于 pgvector 的语义检索 + pg_trgm 词法检索 + Reranker 混合排序，chunk 级别引用追踪
- **任务驱动多 Agent 工作流**：Planner → Retriever → Reviewer → Editor 四步编排，每步产出结构化 artifact
- **Human-in-the-loop 审批**：Agent 产出的修订方案需人工审批确认后才执行，支持批准与拒绝
- **异步执行与文档版本管理**：channel-based worker pool，审批通过后异步将 diff 应用到文档，生成新版本
- **结构化 Diff 提案**：逐章节 original/revised 对比，每条修订附带 reason 和 citation 来源引用
- **全链路可观测性**：结构化 JSON 日志（slog）、HTTP 请求追踪（X-Request-ID）、任务事件审计（task_events 表）

## 本地启动

### 前置条件

- Go >= 1.26
- Node.js >= 18
- Docker（用于启动本地 PostgreSQL）
- 硅基流动 API Key（[SiliconFlow](https://siliconflow.cn)）

### 步骤

```bash
# 1. 克隆仓库
git clone https://github.com/87hujih/Agent_Project.git
cd Agent_Project

# 2. 配置环境变量
cp .env.example .env
# 编辑 .env，填入 SILICONFLOW_API_KEY 和 DATABASE_URL

# 3. 启动 PostgreSQL（pgvector/pgvector:pg16）
docker compose up -d

# 4. 安装前端依赖（首次或 lockfile 变化后执行）
cd apps/web && npm install && cd ../..

# 5. 统一启动本地 web + server
pwsh -File scripts/dev/start-local.ps1

# 6. 打开浏览器
# http://127.0.0.1:3000
```

> `start-local.ps1` 现在会额外托管本地 Apache Tika Server。
> 当 `.env` 使用 `DOCUMENT_PARSER=tika` 且 `TIKA_URL=http://127.0.0.1:9998` 时，
> 脚本会自动启动 / 复用 `docker compose` 里的 `tika` 服务；
> `stop-local.ps1` 会一并停止该服务。

<<<<<<< feat/task-event-chain-consistency
> 如果使用远程或已有的 PostgreSQL 实例，跳过步骤 3，直接在 `.env` 中配置 `DATABASE_URL` 即可。数据库需要预装 `pgvector` 和 `pg_trgm` 扩展，迁移脚本会自动创建。
>
> 当前前端首页已经切到“助手”入口：用户先在 `/` 自由聊天，再通过会话里的任务确认卡片创建任务。聊天内容已经接到真实模型回复；文件上传会真实进入后端会话并自动入资源库。当前仍是单人本地使用形态，暂未引入登录和多用户隔离。
>
> 本地联调当前统一约定为：
> - `web`: `http://127.0.0.1:3000`
> - `server`: `http://127.0.0.1:18080`
>
> 可用以下脚本辅助联调：
> - `pwsh -File scripts/dev/start-local.ps1`
> - `pwsh -File scripts/dev/status-local.ps1`
> - `pwsh -File scripts/dev/stop-local.ps1`

### 文档上传与解析

助手上传入口会按当前 `DOCUMENT_PARSER` 动态收紧支持格式：默认 `DOCUMENT_PARSER=text` 只接受 `.md` / `.txt`；配置 `DOCUMENT_PARSER=tika` 且提供 `TIKA_URL` 后，后端入口才接受 `.doc` / `.docx` / `.pdf` / `.rtf` / `.odt`。

```bash
DOCUMENT_PARSER=tika
TIKA_URL=http://127.0.0.1:9998
TIKA_TIMEOUT_MS=30000
```

原始上传文件会保存在 `UPLOAD_STORAGE_DIR`（默认 `data/uploads`），单文件上限由 `UPLOAD_MAX_BYTES` 控制（默认 20MB）。助手消息中的文件卡片会提供”下载原文件”入口；任务审批执行完成后，任务详情页会提供”查看修订结果”和”下载修订结果”，资源详情页默认以 Markdown / 纯文本展示并导出当前版本。

#### 原文件生命周期

上传的原始文件采用内容寻址存储（`<sha256-prefix>/<sha256>`），相同内容只保存一份物理文件：

- **默认行为**：删除会话或资源**不会**自动删除原文件，`uploaded_files` 元数据和物理文件均保留
- **清理策略**：如需清理无引用文件，需要手动操作（定时 GC CLI 为后续独立议题）
- **删除注意**：物理文件删除前必须确认无其他 `uploaded_files.storage_key` 引用同一内容（内容寻址共享）
- **云服务器部署**：`NEXT_PUBLIC_API_URL` 必须设置为浏览器可访问的公网后端地址，不能使用 Docker 容器 service name 或服务器本机的 `127.0.0.1`

目录导入链现在会优先按 `source_ref` 识别同一份文件；对于历史只按标题入库、尚未写 `source_ref` 的旧资源，会在再次导入时自动回填来源标识。若某个资源当前版本缺少 chunk，导入过程会重建当前版本索引，而不是继续静默跳过。

### 资源索引修复

如果资源详情可以打开，但 `/api/resources/:id/search?q=...` 长期返回空引用，通常表示该资源当前版本缺少 `resource_chunks` 索引。可以使用后端 CLI 重建当前版本索引：

```bash
# 修复单个资源当前版本
go run ./apps/server/cmd/resource-reindex --resource-id <resource_uuid>

# 扫描并修复所有“当前版本 chunk 数为 0”的资源
go run ./apps/server/cmd/resource-reindex --missing-current
```

该命令复用 `.env` / 环境变量中的 `DATABASE_URL`、`SILICONFLOW_API_KEY`、`EMBEDDING_MODEL` 和 `EMBEDDING_DIM`。命令只重建资源的**当前版本**索引，不回填历史版本，也不会修改资源正文。运行后可用资源搜索接口验证 citation 是否恢复。

## 演示走查

1. 打开首页「助手」页面（`/`），先用自然语言描述需求，例如“帮我检查员工手册里考勤相关条款有没有歧义”
2. 在会话中上传 `.md` / `.txt` 文档；如已配置 Tika，也可以上传 `.doc` / `.docx` / `.pdf` / `.rtf` / `.odt`
3. 上传完成后，在助手文件卡片中确认资源已入库，并点击“下载原文件”验证原始文件可取回
4. 助手识别到可落地的审阅 / 修改意图后，会在消息流里插入任务确认卡片；确认后跳转到对应「任务详情」页面
5. 在「任务详情」页面观察状态流转与产物：时间线、审阅摘要、结构化 diff 和引用证据
6. 工作流进入 `awaiting_approval` 后，打开「审批中心」页面（`/approvals`），查看待处理项并执行批准或拒绝
7. 返回任务详情页，等待任务进入 `completed`，点击“查看修订结果”打开资源当前版本
8. 在资源详情页查看最终修订正文，并点击“下载修订结果”导出 Markdown / 纯文本附件

## 验证命令

```bash
go test ./apps/server/internal/document/parser ./apps/server/internal/config ./apps/server/internal/knowledge/ingest ./apps/server/internal/assistant ./apps/server/internal/server/handlers ./apps/server/internal/server/router -v
go test ./apps/server/internal/server/handlers -run TestUploadApproveExecuteAndExportFlow -v

cd apps/web
npm test -- --run components/assistant/assistant-composer.test.tsx components/assistant/assistant-message-list.test.tsx lib/api/files.test.ts components/assistant/assistant-shell.test.tsx
npm test -- --run lib/api/resources.test.ts components/resource-version-viewer.test.tsx components/resource-detail-page.test.tsx components/task-detail-page.test.tsx components/task-detail-layout-css.test.tsx
npm test -- --run
npm run build
```

`TestUploadApproveExecuteAndExportFlow` 需要可访问的 PostgreSQL / pgvector 环境；未配置 `DATABASE_URL` 或数据库不可达时会按 smoke 测试约定跳过，但仍参与编译校验。

## 技术亮点

1. **Citation-aware RAG pipeline**：pgvector 语义检索 + pg_trgm 词法检索 + SiliconFlow Reranker 混合排序，chunk 级别引用追踪，检索结果携带任务内展示用 `citation_id` 和 `section_title`
2. **任务驱动多 Agent 工作流**：Planner → Retriever → Reviewer → Editor 四步编排，每步产出结构化 artifact（review_summary、citations、diff_preview）
3. **Human-in-the-loop 审批**：Agent 产出的修订方案不直接生效，需人工审批确认后才执行
4. **异步执行与文档版本管理**：channel-based worker pool，审批通过后异步将 diff 应用到文档，生成新版本，支持版本追溯
5. **结构化 Diff 提案**：逐章节 original/revised 对比，每条修订附带 reason 和 citation 来源引用
6. **全链路可观测性**：基于标准库 slog 的结构化 JSON 日志，HTTP 请求自动注入 X-Request-ID，任务全生命周期事件审计落库（task_events），日志失败不阻塞业务流程

## 目录结构

```
Agent_Project/
├── apps/
│   ├── server/                Go 后端（Hertz 框架）
│   │   ├── cmd/server/        入口
│   │   └── internal/
│   │       ├── agent/         LLM Agent（planner、reviewer、editor、executor）
│   │       ├── approval/      审批服务
│   │       ├── config/        配置加载（YAML + .env + 环境变量）
│   │       ├── job/           异步执行 worker pool
│   │       ├── knowledge/     知识层（chunker、embedder、ingest、retriever、reranker、citation）
│   │       ├── observability/ 可观测性（结构化日志、context 传播）
│   │       ├── server/        HTTP 层（handlers、router、middleware）
│   │       ├── storage/       数据持久化（postgres、migrations）
│   │       └── task/          任务模型、服务、工作流编排、事件记录
│   └── web/                   Next.js 14 前端
│       ├── app/               App Router 页面
│       ├── components/        UI 组件
│       └── lib/api/           API client 层
├── config/                    默认配置（default.yaml）
├── demo-data/documents/       演示文档
├── deploy/                    生产部署配置
└── .github/workflows/         CI/CD
```

## 许可证

MIT
=======
- 前端：`Next.js`、`React`、`TypeScript`
- 后端：`Go`、`Eino`、`Hertz`
- 数据库：`PostgreSQL`
- 向量能力：`pgvector`
- 部署：`Docker Compose`

## 核心对象

第一版围绕以下对象组织：

- `Resource`
- `ResourceVersion`
- `Task`
- `TaskStep`
- `TaskArtifact`
- `Approval`
- `ExecutionJob`

其中 `Task` 是主对象，聊天不是主对象。

## 页面形态

MVP 最终应至少包含以下页面：

- `首页工作台`
- `资源页`
- `任务创建页`
- `任务详情页`
- `审批页`

聊天页如果保留，也只作为辅助入口，不作为产品主入口。

## 后端能力边界

后端当前应优先完成：

- 资源导入与读取
- 文档切片、检索与 citation
- `Planner -> Retriever -> Reviewer -> Editor` 最小 Agent 工作流
- 审批状态流转
- 异步执行与新版本落库

## 演示流程

适合面试或作品展示的演示顺序：

1. 打开资源页，查看系统内 demo 文档
2. 选择一份文档发起“审阅与修订”任务
3. 在任务详情页查看 citation、审阅摘要和 diff 预览
4. 在审批页批准提案
5. 返回任务详情页查看异步执行完成和新版本生成结果

## 适合写进简历的亮点

- 基于 `RAG + citation` 实现文档检索与证据引用
- 设计了任务驱动的 Agent 工作流，而不是单纯聊天交互
- 实现了 `修订提案 -> 审批 -> 异步执行` 的业务闭环
- 使用结构化产物和 diff 预览提升结果可解释性
- 支持任务状态追踪、执行结果沉淀和版本回看

## 相关文档

- CI/CD 部署配置详见 `deploy/` 目录和 `.github/workflows/`
- Docker Compose 配置详见 `docker-compose.yml`

## 当前说明

根目录 `README.md` 保留为原始主说明文档。  
本文件只服务当前 MVP 收敛与简历表达，不覆盖原文档。

## CI/CD

当前仓库已规划为：

- `CI`: `pull_request` 与 `push main` 触发
- `CD`: `v*` tag 触发正式发布
- 镜像仓库：`GHCR`
- 部署方式：`SSH + Docker Compose`

镜像命名约定：

- `ghcr.io/87hujih/docreview-agent-server:<tag>`
- `ghcr.io/87hujih/docreview-agent-web:<tag>`

远程访问方式：

- 前端：`http://<server-ip>:3000`
- 后端：`http://<server-ip>:8080`
>>>>>>> feat/github-actions-cicd
