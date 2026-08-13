# 区域镜像仓库发布链路重构设计

## 1. 背景

当前 `CI/CD` 已经打通了：

- `CI` 常驻校验
- `tag` 触发镜像构建
- 远程服务器可通过 `docker compose` 运行 `web/server`

但正式发布链路没有稳定下来。`2026-04-03` 的一手排查结论如下：

- 服务器当前线上仍运行 `v0.1.1`
- `Release Deploy` 的 `v0.1.2` workflow 卡在 `Stream web image to remote Docker`
- `server` 镜像约 `22.7MB`，从 GitHub hosted runner 通过 `ssh` 流式传到服务器耗时约 `12` 分钟
- `web` 镜像约 `155MB`，无论是 `runner -> ssh -> docker load`，还是服务器直接从 `GHCR` 拉取，都表现出长时间阻塞
- 问题核心不是 workflow 语法，而是跨境镜像分发路径不稳定

这意味着“`GHCR` 作为生产部署镜像源 + 自动部署生产”在当前网络条件下不具备可重复性。

## 2. 目标

本次重构只解决一个问题：为生产环境建立稳定、可重复、可回滚的正式发布链路。

目标是：

- 保留现有 `CI`
- 将“构建发布镜像”和“切换生产版本”拆成两条独立 workflow
- 将生产镜像源切换到更接近服务器地域的仓库，默认选型为 `Tencent TCR`
- 继续使用显式版本 tag 进行部署和回滚
- 不把数据库变更、域名、HTTPS、反向代理混入本次重构

## 3. 非目标

本次不做：

- 数据库迁移自动化
- 多环境编排
- 自动回滚
- Kubernetes / Terraform / ArgoCD
- 生产机自托管 GitHub runner

## 4. 方案对比

### 4.1 继续使用 `GHCR`，只增加超时和重试

优点：

- 改动最小

缺点：

- 已有实测表明链路瓶颈在跨境镜像分发，而不是超时参数
- 大镜像耗时无法收敛，生产切换时间不可控

结论：

- 不推荐

### 4.2 使用生产机或同地域主机作为 self-hosted runner

优点：

- 可以避免 GitHub hosted runner 到国内服务器的大文件传输

缺点：

- 将 GitHub Actions 执行权限直接耦合到生产侧
- runner 生命周期、升级和安全边界更复杂
- 当前项目阶段过重

结论：

- 可作为应急方案，不作为首选

### 4.3 使用区域镜像仓库，并将 release 与 deploy 拆分

优点：

- 生产部署只依赖“服务器从同地域 registry 拉取镜像”
- 发布与部署职责清晰
- 回滚仍然围绕历史 tag
- 即使构建成功但暂不部署，也不会污染生产状态

缺点：

- 需要新增 registry 配置与 secrets
- 需要重写当前 `release-deploy.yml`

结论：

- 推荐方案

## 5. 推荐设计

### 5.1 总体流程

重构后的流程固定为：

`pull_request / push main -> CI`

`push tag v* -> Release Images -> build & push images to registry`

`workflow_dispatch(image_tag) -> Deploy Production -> pull selected tag on server -> docker compose up -d`

这样做的关键是把“镜像可用”与“生产切换”解耦。

### 5.2 仓库与镜像命名

生产镜像仓库改为通用 OCI 配置，不再在代码中写死 `GHCR`。

运行时使用以下变量：

- `IMAGE_REGISTRY`
- `IMAGE_NAMESPACE`
- `IMAGE_TAG`

镜像地址格式统一为：

- `${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/docreview-agent-server:${IMAGE_TAG}`
- `${IMAGE_REGISTRY}/${IMAGE_NAMESPACE}/docreview-agent-web:${IMAGE_TAG}`

默认落地目标为 `Tencent TCR`，但实现保持通用，便于后续切换。

### 5.3 Workflow 拆分

新增两条 workflow：

#### `release-images.yml`

触发：

- `push tags: v*`

职责：

- 解析 tag
- 登录目标 registry
- 构建并推送 `server`
- 构建并推送 `web`

不做：

- 任何 SSH 部署
- 任何生产切换

#### `deploy-production.yml`

触发：

- `workflow_dispatch`

输入：

- `image_tag`
- 可选 `dry_run`

职责：

- 通过 `SSH` 同步部署脚本和 compose 文件
- 在服务器上仅更新 `IMAGE_TAG`
- 登录目标 registry
- `docker compose pull`
- `docker compose up -d`
- 进行最小 smoke check

### 5.4 远程部署脚本责任边界

`remote-deploy.sh` 只负责：

- 校验 `APP_DIR`、`IMAGE_TAG`
- 校验 `docker-compose.prod.yml` 和 `.env`
- 只更新 `.env` 中的 `IMAGE_TAG`
- 使用外部传入的 registry 凭据执行 `docker login`
- 执行 `docker compose pull/up -d/ps`
- 运行本机健康检查

它不再负责：

- 加载镜像 tar
- 处理 `GHCR` 特有变量
- 管理数据库

### 5.5 Secrets 与服务器配置边界

GitHub Actions Secrets 需要新增或重命名为：

- `REGISTRY_HOST`
- `REGISTRY_NAMESPACE`
- `REGISTRY_USERNAME`
- `REGISTRY_PASSWORD`
- `DEPLOY_HOST`
- `DEPLOY_PORT`
- `DEPLOY_USER`
- `DEPLOY_SSH_KEY`

服务器 `/opt/agent-project/.env` 需要保存：

- `IMAGE_REGISTRY`
- `IMAGE_NAMESPACE`
- `IMAGE_TAG`
- `SERVER_PORT`
- `WEB_PORT`
- `DATABASE_URL`
- `NEXT_PUBLIC_API_BASE_URL`

稳定配置仍然放在服务器本地，部署只更新 `IMAGE_TAG`。

### 5.6 回滚

回滚方式保持不变：

- 手动触发 `Deploy Production`
- 填入历史 `image_tag`

因为镜像和部署分离，回滚不需要重新构建。

## 6. 风险与保护措施

### 6.1 数据库连接

当前服务器 `.env` 中 `DATABASE_URL` 看起来仍是占位值。本次重构不自动修改数据库配置，只在文档和验收中明确要求人工复核。

### 6.2 生产切换权限

把生产切换收敛到手动 `workflow_dispatch`，避免每次 tag push 都自动动生产。

### 6.3 Registry 切换风险

实现使用通用变量，不把 `Tencent TCR` 常量散落到脚本中。这样未来更换 registry 时只需改 secrets 和 `.env`，不必再改 workflow 逻辑。

## 7. 验收标准

当以下条件同时满足时，本次重构才算完成：

- `CI` 不受影响，仍可通过
- `release-images.yml` 能对 `v*` tag 成功构建并推送 `server/web` 镜像
- `deploy-production.yml` 能通过手动输入历史 tag 完成部署
- 远程服务器的 `docker-compose.prod.yml` 与 `.env` 使用通用 registry 变量
- `remote-deploy.sh` 不再依赖镜像 tar/stream
- 文档清楚说明 secrets、服务器 `.env` 和发布/回滚方式

## 8. 结论

当前最优路径不是继续修补 `GHCR -> 生产机` 的跨境传输，而是将生产发布链路重构为：

- `区域 registry` 作为生产镜像源
- `Release Images` 与 `Deploy Production` 两条 workflow 分离
- 生产部署改为显式人工触发

这条路径更符合当前阶段对“稳定、可重复、可回滚”的要求。
