# Resume-Ready Enterprise Document AI MVP Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将当前仓库收敛成一个适合写入 AI 应用开发实习简历、可稳定演示的企业文档智能协作平台 MVP。

**Architecture:** 先打通“平台内受管文档”的完整闭环，而不是一开始就绑定飞书写回。前端继续使用 `Next.js` 承担任务工作台、资源页、审批页和任务详情页；后端使用 `Go + Eino + Hertz` 承担文档解析、检索、任务编排、审批和异步执行。外部连接器保留为下一阶段增强项，第一版执行结果先写回平台内的受管文档版本。

**Tech Stack:** Next.js、React、TypeScript、Vitest、Go、Eino、Hertz、PostgreSQL、pgvector、Docker Compose

---

## 范围收缩原则

这份计划故意收缩当前“大平台”设计，目标是优先交付一个真正能演示、能写简历、能经得起面试追问的 MVP。

- 第一版必须完成：`文档导入/浏览 -> 检索引用 -> 任务创建 -> 修订提案 -> 人工审批 -> 异步执行 -> 结果回看`
- 第一版不强依赖：`飞书真实写回`、`多连接器`、`复杂权限系统`、`多租户`
- 第一版保留扩展点：`connector contracts`、`ExecutionJob`、`Approval`、`TaskArtifact`

## 推荐对外项目定义

`企业文档智能协作平台：支持对企业文档进行检索问答、修订建议生成、人工审批和异步执行的任务型 AI 应用。`

## Task 1: 落地 MVP 数据模型与演示环境

**Files:**
- Create: `apps/server/db/migrations/001_mvp_init.sql`
- Create: `apps/server/internal/storage/postgres/db.go`
- Create: `apps/server/internal/storage/postgres/resources.go`
- Create: `apps/server/internal/storage/postgres/tasks.go`
- Create: `apps/server/internal/storage/postgres/task_steps.go`
- Create: `apps/server/internal/storage/postgres/task_artifacts.go`
- Create: `apps/server/internal/storage/postgres/approvals.go`
- Create: `apps/server/internal/storage/postgres/execution_jobs.go`
- Create: `docker-compose.yml`
- Create: `demo-data/documents/employee-handbook.md`
- Create: `demo-data/documents/security-policy.md`
- Test: `apps/server/internal/storage/postgres/db_test.go`

- [ ] **Step 1: 先写迁移 smoke test**

验证核心表存在：
- `resources`
- `resource_versions`
- `tasks`
- `task_steps`
- `task_artifacts`
- `approvals`
- `execution_jobs`

- [ ] **Step 2: 运行测试确认先失败**

Run: `go test ./internal/storage/postgres -v`
Expected: FAIL，提示 migration 或 DB 初始化未实现。

- [ ] **Step 3: 编写 MVP schema**

第一版只保留简历演示必要对象：
- `resources`
- `resource_versions`
- `tasks`
- `task_steps`
- `task_artifacts`
- `approvals`
- `execution_jobs`

- [ ] **Step 4: 准备可演示 demo 文档**

放入 2 到 3 份企业风格文档，保证后续检索、修订建议和审批展示有稳定素材。

- [ ] **Step 5: 启动数据库并验证迁移**

Run:
- `docker compose up -d`
- `go test ./internal/storage/postgres -v`

Expected: 数据库启动成功，迁移测试通过。

## Task 2: 实现资源导入、切片检索与 citation

**Files:**
- Create: `apps/server/internal/knowledge/ingest/service.go`
- Create: `apps/server/internal/knowledge/retriever/service.go`
- Create: `apps/server/internal/knowledge/citation/builder.go`
- Create: `apps/server/internal/server/handlers/resources.go`
- Modify: `apps/server/internal/server/router/router.go`
- Test: `apps/server/internal/knowledge/retriever/service_test.go`
- Test: `apps/server/internal/server/handlers/resources_test.go`

- [ ] **Step 1: 先写资源读取和 citation 测试**

测试至少覆盖：
- 资源列表可返回 demo 文档
- 资源详情可返回当前文档版本
- 检索结果可附带引用片段和位置

- [ ] **Step 2: 运行测试确认先失败**

