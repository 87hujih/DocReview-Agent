# Regional Registry CD Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将当前生产发布链路从“`GHCR + 自动部署 + 镜像流式传输`”重构为“`区域 registry + release/deploy 分离 + 手动生产切换`”。

**Architecture:** 保留现有 `CI`，把 tag 触发的镜像构建发布抽成独立 workflow，再新增手动触发的生产部署 workflow。远程服务器统一通过通用 registry 变量从目标仓库拉取 `server/web` 镜像，部署脚本只负责更新 `IMAGE_TAG`、执行 `pull/up` 和做最小 smoke check。

**Tech Stack:** GitHub Actions、Docker、Docker Compose、SSH、POSIX shell、Tencent TCR-compatible OCI registry

---

## File Responsibility Map

- Create: `.github/workflows/release-images.yml`
  - tag 触发的镜像构建与推送，不接触生产服务器
- Create: `.github/workflows/deploy-production.yml`
  - 手动触发的生产部署与回滚入口
- Modify: `scripts/deploy/remote-deploy.sh`
  - 去掉 tar/stream 逻辑，改成通用 registry 登录、pull、up、smoke check
- Modify: `deploy/docker-compose.prod.yml`
  - 镜像源从 `GHCR` 变量改为通用 `IMAGE_REGISTRY/IMAGE_NAMESPACE`
- Modify: `deploy/prod.env.example`
  - 补齐区域 registry 所需的示例变量
- Modify: `docs/deployment.md`
  - 说明新 secrets、服务器 `.env`、发布与回滚流程
- Modify: `README.md`
  - 更新 CI/CD 概述，不再描述 tag 自动部署生产
- Modify: `tasks/todo.md`
  - 跟踪这次发布链路重构的执行与复盘
- Delete: `.github/workflows/release-deploy.yml`
  - 移除旧的“构建并立即部署”workflow

## Task 1: Rework the runtime registry contract

**Files:**
- Modify: `deploy/docker-compose.prod.yml`
- Modify: `deploy/prod.env.example`

- [ ] **Step 1: 用当前通用 registry 变量验证旧 compose 不满足新设计**

Run:

```powershell
$tmp = New-TemporaryFile
Set-Content $tmp @"
IMAGE_REGISTRY=ccr.ccs.tencentyun.com
IMAGE_NAMESPACE=demo/docreview-agent
IMAGE_TAG=v0.1.2
SERVER_PORT=8080
WEB_PORT=3000
DATABASE_URL=postgres://postgres:postgres@db.example.internal:5432/agent_project?sslmode=disable
NEXT_PUBLIC_API_BASE_URL=http://106.52.42.194:8080
"@
docker compose -f deploy/docker-compose.prod.yml --env-file $tmp config
```

Expected:
- FAIL，因为当前 compose 仍引用 `GHCR_OWNER`

- [ ] **Step 2: 修改 `deploy/docker-compose.prod.yml` 使用通用 registry 变量**

Implementation requirements:
- `server` 镜像改为 `${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/docreview-agent-server:${IMAGE_TAG}`
- `web` 镜像改为 `${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/docreview-agent-web:${IMAGE_TAG}`
- 其他运行边界保持不变

- [ ] **Step 3: 修改 `deploy/prod.env.example`**

Implementation requirements:
- 删除 `GHCR_OWNER`
- 增加 `IMAGE_REGISTRY`
- 增加 `IMAGE_NAMESPACE`
- 保留 `IMAGE_TAG`、端口和运行时变量
- 默认值使用 `Tencent TCR` 风格示例

- [ ] **Step 4: 重新验证 compose 可解析**

Run:

```powershell
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/prod.env.example config
```

Expected:
- PASS

## Task 2: Refactor the remote deployment script

**Files:**
- Modify: `scripts/deploy/remote-deploy.sh`

- [ ] **Step 1: 用新变量名运行现有脚本，验证它不能满足新流程**

Run:

```powershell
docker run --rm -v ${PWD}:/work -w /work alpine:3.20 sh -lc "
  mkdir -p /tmp/app &&
  cp deploy/docker-compose.prod.yml /tmp/app/docker-compose.prod.yml &&
  cp deploy/prod.env.example /tmp/app/.env &&
  APP_DIR=/tmp/app IMAGE_TAG=v0.1.2 DRY_RUN=1 REGISTRY_HOST=demo REGISTRY_USERNAME=dummy REGISTRY_PASSWORD=dummy sh scripts/deploy/remote-deploy.sh
"
```

Expected:
- FAIL，因为当前脚本仍依赖 `GHCR_*` 或 archive 路径

- [ ] **Step 2: 重写 `scripts/deploy/remote-deploy.sh` 的 registry 合同**

Implementation requirements:
- 保留 `APP_DIR`、`IMAGE_TAG`、`DRY_RUN`
- 删除 `SERVER_IMAGE_ARCHIVE` / `WEB_IMAGE_ARCHIVE`
- 删除 `GHCR_USERNAME` / `GHCR_TOKEN`
- 改为支持 `REGISTRY_HOST`、`REGISTRY_USERNAME`、`REGISTRY_PASSWORD`
- 非 `DRY_RUN` 时执行：
  - `docker login`
  - `docker compose pull`
  - `docker compose up -d`
  - `docker compose ps`
  - `curl -fsS http://127.0.0.1:${SERVER_PORT}/healthz`
