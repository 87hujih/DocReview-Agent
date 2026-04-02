# Task Plan

## Goal

为当前项目完成基于 `GitHub Actions` 的 CI/CD 自动化流水线设计，范围限定为 `CI 校验 + GHCR 镜像发布 + SSH 到单台远程服务器进行 Docker 应用部署`，暂不纳入数据库部署、域名、HTTPS 和反向代理。

## Phases

- [x] 探查仓库当前是否已有 CI/CD 与部署资产
- [x] 通过澄清问题收敛部署边界与约束
- [x] 分段产出 CI/CD 设计并获得用户确认
- [x] 写入 CI/CD 设计文档
- [x] 进入实施计划阶段

## Constraints

- 先完成设计，不直接实现 CI/CD
- 远程部署仅覆盖 `web` 和 `server` 应用容器
- 数据库不纳入 GitHub Actions 部署范围
- 单远程服务器、单部署环境
- 通过公网端口访问，不要求本轮完成域名、HTTPS 或反向代理

## Errors Encountered

| Error | Attempt | Resolution |
|-------|---------|------------|
| 根目录直接读取 `2026-04-02-phase1-rag-work-breakdown.md` 失败 | 1 | 改从 `docs/superpowers/plans/2026-04-02-phase1-rag-work-breakdown.md` 读取 |
| 初次读取 `planning-with-files` 技能路径错误 | 1 | 改为 `C:\\Users\\mhn\\.codex\\skills\\planning-with-files\\SKILL.md` |
