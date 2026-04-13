# 文档导入链与助手会话链巡检结论

## 背景

本文基于根目录 `链路分析.md`，对下面两条后端链路做了一次实际运行验证和代码巡检：

1. 文档导入链
2. 助手会话链

目标是回答三件事：

1. 哪些环节已经跑通
2. 哪些环节没有跑通
3. 哪些地方存在真实的代码级 bug 或结构性风险

本次只做验证和 review，不修改业务实现。

## 巡检范围

- 文档导入链
  - `apps/server/internal/assistant/importer.go`
  - `apps/server/internal/knowledge/ingest/service.go`
  - `apps/server/internal/knowledge/chunker/chunker.go`
  - `apps/server/internal/knowledge/embedder/embedder.go`
  - `apps/server/internal/storage/postgres/resources.go`
- 助手会话链
  - `apps/server/internal/server/handlers/assistant.go`
  - `apps/server/internal/assistant/service.go`
  - `apps/server/internal/assistant/chat_responder.go`
  - `apps/server/internal/storage/postgres/assistant.go`

## 验证环境

- 仓库：`G:\gofile\Agent_Project`
- 日期：`2026-04-13`
- 后端入口：`apps/server/cmd/server/main.go`
- 当前 `.env` 指向：
  - 远程 PostgreSQL：`106.52.42.194:5432`
  - 远程 SiliconFlow

额外说明：

- 在沙箱内直接访问远程 PostgreSQL 会被系统权限拦住，所以涉及真实数据库和上游模型的验证采用了提权运行
- 单元测试和 handler 测试只证明本地 mock 边界可用，不能替代真实链路 smoke

## 总结

| 链路 | 结果 | 结论 |
| --- | --- | --- |
| 文档导入链 | 部分跑通 | Markdown 上传导入可用，但这条链并没有真正支持二进制文档解析 |
| 助手会话链 | 部分跑通 | 没有资源上下文时可用，一旦会话出现 `session_file`，后续消息补全就会失败 |

## 2026-04-13 修复后更新

基于本文识别出的 blocker，后续已完成一轮修复与回归，当前状态更新如下：

- 助手会话链主路径已恢复：
  - `chat_responder.go` 不再发送第二条 `system` message，而是把运行时资源上下文并入首条系统提示
  - `go test ./apps/server/internal/assistant -run TestBuildChatMessages -count=1` 通过
  - `go test ./apps/server/internal/assistant ./apps/server/internal/server/handlers -count=1` 通过
  - 真实 smoke 已验证 `CreateConversationStream -> UploadFile -> AppendMessageStream`，返回 `message_started -> message_delta -> message_completed -> task_suggestion -> done`

- parser 边界已落地：
  - 已新增 `apps/server/internal/document/parser/*`
  - 已接入 `DOCUMENT_PARSER`、`TIKA_URL`、`TIKA_TIMEOUT_MS`
  - 默认 `DOCUMENT_PARSER=text` 时只支持 `md/txt`
  - `go test ./apps/server/internal/document/parser ./apps/server/internal/config ./apps/server/internal/knowledge/ingest -count=1` 通过

- 导入写入一致性已改善：
  - `ingest.Service` 现在先完成 chunk / embedding 计算，再进入数据库写入
  - `ResourceRepo.CreateDocumentGraph()` 已把资源、版本和 chunks 收敛到单事务
  - 定向集成测试 `TestCreateDocumentGraphRollsBackOnChunkFailure` 提权后通过

- 目录导入自愈已补齐第一阶段：
  - `ImportDirectory()` 不再只按标题跳过
  - 现在会优先按 `source_ref` 识别资源
  - 对历史 title 命中的旧资源会回填 `source_ref`
  - 对“有 version 但无 chunk”的资源会追加 repair version 重建索引

- Tika 真人联调已补齐：
  - 已用本机 Java 启动 Apache Tika Server 3.3.0 jar
  - 已在 `DOCUMENT_PARSER=tika` 下完成 `docx` 与 `pdf` 上传、解析入库和资源检索 citation smoke
  - smoke 产生的临时 `uploaded_files`、`assistant_sessions`、`resources` 记录已清理

