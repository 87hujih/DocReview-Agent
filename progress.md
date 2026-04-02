# Progress

## 2026-04-02

- 读取了 Phase 1 工作拆分文档、`README.md`、`docs/development.md`、`go.mod`、`.gitignore` 和 `tasks/todo.md`。
- 确认了当前最关键的结构性风险是根目录 `go.mod` 与模块名不一致。
- 输出了 Task 1 详细执行计划文档 `docs/superpowers/plans/2026-04-02-phase1-task1-scaffold-execution-plan.md`。
- 同步补充了本次规划所需的 `task_plan.md`、`findings.md` 和 `progress.md`。
- 新增 CI/CD 设计收敛工作，确认将采用 `CI 常驻 + Tag/Release 触发 CD + GHCR + SSH + Docker` 的总体方向。
- 已明确本轮不包含数据库部署、域名、HTTPS 和反向代理，远程环境暂时通过公网端口访问。
- 已将完整 CI/CD 设计写入 `docs/superpowers/specs/2026-04-02-github-actions-cicd-design.md`，等待用户审阅后再进入实施计划阶段。
- 已基于已确认的 spec 产出实施计划 `docs/superpowers/plans/2026-04-02-github-actions-cicd-implementation-plan.md`。
- 本地仓库已初始化并关联 GitHub 远程 `git@github.com:87hujih/DocReview-Agent.git`。
- 为隔离实现工作，创建了 `.worktrees/github-actions-cicd` 与分支 `feat/github-actions-cicd`。
- 已完成 Go 侧最小骨架：修正根模块名、加入配置加载、`/healthz` 路由与基础测试。
- 已完成前端最小骨架：Next 应用、首页、`/resources` 预留页面、生产构建。
- 已完成 `docker-compose.yml`、两份 Dockerfile、`.dockerignore`、部署模板、远程部署脚本与两条 GitHub Actions workflow。
- 已完成本地可行验证：`go test ./apps/server/...`、`next build`、`docker compose config`、后端健康检查、前端页面路由访问。
- 当前剩余阻塞已经收敛为两点：本地尚未重新完成 `docker build` 类验证，以及修复后的 `release-deploy.yml` 还未通过新的 GitHub tag 正式验收。
- `main` 上的首轮 `ci.yml` 已在 GitHub Actions 成功通过。
- 首次 `v0.1.0` 的 `release-deploy.yml` 已定位失败根因：远程服务器 `dockerd` 访问 `ghcr.io/token` 超时，导致服务器端 `docker login/pull` 失败。
- 已将部署方案调整为“GitHub runner 从 `GHCR` 拉镜像、导出 tar、传到服务器，服务器只做 `docker load + docker compose up -d`”。
- 新方案已通过手工 archive deploy 验证：`106.52.42.194` 上的 `http://127.0.0.1:8080/healthz`、`http://127.0.0.1:3000/`、`http://127.0.0.1:3000/resources` 均返回成功。
