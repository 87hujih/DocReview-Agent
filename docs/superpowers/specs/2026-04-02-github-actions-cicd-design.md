# GitHub Actions CI/CD Design

## 1. 背景与目标

当前项目需要在继续推进 Phase 1 研发前，先完成一套最小但正规的 `CI/CD` 方案，用于支持后续的持续校验、镜像发布与远程部署。

本设计的目标是：

- 为仓库建立基于 `GitHub Actions` 的持续集成能力
- 为 `web` 与 `server` 建立基于 `GHCR` 的镜像发布流程
- 为单台远程 Linux 服务器建立基于 `SSH + Docker` 的自动部署流程
- 将部署边界收敛到“应用容器发布”，不把数据库、域名、HTTPS 和反向代理混入第一版

该方案面向当前项目阶段，优先保证：

- 实现路径短
- 风险边界清晰
- 便于演示
- 便于后续扩展到更正式的生产化方案

## 2. 设计边界

### 2.1 In Scope

- `pull_request` 和 `push main` 的持续集成校验
- `v*` 版本标签触发的发布流程
- `web` 与 `server` Docker 镜像构建
- 镜像推送到 `GHCR`
- 通过 `SSH` 登录远程服务器并执行 `docker compose pull/up`
- 单台远程服务器上的单套部署环境
- 基于历史镜像 tag 的手动回滚

### 2.2 Out Of Scope

- 数据库实例的创建、迁移和自动化部署
- 域名申请、DNS 管理
- `HTTPS` 证书配置
- `Nginx` / `Caddy` 反向代理
- 多环境部署，例如 `staging + production`
- Kubernetes、ArgoCD、Terraform 或其他更重的交付系统
- 自动化回滚

## 3. 当前约束

- 远程目标是单台已准备好的 Linux 服务器
- 服务器已经安装 `Docker` 与 `docker compose`
- 部署目标只包含应用，不包含数据库
- 远程应用通过公网 `IP:端口` 直接访问
- 远程运行方式使用 Docker 容器
- 镜像仓库使用 `GHCR`
- 部署触发方式采用 `tag/release`

## 4. 总体架构

本方案将流水线拆成两条独立的 `GitHub Actions workflow`：

1. `CI workflow`
2. `Release Deploy workflow`

两者职责严格分离。

### 4.1 CI Workflow

触发条件：

- `pull_request`
- `push` 到 `main`

职责：

- 校验服务端代码可编译、可测试
- 校验前端代码可安装、可构建
- 校验相关 Docker 镜像可成功构建

结果：

- 只做质量门禁
- 不接触远程服务器
- 不发布镜像

### 4.2 Release Deploy Workflow

触发条件：

- `push tags: v*`
- 可选补充 `workflow_dispatch` 作为手动回滚入口

职责：

- 构建 `server` 与 `web` 镜像
- 推送到 `GHCR`
- 登录远程服务器
- 拉取指定 tag 镜像并重启应用容器

结果：

- 只有显式版本发布才会触发部署
- 日常合并和集成不会直接把代码推上服务器

## 5. 发布流程

最终发布链路固定为：

`pull_request/push -> CI`

`push tag v* -> build image -> push GHCR -> SSH -> docker compose pull -> docker compose up -d`

建议版本标签格式统一为：

- `v0.1.0`
- `v0.1.1`
- `v0.2.0`

这样可以确保每次发布、部署和回滚都围绕明确版本进行，而不是依赖不稳定的 `latest`。

## 6. 镜像与制品策略

### 6.1 镜像命名

镜像命名建议固定为：

- `ghcr.io/<github-owner>/agent-project-server:<tag>`
- `ghcr.io/<github-owner>/agent-project-web:<tag>`

其中：

- `<github-owner>` 为 GitHub 仓库 owner
- `<tag>` 为发布版本，例如 `v0.1.0`

### 6.2 镜像策略

- 每次正式发布都生成一组对应版本的不可变镜像
- 第一版不依赖 `latest` 作为部署依据
- 所有部署和回滚都通过显式 tag 完成

这样能保证：

- 版本来源清晰
- 远程服务器的状态可追踪
- 回滚无需重新构建代码

## 7. 远程服务器部署结构

远程服务器建议固定部署目录：

- `/opt/agent-project/`

目录内至少包含：

- `/opt/agent-project/docker-compose.prod.yml`
- `/opt/agent-project/.env`

可选后续扩展：

- `/opt/agent-project/deploy.sh`

第一版中：

- `docker-compose.prod.yml` 负责运行应用镜像
- `.env` 负责保存运行时配置
- GitHub Actions 只负责切换镜像版本并重启服务

### 7.1 生产 Compose 责任边界

`docker-compose.prod.yml` 只应描述：

- `web`
- `server`

不应包含：

- `postgres`
- `redis`
- `build:`
- 本地源码挂载

核心原则是：

- GitHub Actions 负责构建镜像
- 服务器只负责拉取和运行镜像

## 8. 运行时访问模型

由于本轮不纳入域名、HTTPS 和反向代理，远程访问方式采用公网端口：

- 前端：`http://<server-ip>:3000`
- 后端：`http://<server-ip>:8080`

这意味着系统需要明确处理两件事：

1. 前端运行时 API 地址配置
2. 后端 CORS 策略

### 8.1 前端

前端需要显式读取：

- `NEXT_PUBLIC_API_BASE_URL=http://<server-ip>:8080`

### 8.2 后端

后端需要允许来自前端公网地址的访问，至少在当前阶段允许：

- `http://<server-ip>:3000`

