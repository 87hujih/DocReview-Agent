# 整改状态

## 当前阶段

Phase 1——数据与身份基础；Agent 编排已按用户确认执行全量 durable 切流。

## 当前任务

生产 Agent 编排现只装配 durable typed graph，不再装配、路由或回退 legacy assistant/task/job/approval 工作流。配置只接受 `AGENT_RUNTIME_MODE=durable`，可信入口是启动必需条件。代码切流不等于物理删除资格：现有 `legacy-removal-report-v2` 尚未按本次变更重新生成，数据库迁移、历史 Workspace/canonical 数据、ingress 和 canary 证据仍未在当前环境验证，因此 legacy 文件与 Schema 收缩继续由删除保险丝阻止。

## Agent Runtime 主线

全量请求切换与物理删除分开处理：生产调用图已切到 durable-only；在删除报告具备资格前，不删除历史实现文件、不收缩 Schema，也不伪造迁移或生产证据。

## 已完成工作

### 2026-08-11：Agent 编排全量 durable-only 切流

- 新增 `DurableOnlyPipeline`，所有请求直接委托 durable Runner；不存在 cohort、shadow、legacy runner 或错误回退分支。
- server 生产构造图移除旧 Planner/Reviewer/Editor、WorkflowRunner、Task/Job Worker、legacy Approval、旧 responder/deliberation/verifier/summarizer 以及 shadow comparison 装配。
- 配置默认值改为 `durable`，启动校验拒绝 `legacy`/`shadow`，并强制至少 32 字节 trusted-ingress HMAC secret、审计来源和正数有效期。
- assistant stream 与 non-stream 生产入口统一使用同一 durable Turn Pipeline；缺少签名、Workspace、Resource 或身份不匹配时失败关闭。
- durable 公开 DTO 投影直接读取持久化 session/message projection repository，不再经由旧 assistant orchestration service；旧 Service 仅保留非编排会话/上传兼容职责。
- 服务端彻底取消 `/api/tasks`、`/api/approvals`、`/api/jobs`、legacy task-suggestion confirm 和 resource task-context 路由注册；只保留新 typed approval 写入口。
- 前端移除旧任务/审批导航和任务建议确认请求；历史建议只读，旧 `/tasks/*` 重定向到新助手。`/approvals` 随后按下节改为 durable typed approval 页面。

### 2026-08-11：durable Run 与类型化审批前端恢复

- 新增签名身份、精确 Workspace 隔离和 `1..100` 上限的 `/api/agent/runs*`、`/api/agent/approvals*` 查询；生产装配直接读取 durable Run/Step/ToolApproval 仓储，不恢复旧 Task/Job/Approval service。
- Run 详情公开投影仅包含状态、步骤进度、工具元数据、审批状态和诊断告警，不暴露 StateJSON、ContextManifest、Attempt、Outbox、工具输入输出或内部 trace index。
- 前端新增 `/runs`、`/runs/:id` 与 `/approvals`，支持运行筛选/轮询、步骤与工具状态检查、审批载荷审阅和带理由的批准/拒绝；legacy `/api/tasks`、`/api/approvals`、`/api/jobs` 继续返回 404。
- 浏览器不持有 HMAC secret；新页面和 durable 助手共享 protected-ingress 签名边界。仓库内未提供可验证的真实反向代理实例，因此生产 header stripping、签名互操作和 401/tenant rejection 仍属于发布前门禁。
- 保留资源列表、上传、会话查询和文件下载等非编排兼容能力；它们不能触发旧 Agent 执行。历史资源必须另行完成 Workspace 归属和 canonical document 导入，否则 durable 请求失败关闭。
- 未读取仓库 `.env`，未连接数据库，未执行 migration、DDL、backfill 或数据清理。

### 2026-08-09：Agent Runtime 架构基线与 Phase A 复核

- 审计了 assistant、task workflow、agent、knowledge、PostgreSQL、migration、deploy、CI、README 和整改文档路径，全程未读取仓库 dotenv 或任何密钥。
- 在 ADR 0001 中记录了原调用链、状态流、数据流、目标架构、模块边界、迁移兼容策略、旧模块删除清单、风险和回滚方案。
- 增加分阶段生产级 Agent Runtime 实施计划和跨阶段不变量。
- 加固 R0.1 数据库测试保险丝：不安全配置会在任何连接工厂运行前返回；Schema/Extension DDL、migration 和 cleanup 注册都位于保险丝之后。
- 增加纯本地失败路径测试，证明即使生产数据库主机被加入 allowlist，生产命名的数据库也不会触发连接工厂。

### 2026-08-10：Phase B——持久化存储基础

- 新增只向前的 `017_durable_agent_runtime.sql`，覆盖 Run、Step、Attempt、ToolCall、ContextManifest 和事务性 Outbox。
- 增加生命周期、JSON、预算约束，以及 claim、retry、过期 lease、Workspace/request、tool/outbox 幂等索引和 Run 乐观版本。
- 新增独立的 `internal/agent/runtime` 生命周期和重试分类协议，没有引入额外角色 Agent 或新的 God Service。
- 新增 `storage/postgres/agentrun`，实现 Run/Step/Tool 幂等创建、Run 乐观迁移、Step claim/heartbeat/complete/retry/cancel/recover、Attempt、用量遥测和 ContextManifest 持久化。
- 新增 `storage/postgres/outbox`，实现事务入队、claim/lease、发布、retry/dead-letter 和过期 lease 恢复。
- 所有已认领写操作都由 worker owner、lease generation 和未过期 lease 共同 fencing，旧 Worker 无法覆盖新认领结果。
- 迁移保持 expand-only，repository 暂不接入公开流量；legacy assistant/task/job 和公共 API 行为不变。

