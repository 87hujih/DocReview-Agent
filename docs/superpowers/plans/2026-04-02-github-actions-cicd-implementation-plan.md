# GitHub Actions CI/CD Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为当前项目落地一套可运行的 `GitHub Actions` CI/CD 流水线，实现 `CI 校验 -> GHCR 镜像发布 -> SSH 到单台远程服务器部署 web/server 容器 -> 手动回滚历史 tag` 的最小闭环。

**Architecture:** 先确保仓库具备真实 GitHub 运行前提，再补齐应用镜像构建与远程部署资产，最后接入两条 workflow：`ci.yml` 负责持续集成，`release-deploy.yml` 负责版本发布与部署。远程服务器只运行 `web` 和 `server` 两个容器，数据库与反向代理不纳入本计划。

**Tech Stack:** GitHub Actions、GHCR、Docker、Docker Compose、SSH、Go、Next.js、PowerShell、POSIX shell

---

## Current State Snapshot

- 当前工作区没有 `.git` 目录，因此还不具备真实触发 `GitHub Actions` 与 `tag release` 的前提。
- 当前仓库没有 `.github/workflows/`、`Dockerfile`、`.dockerignore`、生产部署编排文件和部署脚本。
- 当前仓库的 `apps/server` 与 `apps/web` 目录为空；本计划默认先完成 [2026-04-02-phase1-task1-scaffold-execution-plan.md](/G:/gofile/Agent_Project/docs/superpowers/plans/2026-04-02-phase1-task1-scaffold-execution-plan.md) 中的最小脚手架，再继续 CI/CD 实施。
- 远程目标服务器已经安装 `Docker` 与 `docker compose`，但本计划不负责创建数据库、不负责域名、HTTPS 或反向代理。

## File Responsibility Map

- Create: `.dockerignore`
  - 为服务端镜像的仓库根构建上下文减小体积，排除无关文件。
- Create: `apps/server/Dockerfile`
  - 构建并运行后端服务镜像。
- Create: `apps/web/Dockerfile`
  - 构建并运行前端服务镜像。
- Create: `apps/web/.dockerignore`
  - 缩小前端镜像构建上下文。
- Modify: `apps/web/next.config.ts`
  - 打开 `standalone` 输出，方便生产容器运行。
- Create: `deploy/docker-compose.prod.yml`
  - 作为远程服务器的生产编排模板，只运行 `web` 和 `server` 两个镜像。
- Create: `deploy/prod.env.example`
  - 记录远程 `.env` 所需的最小配置项。
- Create: `scripts/deploy/remote-deploy.sh`
  - 远程部署脚本，负责安全更新 `IMAGE_TAG` 并执行 `docker compose pull/up -d`。
- Create: `.github/workflows/ci.yml`
  - `pull_request` 与 `push main` 的持续集成校验。
- Create: `.github/workflows/release-deploy.yml`
  - `v*` tag 与手动回滚的发布部署工作流。
- Create: `docs/deployment.md`
  - 记录服务器初始化、GitHub Secrets 和发布/回滚步骤。
- Modify: `README.md`
  - 增加 CI/CD 入口说明与发布方式。
- Modify: `tasks/todo.md`
  - 跟踪 CI/CD 实施进度与验收结果。

## Precondition Gate

开始本计划前，必须同时满足：

- 已完成 [2026-04-02-phase1-task1-scaffold-execution-plan.md](/G:/gofile/Agent_Project/docs/superpowers/plans/2026-04-02-phase1-task1-scaffold-execution-plan.md) 的最小脚手架
- 仓库已经是实际的 Git 仓库
- 仓库已推送到 GitHub，并具备可配置 `Actions` 与 `Secrets` 的远程仓库

如果任一条件未满足，不要直接写 workflow，先补齐前置条件。

### Task 1: Establish repository and scaffold prerequisites

**Files:**
- No source file changes unless prerequisites are missing
- Reference: `docs/superpowers/plans/2026-04-02-phase1-task1-scaffold-execution-plan.md`

- [ ] **Step 1: 确认当前目录已是 Git 仓库**

Run: `git rev-parse --is-inside-work-tree`

Expected:
- 输出 `true`

If not:
- 执行 `git init -b main`
- 创建 GitHub 仓库
- 执行 `git remote add origin <github-repo-url>`

- [ ] **Step 2: 确认已存在 GitHub 远程**

Run: `git remote -v`

Expected:
- 至少存在一个 `origin`
- `origin` 指向 GitHub 仓库 URL

- [ ] **Step 3: 确认 Task 1 脚手架文件已经存在**

Verify these paths exist:
- `apps/server/cmd/server/main.go`
- `apps/server/internal/config/config.go`
- `apps/server/internal/server/router/router.go`
- `apps/server/internal/server/handlers/health.go`
- `apps/web/package.json`
- `apps/web/next.config.ts`
- `apps/web/app/layout.tsx`
- `apps/web/app/page.tsx`

