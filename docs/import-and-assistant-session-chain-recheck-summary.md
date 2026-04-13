# 文档导入链与助手会话链复查总结

## 背景

本次复查基于根目录 `链路分析.md`，重点核对当前工作树里的三段链路：

1. `AssistantRepo.CreateSessionWithMessages / AppendMessages / ListMessages`
2. 助手流式回复分支
3. 助手上传文件链

目标不是重复旧巡检，而是确认当前代码里哪些问题已经修掉，哪些问题还没真正跑通。

## 总结

当前工作树里，旧巡检中最严重的 `system message` 协议问题已经修复，流式回复链和基础上传链的单测也都能通过。  
但还有 4 个真实问题没有闭环：

1. `AssistantRepo` 三个方法在现有集成测试入口下没有真正跑到真实数据库写路径
2. 助手聊天链只做词法召回，没有复用项目已有的 hybrid retriever
3. 上传接口默认放行 `pdf/doc/docx/...`，但默认配置下并不能真正解析这些文件
4. 上传导入失败时会留下前端不可见的原文件记录

## 已确认跑通的部分

### 1. 旧的双 `system` message blocker 已修复

当前 `apps/server/internal/assistant/chat_responder.go` 已把运行时上下文并回单条 `system` prompt，相关测试通过：

```powershell
go test ./apps/server/internal/assistant -run TestBuildChatMessagesMergesRuntimeContextIntoSingleSystemPrompt -count=1 -v
```

这意味着旧巡检中的结论：

