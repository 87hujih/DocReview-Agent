# Agent Runtime durable-only cutover and operations

## Scope

生产 Agent 编排只保留一条可执行链路：

`API/Auth → TurnCoordinator → Durable Run Engine → ContextAssembler → Typed Orchestration → ToolRuntime → EvidenceSet → Patch Validator → external approval → Atomic Committer → Outbox/Projection`.

`AGENT_RUNTIME_MODE` 只接受 `durable`。不存在 cohort 路由、shadow 权威执行、legacy fallback 或按配置即时回退。资源/文件/会话查询等兼容 CRUD 不是 Agent 编排入口，不能启动旧 Workflow。

## Request and trust contract

Streaming and non-streaming assistant endpoints call the same transport-neutral Turn Pipeline. A client sends a stable `X-Request-ID`; a reconnect sends the same request body and ID plus the last persisted SSE sequence in `Last-Event-ID`. The frontend automatically retries an interrupted stream with that tuple. Only persisted terminal or recoverable states—`waiting_input`, `waiting_approval`, `succeeded`, `failed`, or `cancelled`—are public Turn states.

每个请求都必须携带精确 Workspace 和 Resource。浏览器从显式选择的资源或最新持久化且 ready 的 `session_file` 消息取得 `resource_id`。可信反向代理必须移除公网客户端提供的所有身份头，再附加 HMAC-SHA256 证明。签名内容是以下以换行分隔的 UTF-8 序列：

```text
v1
request_id
HTTP_METHOD
request_path
principal_type
principal_id
organization_id
workspace_id
issued_at_rfc3339nano
comma_separated_roles
```

The proxy sends `X-DocReview-Principal-Type`, `X-DocReview-Principal-ID`, `X-DocReview-Organization-ID`, `X-DocReview-Workspace-ID`, `X-DocReview-Identity-Issued-At`, `X-DocReview-Roles`, and the lowercase hexadecimal `X-DocReview-Identity-Signature`. The request ID, method, and path are signed to prevent replay onto another request. Timestamps outside the configured maximum age are rejected. Durable mode fails closed when this boundary is absent, expired, malformed, or mismatched.

The HMAC adapter is a narrow trusted-ingress compatibility boundary, not an identity provider. Replace it with the selected IdP/token verifier behind `identity.Adapter` before broadening access. Never put the HMAC secret in source control, checked-in configuration, logs, or client-side code.

Each tool call reloads the principal, Workspace, Resource, Run, and request facts from the durable Run. Policy checks active membership, role permission, and resource ownership. Every document tool must target the exact immutable `run.resource_id`; the canonical Committer repeats the exact Run/Step/Resource, current-node, expected-hash, and evidence authorization checks. Model output cannot create approvals, permissions, validator results, or commit facts.

## Routing invariant

- assistant stream 与 non-stream 都进入同一个 `DurableOnlyPipeline`。
- 缺失或错误的签名、Workspace、Resource、membership、canonical version 或 policy 权限会返回明确错误；任何错误都不能调用 legacy assistant、Task、Job、Approval 或固定四段 Agent 链。
- `/api/tasks`、`/api/approvals`、`/api/jobs`、legacy task suggestion confirm 和 resource task-context 不注册。
- 前端只读运行与审批查询使用签名、Workspace-scoped、bounded 的 `GET /api/agent/runs*` 和 `GET /api/agent/approvals*`；Run 详情不会公开原始 State、ContextManifest 或工具输入输出。
- 新审批只允许签名的 owner/admin 调用 `/api/agent/approvals/:id/approve` 或 `/reject`；模型和工具不能自行决策。
- shadow/comparison 代码和旧实现可以暂时作为历史源码存在，但不在 server 生产依赖图、路由图或配置表面中。物理删除仍受 `legacy-removal-report-v2` 约束。

## Deployment sequence

