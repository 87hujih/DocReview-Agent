# End-to-End Flow Closure 代码 Review 问题清单

生成日期：2026-04-13

关联变更总结：`docs/2026-04-13-end-to-end-flow-closure-change-summary.md`

审查范围：

- 主工作区 `main` 已落地的上传、助手、任务、审批、资源基础链路。
- worktree 分支 `feat/end-to-end-flow-closure` 相对 `main` 的后续闭环变更。
- 重点链路：原文件上传、文档解析、任务审批执行、原文件下载、资源修订结果查看与导出。

## 结论

当前闭环分支已有基础功能和测试覆盖，但仍存在几个会影响真实端到端体验的问题。最需要优先修复的是下载 URL 指向错误、文档解析格式与执行器能力不匹配、审批执行队列不可靠这三类问题。

## 问题汇总

| 优先级 | 问题 | 影响 |
| --- | --- | --- |
| Important | 前端下载链接使用相对 `/api/...`，没有走后端 API base URL | 本地 `web:3000` 点击下载会请求前端服务，导致原文件下载和修订结果导出失败 |
| Important | 上传支持 `.txt/.docx/.pdf` 等格式，但执行器只识别 Markdown 二级标题 | 非标准 Markdown 或 Tika 纯文本输出审批后容易执行失败 |
| Important | 审批通过到 job 执行不是可靠队列，状态更新也没有事务保护 | 任务可能卡在 `executing` 或 pending job 永久无人消费 |
| Important | 解析后空正文没有被拒绝 | 空 PDF、扫描件、受保护文档可能被当成有效资源入库，后续检索和任务执行失败 |
| Minor | 资源导出 `Content-Disposition` 手写拼接文件名 | 中文标题或特殊字符可能导致下载文件名乱码或响应头异常 |

## 详细问题

### 1. 下载入口在真实前端环境会打到错误服务

证据：

- `apps/web/lib/api/client.ts` 通过 `NEXT_PUBLIC_API_URL` 构造普通 API 请求，默认后端是 `http://127.0.0.1:18080`。
- `apps/web/lib/api/files.ts` 返回 `/api/files/${fileId}/download`。
- `apps/web/lib/api/resources.ts` 返回 `/api/resources/${encodeURIComponent(id)}/export`。
- `apps/web/next.config.mjs` 只有 `output: "standalone"`，没有 `/api` rewrite。
- 下载入口来自：
  - `apps/web/components/assistant/assistant-message-list.tsx`
  - `apps/web/app/tasks/[id]/page.tsx`
  - `apps/web/components/resource-version-viewer.tsx`

影响：

本地开发约定是前端 `http://127.0.0.1:3000`，后端 `http://127.0.0.1:18080`。页面点击下载时会访问前端服务的 `/api/...`，而不是后端服务，因此原文件下载和修订结果导出会失败。

建议修复：

- 把下载 URL 构造逻辑统一接入 API base URL。
- 下载链接建议使用普通 `<a>`，避免 `next/link` 对外部下载 URL 做页面导航语义。
- 补测试覆盖 `NEXT_PUBLIC_API_URL` 存在时生成完整后端 URL。
- 增加浏览器点击验证，确认下载请求实际命中后端。

### 2. 文档解析支持范围和执行器能力不匹配

证据：

- 前端上传提示支持 `.md,.txt,.doc,.docx,.pdf,.rtf,.odt`。
- 后端 `documentparser.IsSupportedFileName` 也接受这些扩展名。
- `DOCUMENT_PARSER=tika` 时，Tika 解析结果会被保存为资源当前版本。
- 执行器 `apps/server/internal/agent/executor/agent.go` 的 `extractSectionTitle` 只识别 `## ` 开头的 Markdown 二级标题。
- 当前 smoke 测试只用带 `## 第一章` 的 `.md` 文件，没有覆盖 `.txt` 或 Tika 输出的纯文本场景。

影响：

用户上传 `.txt`、普通 Word、PDF 或 Tika 解析后的纯文本时，即使入库成功，后续任务审批执行也可能找不到 diff 对应章节，最终任务失败。这个问题会让“支持多格式上传”和“端到端闭环”在真实文件上不一致。

建议修复：

- 导入阶段把解析文本规范化为稳定 Markdown 结构，例如根据文件名生成标题，并按段落或 Tika metadata 生成二级章节。
- 或者增强执行器，支持纯文本 fallback、一级/三级标题，以及基于 `original` 片段的替换策略。
- 补测试：
  - `.txt` 无二级标题的任务执行路径。
  - Tika 返回纯文本后的任务执行路径。
  - diff section 找不到标题但 `original` 能匹配时的 fallback。