- [ ] **Step 4: 运行脚手架 smoke check**

Run:
- `go test ./apps/server/...`
- `npm install`
- `npm run build`

Workdir for npm commands: `apps/web`

Expected:
- 后端至少能完成编译级校验
- 前端生产构建通过

- [ ] **Step 5: 如果脚手架未完成则先停止并执行前置计划**

Stop condition:
- 任一路径缺失
- `go test ./apps/server/...` 失败
- `apps/web` 构建失败

Action:
- 先完成 [2026-04-02-phase1-task1-scaffold-execution-plan.md](/G:/gofile/Agent_Project/docs/superpowers/plans/2026-04-02-phase1-task1-scaffold-execution-plan.md)

### Task 2: Containerize the server application

**Files:**
- Create: `.dockerignore`
- Create: `apps/server/Dockerfile`

- [ ] **Step 1: 先验证服务端镜像当前无法构建**

Run: `docker build -f apps/server/Dockerfile -t agent-project-server:dev .`

Expected:
- FAIL，因为 `apps/server/Dockerfile` 尚不存在

- [ ] **Step 2: 创建根目录 `.dockerignore`**

The file should exclude at least:
- `.git`
- `.idea`
- `node_modules`
- `apps/web/node_modules`
- `apps/web/.next`
- `coverage`
- `*.log`

The file must keep:
- `go.mod`
- `go.sum`
- `apps/server/**`

- [ ] **Step 3: 编写 `apps/server/Dockerfile`**

Implementation requirements:
- 使用多阶段构建
- builder 阶段从仓库根目录读取 `go.mod`、`go.sum` 和 `apps/server`
- 构建目标为 `./apps/server/cmd/server`
- runtime 阶段只保留可执行文件和最小运行依赖
- 默认暴露端口 `8080`

- [ ] **Step 4: 重新构建服务端镜像**

Run: `docker build -f apps/server/Dockerfile -t agent-project-server:dev .`

Expected:
- BUILD SUCCESS

- [ ] **Step 5: 启动服务端容器做 smoke test**

Run: `docker run --rm -p 8080:8080 agent-project-server:dev`

Expected:
- 容器成功启动

- [ ] **Step 6: 访问健康检查接口**

Run: `curl http://localhost:8080/healthz`

Expected response body:

```json
{"status":"ok","service":"server"}
```

### Task 3: Containerize the web application

**Files:**
- Create: `apps/web/Dockerfile`
- Create: `apps/web/.dockerignore`
- Modify: `apps/web/next.config.ts`

- [ ] **Step 1: 先验证前端镜像当前无法构建**

Run: `docker build -f apps/web/Dockerfile -t agent-project-web:dev apps/web`

Expected:
- FAIL，因为 `apps/web/Dockerfile` 尚不存在

- [ ] **Step 2: 在 `apps/web/next.config.ts` 中开启 standalone 输出**

Implementation requirement:
- 导出配置时包含 `output: 'standalone'`

- [ ] **Step 3: 创建 `apps/web/.dockerignore`**

The file should exclude at least:
- `node_modules`
- `.next`
- `coverage`
- `*.log`

- [ ] **Step 4: 编写 `apps/web/Dockerfile`**

Implementation requirements:
- 使用多阶段构建
- build 阶段执行 `npm ci` 和 `npm run build`
- runtime 阶段使用 standalone 产物启动 Next.js
- 默认暴露端口 `3000`

- [ ] **Step 5: 重新构建前端镜像**

Run: `docker build -f apps/web/Dockerfile -t agent-project-web:dev apps/web`

Expected:
- BUILD SUCCESS

- [ ] **Step 6: 启动前端容器做 smoke test**

Run: `docker run --rm -p 3000:3000 agent-project-web:dev`

Expected:
- 容器成功启动

- [ ] **Step 7: 验证首页可访问**

Run: `curl http://localhost:3000/`

Expected:
- 返回 `200`
- 响应体包含首页占位文本

### Task 4: Add production deployment assets

**Files:**
- Create: `deploy/docker-compose.prod.yml`
- Create: `deploy/prod.env.example`
- Create: `scripts/deploy/remote-deploy.sh`

- [ ] **Step 1: 先写 `deploy/prod.env.example`**

The file must contain at least:
- `IMAGE_TAG=v0.1.0`
- `SERVER_PORT=8080`
- `WEB_PORT=3000`
- `DATABASE_URL=postgres://...`
- `NEXT_PUBLIC_API_BASE_URL=http://<server-ip>:8080`

- [ ] **Step 2: 编写 `deploy/docker-compose.prod.yml`**

