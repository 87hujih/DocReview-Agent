# Phase 1 Task 1 Scaffold Detailed Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在当前仓库中搭建 Phase 1 所需的最小可运行骨架，使本地 PostgreSQL + pgvector、Hertz 服务、Next.js 应用可以用统一命令启动并完成 smoke verification。

**Architecture:** 本任务只建立运行边界，不引入业务模型、migration 或检索逻辑。Go 端在 Phase 1 保持根目录单模块方案承载 `apps/server`，前端保持独立 `apps/web` Node 应用，数据库用 `docker-compose` 提供单个 pgvector-capable PostgreSQL 实例；Task 2 再接入 schema、migration 和数据访问层。

**Tech Stack:** Go 1.26、Hertz、PostgreSQL 16、pgvector、Docker Compose、Next.js、React、TypeScript、npm

---

## Current State Snapshot

- Root `go.mod` 已存在，但当前模块名是 `agent_project/apps/server`，与它实际位于仓库根目录的事实不一致。
- `apps/server` 和 `apps/web` 目录已经存在，但仍为空目录。
- `.gitignore` 已覆盖 `node_modules/`、`.next/`、`apps/server/bin/`、`.env*` 和日志文件，因此 Task 1 不需要补 ignore 规则。
- 当前仓库还没有 `docker-compose.yml`、后端启动入口或前端包管理元数据。

## Decisions Locked Before Execution

- Phase 1 保留根目录 `go.mod`，但把模块名改成 `agent_project`。
  - 原因：改动最小，并且保留 `go run ./apps/server/cmd/server` 这种从仓库根目录启动的方式。
- Task 1 不引入 `Redis`、`go.work`、多 Go module、migration runner 或业务数据模型。
- 现在就补 `apps/web/app/resources/page.tsx` 占位页面。
  - 原因：原 Task 1 已要求“资源页路由预留”，尽早固定路由边界可以避免 Task 5 再回头改目录结构。
- Compose 只负责提供“可用 pgvector 扩展能力的 PostgreSQL 实例”，真正的 `CREATE EXTENSION vector` 和表结构初始化留到 Task 2 migration。

## File Responsibility Map

- Modify: `go.mod`
  - 把 Go module 边界固定到仓库根目录，消除后续 `apps/server` 包路径歧义。
- Create: `docker-compose.yml`
  - 提供单个本地 PostgreSQL 服务、端口映射、healthcheck 和持久化 volume。
- Create: `apps/server/cmd/server/main.go`
  - 后端启动入口，负责加载配置、组装路由、启动 Hertz。
- Create: `apps/server/internal/config/config.go`
  - 管理环境变量配置和默认值。
- Create: `apps/server/internal/server/router/router.go`
  - 统一注册当前阶段所需路由。
- Create: `apps/server/internal/server/handlers/health.go`
  - 提供 `/healthz` 健康检查响应。
- Create: `apps/web/package.json`
  - 定义前端依赖和启动命令。
- Create: `apps/web/tsconfig.json`
  - 配置 TypeScript 与 Next.js 编译行为。
- Create: `apps/web/next.config.ts`
  - 保留最小 Next 配置。
- Create: `apps/web/app/layout.tsx`
  - 应用级 HTML 外壳。
- Create: `apps/web/app/page.tsx`
  - 最小首页，占位展示 Phase 1 入口信息。
- Create: `apps/web/app/resources/page.tsx`
  - 资源页占位路由，供 Task 5 扩展。
- Generated during execution, do not hand-edit unless necessary:
  - `apps/web/package-lock.json`
  - `apps/web/next-env.d.ts`

### Task 1: Normalize module boundary and runtime defaults

**Files:**
- Modify: `go.mod`
- Create: `apps/server/internal/config/config.go`

- [ ] **Step 1: 先读取当前根模块声明并记录问题**

Expected finding:
- `go.mod` 位于仓库根目录
- 当前模块路径却指向 `apps/server`
- 一旦开始创建 `apps/server/internal/...` 包，未来 import path 会出现理解成本和维护风险

- [ ] **Step 2: 把根模块名从 `agent_project/apps/server` 改成 `agent_project`**

Implementation notes:
- 保留 `go.mod` 在仓库根目录
- 本任务不要再创建 `apps/server/go.mod`
- 现有 Hertz 依赖先保留，不在这一阶段重构依赖层次

- [ ] **Step 3: 在 `apps/server/internal/config/config.go` 中定义后端运行配置**

Config struct 至少包含：
- `ServerPort string`
- `DatabaseURL string`

Default values:
- `ServerPort`: `8080`
- `DatabaseURL`: `postgres://postgres:postgres@localhost:5432/agent_project?sslmode=disable`

Behavior:
- 优先读取环境变量
- 环境变量不存在时回落到默认值
- 统一暴露一个 `Load()`，避免把配置解析散落到 `main.go`

