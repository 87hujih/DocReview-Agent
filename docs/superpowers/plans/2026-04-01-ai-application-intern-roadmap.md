# AI Application Intern Roadmap Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 6 到 8 周内把企业文档智能协作平台 MVP 收敛成一个适合 2027 届 AI 应用开发实习简历展示的高含金量项目。

**Architecture:** 采用 `AI 闭环优先，平台能力后置` 的冲刺策略。先打通 `RAG + Multi-Agent + structured output`，再接入 `Redis Streams + WebSocket` 实现任务流式状态，最后补齐 `Approval + ExecutionJob + ResourceVersion` 闭环和评测包装。

**Tech Stack:** Go、Hertz、Eino、PostgreSQL、pgvector、Redis 7+ Streams、WebSocket、Next.js、TypeScript、Docker Compose

---

### Task 1: Week 1 完成 RAG 最小闭环

**Files:**
- Create: `apps/server/internal/knowledge/ingest/service.go`
- Create: `apps/server/internal/knowledge/chunk/markdown.go`
- Create: `apps/server/internal/knowledge/retriever/service.go`
- Create: `apps/server/internal/knowledge/citation/builder.go`
- Create: `apps/server/internal/server/handlers/resources.go`
- Create: `apps/web/app/resources/page.tsx`
- Create: `apps/web/components/resource-search.tsx`
- Create: `apps/web/lib/api/resources.ts`
- Test: `apps/server/internal/knowledge/retriever/service_test.go`
- Test: `apps/server/internal/server/handlers/resources_test.go`

- [ ] **Step 1: 明确本阶段只支持平台内 demo 文档**

固定输入源为 `demo-data/documents/*.md`，不接飞书，不接外部连接器。

- [ ] **Step 2: 定义 chunk metadata**

至少包含：
- `resource_id`
- `resource_version_id`
- `section_title`
- `chunk_index`
- `start_offset`
- `end_offset`

- [ ] **Step 3: 先写 citation 检索测试**

验证：
- 能检索到相关 chunk
- 返回 snippet
- 返回 section 与 offset

- [ ] **Step 4: 跑后端测试确认先失败**

Run: `go test ./internal/knowledge/... ./internal/server/handlers -v`

- [ ] **Step 5: 实现 Markdown-aware chunking**

优先按标题、小节和固定长度切片，不做复杂语义切片。

- [ ] **Step 6: 接入 pgvector 检索并封装 citation builder**

返回结构化 payload 供前端直接展示。

- [ ] **Step 7: 暴露最小资源接口**

至少提供：
- `GET /api/resources`
- `GET /api/resources/:id`
- `GET /api/resources/:id/search?q=...`

- [ ] **Step 8: 完成资源搜索页面**

展示：
- 资源列表
- 文档详情
- 检索结果
- citation 片段

- [ ] **Step 9: 验证端到端闭环**

检查用户能在页面中搜索 demo 文档，并看到带引用的结果。

- [ ] **Step 10: 固化简历表达**

提炼：
- `citation-aware RAG`
- `Markdown-aware chunking`
- `pgvector retrieval`

### Task 2: Week 2-3 完成 Eino DAG 与结构化 Diff 提案

**Files:**
- Create: `apps/server/internal/agent/planner/agent.go`
- Create: `apps/server/internal/agent/retriever/agent.go`
- Create: `apps/server/internal/agent/reviewer/agent.go`
- Create: `apps/server/internal/agent/editor/agent.go`
- Create: `apps/server/internal/task/workflow/orchestrator.go`
- Create: `apps/server/internal/task/service/service.go`
- Create: `apps/server/internal/server/handlers/tasks.go`
- Create: `apps/web/app/tasks/new/page.tsx`
- Create: `apps/web/app/tasks/[id]/page.tsx`
- Create: `apps/web/components/diff-preview.tsx`
- Test: `apps/server/internal/task/workflow/orchestrator_test.go`
- Test: `apps/server/internal/server/handlers/tasks_test.go`