Compose requirements:
- 只包含 `web` 和 `server`
- 使用 `ghcr.io/<owner>/agent-project-web:${IMAGE_TAG}`
- 使用 `ghcr.io/<owner>/agent-project-server:${IMAGE_TAG}`
- 通过 `${WEB_PORT}` 与 `${SERVER_PORT}` 暴露公网端口
- 不要包含 `build:`
- 不要包含 `postgres`

- [ ] **Step 3: 先验证 compose 配置可解析**

Run: `docker compose -f deploy/docker-compose.prod.yml --env-file deploy/prod.env.example config`

Expected:
- 成功输出规范化 compose 配置

- [ ] **Step 4: 编写 `scripts/deploy/remote-deploy.sh`**

Implementation requirements:
- `set -euo pipefail`
- 必须校验 `APP_DIR` 与 `IMAGE_TAG`
- 必须校验 `${APP_DIR}/docker-compose.prod.yml` 与 `${APP_DIR}/.env` 存在
- 必须只更新 `.env` 中的 `IMAGE_TAG=...`
- 必须支持 `DRY_RUN=1`
- 非 dry-run 时执行：
  - `docker login ghcr.io`
  - `docker compose pull`
  - `docker compose up -d`
  - `docker compose ps`

- [ ] **Step 5: 先做脚本语法校验**

Run: `docker run --rm -v ${PWD}:/work -w /work alpine:3.20 sh -n scripts/deploy/remote-deploy.sh`

Expected:
- 退出码为 `0`

- [ ] **Step 6: 做 dry-run 行为校验**

Run:

```powershell
docker run --rm -v ${PWD}:/work -w /work alpine:3.20 sh -lc "
  mkdir -p /tmp/app &&
  cp deploy/docker-compose.prod.yml /tmp/app/docker-compose.prod.yml &&
  cp deploy/prod.env.example /tmp/app/.env &&
  APP_DIR=/tmp/app IMAGE_TAG=v0.1.1 DRY_RUN=1 GHCR_USERNAME=dummy GHCR_TOKEN=dummy sh scripts/deploy/remote-deploy.sh &&
  grep '^IMAGE_TAG=v0.1.1$' /tmp/app/.env
"
```

Expected:
- 脚本成功退出
- 输出 `IMAGE_TAG=v0.1.1`

### Task 5: Implement the CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: 先在本地手工跑一遍 CI 等价命令**

Run:
- `go test ./...`
- `npm ci`
- `npm run build`
- `docker build -f apps/server/Dockerfile -t agent-project-server:ci .`
- `docker build -f apps/web/Dockerfile -t agent-project-web:ci apps/web`

Workdir for npm commands: `apps/web`

Expected:
- 五条命令全部成功

- [ ] **Step 2: 创建 `.github/workflows/ci.yml`**

Workflow requirements:
- 触发：`pull_request`、`push` 到 `main`
- job `server-check`:
  - checkout
  - setup-go
  - `go test ./...`
- job `web-check`:
  - checkout
  - setup-node
  - `npm ci`
  - `npm run build`
- job `image-smoke`:
  - checkout
  - docker build `server`
  - docker build `web`

- [ ] **Step 3: 再次运行本地等价命令，确认 workflow 所引用命令都能通过**

Run the same commands as Step 1.

Expected:
- 结果不变

- [ ] **Step 4: 将 workflow 推送到 GitHub 后确认 CI 被触发**

Expected on GitHub:
- `pull_request` 或 `push main` 时自动执行
- 三个 job 全部出现

### Task 6: Implement the release and rollback workflow

**Files:**
- Create: `.github/workflows/release-deploy.yml`
- Modify: `scripts/deploy/remote-deploy.sh`

- [ ] **Step 1: 先在 GitHub 仓库配置 Secrets**

Required secrets:
- `DEPLOY_HOST`
- `DEPLOY_PORT`
- `DEPLOY_USER`
- `DEPLOY_SSH_KEY`
- `GHCR_USERNAME`
- `GHCR_TOKEN`

- [ ] **Step 2: 创建 `.github/workflows/release-deploy.yml`**

Workflow requirements:
- trigger `push tags: v*`
- trigger `workflow_dispatch` with input `image_tag`
- job `prepare`:
  - 解析 tag 或 `workflow_dispatch` 输入
- job `build-and-push-server`:
  - login `GHCR`
  - build and push `server`
- job `build-and-push-web`:
  - login `GHCR`
  - build and push `web`
- job `deploy`:
  - 依赖前两个 push job
  - 通过 `ssh/scp` 将 `deploy/docker-compose.prod.yml` 和 `scripts/deploy/remote-deploy.sh` 同步到 `/opt/agent-project`
  - 在远程执行 `APP_DIR=/opt/agent-project IMAGE_TAG=<tag> sh /opt/agent-project/remote-deploy.sh`

- [ ] **Step 3: 在远程服务器手工准备部署目录与 `.env`**