### 2026-08-10：Phase C——持久化运行引擎

- 新增以数据库轮询为事实来源的 Engine 和 Worker；可选 wake channel 只降低延迟，不决定任务是否可运行，关闭的 channel 不会形成忙循环。
- 实现 claim、lease、heartbeat、启动和周期性过期恢复、遗弃 Attempt 关闭、lease fencing、确定性指数退避、retryable/terminal 分类，以及 Attempt/Step/Run timeout 优先级。
- Step 在不同 Attempt 之间使用稳定幂等键；Outcome 和 Retry 通过匹配的事务 Outbox 事实支持幂等重放。
- 实现确定性取消、等待输入/审批状态唤醒，以及事务性 waiting-state 恢复。
- 在执行前和创建 continuation 前检查 Step、ToolCall、token、cost 和 deadline 限额。
- Attempt 持久化 provider、model、prompt version、temperature、ContextManifest、token、内部 retry、cost、latency、finish reason、trace、Step 和 Run 关联。
- 对 Executor 结果执行严格 JSON object、类型化 next step、重复键、错误分类和非负遥测校验。
- 支持 Run 与初始 Step 原子创建和 allowlist shadow recorder；`legacy` 仍为默认值，`shadow` 只记录指定 Resource 的惰性事实，类型化图未就绪时 `durable` fail closed。
- 保留 legacy WorkflowRunner 作为 Strangler 回滚路径，未切换公共 API、模型或文档写入路径。

### 2026-08-10：Phase D——Turn 与 Context 边界

- 新增 expand-only 迁移 `018_agent_turn_context.sql`：`agent_turns`、有序 `agent_turn_events`、幂等 `agent_turn_outcomes`、不可变 request scope，以及 legacy message 和 Run 的关联字段。
- 新增窄职责 `TurnCoordinator`。`Submit` 是唯一请求入口，强制 `request_id`，对 canonical input 哈希，并原子创建或复用 Turn、Message、Run、初始 `UnderstandGoal` Step、Event 和 Outbox；同键不同内容会冲突。
- Stream 和非 Stream 投影同一批持久化事件；观察者断开不会改变事实，同一 request/cursor 可以继续回放，进程内 stream buffer 不是事实来源。
- Response outcome、Message、Turn event 和 Outbox 原子提交；重试校验 content hash，存储调用者不能伪造 coordinator hash。
- 新增统一 `ContextAssembler`，包含 control、task、working、evidence、conversation、artifact 分层、输出预留、分层/全局 token 预算、按相关性选证据、不可变 control/task、provenance hash、trust boundary 和大对象引用。
- 新增版本化保守 token estimator，不引入生产依赖；profile 和每个 item 的 token 数都会持久化，未来可用相同接口替换为 provider 精确 tokenizer。
- ContextManifest 可以持久化并按 ID 不可变读取，能够复现某次 Attempt 真正看到的有序上下文。
- `AGENT_RUNTIME_MODE=legacy` 继续作为默认值，durable 模式保持 fail closed。

### 2026-08-10：Phase E——ToolRuntime 与 PolicyEngine

- 新增版本化类型化 Tool 协议、线程安全 registry/discovery、fail-closed JSON Schema 子集、严格输入输出校验、Resource selector、统一错误分类、provenance 和结果 token 预算。
- 所有工具调用经过同一个 ToolRuntime：持久化审计 claim/replay、强制幂等、确定性 Policy/Approval、持久化限流、Attempt timeout、分类重试、输出校验和超大结果 Artifact 化。
- `tool_calls` 增加 owner、lease、generation 和 attempt 事实；过期调用可被重新认领，晚到 Worker 不能覆盖新 generation。
- `PolicyEngine` 确定性检查 permission、Workspace Resource 和 risk。High/Critical 操作只接受外部持久化、且绑定 Workspace、Tool/Version、幂等键和 canonical Resource hash 的审批。
- 审批请求只能创建 pending 事实；最终决定只能由 admin/owner 外部接口写入并与 Outbox 同事务提交。`workflow.request_approval` 不能自行批准，也不能指向其他 Run/Step。
- 新增 Workspace 范围的不可变 Artifact、持久化固定窗口限流、文档/检索/Artifact/Patch/审批/Web 类型化工具适配器。
- Web Search 增加隐私检测、域名 allowlist、provider 错误分类、trace、显式 mock/production 类型和固定 provider 身份；外部内容始终视为不可信 Evidence。
- 新增 expand-only 迁移 `019_tool_runtime_controls.sql`。

### 2026-08-10：Phase F——类型化编排

