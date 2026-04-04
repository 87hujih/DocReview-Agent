# 企业文档智能协作平台 MVP

这份文档是独立于根目录 `README.md` 的补充说明，专门服务当前的简历可用 MVP 版本，不替代原有项目主说明。

## 项目简介

企业文档智能协作平台 MVP 是一个面向 `AI 应用开发实习` 场景收敛出来的可演示项目。  
它不是纯聊天 Demo，也不是抽象 Agent 框架，而是一个围绕企业文档处理闭环构建的任务型 AI 应用。

当前 MVP 的核心目标是打通这条链路：

`资源浏览 -> 检索引用 -> 任务创建 -> 修订提案 -> 人工审批 -> 异步执行 -> 新版本沉淀`

## 一句话定位

`一个支持企业文档检索问答、修订建议生成、人工审批和异步执行的任务型 AI 应用。`

## MVP 范围

第一版必须完成：

- 文档资源浏览
- 文档内容检索与 citation 展示
- 基于指定文档创建“审阅与修订”任务
- 生成结构化审阅结论与 diff 预览
- 人工审批通过或拒绝提案
- 审批通过后异步执行并生成新文档版本
- 在任务详情页回看状态、步骤、产物和结果

第一版明确不优先做：

- 飞书真实写回
- 多连接器接入
- 多租户
- 重权限系统
- 通用 Agent 平台配置中心

## 当前技术方案

- 前端：`Next.js`、`React`、`TypeScript`
- 后端：`Go`、`Eino`、`Hertz`
- 数据库：`PostgreSQL`
- 向量能力：`pgvector`
- 部署：`Docker Compose`

## 核心对象

第一版围绕以下对象组织：

- `Resource`
- `ResourceVersion`
- `Task`
- `TaskStep`
- `TaskArtifact`
- `Approval`
- `ExecutionJob`

其中 `Task` 是主对象，聊天不是主对象。

## 页面形态

MVP 最终应至少包含以下页面：

- `首页工作台`
- `资源页`
- `任务创建页`
- `任务详情页`
- `审批页`

聊天页如果保留，也只作为辅助入口，不作为产品主入口。

## 后端能力边界

后端当前应优先完成：

- 资源导入与读取
- 文档切片、检索与 citation
- `Planner -> Retriever -> Reviewer -> Editor` 最小 Agent 工作流
- 审批状态流转
- 异步执行与新版本落库

## 演示流程

适合面试或作品展示的演示顺序：

1. 打开资源页，查看系统内 demo 文档
2. 选择一份文档发起“审阅与修订”任务
3. 在任务详情页查看 citation、审阅摘要和 diff 预览
4. 在审批页批准提案
5. 返回任务详情页查看异步执行完成和新版本生成结果

## 适合写进简历的亮点

- 基于 `RAG + citation` 实现文档检索与证据引用
- 设计了任务驱动的 Agent 工作流，而不是单纯聊天交互
- 实现了 `修订提案 -> 审批 -> 异步执行` 的业务闭环
- 使用结构化产物和 diff 预览提升结果可解释性
- 支持任务状态追踪、执行结果沉淀和版本回看

## 相关文档

- [简历版 MVP 实施计划](/G:/gofile/Agent_Project/docs/superpowers/plans/2026-04-01-resume-ready-enterprise-document-ai-mvp.md)
- [CI/CD 部署文档](/G:/gofile/Agent_Project/docs/deployment.md)
- [根目录 README](/G:/gofile/Agent_Project/README.md)

## 当前说明

根目录 `README.md` 保留为原始主说明文档。  
本文件只服务当前 MVP 收敛与简历表达，不覆盖原文档。

## CI/CD

当前仓库已规划为：

- `CI`: `pull_request` 与 `push main` 触发
- `CD`: `v*` tag 触发正式发布
- 镜像仓库：`GHCR`
- 部署方式：`SSH + Docker Compose`

镜像命名约定：

- `ghcr.io/87hujih/docreview-agent-server:<tag>`
- `ghcr.io/87hujih/docreview-agent-web:<tag>`

远程访问方式：

- 前端：`http://<server-ip>:3000`
- 后端：`http://<server-ip>:8080`