Run on server:
- `sudo mkdir -p /opt/agent-project`
- `sudo chown -R <deploy-user>:<deploy-user> /opt/agent-project`
- 创建 `/opt/agent-project/.env`

Expected:
- 部署用户对目录有写权限
- `.env` 已包含 `DATABASE_URL`、`SERVER_PORT`、`WEB_PORT`、`NEXT_PUBLIC_API_BASE_URL`

- [ ] **Step 4: 推送 workflow 后先做一次手动回滚链路 dry-run**

Action:
- 在 GitHub Actions 页面手动触发 `workflow_dispatch`
- 传入一个已存在的 `image_tag`

Expected:
- `prepare` job 正确解析参数
- 如果目标镜像存在，则远程部署成功

- [ ] **Step 5: 正式做第一次 tag 发布 smoke test**

Run:
- `git tag v0.1.0`
- `git push origin v0.1.0`

Expected:
- release workflow 自动触发
- `GHCR` 中出现两个 `v0.1.0` 镜像
- `deploy` job 成功结束

- [ ] **Step 6: 验证远程服务可访问**

Run:
- `curl http://<server-ip>:8080/healthz`
- 打开 `http://<server-ip>:3000`

Expected:
- 后端返回健康检查 JSON
- 前端首页可访问

### Task 7: Document the deployment contract and final verification

**Files:**
- Create: `docs/deployment.md`
- Modify: `README.md`
- Modify: `tasks/todo.md`

- [ ] **Step 1: 写 `docs/deployment.md`**

The document must explain:
- GitHub Secrets 列表
- 服务器初始化步骤
- `/opt/agent-project/.env` 需要哪些变量
- 如何发布一个新版本 tag
- 如何手动回滚旧 tag

- [ ] **Step 2: 在 `README.md` 增加 CI/CD 入口说明**

At minimum document:
- CI 触发方式
- CD 触发方式
- `GHCR` 镜像命名规则
- 远程服务器访问方式

- [ ] **Step 3: 在 `tasks/todo.md` 中记录实施结果和回顾**

At minimum document:
- 哪些文件新增
- 首次发布的 tag
- 是否成功完成远程部署
- 遇到的主要问题和解决方式

- [ ] **Step 4: 运行最终本地验证**

Run:
- `go test ./...`
- `npm ci`
- `npm run build`
- `docker build -f apps/server/Dockerfile -t agent-project-server:final .`
- `docker build -f apps/web/Dockerfile -t agent-project-web:final apps/web`
- `docker compose -f deploy/docker-compose.prod.yml --env-file deploy/prod.env.example config`

Workdir for npm commands: `apps/web`

Expected:
- 全部成功

- [ ] **Step 5: 运行最终远程验证**

Verify:
- GitHub `ci.yml` 最近一次运行通过
- GitHub `release-deploy.yml` 最近一次发布运行通过
- `GHCR` 中可见当前 tag 的 `web/server` 镜像
- 远程服务器 `docker ps` 中 `web/server` 容器存活
- `http://<server-ip>:8080/healthz` 返回正常
- `http://<server-ip>:3000` 可访问

- [ ] **Step 6: 按 `@verification-before-completion` 做结束前复核**

Checklist:
- 不要在没有真实 GitHub workflow 运行记录的情况下声称 CI/CD 已完成
- 不要在没有真实远程服务器验证的情况下声称部署成功
- 如果只完成了本地文件编写但未完成 GitHub/服务器验证，明确标注为“配置完成，未完成验收”

## Risks and Guardrails

- 当前仓库如果仍然不是 Git 仓库，不要继续写 workflow 并自称“已接入 GitHub Actions”；那只是本地 YAML 草稿。
- 在远程部署脚本中不要重写整份 `.env`，只更新 `IMAGE_TAG`，避免覆盖数据库连接等稳定配置。
- 第一次发布时不要直接用模糊的 `latest`；必须使用显式 tag，例如 `v0.1.0`。
- 远程 compose 不要包含数据库服务，否则会把“应用发布”和“数据层生命周期”错误耦合。
- 如果第一次 tag 发布失败，不要立刻重试同样命令三次；先检查 GitHub Actions 日志、GHCR push 结果和服务器上的 `/opt/agent-project/.env`。

## Definition of Done

本计划只在以下条件同时满足时才算完成：

- 仓库已经接入真实 GitHub 远程，并能触发 `GitHub Actions`
- `ci.yml` 在 `pull_request` 或 `push main` 时稳定运行通过
- `release-deploy.yml` 能在 `v*` tag 时构建并推送 `web/server` 镜像到 `GHCR`
- 远程服务器能通过发布 workflow 成功拉起新版本容器
- 可通过 `workflow_dispatch` 指定旧 tag 完成手动回滚
- 文档已写清 Secrets、服务器初始化、发布与回滚流程