- 新增完整类型化节点集、可拒绝重复键/未知字段/尾随值的严格 Decision codec、确定性 action→node/tool 校验，以及 Goal/Finding/Patch/Approval continuation 状态。
- 单一有界 Supervisor 实现 Understand → Context → Decide → Act → Observe 循环，以及 Evidence 读取、分析、Patch 生成/验证、审批等待、批准后提交和结果渲染。
- 每次模型调用都使用不可变 ContextManifest，每个工具节点都经过 ToolRuntime；单个 Attempt 的 trace 贯穿 Attempt、ModelRequest 和 Tool audit。
- Supervisor 处理无进展、达成目标和等待状态；Engine 继续负责 Step/Tool/token/cost/deadline/timeout/cancel/retry 限制。
- 新增事务性持久化 Observation、查询 API 和 `020_typed_orchestration.sql`。Outcome 幂等 hash 包含 Observation，不同内容的重放会冲突。
- 外部审批决定与精确 CommitPatch continuation 或 rejected terminal state 原子提交；模型无法调用审批决定方法。
- 新增 canonical JSON shadow comparison 及幂等冲突检测。

### 2026-08-10：Phase G——规范化文档运行时

- 新增 Canonical Document AST 1.0：稳定持久化 node ID、类型、attributes/content/children、source/page mapping、metadata、schema/content hash、图校验，以及由 AST 派生的 Section/Chunk/pending embedding 投影。
- Markdown、DOCX extracted-text 和 PDF extracted-text 使用独立 Importer/Renderer 边界；重复导入 ID 稳定，不同节点不冲突，PDF 页码保留，大文档保持节点图。
- 新增严格 PatchSet 1.0，支持 replace、insert-before、insert-after、delete、update-attributes，并限制重复键、未知字段、尾随 JSON、字节数、操作数、Evidence 数和深度。
- 确定性 Validator 检查当前版本、hash、节点存在、新 ID、顺序、父子关系、循环、孤儿、Evidence、授权范围、Workspace/Resource、必需结构、Metadata 和页码。Prompt Injection 不能改变可信节点授权范围。
- Committer 在写入前再次验证，支持 Patch-hash 幂等重放、同键不同 Patch 冲突、不可变派生 bundle 和确定性 pending embedding。
- PostgreSQL adapter 按 Workspace/幂等键串行化，锁定 Resource 和当前版本，重新检查 base/hash，并原子创建 Version、AST、Node/Mapping、Section、Chunk、Resource Metadata、Commit fact 和 Outbox。
- `patch.validate`/`patch.commit` 只接受 document commit 包内的 canonical backend；ToolRuntime、PolicyEngine 和绑定审批仍为必经边界。
- 新增 append-only、expand-only 迁移 `021_canonical_document_ast.sql`。legacy Markdown/标题字符串路径继续作为回滚路径。

### 2026-08-10：Phase H——EvidenceSet 与生产级检索

- 新增严格 EvidenceSet 1.0，包含 Evidence/Resource/Version/Node 身份、source/content/hash、lexical/vector/fused score、untrusted trust、时间和完整 recall/filter/fusion/rerank/degradation provenance。
- 默认只检索当前版本；历史版本必须显式设置 `include_history=true` 或提供精确 `version_id`。Service 和 PostgreSQL 查询都会重新校验 Workspace/Resource/Version。
- lexical 与 semantic channel 可独立降级；支持版本化 weighted-sum/RRF fusion、可配置权重/阈值/candidate 上限、可选版本化 rerank 和确定性 fallback。
- embedding profile/model/dimension/database vector/index 必须严格匹配；profile/dimension mismatch 明确失败，provider/index 不可用时只能降级到另一个已授权 channel。
- 新增 `022_evidence_retrieval.sql`，包含 ready vector 的 model/dimension/index metadata、retrieval profile、范围索引和 1024 维 cosine HNSW。
- 新增 Workspace-safe PostgreSQL current/history、lexical、semantic 和 vector-type adapter，canonical node ID 是引用定位目标。
- 类型化协议升级为 `retrieval.search@2.0.0`；只有 ToolRuntime 能传入可信 SecurityContext，PolicyEngine 在 backend 前执行 Resource 检查，选定 Tool version 在编排状态中持久化并 fencing。
- 完整 EvidenceSet 过大时保存为 Artifact，模型只看到 set/version/profile 计数和 node-level citation summary；Evidence→Context 继续使用 ContextAssembler 预算。
- 新增 `retrieval-eval-v1` 和 Recall@K、混合检索、长文档、引用/node、工具降级、Prompt Injection、跨 Workspace、维度、fusion/threshold、rerank 和 Artifact 测试。

### 2026-08-10：Phase I——垂直闭环与 Strangler 切流

