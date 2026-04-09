# DocReview Agent

一个支持企业文档检索问答、修订建议生成、人工审批和异步执行的任务型 AI 应用。

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

# 4. 启动后端（迁移在启动时自动执行）
cd apps/server && go run ./cmd/server

# 5. 新开终端，启动前端
cd apps/web && npm install && npm run dev

# 6. 打开浏览器
# http://localhost:3000
```

> 如果使用远程或已有的 PostgreSQL 实例，跳过步骤 3，直接在 `.env` 中配置 `DATABASE_URL` 即可。数据库需要预装 `pgvector` 和 `pg_trgm` 扩展，迁移脚本会自动创建。

## 演示走查

1. 打开「资源库」页面（`/resources`），浏览已导入的企业文档（employee-handbook、security-policy）
2. 选择一份文档，点击「创建修订任务」，输入修订要求（如"检查考勤相关条款是否有歧义"）
3. 系统自动执行 Agent 工作流，在「任务详情」页面观察实时状态流转：`pending → planning → retrieving → reviewing → drafting → awaiting_approval`
4. 工作流完成后，打开「审批中心」页面（`/approvals`），查看修订提案的结构化 diff 预览，点击「批准」
5. 返回任务详情页，确认状态变为 `completed`，新文档版本已生成

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