后续引入域名和反向代理后，再收紧为正式来源。

## 9. 环境变量与 Secrets 边界

### 9.1 GitHub Secrets

GitHub Actions 至少需要以下 Secrets：

- `DEPLOY_HOST`
- `DEPLOY_PORT`
- `DEPLOY_USER`
- `DEPLOY_SSH_KEY`
- `GHCR_USERNAME`
- `GHCR_TOKEN`

### 9.2 服务器本地 `.env`

建议将运行时配置保存在远程服务器的：

- `/opt/agent-project/.env`

该文件至少会包含：

- `IMAGE_TAG`
- `NEXT_PUBLIC_API_BASE_URL`
- `PORT`
- `DATABASE_URL`

### 9.3 配置原则

第一版不建议在 GitHub Actions 中每次发布都重写整份生产 `.env`。  
推荐方式是：

- 稳定配置保存在服务器本地 `.env`
- 发布时只更新 `IMAGE_TAG`
- 然后执行 `docker compose pull` 与 `docker compose up -d`

这样可以降低：

- 远程配置被误覆盖的风险
- Secrets 暴露面
- 运行时变更与版本发布耦合的问题

## 10. Workflow 设计

## 10.1 `ci.yml`

建议包含以下 job：

### `server-check`

职责：

- 安装 Go
- 安装依赖
- 运行 `go test ./...`

目的：

- 尽早发现后端编译、单元测试和依赖问题

### `web-check`

职责：

- 安装 Node
- 在 `apps/web` 下执行 `npm ci`
- 执行 `npm run build`
- 后续可扩展 `npm run lint`

目的：

- 确认前端依赖和构建流程可正常执行

### `image-smoke`

职责：

- 执行 `server` 镜像构建
- 执行 `web` 镜像构建

目的：

- 尽早发现 Dockerfile、构建上下文和镜像依赖问题

该 job 在 `CI` 中只验证构建，不推送镜像。

## 10.2 `release-deploy.yml`

建议包含以下 job：

### `prepare`

职责：

- 解析当前 tag
- 统一生成后续 job 需要使用的镜像 tag

### `build-and-push-server`

职责：

- 构建 `server` 镜像
- 登录 `GHCR`
- 推送 `server` 镜像

### `build-and-push-web`

职责：

- 构建 `web` 镜像
- 登录 `GHCR`
- 推送 `web` 镜像

### `deploy`

职责：

- 依赖前两个镜像 push job 成功
- 通过 `SSH` 登录远程服务器
- 登录 `GHCR`
- 更新部署使用的镜像 tag
- 执行 `docker compose pull`
- 执行 `docker compose up -d`
- 做最小 smoke check

## 10.3 手动回滚入口

建议额外提供一个：

- `workflow_dispatch`

输入参数至少包含：

- `image_tag`

回滚方式是：

- 手动指定一个已经存在于 `GHCR` 的历史 tag
- 在服务器上重新拉取并部署该版本

这样可以避免：

- 为回滚重新构建代码
- 通过修改代码仓库状态来实现版本切换

## 11. 失败策略

### 11.1 CI 阶段

- 任一 job 失败即标记 workflow 失败
- PR 或主分支检查失败
- 不触发部署

### 11.2 CD 阶段

- 任一镜像构建失败即阻断部署
- `deploy` 失败即整个发布流程失败
- 第一版不做自动回滚

保留现场的原因是：

- 便于排查真实失败原因
- 避免自动脚本在错误假设下扩大影响范围

## 12. 回滚策略

第一版回滚方案固定为：

- 通过手动 `workflow_dispatch`
- 指定历史 tag
- 重新部署旧版本镜像

这是一种显式回滚，而不是自动回滚。  
这样更适合当前项目阶段，因为：

- 系统仍处于早期演进阶段
- 风险主要在部署链路稳定性，而不是大规模流量
- 显式回滚更容易验证和解释

## 13. 验收标准

### 13.1 CI 验收

- `pull_request` 时自动执行 CI
- `push main` 时自动执行 CI
- 后端测试通过
- 前端构建通过
- Docker 镜像构建校验通过

### 13.2 Release/CD 验收

- 推送 `v*` tag 后自动触发部署 workflow
- `web` 与 `server` 镜像成功推送到 `GHCR`
- 远程服务器成功完成 `pull + up -d`

### 13.3 远程运行验收

- `docker ps` 中可见 `web` 与 `server` 容器处于运行状态
- `http://<server-ip>:8080/healthz` 可访问
- `http://<server-ip>:3000` 可访问

### 13.4 回滚验收

- 可通过手动 workflow 指定旧 tag
- 远程环境成功切换到旧版本
- 回滚过程不需要重新构建镜像

## 14. 后续扩展方向

当本轮最小方案稳定后，可以按顺序演进：

1. 增加 `Dockerfile` 最佳实践与构建缓存
2. 引入 `staging` 环境
3. 引入域名、`HTTPS` 和反向代理
4. 收紧 CORS 和公网端口暴露
5. 为部署增加更细的 smoke check
6. 在数据库 migration 稳定后再讨论是否把“迁移执行”接入 CD

## 15. 结论

当前项目最合适的 CI/CD 方案是：

- `CI` 常驻运行
- `Release Tag` 触发 `CD`
- 镜像推送到 `GHCR`
- 远程服务器通过 `SSH + Docker Compose` 拉取并运行版本镜像
- 数据库、域名、HTTPS 与反向代理暂不纳入第一版

这套方案既满足当前最小工程化要求，也为后续平台扩展保留了清晰边界。