- 新增可信 `Principal`/`WorkspaceScope` 边界，使用绑定 request/method/path 和时间戳的 HMAC-SHA256 trusted-ingress adapter。Shadow/durable 启动要求精确 Workspace/Resource cohort 和信任配置。
- Stream 与非 Stream assistant 请求统一经过 transport-neutral Turn Pipeline。Durable 请求原子创建 User Message、Turn、Run、初始 typed Step、Event 和 Outbox；相同 Workspace/request/body 可重放，不同内容会冲突。
- 打通 ContextAssembler → typed model decision → EvidenceSet → document node read → typed finding → PatchSet → deterministic validation → external approval → exact CommitPatch → atomic canonical commit → Outbox → public Turn projection。
- SecurityContext adapter 会在每次 ToolRuntime 调用时重新加载不可变 Principal、Workspace、Resource、Run 和 Request 事实；Policy 重新检查有效 membership/role 和 Resource 所有权，所有文档工具与 Committer 必须匹配 `run.resource_id`。
- 审批决定仍在 Model/Tool 表面之外，仅允许签名外部用户且拥有 active owner/admin membership。批准与精确 CommitPatch continuation 原子提交，拒绝与 terminal Run 原子提交；重放幂等，不同决定冲突。
- Runtime/Projection Worker 在启动和周期执行时恢复过期 lease。Outbox projection 使用过滤 claim、lease/generation fencing、指数退避、projection receipt、幂等 replay 和 dead-letter。
- SSE 使用持久化 sequence 和 `Last-Event-ID` 重连，前端只暴露确定且可恢复的 Turn 状态。
- 新增 `legacy | shadow | durable` 精确 cohort 路由。Shadow 只执行一次权威 legacy 写入，再运行只读 typed EvidenceSet 并持久化 result/event/DTO hash 对账，不产生重复 Turn、Run、Tool、Approval、Patch 或 Commit 写入。
- 新请求可立即切回 `legacy`，而 Worker 继续排空依据不可变 `runtime_mode='durable'` 事实启动的 Run。
- 新增 append/expand-only 迁移 `023_agent_runtime_vertical_cutover.sql` 和切流、ingress、部署、恢复、运维、回滚 runbook；迁移仅做静态验证，未执行。

### 2026-08-10：Phase J——评测、运维与旧模块删除门槛

- 新增严格版本化 `agent-runtime-eval-v1` dataset/candidate/report 和无需付费模型的 CLI。12 个 case、13 个 metric 覆盖目标理解、Retrieval Recall/引用/node、Patch fidelity/越权修改、Prompt Injection、长文档、lexical/semantic/Web 降级、Worker 崩溃恢复、request/tool/commit 幂等、审批提交一致性和 Workspace 隔离。
- Report 绑定精确 dataset/candidate SHA-256，每个 case 必须包含 Run、Step、Attempt、ToolCall、ContextManifest 和 EvidenceSet 身份。CI 会运行 gate 并保留 JSON report artifact。
- 新增 Workspace-scoped operations service 和 PostgreSQL adapter，提供完整 Run 诊断、trace index，以及版本化 queue/lease/error/token/cost/Outbox/retrieval/shadow 指标。
- 新增带审计的 cancel、retry 和 dead-letter replay。Retry 只接受明确 retryable 的失败 Step，保留 Attempt 历史并复用下游幂等身份；Replay 保留原 Outbox Event ID，且仅允许两个幂等公共 projection 类型。
- Approval 继续通过签名 trusted-ingress owner/admin API 完成，不提供手工数据库审批路径。
- 新增 append/expand-only `024_agent_runtime_operations.sql` 和 `agent-runtime-ops` 二进制；本地未执行 migration 或数据库连接。
- 新增显式 legacy-removal gate，要求 shadow/durable 数值阈值、零 dead letter/profile mismatch、数据库/ingress/canary/兼容证据、全部 durable 验收场景，以及零生产调用方/配置依赖。
- 审计 ADR 0001 删除清单后确认 `cmd/server/main.go`、task/job/approval wiring、`AGENT_RUNTIME_MODE=legacy`、生产 Compose/README、标题/sub-string 编辑和 legacy Section/Chunk projection 仍可触达，因此删除 gate 正确阻止收缩，本阶段没有删除 legacy 文件或公开行为。

### 2026-08-10：Agent Runtime 最终收口复核

- 完整重读治理、架构、运维、切流、评测、回滚和 ADR 文档，并重新审计 server 构造图、公开 task/approval 路由、assistant legacy adapter、WorkflowRunner、固定四段链、字符串编辑、legacy Section/Chunk、维护命令、配置、Compose、README 和 CI；没有读取仓库 `.env` 或输出任何密钥。
- 将删除保险丝升级为 evidence schema `1.1` / report `legacy-removal-report-v2`：固定 100/99%/0%/20/99% 最低门槛不能降低；shadow 必须记录人工复核数；dead-letter/profile-mismatch 的零值必须有生产指标审计；空 caller/config 列表必须有依赖审计；安全验收和完整故障/一致性场景全部为必需证据。
- 新增只读 `agent-legacy-report` CLI。当前 evidence 和报告分别为 `legacy-removal-evidence.current.json` 与 `legacy-removal-report.current.json`；实测退出码为 `1`，`eligible=false`，包含 23 个生产 caller、6 个配置/CI/部署依赖和 55 个明确 blocker。
- 加固生产证据导出：retrieval profile mismatch 改用稳定 `details.reason_code`，旧的缺少 reason code 或 error category 的 retrieval failure 会 fail-closed 计入阻塞值；`agent-runtime-ops` metrics schema `1.1` 支持精确 Resource、仅统计窗口内 durable Run 状态，并保留该 cohort 的当前队列/审批/Outbox 排空视图。
- 新增只读 `agent-runtime-ops -action comparisons`，按精确 Workspace/Resource、时间窗和有界行数导出 comparison ID、request/Run 身份、hash、details 和时间，供至少 100 条人工复核留档；CLI 不会自动标记 reviewed 或设置任何删除门槛 verified 字段。
- 新增只读离线 `agent-shadow-review`：`template` 将复核清单绑定到 comparison export 原始 SHA-256，`verify` 强制精确 cohort、完整覆盖、ID/request/Run/status/hash 一致、reviewer/time/decision/notes，并对重复、篡改、缺项、争议或疑似 limit 截断 fail closed。输出仅是可转录的 shadow 候选计数，不会修改 removal evidence 或替代外部 reviewer 认证。
- 完整后端回归首次暴露 14 个数据库前置校验错误丢失稳定英文字段标识；保留现有红测并仅恢复 `workspace_id`、`idempotency_key`、`input_hash`、`lease_generation`、`event`、`receipt`、`trusted` 等错误契约，没有改变 SQL、状态机或公开 API。
- 进程环境未设置 `ALLOW_DB_TESTS=1`、`TEST_DATABASE_URL` 或 `TEST_DATABASE_HOST_ALLOWLIST`，安全判定为 `safe_to_connect=false`；没有建立数据库连接、执行 migration/DDL/query/cleanup/backfill/replay。
- 因删除报告不具备资格，本轮没有删除重复传输、WorkflowRunner、固定 Agent 链、assistant.Service、字符串编辑、legacy 投影、配置 adapter 或任何 Schema；回滚路径完整保留。