## 已跑通的部分

### 1. Markdown 文档导入主链路可用

已验证：

- `go test ./apps/server/internal/knowledge/retriever -run TestSearchIntegration -count=1 -v`
- 真实 smoke：
  - 创建会话
  - 上传 Markdown 文件
  - 检索刚导入资源

真实上传的资源如下：

- `resource_id = 2121ccae-748c-427a-a4fb-6bf46608ac83`
- `title = 蓝鲸考勤制度`

随后请求：

```text
GET /api/resources/2121ccae-748c-427a-a4fb-6bf46608ac83/search?q=蓝鲸考勤
```

返回：

```json
{
  "query": "蓝鲸考勤",
  "citations": [
    {
      "citation_id": "cite_1",
      "resource_id": "2121ccae-748c-427a-a4fb-6bf46608ac83",
      "section_title": "考勤要求",
      "snippet": "员工每天需要在 9 点前签到，测试词：蓝鲸考勤样例。"
    }
  ]
}
```

这说明下面这段链路对 Markdown 是通的：

`UploadFile -> assistant/importer.go -> ingest.ImportDocument -> chunker.ChunkMarkdown -> embedder.Embed -> ResourceRepo.Create/CreateVersion/CreateChunk`

### 2. 没有资源上下文时，助手流式会话链可用

真实请求：

```text
POST /api/assistant/conversations/stream
{"message":"你好，请简单介绍一下你能做什么"}
```

返回了完整 SSE 事件序列：

- `session_created`
- `message_started`
- 多个 `message_delta`
- `message_completed`
- `done`

而且 assistant 回复已成功持久化到 `assistant_messages`。

## 没跑通的部分

### 1. 上传文件后继续对话没有跑通

这是当前最关键的问题。

#### 现象

同一个会话在上传文件后，再发下一条消息：

```text
POST /api/assistant/sessions/:id/messages/stream
{"message":"请根据这份资料整理成任务，并说明考勤要求"}
```

返回：

```text
event: error
data: {"code":"assistant_stream_failed","message":"助手流式回复失败，请重试。"}
```

如果走非流式接口：

```text
POST /api/assistant/sessions/:id/messages
```

则返回：

```json
{"error":"追加消息失败"}
```

状态码：`500`

#### 落库结果

这一步之后，会话里只追加了用户消息，不会落 assistant 回复，也不会落 `task_suggestion`。

也就是说，当前链路实际停在：

`AppendMessage / AppendMessageStream -> responder 调用上游模型失败`

#### 根因

根因在 `apps/server/internal/assistant/chat_responder.go:134-146`。

当前 `buildChatMessages()` 组包逻辑是：

1. 第一条 `system` message：固定系统提示
2. 第二条 `system` message：运行时资源上下文 `buildRuntimeContext()`
3. 然后再拼历史消息和当前用户消息

问题在于 SiliconFlow 当前不接受“第二条 system message”。  
直接按同样请求体调用上游接口，返回：

```json
{"code":20015,"message":"System message must be at the beginning.","data":null}
```

因此：

- 没有资源上下文时，只有一条 `system` message，链路能跑通
- 一旦 `latestResourceFromMessages()` 命中 `session_file`，`buildRuntimeContext()` 生效，请求就会变成“两条 system”，上游直接拒绝

#### 代码定位

- `apps/server/internal/assistant/chat_responder.go:134`
- `apps/server/internal/assistant/chat_responder.go:139`
- `apps/server/internal/assistant/chat_responder.go:155`
- `apps/server/internal/assistant/service.go:178`
- `apps/server/internal/assistant/service.go:258`
- `apps/server/internal/assistant/service.go:616`

#### 影响

- 上传文件后，助手不能继续基于资源回答问题
- 上传文件后，助手不能再生成 `task_suggestion`
- “助手上传文件 -> 继续追问 -> 建议建任务”这条用户主路径当前是断的

## 代码级问题与风险

### 1. 当前“文档导入链”其实只支持文本输入，不是真正文档解析

`apps/server/internal/assistant/importer.go:20-24` 只是把原始 `[]byte` 直接传给 `ingest.ImportDocument()`。

