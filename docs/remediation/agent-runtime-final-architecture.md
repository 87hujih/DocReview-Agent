# Agent Runtime 最终架构与旧模块删除门槛

## Phase J 架构

持久化数据链路如下：

```text
API/Auth/WorkspaceScope -> TurnCoordinator -> Durable Run Engine
  -> ContextAssembler -> Typed Supervisor -> ToolRuntime/PolicyEngine
  -> EvidenceSet -> Patch Validator -> External Approval -> Atomic Committer
  -> Transactional Outbox -> Public Turn Projection
```

控制面新增两个彼此独立的边界：

```text
Versioned dataset + recorded candidate -> Offline scorers -> CI report/gate
Operator identity + Workspace -> Diagnose/Metrics or audited safe action -> durable facts
```

Run、Step、Attempt、Tool call、ContextManifest、EvidenceSet、approval、commit、Outbox event、projection receipt、cutover comparison 和 operator action 都是持久化事实。Channel、SSE 连接、摘要、UI 状态和指标只是通知或可重建投影。

## 删除门槛

`cutover.EvaluateLegacyRemoval` 是进入收缩阶段前的确定性保险丝。当前契约为 evidence schema `1.1` / report `legacy-removal-report-v2`。固定最低阈值不能由 evidence 输入降低；shadow 总数和人工复核数分开计数，匹配率与不可用率只以已复核样本为分母；数值为零还必须有对应生产指标审计，空 caller/config 列表也必须有完成依赖审计的证据。只有同时满足以下全部条件，才允许删除旧实现：

- 至少有 100 条经过人工复核的 shadow comparison，匹配率不低于 99%，不可用率为 0%；
- 至少有 20 个 durable cohort Run，成功率不低于 99%；
- projection dead letter 和 retrieval profile mismatch 均为 0；
- PostgreSQL round trip、受保护 ingress 和 durable canary 已获得授权并完成验证；
- 数据兼容性、回滚能力和公开行为已经验证；
- 离线评测、崩溃恢复、request/tool/commit 幂等、审批与提交一致性、跨 Workspace 隔离均已验收；
- 目标旧模块的生产调用方和配置依赖均为 0。

这些阈值只是初始最低标准。生产负责人可以通过新的证据版本提高阈值，但禁止静默降低阈值，也禁止用缺失数据推断门槛已经通过。

当前 evidence 与生成报告分别保存在 [`legacy-removal-evidence.current.json`](./legacy-removal-evidence.current.json) 和 [`legacy-removal-report.current.json`](./legacy-removal-report.current.json)。重新生成命令为：

```text
go run ./apps/server/cmd/agent-legacy-report \
  -evidence docs/remediation/legacy-removal-evidence.current.json
```

退出码 `0` 表示 eligible，`1` 表示证据有效但仍被阻止，`2` 表示 evidence/阈值/JSON 无效。CLI 只读显式文件，不加载应用配置、dotenv 或数据库。

生产事实通过 `agent-runtime-ops` 导出：metrics schema `1.1` 可按精确 Workspace/Resource 统计窗口内 durable Run 和 shadow comparison，并以当前 durable cohort 对账队列、审批和 Outbox；`comparisons` 动作按稳定顺序导出 comparison ID、request/Run 身份、hash 和 details，供人工复核留档。Embedding profile mismatch 使用结构化 reason code；旧的缺少 reason code 或 error category 的 retrieval failure 会保守计入同一阻塞指标。任何导出都不会自动把样本标记为 reviewed，也不会自动设置 `*_verified`，因此不能把原始计数当成删除授权。

`agent-shadow-review` 只读绑定 comparison export 的原始 SHA-256，并校验精确 cohort、窗口、limit、完整逐条覆盖、ID/request/Run/status/hash 和 reviewer metadata。重复、未知或篡改身份直接判无效；缺失、争议、未备注的 diverged/unavailable 以及疑似 limit 截断都会 fail closed。只有完整报告中的 shadow 候选计数可进入下一轮人工 evidence 审查；CLI 不修改 evidence，reviewer 字符串也不替代外部认证或签名审计。

## 当前旧模块删除审计

| ADR 删除目标 | 当前生产证据 | Phase J 决策 |
| --- | --- | --- |
| 以内存 `task/workflow.WorkflowRunner` 队列作为事实来源 | `cmd/server/main.go` 仍会实例化它；创建任务时仍会入队；`WORKFLOW_*` 配置仍然存在 | 保留；不满足删除条件 |
| 固定 Planner → Retriever → Reviewer → Editor 链 | `cmd/server/main.go` 仍会为 legacy 流量实例化这些 Agent 和 `workflow.New` | 保留；不满足删除条件 |
| 已迁移的 `assistant.Service` 上下文、工具、提交和编排职责 | `legacy` 和 shadow 的权威响应仍调用该服务；多个兼容适配器仍然存在 | 保留；不满足删除条件 |
| 基于标题或 substring 的文档编辑 | legacy executor/editor 范围以及审批 job 路径仍可由 legacy 任务触达 | 保留；不满足删除条件 |
| Stream 与非 Stream 的重复编排 | 公开 Handler 已共享 Turn Pipeline，但其 legacy adapter 仍委托给旧 Service 行为 | 传输层重复已降级；Service 删除仍受阻 |
| 每个 Agent 独立拼 Prompt/上下文以及直接审批/job channel | legacy Planner/Reviewer/Editor/Executor 和审批 Worker 仍连接在生产路径中 | 保留；不满足删除条件 |
| legacy Section/Chunk 重建以及纯文本 canonical identity | 上传、重建索引、修复命令和回滚兼容仍使用 legacy 投影 | 保留；不满足删除条件 |

仓库配置也证明了这些依赖：应用配置、生产 Compose、README 和部署示例中的 `AGENT_RUNTIME_MODE` 均默认采用 `legacy`。2026-08-10 最终收口审计记录了 23 个生产 caller 和 6 个配置/CI/部署依赖；当前工作区没有达到数值阈值的已授权 cohort 报告，没有执行迁移 021–024，也没有 protected-ingress/canary 证据。生成报告为 `eligible=false`，本阶段没有删除任何 legacy 文件。

这并不表示 Phase J 被豁免。评测、运维控制和删除保险丝已经实现；具有破坏性的收缩工作会一直推迟，直到证据完整。门槛满足后，应当每次只删除一个可独立审查的 legacy 目标，重新运行完整后端、前端和评测套件，为该目标验证回滚，并更新上表。禁止把数据库收缩与代码删除合并到同一次变更中。

## 兼容与回滚

公开 assistant DTO、SSE 事件语义、审批端点、resource、task 和前端行为保持不变。迁移 024 是增量扩展。需要立即回滚时，把新流量切到 `legacy`，同时让 Worker 排空已经接受的 durable Run。Canonical version 保留 `resource_versions.content` 兼容投影。Runtime、评测和运维事实必须继续留存用于审计，回滚期间不得删除。
