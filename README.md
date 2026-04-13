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

## 演示走查

1. 打开首页「助手」页面（`/`），先用自然语言描述需求，例如“帮我检查员工手册里考勤相关条款有没有歧义”
2. 如需明确使用哪份材料，可以到「资源库」页面（`/resources`）挑选资源，再通过「在助手中使用」把目标资源带回首页会话
3. 助手识别到可落地的审阅 / 修改意图后，会在消息流里插入任务确认卡片；确认后跳转到对应「任务详情」页面
4. 在「任务详情」页面观察状态流转与产物：左侧查看步骤时间线，右侧查看审阅摘要、结构化 diff 和引用证据
5. 工作流进入 `awaiting_approval` 后，打开「审批中心」页面（`/approvals`），查看待处理项并执行批准或拒绝
6. 返回任务详情页，确认状态推进并继续跟踪结果

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
