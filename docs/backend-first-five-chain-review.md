# 后端前 5 条链路巡检结论

## 背景

本文基于根目录 `链路分析.md`，对后端前 5 条链路做了一次实际运行验证和代码巡检，目标是回答两件事：

1. 哪些链路已经可以跑通
2. 哪些链路没有完全跑通，或者存在会在真实使用中暴露的问题

本次只做验证和 review，不修改业务实现。

## 巡检范围

- 1. 启动装配链
- 2. 配置与可观测性链
- 3. 基础 HTTP 链
- 4. 资源读取链
- 5. 资源检索链

## 验证环境

- 仓库：`G:\gofile\Agent_Project`
- 日期：`2026-04-13`
- 后端源码入口：`apps/server/cmd/server/main.go`
- 本地现状：
  - `.env` 当前指向远程 PostgreSQL：`106.52.42.194:5432`
  - `.env` 当前指向远程 SiliconFlow
  - 因此本次验证优先按“当前环境约束”执行，而不是默认切回本地 `docker-compose`

额外说明：

- 在沙箱内直接起后端临时实例时，连接远程 PostgreSQL 会被系统权限拦住，报错为 `connectex: An attempt was made to access a socket in a way forbidden by its access permissions`
- 这不是当前仓库代码本身的编译错误，所以后续验证改为在提权环境启动临时 `18081` 实例完成

## 总结

| 链路 | 结果 | 结论 |
| --- | --- | --- |
| 启动装配链 | 跑通 | 当前源码可编译、可启动、可监听端口 |
| 配置与可观测性链 | 部分跑通 | 普通请求有 `X-Request-ID` 和 access log，预检请求缺少 `X-Request-ID` |
| 基础 HTTP 链 | 跑通 | `/healthz` 和 `OPTIONS /api/*` 可用 |
| 资源读取链 | 跑通 | `/api/resources` 与 `/api/resources/:id` 正常 |
| 资源检索链 | 部分跑通 | 检索实现本身可工作，但部分历史资源没有 chunk 索引，导致“详情能看，搜索为空” |

## 关键问题

### 1. 资源检索链没有完全跑通，部分历史资源无法检索

**现象**

- `GET /api/resources/2c84a7c4-3017-4f7a-8e04-aa9054e9bbca` 能正常返回资源详情和正文
- 但 `GET /api/resources/2c84a7c4-3017-4f7a-8e04-aa9054e9bbca/search?q=考勤` 返回：

```json
{"query":"考勤","citations":[]}
```

**对照验证**

- `GET /api/resources/de51cf11-3223-47bd-a82e-18f31da4070d/search?q=保密` 能返回 5 条 citation
- `GET /api/resources/46a5e657-2794-457b-b333-77146f76a5c8/search?q=attendance` 能返回 1 条 citation
- `go test ./apps/server/internal/knowledge/retriever -run TestSearchIntegration -count=1 -v` 通过，说明“当前检索实现 + 新导入数据”本身可以工作

**根因**

远端数据库里部分历史资源没有对应的 `resource_chunks` 数据。实际查询结果如下：

| resource_id | title | resource_chunks 数量 |
| --- | --- | --- |
| `2c84a7c4-3017-4f7a-8e04-aa9054e9bbca` | 新员工入职手册 | `0` |
| `7df3e598-65c6-4e2c-bac2-803d1ed568d3` | Attendance Policy | `0` |
| `46a5e657-2794-457b-b333-77146f76a5c8` | Attendance Policy 1775377794039667900 | `1` |
| `de51cf11-3223-47bd-a82e-18f31da4070d` | 企业信息安全管理制度 | `5` |

也就是说，现在不是“检索服务整体坏了”，而是“部分资源根本没有被索引”。

**代码定位**

- `apps/server/internal/knowledge/ingest/service.go:119`
- `apps/server/internal/knowledge/ingest/service.go:123`
- `apps/server/internal/knowledge/ingest/service.go:125`

启动时 demo data 导入逻辑只按标题去重：

- 只要发现同名资源已存在，就直接 `跳过已导入文件`
- 不会继续检查该资源是否缺版本、缺 chunk、缺 embedding
- 因此一旦历史数据落成“有 resource / version，但没有 resource_chunks”，启动导入也不会自愈

**影响**

- 资源详情页看起来正常，容易误判为资源链路已通
- `/api/resources/:id/search` 返回空引用
- 后续依赖引用证据的任务编排、审阅、编辑链路会拿不到检索结果

### 2. 资源检索接口对非法资源 ID 返回 500，而不是稳定的 4xx

**现象**

请求：

```text
GET /api/resources/non-existent-id/search?q=考勤
```

返回：

```json
{"error":"检索资源失败"}
```

状态码：`500`

但如果传的是格式正确、只是不存在的 UUID，例如：

```text
GET /api/resources/00000000-0000-0000-0000-000000000000/search?q=考勤
```

则返回：

```json
{"query":"考勤","citations":[]}
```

状态码：`200`

**根因**