- 继续只更新 `.env` 里的 `IMAGE_TAG`

- [ ] **Step 3: 校验脚本语法**

Run:

```powershell
docker run --rm -v ${PWD}:/work -w /work alpine:3.20 sh -n scripts/deploy/remote-deploy.sh
```

Expected:
- PASS

- [ ] **Step 4: 校验 dry-run 行为**

Run:

```powershell
docker run --rm -v ${PWD}:/work -w /work alpine:3.20 sh -lc "
  mkdir -p /tmp/app &&
  cp deploy/docker-compose.prod.yml /tmp/app/docker-compose.prod.yml &&
  cp deploy/prod.env.example /tmp/app/.env &&
  APP_DIR=/tmp/app IMAGE_TAG=v0.1.2 DRY_RUN=1 REGISTRY_HOST=ccr.ccs.tencentyun.com REGISTRY_USERNAME=dummy REGISTRY_PASSWORD=dummy sh scripts/deploy/remote-deploy.sh &&
  grep '^IMAGE_TAG=v0.1.2$' /tmp/app/.env
"
```

Expected:
- PASS
- 输出 `IMAGE_TAG=v0.1.2`

## Task 3: Split release and deploy workflows

**Files:**
- Create: `.github/workflows/release-images.yml`
- Create: `.github/workflows/deploy-production.yml`
- Delete: `.github/workflows/release-deploy.yml`

- [ ] **Step 1: 删除旧的单 workflow 假设**

Implementation requirement:
- 移除 `.github/workflows/release-deploy.yml`

- [ ] **Step 2: 新建 `.github/workflows/release-images.yml`**

Workflow requirements:
- 触发：`push tags: v*`
- 登录目标 registry
- 构建并推送 `server`
- 构建并推送 `web`
- 使用 secrets：
  - `REGISTRY_HOST`
  - `REGISTRY_NAMESPACE`
  - `REGISTRY_USERNAME`
  - `REGISTRY_PASSWORD`

- [ ] **Step 3: 新建 `.github/workflows/deploy-production.yml`**

Workflow requirements:
- 触发：`workflow_dispatch`
- 输入：
  - `image_tag`
  - `dry_run`（boolean）
- 通过 `ssh/scp` 同步 `deploy/docker-compose.prod.yml` 与 `scripts/deploy/remote-deploy.sh`
- 在远程执行：
  - `APP_DIR=/opt/agent-project`
  - `IMAGE_TAG=<input>`
  - `DRY_RUN=<input>`
  - `REGISTRY_HOST=<secret>`
  - `REGISTRY_USERNAME=<secret>`
  - `REGISTRY_PASSWORD=<secret>`

- [ ] **Step 4: 对 workflow 做静态校验**

Run:

```powershell
docker run --rm -v ${PWD}:/repo -w /repo rhysd/actionlint:latest
```

Expected:
- PASS

## Task 4: Update documentation and task tracking

**Files:**
- Modify: `docs/deployment.md`
- Modify: `README.md`
- Modify: `tasks/todo.md`

- [ ] **Step 1: 更新 `docs/deployment.md`**

The document must explain:
- 为什么旧链路不再使用
- `Tencent TCR` 兼容的 secrets 列表
- `/opt/agent-project/.env` 需要的变量
- `Release Images` 与 `Deploy Production` 的职责
- 如何发布新版本
- 如何回滚旧 tag
- 需要人工复核服务器 `DATABASE_URL`

- [ ] **Step 2: 更新 `README.md` 中的 CI/CD 段落**

At minimum document:
- `CI` 触发方式不变
- `CD` 改为“tag 发布镜像 + 手动触发生产部署”
- 镜像地址使用通用 registry 变量

- [ ] **Step 3: 更新 `tasks/todo.md`**

At minimum document:
- 新增“区域 registry 发布链路重构”任务清单
- 记录实测根因：跨境镜像分发导致 `v0.1.2` 卡在流式传输
- 记录推荐方案和剩余外部前置条件

## Task 5: Run verification and handoff

**Files:**
- No new file changes unless verification exposes issues

- [ ] **Step 1: 安装前端依赖以恢复 worktree 基线**

Run:

```powershell
npm ci
```

Workdir:
- `apps/web`

Expected:
- PASS

- [ ] **Step 2: 跑本地验证**

Run:

```powershell
go test ./apps/server/...
npm run build
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/prod.env.example config
docker run --rm -v ${PWD}:/repo -w /repo rhysd/actionlint:latest
docker run --rm -v ${PWD}:/work -w /work alpine:3.20 sh -n scripts/deploy/remote-deploy.sh
```

Expected:
- 全部 PASS

- [ ] **Step 3: 说明未完成的外部验证**

Required note:
- 未在本次实现中完成真实 `Tencent TCR` push/pull 验证，除非已经拿到 registry 凭据
- 未自动修改生产服务器 `DATABASE_URL`

- [ ] **Step 4: 按 `@verification-before-completion` 复核**

Checklist:
- 不要在没有 fresh command output 的情况下声称 workflow 或脚本通过
- 如果没有真实 registry secrets，就明确标记“配置已就绪，待外部凭据完成最终验收”