而 `apps/server/internal/knowledge/ingest/service.go:99-101` 会直接：

- `string(input.Content)`
- 提取标题
- 分块
- 向量化

这意味着当前真实支持的是：

- `md`
- `txt`
- 其他本身就是纯文本的内容

而不是：

- `pdf`
- `doc`
- `docx`
- `odt`

如果把二进制文件直接走这条链，正文大概率会变成乱码或脏文本，chunk 和 embedding 结果也会失真。

### 2. `saveDocument()` 非事务，可能留下半成品资源

`apps/server/internal/knowledge/ingest/service.go:148-195` 的顺序是：

1. `CreateWithSourceRef`
2. `CreateVersion`
3. `Embed`
4. 循环 `CreateChunk`

但整个过程没有事务，也没有补偿。

因此只要在第 3 或第 4 步失败，就会留下：

- `resources` 已有记录
- `resource_versions` 已有记录
- `resource_chunks` 部分缺失，甚至为 0

这会直接制造“详情能看、检索为空”的脏数据。

### 3. `ImportDirectory()` 只按标题跳过，不能自愈历史坏数据

`apps/server/internal/knowledge/ingest/service.go:119-127` 当前逻辑是：

- 读取已有资源列表
- 只要发现 `resource.Title == title`
- 直接 `跳过已导入文件`

问题是它不会继续检查：

- 当前资源是否有 version
- 当前资源是否有 chunk
- chunk 是否完整
- 文件内容是否已经变化

因此启动导入不具备这些能力：

- 修复历史半成品资源
- 补建缺失 chunk
- 根据源文件变化重建索引

## 建议动作

### P1

1. 修复 `chat_responder.go` 的消息组装方式  
   不要再给 SiliconFlow 发送第二条 `system` message。运行时资源上下文应并入首条系统提示，或改写为普通历史消息。

2. 给“上传文件后继续对话”补真实回归测试  
   当前测试大多是 fake responder，没有覆盖上游协议约束。

### P2

3. 给导入链补 parser 边界  
   在 `assistant/importer.go` 或 `ingest.Service` 前加 parser 层，不要再把二进制 bytes 直接当正文。

4. 让 `saveDocument()` 具备事务或补偿能力  
   至少保证 `CreateVersion` 和 `CreateChunk` 不会把库写成半成品状态。

### P3

5. 调整 `ImportDirectory()` 去重与自愈逻辑  
   不能只按 title 跳过。至少需要支持“已存在资源但无 chunk”的补建路径。

## 本次主要验证命令

```text
go test ./apps/server/internal/knowledge/ingest ./apps/server/internal/assistant ./apps/server/internal/server/handlers -count=1
go test ./apps/server/internal/knowledge/retriever -run TestSearchIntegration -count=1 -v
POST /api/assistant/conversations/stream
POST /api/assistant/sessions/:id/files
GET /api/resources/:id/search?q=蓝鲸考勤
POST /api/assistant/sessions/:id/messages
POST /api/assistant/sessions/:id/messages/stream
```

## 结论

当前两条链路里：

- **文档导入链对 Markdown 已跑通，parser / 事务写入 / 目录自愈第一阶段已落地**
- **助手会话链对普通对话和“上传后继续追问”主路径都已跑通**
- **真实 Tika 服务下的 `docx/pdf` 二进制文档人工 smoke 已补齐**

最核心的 blocker 不是数据库，不是 chunker，也不是 SSE writer，而是：

**上游 SiliconFlow 不接受 `chat_responder.go` 组出来的“两条 system message”请求；这一点已通过“合并到首条 system prompt”修复。**

其次，导入链原本的两个明显技术债也已进入首轮修复：

1. parser 边界已补，二进制解析依赖 Tika runtime
2. 原子写入与基础索引自愈已补，后续还可继续扩展到内容变化检测

所以如果要继续推进这块，优先级应该是：

1. 先决定是否引入基于内容 hash 的自动重建
2. 再处理 assistant 聊天链路是否复用 hybrid retriever
3. 最后再考虑 OCR、对象存储和导出 docx/pdf 成品等超出当前范围的能力
