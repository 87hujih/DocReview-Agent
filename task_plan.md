# Task Plan

## Goal

在已确认的实施计划基础上，实际落地当前项目的最小可运行骨架和 CI/CD 资产，包括 `apps/server`、`apps/web`、Dockerfile、部署模板、远程脚本与 GitHub Actions workflow，并尽可能完成本地验证。

## Phases

- [x] 建立 Git 基线并创建隔离 worktree
- [x] 完成 Task 1 最小后端、前端与本地 compose 骨架
- [x] 完成 Dockerfile、部署模板、远程部署脚本和 workflow 文件
- [ ] 完成 Docker 本地镜像验证
- [ ] 完成 GitHub Actions、GHCR 与远程服务器验收

## Constraints

- 远程部署仅覆盖 `web` 和 `server` 应用容器
- 数据库不纳入 GitHub Actions 部署范围
- 单远程服务器、单部署环境
- 通过公网端口访问，不要求本轮完成域名、HTTPS 或反向代理
- 当前会话无法代替用户配置 GitHub Secrets 或远程服务器 `.env`

## Errors Encountered

| Error | Attempt | Resolution |
|-------|---------|------------|
| 根目录直接读取 `2026-04-02-phase1-rag-work-breakdown.md` 失败 | 1 | 改从 `docs/superpowers/plans/2026-04-02-phase1-rag-work-breakdown.md` 读取 |
| 初次读取 `planning-with-files` 技能路径错误 | 1 | 改为 `C:\\Users\\mhn\\.codex\\skills\\planning-with-files\\SKILL.md` |
| `.worktrees` 尚未被 git 忽略且仓库没有初始提交 | 1 | 将 `.worktrees/` 加入 `.gitignore`，创建初始提交后再创建 worktree |
| `next.config.ts` 在当前 Next.js 版本下无法构建 | 1 | 改为 `next.config.mjs`，并继续保留最小配置 |
| 本地 Docker 验证失败，Docker daemon 未启动 | 1 | 先完成非 Docker 资产实现与非 Docker 验证，等待用户启动 Docker 后补完本地镜像验证 |
| 首次 `release-deploy.yml` 在远程部署阶段失败 | 1 | 通过 GitHub API、远程 `docker pull`、`curl ghcr.io/token` 与 `dockerd` 日志定位为服务器侧 `dockerd -> GHCR` 超时，改为 runner 传输镜像归档再部署 |