1. 在获授权的 `_test` 数据库执行数据库保险丝验证和 migrations 016–024 round trip；生产只通过独立 migrator 顺序应用已校验 migration。
2. 完成现有 Resource 的 Workspace/Organization 归属和 canonical document 导入/核对。016 的 nullable 扩展不会自动 backfill；未完成的数据不能进入 durable Runtime。
3. 在部署系统中设置 `AGENT_RUNTIME_MODE=durable`、`AGENT_RUNTIME_TRUSTED_INGRESS_HMAC_SECRET`、`AGENT_RUNTIME_TRUSTED_INGRESS_SOURCE`、`AGENT_RUNTIME_TRUSTED_INGRESS_MAX_AGE_MS`。secret 至少 32 字节且不得进入仓库、日志或浏览器。
4. 配置可信代理：清除外来身份头，绑定 request ID/method/path 签名，校准时钟，并验证 active membership 与 Resource 所有权。
5. 启动 server，确认 Runtime/Projection Worker 能恢复 lease，且 pending approval、Outbox dead letter、profile mismatch 均符合门禁。
6. 依次验证检索、Patch validation、approve/reject、atomic commit、SSE 重连、重复 request/tool/commit 幂等和跨 Workspace 拒绝。

本地代码验证不会替代上述数据库、数据迁移、ingress 或 canary 证据。缺少任何前置条件时应阻止发布，不得临时启用旧链路。

## Rollback

当前版本不支持通过配置回退到 legacy；把 `AGENT_RUNTIME_MODE` 改为 `legacy` 会导致启动校验失败。紧急回滚必须部署上一个已验证 release，并首先停止接收新请求、让已接受的 durable Run/Outbox 安全排空。

Do not stop workers merely to roll back routing. Do not delete Runs, approvals, tool audits, commits, Outbox rows, receipts, comparisons, or canonical versions. Do not reverse migration 023. A Run accepted as durable remains durable and may be safely re-claimed after a lease expires; approved Runs create the deterministic `CommitPatch` continuation, while rejected Runs become deterministic failures.

如果旧 release 无法理解 durable facts，应先从入口摘除新流量，保持当前 Worker 排空，再在完成 reconciliation 后部署旧 release。已经提交的版本和审计事实必须保留。

## Recovery and operations

- Runtime recovery: startup and periodic recovery requeue expired Step leases. Stale workers cannot heartbeat or complete after the lease generation changes.
- Approval recovery: an external owner/admin decision is atomic with either the exact `CommitPatch` continuation or the rejected terminal Run state. Repeating the same decision is idempotent; a different decision conflicts.
- Projection recovery: the worker claims exact event types with `SKIP LOCKED`, a bounded lease and generation, retries with exponential backoff, records an event/projection receipt, and dead-letters after the configured attempt limit. Replaying after a crash between projection and publication is safe.
- Stream recovery: reconnect using the same request body, request ID, and last persisted sequence. The observer can disconnect without cancelling the durable Run.
- Commit recovery: retry with the same tool and commit idempotency keys. Expected node hashes and base version turn concurrent edits into conflicts, never blind overwrites.

Monitor at minimum:

- durable Run and Step counts by status, oldest queued age, expired/reclaimed leases, attempts, and terminal error category;
- pending/waiting approval age and decision conflicts;
- Outbox pending age, retry count, lease expiry, and `dead_letter` count for Phase I event types;
- projection receipt lag and public Turn status/event sequence;
- EvidenceSet lexical/semantic/rerank degradation modes and profile mismatches;
- Patch validation conflicts, unauthorized-resource denials, commit idempotency conflicts, latency, and commit Outbox lag.

Operational inspection must be read-only unless a separate runbook explicitly authorizes a repair. Never manually mark approvals approved, rewrite Run status, delete Outbox facts, reset leases, replay commits under new keys, or perform schema/data cleanup during an incident.

## Phase I gate

本地实现门禁要求 deterministic unit、race、vet、build、前端回归和 diff 检查通过。生产发布还要求获授权的 PostgreSQL migration/round-trip、Workspace/canonical 数据核对、protected-ingress、跨租户拒绝和显式 durable canary 证据。物理删除 legacy 文件与 Schema 收缩继续要求独立 removal report 合格。