Run: `go test ./internal/knowledge/retriever ./internal/server/handlers -v`
Expected: FAIL，提示 handler 或 retriever 未实现。

- [ ] **Step 3: 实现文档导入与切片**

第一版可以使用：
- 启动时导入 `demo-data/documents/*.md`
- 固定 chunk 策略
- 最小 metadata 结构

- [ ] **Step 4: 实现检索与 citation builder**

输出结果至少包含：
- `snippet`
- `resource_id`
- `section_title`
- `start_offset`
- `end_offset`

- [ ] **Step 5: 暴露资源 API**

至少提供：
- `GET /api/resources`
- `GET /api/resources/:id`
- `GET /api/resources/:id/search?q=...`

- [ ] **Step 6: 手工验证资源浏览链路**

Expected:
- 前端能看到资源列表
- 能查看资源详情
- 能看到带引用的检索结果

## Task 3: 实现任务模型与最小 Agent 工作流

**Files:**
- Create: `apps/server/internal/task/models/task.go`
- Create: `apps/server/internal/task/models/task_step.go`
- Create: `apps/server/internal/task/models/task_artifact.go`
- Create: `apps/server/internal/task/service/service.go`
- Create: `apps/server/internal/task/workflow/orchestrator.go`
- Create: `apps/server/internal/agent/planner/agent.go`
- Create: `apps/server/internal/agent/retriever/agent.go`
- Create: `apps/server/internal/agent/reviewer/agent.go`
- Create: `apps/server/internal/agent/editor/agent.go`
- Create: `apps/server/internal/server/handlers/tasks.go`
- Test: `apps/server/internal/task/workflow/orchestrator_test.go`
- Test: `apps/server/internal/server/handlers/tasks_test.go`

- [ ] **Step 1: 先写任务状态机测试**

覆盖最小状态流转：
- `pending -> planning`
- `planning -> retrieving`
- `retrieving -> drafting`
- `drafting -> awaiting_approval`

- [ ] **Step 2: 运行测试确认先失败**

Run: `go test ./internal/task/... ./internal/server/handlers -v`
Expected: FAIL，提示 task model 或 workflow 未实现。

- [ ] **Step 3: 实现任务主模型**

第一版只保留必要状态：
- `pending`
- `planning`
- `retrieving`
- `drafting`
- `awaiting_approval`
- `executing`
- `completed`
- `failed`

- [ ] **Step 4: 实现最小 Agent 链路**

串起：
- `Planner`：把用户要求整理成执行意图
- `Retriever`：检索引用证据
- `Reviewer`：给出问题点和修订方向
- `Editor`：输出结构化修订提案

- [ ] **Step 5: 落地结构化任务产物**

至少生成：
- `review_summary`
- `citations`
- `change_proposals`
- `diff_preview`

- [ ] **Step 6: 暴露任务 API**

至少提供：
- `POST /api/tasks`
- `GET /api/tasks`
- `GET /api/tasks/:id`
- `GET /api/tasks/:id/steps`
- `GET /api/tasks/:id/artifacts`

## Task 4: 完成前端工作台、任务详情页与审批页

**Files:**
- Create: `apps/web/components/task-create-form.tsx`
- Create: `apps/web/components/resource-list.tsx`
- Create: `apps/web/components/task-timeline.tsx`
- Create: `apps/web/components/diff-preview.tsx`
- Create: `apps/web/lib/api/client.ts`
- Modify: `apps/web/app/page.tsx`
- Modify: `apps/web/app/resources/page.tsx`
- Modify: `apps/web/app/tasks/new/page.tsx`
- Modify: `apps/web/app/tasks/[id]/page.tsx`
- Modify: `apps/web/app/approvals/page.tsx`
- Test: `apps/web/components/task-create-form.test.tsx`
- Test: `apps/web/components/diff-preview.test.tsx`

- [ ] **Step 1: 先写任务创建和 diff 组件测试**

验证：
- 能选择资源并提交任务
- diff 组件能渲染修改前后对照

- [ ] **Step 2: 运行前端测试确认先失败**

Run: `npm test -- --run`
Expected: FAIL，提示组件或页面未实现。

- [ ] **Step 3: 接入资源与任务 API**