### Phase 0 与 R1 基础整改

- R0.1：数据库测试只允许共享 helper 读取进程级 `TEST_DATABASE_URL`；禁止读取 `DATABASE_URL` 或生产配置。连接前必须同时满足 `ALLOW_DB_TESTS=1`、数据库名 `_test` 后缀和显式 host allowlist。
- R0.2：新增 `APP_ENV`、`CORS_ALLOWED_ORIGINS` 和生产 fail-closed 校验；拒绝 wildcard、非 HTTP(S)、含凭据或 path/query/fragment 的 Origin。生产 backend 只绑定 `127.0.0.1`，必须经过受保护反向代理。
- R0.3：CI 新增前端测试并保留 lint/build/image job；README 与 CI 命令同步，治理文档不再被 ignore。
- R1.1：新增 migration ledger、SHA-256 checksum、advisory lock、drift 检测和显式 `cmd/migrate`；server、reindex、repair 不再隐式跑 migration；部署顺序改为一次性 migrator 成功后再启动服务。
- R1.2：新增 provider-neutral User、Organization、Workspace、Membership 和 principal audit schema，旧表只增加 nullable scope 字段和部分索引；没有切换旧读写或执行 backfill。

## 验证结果

### 2026-08-11 durable-only 全量切流

- 显式清除 `ALLOW_DB_TESTS`、`TEST_DATABASE_URL`、`TEST_DATABASE_HOST_ALLOWLIST` 后，`go test ./apps/server/... -count=1` 通过；PostgreSQL 用例在连接工厂前安全跳过。
- `go vet ./apps/server/...` 与 `go build ./apps/server/cmd/server` 通过。
- 前端受控并发回归通过 29 个测试文件、104 个用例；`npm run lint` 无警告或错误，`npm run build` 通过，旧 `/tasks/*`、`/approvals` bundle 仅保留 146 B redirect page。

### 2026-08-11 durable 前端运行与审批查询

- `go test ./apps/server/... -count=1`、`go vet ./apps/server/...` 和 `go build ./apps/server/cmd/server` 通过；查询 Handler 覆盖可信 Workspace/filter/limit、无签名拒绝和内部 Runtime payload 不公开，SQL 静态测试覆盖 Workspace scope、稳定排序与上限。
- `npm test -- --run` 通过 30 个测试文件、104 个用例；`npm run lint` 无警告或错误，`npm run build` 通过并生成 `/runs`、`/runs/[id]`、`/approvals` 页面。
- `git diff --check` 通过。未连接数据库；数据库 round trip 与 protected-ingress 互操作仍按下述发布门禁处理。
- 默认 Vitest fork pool 曾在断言无失败后出现一次 Windows worker 意外退出；使用 `--pool=threads --maxWorkers=2 --minWorkers=1` 完整重跑通过，未隐藏测试失败。
- production server 直接 caller 审计未发现 legacy/shadow router、LegacyAssistantRunner、WorkflowRunner、Planner/Reviewer/Editor、Task/Job/legacy Approval 构造；可达 page 审计未发现旧 task/approval/job/task-context/confirm API 请求。
- `git diff --check` 与 `git diff --cached --check` 通过，仅有工作区既有 LF→CRLF 提示。
- 未读取仓库 `.env`，未连接数据库，未执行 migration、DDL、backfill、reindex、repair、replay 或数据删除。

### Phase J / 最终门禁

