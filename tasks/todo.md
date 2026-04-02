# 路线规划任务

- [x] 阅读 `README.md`、设计文档、实施计划
- [x] 收敛 6 到 8 周冲刺目标
- [x] 输出 5 阶段 AI 工程优先路线图
- [x] 写入规格文档 `docs/superpowers/specs/2026-04-01-ai-application-intern-roadmap-design.md`
- [x] 写入实施计划 `docs/superpowers/plans/2026-04-01-ai-application-intern-roadmap.md`
- [x] 用户确认路线图和阶段划分
- [x] 拆出 Phase 1 工作拆分文档 `docs/superpowers/plans/2026-04-02-phase1-rag-work-breakdown.md`
- [ ] 用户确认 Phase 1 工作拆分
- [ ] 根据确认结果直接开始 Phase 1 实现

## Review

- 本次路线规划已明确优先级：先做 `RAG + Multi-Agent + structured output`，再做 `Streams + WebSocket`，最后补 `Approval + ExecutionJob`
- Phase 1 已补充为可执行拆分，额外纳入了向量化、最小脚手架、migration 和验收工作
- 当前仓库不是 git 仓库，无法执行文档提交流程

## Task 1 计划细化

- [x] 重新读取 `docs/superpowers/plans/2026-04-02-phase1-rag-work-breakdown.md` 中的 Task 1
- [x] 结合仓库现状识别 Task 1 的模块边界与脚手架风险
- [x] 输出详细计划 `docs/superpowers/plans/2026-04-02-phase1-task1-scaffold-execution-plan.md`

## Task 1 Review

- 当前最关键的前置动作是修正根目录 `go.mod` 的模块边界，否则后续 `apps/server` 包路径会混乱
- Task 1 详细计划明确保持“根目录启动 Go、`apps/web` 启动 Node”的最小运行边界，不提前引入 migration、Redis 或业务模型
- 资源页占位路由被提前纳入 Task 1，避免 Task 5 再回头调整 App Router 目录结构

## CI/CD 设计

- [x] 调研当前仓库是否已有 CI/CD 与部署资产
- [x] 收敛 GitHub Actions + GHCR + SSH + Docker 的发布边界
- [x] 写入规格文档 `docs/superpowers/specs/2026-04-02-github-actions-cicd-design.md`
- [x] 用户审阅 CI/CD 规格文档
- [x] 基于规格文档输出实施计划

## CI/CD Review

- 当前建议先做单远程环境的最小闭环：`CI` 常驻，`tag/release` 触发 `CD`
- 发布范围只包含 `web` 和 `server` 容器，不把数据库、域名、HTTPS 和反向代理混入第一版
- 回滚策略固定为“重新部署已有历史 tag”，而不是自动回滚或重新构建旧版本

## CI/CD 实施计划

- [x] 基于 CI/CD 规格文档输出实施计划
- [x] 用户确认实施计划
- [x] 开始落地 `.github/workflows`、Dockerfile 和部署资产
- [x] 建立 Git 基线并关联 GitHub 远程
- [x] 创建隔离 worktree 并在分支中执行实现
- [x] 完成 Task 1 最小脚手架
- [x] 完成 workflow、部署模板和远程脚本
- [ ] 启动本机 Docker 并完成镜像构建验证
- [ ] 在 GitHub 仓库中配置 Secrets
- [ ] 在远程服务器准备 `/opt/agent-project/.env`
- [ ] 推送分支并验证 `ci.yml`
- [ ] 推送 tag 并验证 `release-deploy.yml`

## CI/CD 实施 Review

- 实施计划明确把“真实 GitHub 仓库”和“Task 1 最小脚手架完成”列为前置条件，避免出现只写本地 YAML 的伪完成
- 部署资产被收敛为 `Dockerfile + deploy/docker-compose.prod.yml + remote-deploy.sh + 两条 workflow`
- 验收标准要求同时覆盖 GitHub workflow、GHCR 镜像和远程服务器健康检查
- 实现过程中出现的两个真实偏差已经固定：
  - `next.config.ts` 被改为 `next.config.mjs`，因为当前 Next.js 版本不支持 `ts` 配置文件
  - Docker 本地验证尚未完成，因为当前机器上的 Docker daemon 未启动