- [ ] **Step 1: 固定 Agent DAG，不做自由代理**

链路限定为：
- `Planner`
- `Retriever`
- `Reviewer`
- `Editor`

- [ ] **Step 2: 定义 Editor 强制 JSON 输出结构**

至少包含：
- `review_summary`
- `citations`
- `change_proposals`
- `diff_blocks`
- `confidence`
- `needs_human_confirmation`

- [ ] **Step 3: 先写结构化输出解析测试**

验证：
- 合法 JSON 可解析
- 缺字段会失败
- 可触发一次 repair/retry

- [ ] **Step 4: 跑任务工作流测试确认先失败**

Run: `go test ./internal/task/... ./internal/server/handlers -v`

- [ ] **Step 5: 实现 Planner 与 Retriever 节点**

前者负责整理任务意图，后者只负责取证，不产结论。

- [ ] **Step 6: 实现 Reviewer 与 Editor 节点**

Reviewer 负责审阅判断，Editor 负责结构化提案。

- [ ] **Step 7: 增加模型输出校验与最小修复**

实现：
- schema 校验
- JSON 反序列化
- 单次 repair prompt

- [ ] **Step 8: 暴露任务接口**

至少提供：
- `POST /api/tasks`
- `GET /api/tasks`
- `GET /api/tasks/:id`

- [ ] **Step 9: 完成任务创建页和详情页初版**

支持选择文档、输入要求、查看审阅摘要和 diff 预览。

- [ ] **Step 10: 验证端到端闭环**

检查用户能创建任务并看到结构化 AI 产物。

- [ ] **Step 11: 固化简历表达**

提炼：
- `Eino DAG orchestration`
- `structured JSON output`
- `Diff-ready proposal rendering`

### Task 3: Week 4 完成流式任务状态与实时同步

**Files:**
- Create: `apps/server/internal/task/models/task.go`
- Create: `apps/server/internal/task/models/task_step.go`
- Create: `apps/server/internal/task/models/task_artifact.go`
- Create: `apps/server/internal/stream/events.go`
- Create: `apps/server/internal/stream/redis_stream.go`
- Create: `apps/server/internal/server/handlers/ws.go`
- Create: `apps/web/lib/ws/client.ts`
- Create: `apps/web/components/task-timeline.tsx`
- Modify: `apps/web/app/tasks/[id]/page.tsx`
- Test: `apps/server/internal/stream/redis_stream_test.go`

- [ ] **Step 1: 收敛最小任务模型**

只保留：
- `Task`
- `TaskStep`
- `TaskArtifact`

- [ ] **Step 2: 设计 Redis Streams 事件类型**

至少定义：
- `task.created`
- `step.started`
- `llm.delta`
- `artifact.generated`
- `step.completed`
- `task.failed`

- [ ] **Step 3: 先写事件发布与消费测试**

验证：
- 事件能写入 stream
- 消费者能按顺序消费
- 可按任务过滤

- [ ] **Step 4: 跑流事件测试确认先失败**

Run: `go test ./internal/stream -v`

- [ ] **Step 5: 在 orchestrator 中发射步骤事件**

每个 Agent 节点开始、完成、失败都要发事件。

- [ ] **Step 6: 增加 WebSocket 网关**

从 stream 消费事件并推给订阅对应任务的前端连接。

- [ ] **Step 7: 完成任务详情页实时时间线**

展示：
- 当前状态
- 当前步骤
- 中间产物流式更新

- [ ] **Step 8: 实现断线补发策略**

前端带最近事件 ID，后端按 stream 偏移补发缺失事件。

- [ ] **Step 9: 验证端到端闭环**

检查用户创建任务后，页面能实时看到执行进度和中间产物。

- [ ] **Step 10: 固化简历表达**

提炼：
- `Redis Streams event bus`
- `WebSocket real-time task sync`
- `long-running LLM workflow observability`

### Task 4: Week 5-6 完成审批与异步执行闭环

