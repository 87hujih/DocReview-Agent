# Phase 1 RAG Minimum Closed Loop Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在一周内完成 `企业文档智能协作平台 MVP` 的 Phase 1，实现从 demo 文档导入、Markdown 切片、向量化、pgvector 检索到前端 citation 展示的最小闭环。

**Architecture:** Phase 1 只做平台内受管文档，不接飞书，不接真实外部连接器。后端最小闭环为 `demo-data -> ingest -> chunk -> embed -> pgvector -> retrieve -> citation API`，前端只保留 `资源页` 作为可演示入口。

**Tech Stack:** Go、Hertz、PostgreSQL、pgvector、Next.js、TypeScript、Docker Compose

---

## Definition Of Done

完成 Phase 1 后，系统至少满足：

- 能读取 `demo-data/documents/*.md`
- 能把 Markdown 文档切成带章节语义的 chunk
- 能为 chunk 生成 embedding 并写入 `pgvector`
- 能通过资源 API 检索相关 chunk
- 检索结果能返回 `snippet + section_title + offsets`
- 前端资源页能展示资源列表、文档详情、搜索结果和 citation 片段

## Out Of Scope

本阶段明确不做：

- 飞书 / MCP 连接器
- Multi-Agent 工作流
- 结构化 diff 提案
- Redis Streams / WebSocket
- 审批与异步执行
- 权限系统和多租户

## Task 1: 最小脚手架与本地依赖

**Files:**
- Create: `docker-compose.yml`
- Create: `apps/server/cmd/server/main.go`
- Create: `apps/server/internal/config/config.go`
- Create: `apps/server/internal/server/router/router.go`
- Create: `apps/server/internal/server/handlers/health.go`
- Create: `apps/web/package.json`
- Create: `apps/web/tsconfig.json`
- Create: `apps/web/next.config.ts`
- Create: `apps/web/app/layout.tsx`
- Create: `apps/web/app/page.tsx`

- [ ] **Step 1: 固定 Phase 1 的运行边界**

约束：
- 只需要一个后端服务
- 只需要一个前端应用
- 只需要 PostgreSQL + pgvector
- 不要在这阶段引入 Redis

- [ ] **Step 2: 启动本地 PostgreSQL + pgvector**

`docker-compose.yml` 至少提供：
- `postgres`
- `pgvector extension`
- 持久化 volume

- [ ] **Step 3: 建立最小后端骨架**

至少跑通：
- Hertz server 启动
- `/healthz` 健康检查
- 基础配置加载

- [ ] **Step 4: 建立最小前端骨架**

至少跑通：
- Next.js 应用启动
- 首页可访问
- 资源页路由预留

- [ ] **Step 5: 记录当前仓库的模块边界处理**

当前仓库已经存在根目录 `go.mod`，Phase 1 开始前先固定 Go 模块边界，避免后续服务端包路径混乱。

- [ ] **Step 6: 验证脚手架**

Run:
- `docker compose up -d`
- `go run ./apps/server/cmd/server`
- `npm run dev`

Expected:
- 数据库可连接
- 后端健康检查可访问
- 前端首页可打开

## Task 2: 资源表、版本表与向量表

**Files:**
- Create: `apps/server/db/migrations/001_phase1_resources.sql`
- Create: `apps/server/internal/storage/postgres/db.go`
- Create: `apps/server/internal/storage/postgres/resources.go`
- Create: `apps/server/internal/storage/postgres/resource_versions.go`
- Create: `apps/server/internal/storage/postgres/document_chunks.go`
- Test: `apps/server/internal/storage/postgres/db_test.go`

- [ ] **Step 1: 先写 migration smoke test**

验证下列表存在：
- `resources`
- `resource_versions`
- `document_chunks`

- [ ] **Step 2: 设计最小 Phase 1 表结构**

`resources` 至少包含：
- `id`
- `title`
- `resource_type`
- `status`

`resource_versions` 至少包含：
- `id`
- `resource_id`
- `version_number`
- `raw_content`

`document_chunks` 至少包含：
- `id`
- `resource_id`
- `resource_version_id`
- `chunk_index`
- `section_title`
- `content`
- `start_offset`
- `end_offset`
- `embedding`

- [ ] **Step 3: 跑数据库测试确认先失败**

Run: `go test ./apps/server/internal/storage/postgres -v`

- [ ] **Step 4: 实现数据库连接与迁移初始化**

要求：
- 服务启动时能初始化连接
- 可自动执行 migration 或明确提供 migration 命令

- [ ] **Step 5: 实现最小资源与 chunk 存储层**

支持：
- 创建资源
- 创建版本
- 批量写入 chunk
- 根据资源查询当前版本和 chunk

- [ ] **Step 6: 验证数据层**

Expected:
- demo 文档可落到资源表
- chunk 可写入向量表

## Task 3: Demo 文档导入、Markdown 切片与向量化

**Files:**
- Create: `demo-data/documents/employee-handbook.md`
- Create: `demo-data/documents/security-policy.md`
- Create: `apps/server/internal/knowledge/ingest/service.go`
- Create: `apps/server/internal/knowledge/chunk/markdown.go`
- Create: `apps/server/internal/knowledge/embed/service.go`
- Test: `apps/server/internal/knowledge/chunk/markdown_test.go`
- Test: `apps/server/internal/knowledge/ingest/service_test.go`

- [ ] **Step 1: 准备 2 到 3 份稳定的企业风格 demo 文档**

要求：
- 有明确章节
- 有适合检索的问题点
- 有后续可用于修订的内容

- [ ] **Step 2: 定义 chunk metadata**