- [ ] **Step 4: 先跑一次模块边界 smoke check**

Run: `go list ./...`

Expected:
- 命令执行成功
- 不应再出现 “根模块在仓库根目录，但路径却嵌到 `apps/server`” 的隐性歧义

- [ ] **Step 5: 在服务端 package 建好之后再执行依赖整理**

Run later in this task: `go mod tidy`

Reason:
- 如果在创建 package 前过早执行，容易把紧接着就会用到的依赖抖掉

### Task 2: Provision local PostgreSQL + pgvector with Docker Compose

**Files:**
- Create: `docker-compose.yml`

- [ ] **Step 1: 添加单个 `postgres` 服务，并使用带 pgvector 能力的 PostgreSQL 镜像**

Service requirements:
- 兼容 PostgreSQL 16
- 服务名保持为 `postgres`
- 端口映射：`5432:5432`
- environment:
  - `POSTGRES_DB=agent_project`
  - `POSTGRES_USER=postgres`
  - `POSTGRES_PASSWORD=postgres`

- [ ] **Step 2: 增加持久化 volume 和容器健康检查**

Compose requirements:
- 使用 named volume 保存数据库数据
- 使用 `pg_isready` 作为 `healthcheck`
- `restart: unless-stopped` 可选，但不要为 Task 1 引入额外复杂逻辑

- [ ] **Step 3: 明确把 migration 生命周期排除在 Task 1 之外**

Do not add in this task:
- schema SQL
- migration 容器
- Redis
- Admin GUI

Rationale:
- Task 1 只需要一个“可连接、可扩展”的数据库基础设施
- schema、`CREATE EXTENSION vector` 的真正落地应归到 Task 2

- [ ] **Step 4: 先只启动数据库服务**

Run: `docker compose up -d postgres`

Expected:
- `postgres` 容器启动成功
- `docker compose ps` 最终能看到 healthy 状态

- [ ] **Step 5: 验证镜像里确实具备 pgvector 能力**

Run:
- `docker compose exec postgres psql -U postgres -d agent_project -c "SELECT name FROM pg_available_extensions WHERE name = 'vector';"`

Expected:
- 返回 1 行 `vector`
- 如果没有结果，不要继续后面的后端工作，先更换镜像

- [ ] **Step 6: 验证基础数据库连通性**

Run:
- `docker compose exec postgres pg_isready -U postgres -d agent_project`

Expected:
- 输出包含 `accepting connections`

### Task 3: Build the minimal Hertz server scaffold

**Files:**
- Create: `apps/server/cmd/server/main.go`
- Create: `apps/server/internal/server/router/router.go`
- Create: `apps/server/internal/server/handlers/health.go`
- Modify: `apps/server/internal/config/config.go`

- [ ] **Step 1: 先实现最窄职责的 `health.go`**

Response shape:

```json
{
  "status": "ok",
  "service": "server"
}
```

Rules:
- 使用 HTTP 200
- Task 1 不做数据库 ping
- handler 保持无副作用

- [ ] **Step 2: 在 `router.go` 中只注册当前必须存在的路由**

Router responsibilities:
- 创建 Hertz engine
- 注册 `GET /healthz`
- 把路由装配逻辑和 `main.go` 分离

Avoid in Task 1:
- middleware 栈
- 还没定义好的业务 API group
- 复杂依赖注入

- [ ] **Step 3: 在 `main.go` 中完成配置和路由装配**

`main.go` should:
- 调用 `config.Load()`
- 创建 router
- 监听 `:` + `ServerPort`
- 对启动失败打印清晰错误并非零退出

- [ ] **Step 4: 先做编译级验证，再跑服务**

Run: `go test ./apps/server/...`

Expected:
- 包可以正常编译
- 没有测试文件是可以接受的，本阶段主要是利用 `go test` 做 compile smoke check

- [ ] **Step 5: 从仓库根目录启动后端服务**

Run: `go run ./apps/server/cmd/server`

Expected:
- 服务启动成功
- 默认监听 `http://localhost:8080`

- [ ] **Step 6: smoke test `/healthz`**

Run:
- `curl http://localhost:8080/healthz`

Expected response body:

```json
{"status":"ok","service":"server"}
```

### Task 4: Build the minimal Next.js app scaffold

**Files:**
- Create: `apps/web/package.json`
- Create: `apps/web/tsconfig.json`
- Create: `apps/web/next.config.ts`
- Create: `apps/web/app/layout.tsx`
- Create: `apps/web/app/page.tsx`
- Create: `apps/web/app/resources/page.tsx`

- [ ] **Step 1: 手写前端 `package.json`，不要用脚手架生成器**

`package.json` 至少包含 scripts:
- `dev`
- `build`
- `start`
- `lint`