- TDD 红测先暴露缺失的评测协议、scorer、完整 trace report、运维诊断/动作、retry/replay 安全规则、retrieval degradation summary 和 legacy-removal evidence gate；随后逐项实现。
- 在显式移除 `ALLOW_DB_TESTS`、`TEST_DATABASE_URL` 和 host allowlist 后，`go test ./apps/server/...` 与 `go test -race ./apps/server/...` 均通过；所有 PostgreSQL 集成测试在创建连接前安全跳过。
- `go vet ./apps/server/...` 通过。
- `go build ./apps/server/cmd/server ./apps/server/cmd/migrate ./apps/server/cmd/agent-runtime-ops ./apps/server/cmd/agent-eval ./apps/server/cmd/agent-legacy-report` 通过。
- 无付费模型的离线 gate 通过全部 12 个 case 和 13 个 metric 阈值，生成 `agent-runtime-eval-report-v1`，并绑定精确 dataset/candidate SHA-256。
- 前端回归通过：29 个测试文件、119 个测试；lint 无警告或错误；Next.js production build 通过。
- CI YAML 可解析；Phase J 范围内 25 个新增/修改 Go 文件均符合 `gofmt`；没有尾随空白；`git diff --check` 和 `git diff --cached --check` 通过，仅有 LF→CRLF 提示。
- 删除审计确认仍有 live legacy import/constructor 和 `AGENT_RUNTIME_MODE=legacy` 配置依赖，因此 removal gate 正确返回 ineligible，没有删除模块。
- 最终收口复核：`go test ./apps/server/... -count=1`、`go test -race ./apps/server/... -count=1`、`go vet ./apps/server/...`、四个生产命令 build 和 `agent-legacy-report` build 通过；数据库集成用例均在连接前安全跳过。
- `agent-runtime-eval-v1` 再次通过 12/12 case、13/13 metric；前端 29 个测试文件、119 个测试、lint 和 production build 通过。
- 当前 `legacy-removal-report-v2` 已与 CLI 输出逐字段比对，`eligible=false`、55 个 blocker；因此没有进入任何单项 legacy 删除循环。
- 本轮证据导出加固后再次通过全后端 test/race/vet、五个命令 build 和 diff check；未连接数据库，删除报告仍为 `eligible=false` 且与当前签入报告一致。
- 离线 shadow review bundle 加固后再次通过全后端 `go test ./apps/server/... -count=1`、全后端 race、`go vet`、server/migrate/runtime-ops/eval/legacy-report/shadow-review 六个命令 build 和 diff check；数据库保险丝保持关闭，删除报告仍为 `eligible=false`、55 个 blocker，且与当前签入报告一致。

### 各阶段回归证据

- Phase B：Runtime lifecycle、错误分类、Run/Step/Tool/Outbox 幂等、`SKIP LOCKED`、lease fencing、Attempt、ContextManifest 和 migration 静态约束测试通过。
- Phase C：结果 Schema、waiting resume、稳定 retry idempotency、周期恢复、遗弃 Attempt、取消唤醒、预算、遥测、shadow wiring、race、vet、server/migrator build 和前端回归通过。
- Phase D：migration 018、TurnCoordinator、首 Turn scope、atomic outcome、Evidence trust、manifest reproduction、stream replay、race、vet/build 和前端回归通过。
- Phase E：ToolRuntime、migration 019、非法 JSON、Schema、Resource deny、伪造审批、timeout、cancel audit、retry/terminal、rate limit、provenance、Artifact、敏感 Web 查询、provider 错误和 Prompt Injection 测试通过。
- Phase F：严格 Decision/node、ActionValidator、Supervisor、trace、Observation、Approval continuation、Model failure taxonomy、shadow comparison、race、vet/build 和前端回归通过。最终自审补充 canonical Observation hash、Validated Patch→Approval 确定性路由、跨 Step 审批绑定和拒绝审批 terminal 测试。
- Phase G：稳定 node ID、不同节点防碰撞、Markdown/DOCX/PDF、PDF 页码、大文档、AST 派生 Section/Chunk、严格 Patch、五类操作、版本/hash/授权/引用/scope/结构/cycle/orphan/page、Prompt Injection、Commit 幂等冲突、原子回滚、结构保持和 canonical Tool policy 测试通过。
- Phase H：current/history、lexical/semantic 双向降级、fusion/threshold、rerank、embedding profile/dimension/vector mismatch、跨 Workspace、citation/node、Prompt Injection、Policy-before-backend、Artifact summary 和 Context budget 测试通过；`retrieval-eval-v1` 的 Recall@K 全部通过，包括 40 个干扰项的长文档场景。
- Phase I：trusted identity/request binding、精确 cohort、Stream/非 Stream 收敛、request/Turn/Run replay、持久化事件续传、只读 shadow、comparison、lease recovery、projection retry/dead-letter/idempotency、外部审批、Run/Resource 授权、前端重连和 ready-resource binding 测试通过；全后端 test/race/vet/build 和前端 29 文件/119 测试通过。
- Phase A/R0/R1：数据库保险丝、CORS、migration ledger、identity expand、Compose 解析、PowerShell 测试、配置/router 测试、Go build/vet 和前端回归通过。

## 未执行的测试与验证

- 未执行 migration 017–024 或任何真实 PostgreSQL round trip，因为没有同时提供 `ALLOW_DB_TESTS=1`、明确安全的 `TEST_DATABASE_URL` 和 host allowlist；没有建立连接、执行 DDL、migration、query、cleanup、backfill 或 replay。
- 因此尚未实测：canonical document transaction、HNSW 创建/查询计划、vector catalog compatibility、current/history SQL、Turn/Run 原子接受、trusted scope query、Approval continuation/rejection、canonical commit、comparison、projection receipt、Outbox claim/retry/dead-letter、lease recovery、stream restart、operations diagnose/metrics/cancel/retry/audit/replay 和跨 Workspace SQL 隔离。
- 未执行 production protected-ingress、reverse proxy interoperability、历史 Workspace/canonical 数据迁移与核对、durable canary、付费 provider 质量评测、多副本 lease/load、代表性文档性能、事故演练或真实部署。
- Docker Desktop Linux engine 不可用，因此未完成 Server/Web Docker image build；原生 Go 和 Next.js production build 已通过。
- Windows 环境没有 Bash，未执行 `sh -n scripts/deploy/remote-deploy.sh`；相关修改仅人工复核。
- GitHub-hosted CI 本身未在该工作区运行，只有本地 YAML 解析和等价命令验证。

