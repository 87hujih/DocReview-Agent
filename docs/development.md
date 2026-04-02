# 开发文档

本文档用于约束企业文档任务协作平台项目的日常开发方式，确保后续实现与当前平台设计保持一致，避免再次退回到“知识库助手”或“聊天 Demo”思路。

## 1. 当前开发基线

当前项目已经确定以下技术基线：

- 前端：`Next.js + React + TypeScript`
- 后端：`Go + Eino + Hertz`
- 数据层：`PostgreSQL + pgvector`
- 外部系统接入：`MCP Connector Layer`
- 首批连接器：`飞书文档`
- 文件存储：本地文件系统
- 流式状态更新：`SSE`
- 部署：`Docker Compose`

除非出现明确的架构问题，否则后续开发不再切换到：

- `Go 单体前后端`
- `React + Node + Go` 三层业务架构
- 单纯知识库聊天产品形态
- 额外的 Node BFF

## 2. 当前产品目标

项目当前目标不是“企业知识库助手”，而是：

`一个面向普通员工的企业文档任务协作平台。`

第一版主链路是：

- 用户在已授权知识空间中选择飞书文档
- 发起“内容审阅与修订”任务
- 平台自动编排多个 Agent 生成修订提案
- 用户或审批人查看 diff 预览
- 审批通过后后台异步写回飞书文档
- 全链路保留任务、审批、执行和审计记录

## 3. 系统边界与核心上下文

当前后端至少要划分以下稳定上下文：

- `task`
- `agent`
- `connector`
- `knowledge`
- `approval`
- `job`
- `audit`
- `storage`

每个上下文只负责一类职责，不要把平台再堆回单一大模块。

### 3.1 `task`

负责任务生命周期、状态机、任务步骤、任务产物和工作流编排。

### 3.2 `agent`

负责 `Planner / Retriever / Review / Edit / Execution` 角色能力。

### 3.3 `connector`

负责外部系统统一接入，首批是飞书文档。所有 MCP 逻辑都应位于这一层。

### 3.4 `knowledge`

负责知识空间、检索、citation 和相关知识数据组织。

### 3.5 `approval`

负责 diff 预览、审批记录和审批状态流转。

### 3.6 `job`

负责后台异步执行、重试和结果回传。

### 3.7 `audit`

负责审计事件、执行日志和任务回放。

## 4. 前后端职责边界

### 4.1 前端负责

- 页面路由
- SSR / SEO
- 平台工作台 UI
- 任务创建交互
- 任务详情展示
- 资源浏览
- 审批中心
- 聊天辅助页
- SSE / 轮询消费任务进度

### 4.2 后端负责

- 任务创建与状态流转
- 多 Agent 编排
- 飞书连接器与 MCP 调用
- 资源发现、读取和受控写入
- 审批判定与执行上下文校验
- `ExecutionJob` 执行与重试
- 审计落库和回放
- 权限校验与安全边界控制

### 4.3 明确不能放到前端的能力

以下能力不能放到 `Next.js`：

- Agent 执行逻辑
- MCP 调用细节
- 飞书文档读写逻辑
- 检索主逻辑
- 审批最终判定逻辑
- 后台 Job 执行
- 数据库核心写入逻辑

## 5. 目标仓库结构

```text
Agent_Project/
  apps/
    web/
    server/
  docs/
    development.md
    superpowers/
      specs/
      plans/
  demo-data/
  docker-compose.yml
  README.md
```

### 5.1 `apps/web`

建议结构：

```text
apps/web/
  app/
    page.tsx
    tasks/new/page.tsx
    tasks/[id]/page.tsx
    approvals/page.tsx
    resources/page.tsx
    chat/page.tsx
  components/
  lib/
    api/
    sse/
    auth/
  styles/
  package.json
```

### 5.2 `apps/server`

建议结构：

```text
apps/server/
  cmd/server/
  internal/
    config/
    server/
      router/
      handlers/
      middleware/
    task/
      service/
      workflow/
      models/
    agent/
      planner/
      retriever/
      review/
      editor/
      execution/
    connector/
      mcp/
      feishu/
      contracts/
    knowledge/
      ingest/
      index/
      retriever/
      citation/
    approval/
    audit/
    job/
    storage/
      postgres/
      files/
    stream/
  db/migrations/
  go.mod
```

## 6. 推荐开发顺序

后续开发按下面顺序推进，不建议跳步：

1. 搭建 `apps/web`、`apps/server` 和根目录 `.gitignore`
2. 初始化平台工作台骨架和 Go 服务端骨架
3. 落地 `Task / Resource / Approval / ExecutionJob` 数据模型
4. 接入飞书资源浏览和只读连接器能力
5. 打通 `Planner -> Retriever -> Review -> Edit` 链路
6. 实现 diff 预览、审批中心和任务详情页
7. 实现 `ExecutionJob`、后台 Worker 和飞书写回
8. 增加审计、回放、失败重试和权限校验
9. 最后再补聊天辅助入口和第二任务模板

## 7. 每次开发任务的执行方式

每次只做一个最小闭环，不要同时推进多个主功能。

建议执行方式：

1. 从平台实施计划中只取一个任务
2. 明确这次只改哪些文件
3. 先完成最小可运行闭环
4. 立刻验证
5. 验证通过后再进入下一个任务

不要优先做这些事情：

- 多连接器并行接入
- 多文档批处理
- 用户自定义 Agent 编排
- 很重的工作流引擎
- 复杂权限系统一次做满
- 把产品重新做成聊天主界面

## 8. 验证要求

每完成一块能力，都要做最小验证。

### 后端

- `go test ./...`
- 手工调用对应接口
- 检查数据库落库是否符合预期

### 前端

- `npm run lint`
- `npm run build`
- 手工访问对应页面

### 整体

- 能从页面走完对应任务链路
- 能看到任务状态推进
- 能看到审批状态变化
- 能看到写回结果或失败原因

## 9. 文档同步规则

后续开发优先参考以下文档：

- [平台设计文档](/G:/gofile/Agent_Project/docs/superpowers/specs/2026-03-31-enterprise-document-task-platform-design.md)
- [平台实施计划](/G:/gofile/Agent_Project/docs/superpowers/plans/2026-03-31-enterprise-document-task-platform-implementation.md)
- [README](/G:/gofile/Agent_Project/README.md)

以下文档已降级为历史参考，不再作为主目标：

- [旧版 Next.js + Go 架构设计](/G:/gofile/Agent_Project/docs/superpowers/specs/2026-03-31-enterprise-knowledge-base-nextjs-go-design.md)
- [旧版实施计划](/G:/gofile/Agent_Project/docs/superpowers/plans/2026-03-31-enterprise-knowledge-base-nextjs-go-implementation.md)

## 10. 当前建议的第一步

下一步优先做下面四件事：

1. 创建 `apps/web`
2. 创建 `apps/server`
3. 创建根目录 `.gitignore`
4. 初始化平台型前后端骨架和基础健康检查