Dependencies 至少包含：
- `next`
- `react`
- `react-dom`

Dev dependencies 至少包含：
- `typescript`
- `@types/node`
- `@types/react`
- `@types/react-dom`
- `eslint`
- `eslint-config-next`

Reason:
- 手写脚手架能保证文件集合和 Task 1 范围完全一致，不被生成器带出额外样板

- [ ] **Step 2: 添加最小 TypeScript 与 Next 配置**

`tsconfig.json`:
- 采用 Next.js 推荐设置
- 显式包含 `app/**/*`
- 显式包含生成文件 `next-env.d.ts`

`next.config.ts`:
- 导出空或近乎空的配置对象
- 不要在 Task 1 打开实验性选项

- [ ] **Step 3: 在 `layout.tsx` 中定义共享外壳**

Layout requirements:
- 渲染 `html` 和 `body`
- 设置一个和 Phase 1 对齐的标题与描述
- 样式保持最小化，避免现在就引入额外 `styles/` 目录

- [ ] **Step 4: 实现首页 `page.tsx`**

Homepage content should communicate:
- 当前是 Phase 1 MVP scaffold
- 后端健康检查地址
- 资源页路径已经预留

- [ ] **Step 5: 立即补上 `resources` 占位页**

`apps/web/app/resources/page.tsx` should:
- 渲染简单标题
- 明确说明完整资源浏览器会在 Task 5 完成
- 让后续前端联调时不再改动路由边界

- [ ] **Step 6: 安装前端依赖**

Run: `npm install`

Workdir: `apps/web`

Expected:
- 依赖安装成功
- 生成 `package-lock.json`
- `next-env.d.ts` 可能会在首次 `dev` 或 `build` 时自动生成

- [ ] **Step 7: 启动前端开发服务**

Run: `npm run dev`

Workdir: `apps/web`

Expected:
- Next dev server 在 `http://localhost:3000` 启动成功

- [ ] **Step 8: smoke test 两个初始路由**

Open:
- `http://localhost:3000/`
- `http://localhost:3000/resources`

Expected:
- 首页正常渲染
- 资源页占位页面正常渲染
- 不出现 hydration 或 TypeScript 编译错误

- [ ] **Step 9: 补一个生产构建 smoke check**

Run: `npm run build`

Workdir: `apps/web`

Expected:
- 生产构建通过
- `.next` 产物继续由 `.gitignore` 忽略

### Task 5: Run the full Task 1 verification checklist

**Files:**
- No new source files
- 只在需要时补充任务记录，不在本任务里修改 README

- [ ] **Step 1: 启动数据库**

Run: `docker compose up -d`

Expected:
- `postgres` 处于 healthy 状态

- [ ] **Step 2: 在一个终端启动后端**

Run: `go run ./apps/server/cmd/server`

Expected:
- 进程保持存活
- `/healthz` 可访问

- [ ] **Step 3: 在另一个终端启动前端**

Run: `npm run dev`

Workdir: `apps/web`

Expected:
- 首页和 `/resources` 都可访问

- [ ] **Step 4: 对照 Task 1 验收标准做三项确认**

Checklist:
- 数据库可连接
- 后端健康检查返回 200 JSON
- 前端首页和资源占位页可打开

- [ ] **Step 5: 在离开 Task 1 前记录会影响 Task 2 的固定信息**

至少记录：
- 最终确认的根 Go module 策略
- Compose 中确定下来的服务名和数据库凭证
- 前端启动与构建命令
- 如果存在阻塞 Task 2 的问题，明确写出来，不要把风险带入下一个任务

## Risks and Guardrails

- 如果 `go list ./...` 或 `go test ./apps/server/...` 出现 import path 混乱，立即回到根模块命名处理，不要带着错误边界继续加文件。
- 如果 `docker compose` 显示容器启动但迟迟不 healthy，先检查本机 `5432` 端口冲突和 `docker compose logs postgres`，不要先入为主地怀疑后端代码。
- 如果 `npm run dev` 报 `next-env.d.ts` 缺失，优先让 Next 自动生成，再决定是否需要显式纳入版本管理。
- `/healthz` 在 Task 1 不要绑定数据库检查，否则会把 Task 1 的 smoke verification 和 Task 2 的数据层实现耦合在一起。

## Definition of Done for This Task

Task 1 只在以下条件全部满足时才算完成：

- `docker compose up -d` 能拉起 healthy 的 `postgres`
- `go run ./apps/server/cmd/server` 能从仓库根目录成功启动
- `curl http://localhost:8080/healthz` 返回预期 JSON
- `apps/web` 下的 `npm run dev` 能提供 `/` 与 `/resources`
- `apps/web` 下的 `npm run build` 能成功完成
- Go 模块边界已被固定，Task 2 不会再遇到 import 路径歧义