## 剩余风险

- Migration 021–024 及其 locking、isolation、query plan、rollback 只有静态/单元覆盖，必须在授权 `_test` PostgreSQL 或 CI 上验证后才能启用。
- `agent-runtime-eval-v1` 是确定性最小回归集，不代表付费 provider 质量、生产语料分布、多语言、延迟、容量或真实查询分布。
- `agent-runtime-ops` 只能运行在经过认证和访问控制的运维边界内；`operator-id` 只是审计身份，不是认证机制，平台仍需最小权限数据库凭据。
- Legacy workflow、固定四段 Agent、旧 Task/Job/Approval 与 shadow router 源码仍存在，但不再由 production server 构造或注册；物理删除前仍需重新生成 caller/config 审计和 removal report。
- HMAC adapter 只是窄 trusted reverse-proxy 边界，不是最终身份提供商。代理必须剥离公开 identity header、保护并轮换 secret、保持时钟同步并签名精确 request tuple。
- Runtime/Projection Worker 是以持久化事实为基础的进程内 poller；容量、多副本 lease 竞争、dead-letter 告警、队列延迟、模型/provider 限额仍需 staging canary。
- Durable 请求在 Turn/Run/Tool/Commit 层幂等。当前版本不支持配置级 legacy fallback；回滚只能摘除流量、排空 durable 事实后部署上一已验证 release。
- 现有上传仍通过兼容 ingest/resource API；它不会自动补齐历史 Workspace 归属或 canonical AST。正式发布前必须实现或执行受控 importer/backfill/reconciliation，否则新 Runtime 会对这些资源失败关闭。
- 既有 embedding 早于 Phase H profile/model/dimension metadata，严格 semantic path 会排除这些记录，直到经过授权的 dual-write/backfill/reconciliation。
- DOCX/PDF 当前边界消费可信 UTF-8 extractor 输出并生成确定性兼容文本，不等同于完整生产二进制渲染；正式切换前需要代表性 fixture 对账。
- Importer 对相同源结构可产生稳定 ID，Patch 应用后也稳定；全新导入且结构重排时，位置型 ID 可能变化，必须显式 reconciliation。
- 内置 JSON Schema validator 是 fail-closed 的受支持子集；需要未支持 Draft 2020-12 keyword 的工具必须扩展实现并补测试，禁止弱化 Schema。
- Tool Attempt timeout 依赖 backend 响应 Context cancellation；未来忽略 cancellation 的 backend 必须在评审和测试中拒绝。
- 固定窗口 rate-limit bucket 在高流量启用前需要运维 retention job。
- `agent_runs.workspace_id` 为 Strangler 兼容仍允许 null；完成 identity dual-write、reconciliation 和 policy gate 前不能切换强制租户约束。
- 现有数据库第一次显式运行 migrator 时会重放历史幂等迁移以建立 ledger；生产前必须在授权副本验证该兼容路径。
- 生产可用性依赖部署主机提供到 `127.0.0.1:${SERVER_PORT}` 的受保护反向代理。

## Phase 0 门槛

- R0.1 测试数据库隔离：已满足。
- R0.2 CORS allowlist、`PATCH`、生产 fail closed 和直接网络隔离：已满足。
- R0.3 前端 CI 与治理文档跟踪：已满足。
- 安全允许的验证和完整 diff 自审：已满足；环境限制已在上文明确记录。
- 实际数据库/Schema 变更：未执行。
- 公共 API/产品语义变更：除必需的 CORS 与部署安全边界外，没有切换。

## 下一步

## Python + LangGraph 重写方案

已整理完整的 Python 后端重写方案，见 [`python-langgraph-rewrite-plan.md`](./python-langgraph-rewrite-plan.md)。该方案仅记录架构和迁移计划，不代表已开始 Python 实现、数据库迁移、backfill、生产切流或 Go 代码删除。

方案的关键边界是：LangGraph 负责 `Understand -> Decide -> Act -> Observe` 决策图、图状态和受控 checkpoint；项目自己的 Runtime、PostgreSQL Storage、Policy、ToolRuntime、Approval、Canonical Commit、Outbox 和 Projection 继续作为业务事实与可靠性边界。Next.js 前端、公开 API/SSE 契约和现有数据库模型保持兼容。

2026-08-12 已将重写范围收缩到当前生产实际使用的 Go 代码：以 `cmd/server` 构造图、当前注册路由、前端调用、部署必需能力和 durable 请求闭包为准，并要求 Phase 0 生成 active-scope 清单。未被生产装配的 legacy Task/Job/Approval、旧 Agent 编排、legacy/shadow router 和无活跃调用证据的维护 CLI 不迁移；Go migrator、operations 和 evaluation 工具可在过渡期继续使用。Python canary 通过受保护入口单写切流，不恢复 `AGENT_RUNTIME_MODE=legacy|shadow`，同一请求禁止 Go/Python 双重产生 Tool、Approval、Commit 或 Outbox 写入。