前端至少能：
- 浏览资源
- 创建任务
- 查看任务详情
- 查看审批列表

- [ ] **Step 4: 完成 4 个 MVP 页面**

必须有：
- 首页工作台
- 资源页
- 任务创建页
- 任务详情/审批页

- [ ] **Step 5: 在任务详情页展示结构化产物**

至少展示：
- 当前状态
- 时间线
- citation
- review summary
- diff preview

- [ ] **Step 6: 运行前端验证**

Run:
- `npm run lint`
- `npm test -- --run`
- `npm run build`

Expected: 全部通过，页面可演示。

## Task 5: 实现审批、异步执行和结果沉淀

**Files:**
- Create: `apps/server/internal/approval/service.go`
- Create: `apps/server/internal/job/worker.go`
- Create: `apps/server/internal/server/handlers/approvals.go`
- Create: `apps/server/internal/agent/execution/agent.go`
- Modify: `apps/server/internal/task/workflow/orchestrator.go`
- Modify: `apps/server/internal/server/router/router.go`
- Test: `apps/server/internal/approval/service_test.go`
- Test: `apps/server/internal/job/worker_test.go`

- [ ] **Step 1: 先写审批与执行测试**

覆盖：
- 草案产出后自动生成审批记录
- 审批通过后创建执行 Job
- 审批拒绝后任务终止
- 执行成功后写入新的 `resource_version`

- [ ] **Step 2: 运行测试确认先失败**

Run: `go test ./internal/approval ./internal/job -v`
Expected: FAIL，提示审批服务或 worker 未实现。

- [ ] **Step 3: 实现审批 API**

至少提供：
- `GET /api/approvals`
- `POST /api/approvals/:id/approve`
- `POST /api/approvals/:id/reject`

- [ ] **Step 4: 实现平台内执行闭环**

第一版执行成功后的行为：
- 不写回外部系统
- 生成新的平台内文档版本
- 更新任务状态与最终产物

- [ ] **Step 5: 在前端沉淀执行结果**

用户必须能回看：
- 谁发起任务
- 提案是什么
- 审批结果是什么
- 执行后生成了哪个新版本

- [ ] **Step 6: 手工跑通完整演示链路**

演示顺序：
- 选择 demo 文档
- 创建“审阅与修订”任务
- 查看引用和 diff
- 审批通过
- 查看新版本与任务完成状态

## Task 6: 简历表达、README 和二期扩展边界

**Files:**
- Modify: `README.md`
- Modify: `docs/development.md`

- [ ] **Step 1: 在 README 固化项目定位**

强调：
- 任务型 AI 应用
- 文档智能协作
- 审批与异步执行
- 结构化产物与可追踪性

- [ ] **Step 2: 写清楚本地演示步骤**

至少包含：
- 数据库启动
- 服务端启动
- 前端启动
- Demo 流程

- [ ] **Step 3: 单独列出二期增强项**

明确放到二期：
- 飞书/Notion 连接器
- 真正的外部写回
- 更复杂权限模型
- 多 Agent 并行扩展

- [ ] **Step 4: 提炼简历要点**

最终简历强调点应固定为：
- `RAG + citation`
- `任务驱动 Agent workflow`
- `人工审批`
- `异步执行`
- `结构化 diff 提案`

## 完成标准

满足以下条件，才能把项目作为“实习简历项目”对外讲述：

- 用户能在 Web 平台中浏览企业文档资源
- 用户能基于指定文档创建“审阅与修订”任务
- 任务能产生 citation、review summary 和 diff preview
- 草案不会直接生效，而是进入审批
- 审批通过后系统会异步生成新文档版本
- 任务详情页能完整回看状态、步骤、产物和执行结果
- README 能指导面试官或同学本地复现完整演示
- 项目整体更像“业务闭环产品”，而不是“聊天 Demo”

## 推荐开发节奏

- 第 1 周：完成 `Task 1` 和 `Task 2`
- 第 2 周：完成 `Task 3`
- 第 3 周：完成 `Task 4`
- 第 4 周：完成 `Task 5` 和 `Task 6`

如果时间不足，绝不压缩审批与执行闭环；优先砍掉外部连接器，而不是砍掉完整业务链路。