**Files:**
- Create: `apps/server/internal/approval/service.go`
- Create: `apps/server/internal/job/worker.go`
- Create: `apps/server/internal/job/queue/redis_stream.go`
- Create: `apps/server/internal/server/handlers/approvals.go`
- Create: `apps/server/internal/agent/execution/agent.go`
- Create: `apps/web/app/approvals/page.tsx`
- Modify: `apps/server/internal/task/workflow/orchestrator.go`
- Modify: `apps/web/app/tasks/[id]/page.tsx`
- Test: `apps/server/internal/approval/service_test.go`
- Test: `apps/server/internal/job/worker_test.go`

- [ ] **Step 1: 定义审批与执行对象**

至少包含：
- `Approval`
- `ExecutionJob`
- `ResourceVersion`

- [ ] **Step 2: 先写审批与异步执行测试**

验证：
- draft 生成后进入 `awaiting_approval`
- 审批通过后创建 job
- 审批拒绝后任务终止
- 执行成功后生成新版本

- [ ] **Step 3: 跑审批与 Worker 测试确认先失败**

Run: `go test ./internal/approval ./internal/job -v`

- [ ] **Step 4: 实现审批接口**

至少提供：
- `GET /api/approvals`
- `POST /api/approvals/:id/approve`
- `POST /api/approvals/:id/reject`

- [ ] **Step 5: 使用 Redis Streams consumer group 实现执行队列**

要求支持：
- 消费组消费
- pending 可观测
- 超时接管
- 幂等控制

- [ ] **Step 6: 实现平台内版本写回**

第一版只生成新的 `resource_version`，不做真实外部写回。

- [ ] **Step 7: 在前端展示审批和执行结果**

用户能回看：
- 提案内容
- 审批状态
- 执行状态
- 新版本结果

- [ ] **Step 8: 通过 WebSocket 回推执行结果**

执行完成或失败都应更新任务详情页状态。

- [ ] **Step 9: 验证端到端闭环**

检查用户能从提案查看、审批通过、执行完成一路走通。

- [ ] **Step 10: 固化简历表达**

提炼：
- `proposal-approval-execution pipeline`
- `consumer group retry and claim`
- `versioned document write-back`

### Task 5: Week 7-8 完成评测、审计与简历包装

**Files:**
- Modify: `README.md`
- Modify: `docs/development.md`
- Create: `demo-data/evals/review-cases.json`
- Create: `apps/server/internal/audit/service.go`
- Create: `apps/server/internal/eval/pipeline_test.go`
- Create: `docker-compose.yml`

- [ ] **Step 1: 固定 demo 数据和评测 case**

准备 10 到 20 个固定问题和修订场景。

- [ ] **Step 2: 定义评测指标**

至少记录：
- citation 命中质量
- JSON 解析成功率
- diff 可用率
- 任务完成率

- [ ] **Step 3: 先写最小评测脚本或测试**

Run: `go test ./internal/eval -v`

- [ ] **Step 4: 增加基础审计事件**

至少记录：
- 任务创建
- 步骤推进
- 提案生成
- 审批结果
- 执行结果

- [ ] **Step 5: 固化本地启动方式**

通过 `Docker Compose` 启动 PostgreSQL、Redis 和必要依赖。

- [ ] **Step 6: 重写 README 的对外表达**

重点强调：
- `RAG + citation`
- `Multi-Agent DAG`
- `Redis Streams + WebSocket`
- `Approval + async execution`

- [ ] **Step 7: 编写标准 demo 路径**

固定：
- 进入资源页
- 检索文档
- 创建任务
- 查看 diff
- 审批
- 回看新版本

- [ ] **Step 8: 全链路回归验证**

至少检查：
- `go test ./...`
- `npm run build`
- 手工跑完整 demo

- [ ] **Step 9: 固化简历表达**

提炼：
- `AI workflow evaluation`
- `auditable agent pipeline`
- `demo-ready enterprise AI application`