后续如正式启动该重写，必须先完成契约冻结和独立 Python 基础服务，再按 read-only、离线 parity、入口单写 durable canary 的顺序推进。当前 Phase 1 gate、数据库授权、protected ingress、历史资源 reconciliation 和 legacy removal gate 仍然有效。

Agent Runtime 主线：

1. 在 CI 或授权 `_test` 数据库显式设置 `ALLOW_DB_TESTS=1`、只含 `_test` 数据库的 `TEST_DATABASE_URL` 和精确 `TEST_DATABASE_HOST_ALLOWLIST`，再运行 `go test ./apps/server/... -count=1`，保留 migration 021–024、canonical/retrieval/Turn/Approval/Projection/Operations round trip、HNSW/catalog/query-plan 和跨 Workspace SQL 证据。任一保险丝条件不满足时禁止运行。
2. 设计并评审历史 Resource 的 Workspace/Organization 归属与 canonical document importer/backfill/reconciliation；只在获授权环境按 expand → dual write/backfill → verify → switch read → contract 执行。
3. 配置并验证受保护 ingress 的 header stripping、签名、时钟、membership 和 Resource ownership；随后执行 durable canary，演练 Worker lease、request/tool/commit 重放、Stream 断连、审批通过/拒绝/重复决定、Patch 冲突、Outbox replay、租户隔离和 Canonical AST/Section/Chunk/page/metadata/embedding 对账。
4. 使用 `agent-runtime-ops` 证明所有 canary Run terminal、Outbox 无 dead letter、retrieval profile mismatch 为 0，并验证入口摘流 → durable drain → 上一 release 的真实回滚流程；禁止把模式改成 legacy 作为当前版本回滚方案。
5. 重新审计 caller/config，更新 `legacy-removal-evidence.current.json` 并运行 `agent-legacy-report`。只有退出码为 0、`eligible=true` 且 caller/config 为 0，才能一次删除一个历史目标；Schema 收缩另建阶段和 append-only migration。

禁止伪造 cohort、降低阈值、把本地 fixture 当生产证据、停止 Worker 来制造空队列、未验证 backfill、合同迁移或批量删除。

更广泛的 R1.3 主线：确定最终 identity provider/token-validation 协议，并决定保留还是替换 Phase I 的窄 trusted-ingress adapter。不得自动开始 R1.4 或 R1.5。

## Phase 1 进度

- R1.1 migration ledger 和离线生命周期：已实现并通过安全本地验证；授权数据库和 Docker/CI 验证仍受环境限制。
- R1.2 identity/tenancy expand：已实现并通过安全本地验证；migration 未执行。
- R1.3 Principal/authentication adapter/WorkspaceScope：全量 durable 请求使用的窄签名 trusted-ingress adapter 已实现；最终 identity provider 和公共认证决策仍未完成。
- R1.4 ACL/quota/rate-limit/audit enforcement：未开始，依赖 R1.3。
- R1.5 dual-write/backfill/reconciliation/read-switch/cross-tenant gate：未开始，依赖 R1.3/R1.4 和显式数据库授权。
- Phase 1 gate：未满足；该阶段主动暂停，Phase 2 未开始。

## 回滚

- 当前版本配置只接受 durable；设置 `AGENT_RUNTIME_MODE=legacy|shadow` 会启动失败，不能作为流量回滚。
- 立即回滚：先在可信入口摘除新 Agent 流量，保持当前 Runtime/Projection Worker 运行，直到已接受 Run terminal、Outbox 完成且无 dead letter，再部署上一已验证 release。
- 保留 trusted-ingress 配置、扩展 Schema、Turn/Run/Step/Attempt/Tool/Approval/Commit/Outbox/Projection/Comparison 和 operator-action audit；禁止删除审计行、发明新幂等键或重放任意/commit 事件。
- Phase H migration 前：从候选构建移除 migration 022 和 EvidenceSet/Retrieval repository/tool upgrade；legacy retriever、公共 API、默认读取和流量不变。迁移后回滚只停止新 backend/worker 并恢复 legacy，不删除 HNSW、profile、Artifact 或 audit 事实。
- Phase G migration 前：移除 migration 021 和 canonical document package/tool adapter；legacy ingestion、字符串编辑、读取、API 和流量不变。迁移后停止 canonical writer 并恢复 legacy，保留 AST/Node/Projection/Commit/Outbox 事实以及 `resource_versions.content` 兼容投影。
- Phase F/E/D/B/C 的历史代码回滚必须部署匹配 release，并保留所有增量表和审计事实；不得通过删除历史或新幂等键重放写操作来回滚。
- R0.1：仅回滚测试 helper/CI 配置，不涉及生产数据。R0.2 回滚会重新暴露 wildcard CORS 或公共 backend，应只用于诊断。R0.3 只影响 CI/README。
- R1.1 migration 失败：不得启动 server/web；事务会回滚 pending batch 和 ledger。修复必须新增 migration 文件，禁止编辑已应用迁移。R1.2 migration 执行后只允许应用层忽略新增表/nullable 字段，破坏性 Schema 收缩必须另行评审。