至少包含：
- `resource_id`
- `resource_version_id`
- `section_title`
- `chunk_index`
- `start_offset`
- `end_offset`

- [ ] **Step 3: 先写 Markdown chunking 测试**

验证：
- 能按标题切分
- 超长段落会继续按固定长度拆分
- offset 计算稳定

- [ ] **Step 4: 跑 chunking 测试确认先失败**

Run: `go test ./apps/server/internal/knowledge/chunk -v`

- [ ] **Step 5: 实现 Markdown-aware chunking**

原则：
- 优先保留章节语义
- 再做长度兜底
- 不做复杂语义切片

- [ ] **Step 6: 抽象 embedding service**

要求：
- 与具体模型供应商解耦
- 支持单文档批量生成向量
- 明确向量维度

- [ ] **Step 7: 实现 demo 文档导入流程**

流程：
- 读取 `demo-data/documents/*.md`
- 创建资源和版本
- 切片
- 调 embedding
- 写入 chunk 与向量

- [ ] **Step 8: 验证导入链路**

Expected:
- 服务启动或显式导入命令后，数据库中能看到 demo 资源和 chunk

## Task 4: 检索服务与 citation builder

**Files:**
- Create: `apps/server/internal/knowledge/retriever/service.go`
- Create: `apps/server/internal/knowledge/citation/builder.go`
- Test: `apps/server/internal/knowledge/retriever/service_test.go`

- [ ] **Step 1: 先写 citation 检索测试**

验证：
- 能检索到相关 chunk
- 返回 snippet
- 返回 section title
- 返回 start/end offsets

- [ ] **Step 2: 跑 retriever 测试确认先失败**

Run: `go test ./apps/server/internal/knowledge/retriever -v`

- [ ] **Step 3: 实现 query embedding**

要求：
- 用户 query 先向量化
- 与 chunk embedding 做相似度搜索

- [ ] **Step 4: 实现 pgvector Top-K 检索**

要求：
- 支持按 `resource_id` 过滤
- 支持可调 `top_k`

- [ ] **Step 5: 实现 citation builder**

输出至少包含：
- `snippet`
- `resource_id`
- `resource_version_id`
- `section_title`
- `start_offset`
- `end_offset`
- `score`

- [ ] **Step 6: 验证检索链路**

Expected:
- 对 demo 文档输入问题时，能稳定返回可解释的引用片段

## Task 5: 资源 API 与资源页

**Files:**
- Create: `apps/server/internal/server/handlers/resources.go`
- Modify: `apps/server/internal/server/router/router.go`
- Create: `apps/web/app/resources/page.tsx`
- Create: `apps/web/components/resource-list.tsx`
- Create: `apps/web/components/resource-search.tsx`
- Create: `apps/web/lib/api/resources.ts`
- Test: `apps/server/internal/server/handlers/resources_test.go`

- [ ] **Step 1: 先写资源接口测试**

验证：
- 资源列表可返回 demo 文档
- 资源详情可返回当前版本摘要
- 搜索接口可返回 citation 列表

- [ ] **Step 2: 跑 handler 测试确认先失败**

Run: `go test ./apps/server/internal/server/handlers -v`

- [ ] **Step 3: 实现最小资源接口**

至少提供：
- `GET /api/resources`
- `GET /api/resources/:id`
- `GET /api/resources/:id/search?q=...`

- [ ] **Step 4: 完成资源页 UI**

至少展示：
- 资源列表
- 文档详情
- 检索输入框
- citation 搜索结果

- [ ] **Step 5: 实现前后端联调**

要求：
- 前端可直接调用资源接口
- 搜索结果可渲染 citation 片段

- [ ] **Step 6: 验证端到端页面闭环**

Expected:
- 用户能在页面中浏览 demo 文档并完成带 citation 的搜索

## Task 6: Phase 1 验收、回归与简历术语

**Files:**
- Modify: `README.md`
- Modify: `tasks/todo.md`

- [ ] **Step 1: 固定 Phase 1 验收用例**

至少准备：
- 2 个搜索问题
- 1 个资源列表检查
- 1 个 citation 展示检查

- [ ] **Step 2: 跑 Phase 1 回归**

Run:
- `go test ./apps/server/internal/storage/postgres -v`
- `go test ./apps/server/internal/knowledge/... -v`
- `go test ./apps/server/internal/server/handlers -v`
- `npm run build`

- [ ] **Step 3: 手工走通演示路径**

路径：
- 打开资源页
- 查看 demo 文档
- 输入搜索问题
- 查看 citation 片段和来源位置

- [ ] **Step 4: 回写 README 的 Phase 1 演示方式**

至少写清：
- 如何启动数据库
- 如何启动后端
- 如何启动前端
- 如何验证 citation 搜索结果

- [ ] **Step 5: 固化简历表达**

提炼：
- `citation-aware RAG pipeline`
- `Markdown-aware chunking and vector indexing`
- `pgvector-based evidence retrieval`

## Recommended 5-Day Split

- Day 1: 完成 Task 1 和 Task 2，先把数据库、服务骨架和 migration 跑起来
- Day 2: 完成 Task 3 的文档导入和 Markdown 切片
- Day 3: 完成向量化、chunk 入库和检索服务
- Day 4: 完成 citation builder、资源 API 和接口测试
- Day 5: 完成资源页联调、回归验证和 README 演示文档

## Phase 1 最终交付物

- 一个可运行的后端服务
- 一个可访问的前端资源页
- 两到三份 demo 企业文档
- 一套稳定的 chunk + embedding + retrieval + citation 链路
- 一份可对外演示的 Phase 1 说明