### 3. 审批执行链路不是可靠队列

证据：

- `apps/server/internal/approval/service.go` 中 `Approve` 依次执行：
  - 更新 approval 为 `approved`。
  - 创建 pending job。
  - 更新 task 为 `executing`。
  - 通过 channel 通知 worker。
- 上述数据库操作不在同一个事务中。
- `enqueueJob` 使用非阻塞发送，channel 满了只记录日志，不向调用方返回失败。
- `apps/server/internal/job/worker.go` 的 worker 只有收到 channel 信号后才 `ClaimNext` pending job。

影响：

如果服务在状态更新中途失败，或 channel 满导致信号丢失，或服务重启后已有 pending job，任务可能永久停在不一致状态。用户在任务详情页只会看到任务没有继续完成。

建议修复：

- 将审批状态、job 创建、任务状态更新放进同一个事务。
- worker 启动时主动扫描 pending jobs。
- 增加周期性 claim 或可靠唤醒机制，不能只依赖内存 channel。
- channel 投递失败时要有可观测的业务状态或补偿路径。
- 补测试覆盖：
  - worker 重启后能处理已有 pending job。
  - channel 满时任务不会永久卡住。
  - approve 中途失败不会留下 approved 但 task 未 executing 的状态。

### 4. 解析后空正文没有被拒绝

证据：

- `apps/server/internal/knowledge/ingest/service.go` 只检查原始上传 `Content` 是否为空。
- 当 parser 返回空字符串时，代码仍会调用 `saveDocument` 创建资源和版本。
- 对扫描 PDF、图片型 PDF、受保护文档、空文档，Tika 可能返回空正文。

影响：

系统会把无法解析出正文的文件显示为已入库资源，但资源没有有效检索内容，后续创建任务、检索证据和执行修订都会出现后置失败。

建议修复：

- parser 返回后检查 `result != nil`。
- 检查 `strings.TrimSpace(result.Text) != ""`。
- 返回明确错误，例如“未从文件中解析出可导入正文”。
- 补测试覆盖 parser 返回空正文、nil result 的场景。

### 5. 资源导出响应头文件名拼接不够稳健

证据：

- `apps/server/internal/server/handlers/resources.go` 使用 `fmt.Sprintf` 手写 `Content-Disposition`。
- 已有原文件下载 handler 使用 `mime.FormatMediaType`，资源导出没有复用同样策略。

影响：

资源标题包含中文、引号、控制字符或其他特殊字符时，浏览器下载文件名可能乱码、截断，甚至造成响应头解析异常。

建议修复：

- 使用 `mime.FormatMediaType("attachment", map[string]string{"filename": name})`。
- 继续保留路径分隔符和危险字符清理。
- 额外过滤控制字符。
- 补包含中文标题、引号、换行字符的导出响应头测试。

## 验证记录

已运行 targeted 验证：

```bash
go test ./apps/server/internal/document/parser ./apps/server/internal/knowledge/ingest ./apps/server/internal/agent/executor -count=1
go test ./apps/server/internal/approval ./apps/server/internal/job -count=1
npm test -- --run components/assistant/assistant-message-list.test.tsx lib/api/files.test.ts lib/api/resources.test.ts components/resource-version-viewer.test.tsx components/task-detail-page.test.tsx
```

结果：

- Go targeted 测试通过。
- 前端 targeted 测试通过。
- 前端测试第一次在沙箱内因 esbuild `spawn EPERM` 失败，已按权限要求在沙箱外重跑通过。

未覆盖或仍需补充的验证：

- 浏览器点击下载，确认请求命中后端而不是 Next 前端。
- `.txt`、Tika 解析 Word/PDF 后的任务审批执行闭环。
- worker 重启后 pending job 自动恢复。
- parser 返回空正文时拒绝入库。

## 建议修复顺序

1. 先修下载 URL，成本低且直接影响用户可见闭环。
2. 再修解析正文空值校验，避免继续产生不可用资源。
3. 修文档结构规范化或执行器 fallback，让多格式上传真的能走到任务执行完成。
4. 加强审批/job 队列可靠性，避免生产或长时间本地运行时任务卡死。
5. 最后处理导出响应头文件名编码与测试补齐。