`Search` handler 没有先校验资源 ID，也没有先确认资源是否存在，而是直接把 `ctx.Param("id")` 传给 repo 层。

**代码定位**

- `apps/server/internal/server/handlers/resources.go:140`
- `apps/server/internal/storage/postgres/resources.go:230`
- `apps/server/internal/storage/postgres/resources.go:233`

当前行为会分裂成两种不一致结果：

- 非 UUID 字符串，打到 PostgreSQL UUID cast 错误，最终变成 `500`
- UUID 合法但资源不存在，直接查空，最终变成 `200 + []`

**影响**

- API 语义不稳定，前端和调用方无法明确区分“输入错误”“资源不存在”“检索真的没结果”
- 错误会被包装成通用 500，调试成本高

### 3. 预检请求缺少 `X-Request-ID`，配置与可观测性链不完整

**现象**

请求：

```text
OPTIONS /api/resources
Origin: http://127.0.0.1:3000
Access-Control-Request-Method: GET
Access-Control-Request-Headers: Content-Type, X-Request-ID
```

返回：

- 状态码：`204`
- 含 `Access-Control-Allow-Origin: *`
- 含 `Access-Control-Allow-Methods: GET, POST, DELETE, OPTIONS`
- 含 `Access-Control-Allow-Headers: Content-Type, X-Request-ID`
- **不含 `X-Request-ID`**

**根因**

middleware 顺序是：

1. `CORS()`
2. `RequestContext()`

而 `CORS()` 在收到 `OPTIONS` 时会直接 `AbortWithStatus(204)`，导致后面的 `RequestContext()` 根本没有机会运行。

**代码定位**

- `apps/server/internal/server/router/router.go:25`
- `apps/server/internal/server/router/router.go:26`
- `apps/server/internal/server/router/router.go:28`
- `apps/server/internal/server/middleware/cors.go:17`
- `apps/server/internal/server/middleware/cors.go:24`

**影响**

- 浏览器预检虽然成功，但没有统一 request id
- 预检链路没有 access log，排查跨域问题时证据不完整
- 第 2 条“配置与可观测性链”只能算部分跑通，不能算完全闭环

## 通过项

### 1. 启动装配链已跑通

已验证：

```text
go build -o .tmp/server-smoke.exe ./apps/server/cmd/server
```

通过。

在提权环境启动临时实例后：

```text
GET http://127.0.0.1:18081/healthz
```

返回：

```json
{"service":"server","status":"ok"}
```

同时启动日志确认：

- 配置加载成功
- PostgreSQL 连接成功
- migration 执行成功
- repo / service / handler 完成装配
- Hertz 开始监听 `:18081`

### 2. 基础 HTTP 链已跑通

已验证：

- `GET /healthz` 返回 `200`
- `OPTIONS /api/resources` 返回 `204`

### 3. 资源读取链已跑通

已验证：

- `GET /api/resources` 返回资源列表
- `GET /api/resources/:id` 返回资源摘要与 `current_version`

## 建议动作

### 优先级 P1

1. 修复资源检索链的数据自愈问题
   - 启动导入不要只按标题跳过
   - 至少补一条“资源已存在但 chunk 缺失”的自愈路径，或提供显式重建索引命令

2. 修复 `/api/resources/:id/search` 的资源校验边界
   - 非法 ID 返回 `400`
   - 资源不存在返回 `404`
   - 真正“检索无结果”再返回 `200 + []`

### 优先级 P2

3. 调整 middleware 顺序或补齐预检 request id
   - 让 `OPTIONS` 请求也能拿到 `X-Request-ID`
   - 保证预检链路也有一致的 access log

### 优先级 P3

4. 补齐资源检索 handler 的测试
   - 当前 `resources_test.go` 只覆盖了：
     - 列表
     - 详情
     - 搜索缺少 `q`
   - 还缺：
     - 非法 `id`
     - 不存在资源
     - 已存在资源但无 chunk
     - 检索成功返回 citation

## 本次使用的主要验证命令

```text
go build -o .tmp/server-smoke.exe ./apps/server/cmd/server
go test ./apps/server/internal/config ./apps/server/internal/server/router ./apps/server/internal/server/handlers ./apps/server/internal/knowledge/retriever -count=1
go test ./apps/server/internal/knowledge/retriever -run TestSearchIntegration -count=1 -v
GET /healthz
GET /api/resources
GET /api/resources/:id
GET /api/resources/:id/search?q=...
OPTIONS /api/resources
```

## 结论

当前后端前 5 条链路里，真正没有完全跑通的是“资源检索链”和“预检可观测性链”。

更准确地说：

- **资源检索实现本身能工作**
- **但历史数据完整性和 handler 边界处理有缺口**

所以现在最值得优先修的不是再去怀疑 embedding / reranker 主流程，而是：

1. 先补资源索引自愈或重建能力
2. 再修搜索接口的资源 ID / 资源存在性校验
3. 顺手补齐 OPTIONS 预检的 request id 与日志闭环