- “会话一旦出现 `session_file` 就会因为第二条 `system` message` 导致 SiliconFlow 400”

在当前工作树里已经不再成立。

### 2. 流式回复分支当前单测通过

已验证：

```powershell
go test ./apps/server/internal/assistant ./apps/server/internal/server/handlers -run "TestStartConversationStreamPersistsUserFirstAndAssistantAfterCompletion|TestAppendMessageStreamDoesNotPersistAssistantOnCancellation|TestCreateConversationStreamHandler|TestAppendMessageStreamHandler" -count=1 -v
```

说明下面这段链路当前是通的：

- `assistant/chat_responder.go`
- `replyJSONStreamExtractor`
- `assistant/service.go` 中的 `message_delta / message_completed`
- `server/handlers/assistant.go` 的 SSE 输出

### 3. 上传链的基础单测通过

已验证：

```powershell
go test ./apps/server/internal/assistant ./apps/server/internal/server/handlers -run "TestUploadFilePersistsSessionFileMessage|TestUploadFileStoresOriginalFileAndPersistsFileID" -count=1 -v
```

说明下面这段基础链路在测试里可跑：

- `AssistantHandler.UploadFile`
- `assistant.Service.UploadFile`
- `filestore.LocalStore.Save`
- `UploadedFileRepo.Create / UpdateResourceID`
- `session_file` 消息写回会话

## 当前没有真正跑通或存在 bug 的地方

### 1. `AssistantRepo` 三个方法没有被真实数据库测试覆盖

目标方法：

- `CreateSessionWithMessages`
- `AppendMessages`
- `ListMessages`

现有测试入口是：

- `apps/server/internal/storage/postgres/assistant_test.go`

实际结果：

```powershell
go test ./apps/server/internal/storage/postgres -run TestAssistantRepoSessionLifecycle -count=1 -v
```

第一次运行：因为 shell 没有导出 `DATABASE_URL`，直接 skip。  
第二次把 `.env` 的 `DATABASE_URL` 注入测试进程后，仍然 skip，因为测试只允许本地数据库 host，而当前 `.env` 指向远程 `106.52.42.194`。

所以当前结论是：

- 这三个 repo 方法从代码阅读上看实现完整
- 但在当前仓库现成验证路径下，没有真实数据库运行证据

### 2. 助手聊天链没有走 hybrid retriever

当前装配：

- `apps/server/cmd/server/main.go` 把 `resourceRepo` 直接注入 `assistant.NewService`

当前 assistant 检索调用：

- `apps/server/internal/assistant/service.go` 只依赖 `SearchChunksLexicalByResource`

而项目里真正的混合检索入口在：

- `apps/server/internal/knowledge/retriever/service.go`

这意味着：

- 资源页 / 任务编排链可以用 `semantic + lexical + reranker`
- 助手聊天链只能做单资源词法召回

结果是“上传后继续追问”这条路径虽然不再被协议卡死，但在语义改写、同义表达、弱关键词场景下会明显退化。

### 3. 上传接口默认声称支持二进制文档，但默认配置下其实不支持

HTTP 层放行的扩展名：

- `md`
- `txt`
- `doc`
- `docx`
- `pdf`
- `rtf`
- `odt`

见：

- `apps/server/internal/server/handlers/assistant.go`
- `apps/server/internal/document/parser/parser.go`

但默认配置是：

- `apps/server/internal/config/config.go` 中 `defaultDocumentParser = "text"`

而 parser 行为是：

- `text` 模式下只真正支持 `md/txt`
- `pdf/doc/docx/...` 需要显式启用 `tika`

所以当前默认运行态的真实情况是：

- `.md/.txt` 可以导入
- `.pdf/.docx/...` 会在导入阶段失败，不会生成 `session_file ready`

这属于“handler 能收，默认服务却不能处理”的能力错配。

### 4. 上传失败时会留下前端不可见的原文件记录

当前 `assistant.Service.UploadFile` 的顺序是：

1. 先 `persistUploadedFile`
2. 再 `importer.ImportDocument`
3. 成功才生成 `session_file`

如果第 2 步失败：

- 原文件已经落盘
- `uploaded_files` 元数据已经落库
- 会话里只会追加一条 `system` error
- 响应里不会暴露 `file_id`

而下载接口 `GET /api/files/:id/download` 又依赖 `file_id`。  
所以失败态下会留下用户看不见、也没法下载的孤儿原文件记录。

### 5. 流式 JSON 提取器没有处理 surrogate pair

`apps/server/internal/assistant/chat_responder.go` 中的 `replyJSONStreamExtractor` 只按单个 `\uXXXX` 解码。

这对普通中文没问题，但如果模型流里出现：

- emoji
- 非 BMP 字符

就可能把一对 surrogate 拆坏，导致 `message_delta` 输出异常字符。

当前仓库没有针对这条边界的测试。

## 验证记录

已运行命令：

```powershell
go test ./apps/server/internal/storage/postgres -run TestAssistantRepoSessionLifecycle -count=1 -v
go test ./apps/server/internal/assistant ./apps/server/internal/server/handlers -run "TestBuildChatMessagesMergesRuntimeContextIntoSingleSystemPrompt|TestUploadFilePersistsSessionFileMessage|TestUploadFileStoresOriginalFileAndPersistsFileID" -count=1 -v
go test ./apps/server/internal/assistant ./apps/server/internal/server/handlers -run "TestStartConversationStreamPersistsUserFirstAndAssistantAfterCompletion|TestAppendMessageStreamDoesNotPersistAssistantOnCancellation|TestCreateConversationStreamHandler|TestAppendMessageStreamHandler" -count=1 -v
go test ./apps/server/internal/document/parser ./apps/server/internal/knowledge/ingest -count=1 -v
```

结果概览：

- `AssistantRepo` 生命周期测试：跳过
- assistant/chat responder 单测：通过
- assistant/handler SSE 单测：通过
- parser 单测：通过
- ingest 单测：通过

## 建议优先级

### P0

- 把 assistant 聊天链接到 `knowledge/retriever.Service.SearchByResource`

### P1

- 收紧上传接口能力声明，或者把默认 parser 从“只支持文本”改成和产品预期一致的装配方式

### P1

- 修补上传失败态，至少让失败响应也能返回 `file_id`，避免孤儿原文件记录

### P2

- 为 `replyJSONStreamExtractor` 增加 surrogate pair 测试和解码修复

### P2

- 单独提供一个可对当前环境执行的 `AssistantRepo` 验证入口，避免 repo 生命周期长期只靠代码阅读
