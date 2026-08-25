# membox × Pi Agent 集成设计

> 状态：In progress (Phase 0–2 foundation landed; write tools / Web drawer / full TUI workspace still open)
> 
> 目标：把 TUI 中现有的 `AGENT` 输入模式实现为真正可用、可流式显示、可恢复会话的 Pi Agent 客户端，并在 Web Companion 阅读器中提供同一套会话和聊天窗口。
> 
> 本文是实施规范。除标记为“后续”的内容外，接口、进程边界、安全约束和验收条件都应按本文实现。

## 1. 结论摘要

membox 不在 TUI 和浏览器中各自启动 Agent，也不让前端直接访问模型供应商。最终结构是：

```text
TUI ───────────────┐
                   │ HTTP + SSE
Web chat drawer ───┼────────► Web Companion / Agent Manager
                   │                 │
CLI doctor ────────┘                 │ JSONL stdin/stdout
                                     ▼
                              pi --mode rpc
                              （每个活跃会话一个 worker）
                                     │
                                     │ 仅 membox extension tools
                                     ▼
                         Companion 内部受权威协调的 API
                                     │
                         Markdown + SQLite + FTS
```

核心决定：

1. **V1 使用 Pi RPC 模式，而不是再开发一个常驻 Node HTTP sidecar。** Go 已是宿主进程，RPC 正是 Pi 为 IDE/自定义 UI 提供的边界；这减少一层服务发现、端口、部署和故障恢复。
2. **Web Companion 是每个 `MEMBOX_HOME` 的 Agent 控制面。** TUI 和 Web 使用同一会话、同一事件流、同一审批状态。
3. **一个活跃会话对应一个 Pi RPC 子进程。** 同一会话永远只有一个 worker；不同会话可以在资源上限内并行。
4. **Agent 会话由 Pi JSONL 持久化。** SQLite 只保存 membox 会话目录元数据，不复制完整聊天记录。
5. **默认只开放 membox 专用工具。** V1 禁用 Pi 的 `bash`、`write`、`edit` 等内建工具，也不自动加载用户或项目 extensions、skills、prompt templates 和 context files。
6. **所有文档修改必须经过 Companion 的单一 mutation coordinator。** Agent 不直接写 Markdown，也不独立打开 `membox.db`。
7. **只读工具自动执行，写工具逐次确认。** 确认请求只交给发起本轮对话的控制客户端；观察客户端只能旁观。
8. **SSE 是客户端事件协议，Pi JSONL 不是公共 API。** Companion 负责事件归一化、脱敏、序号、短期重放和快照恢复。
9. **Agent 生命周期从属于 Companion，而不是某个 TUI。** Companion 为 Keep 模式时 Agent 可在 TUI 退出后继续；Session 模式按现有 controller lease 规则退出。

## 2. 产品范围

### 2.1 V1 用户能力

用户可以在 TUI 或 Web 中：

- 新建、选择、重命名和继续 Agent 会话；
- 向 Pi Agent 发送问题并实时查看回答；
- 将当前选中的 Document UUID 作为本轮上下文附加；
- 查看正在调用的 membox 工具及成功/失败摘要；
- 中止当前生成；
- 在另一客户端打开同一会话并继续接收事件；
- 选择 Pi 已配置的模型和 thinking level；
- 允许或拒绝一次文档写操作；
- 从回答中的 `membox://doc/<uuid>` 引用打开文档。

Agent 可以：

- 搜索文档；
- 按 UUID 读取文档及必要元数据；
- 浏览与当前文档相关的 notes、links 和 topics；
- 创建普通笔记或 annotation note；
- 在用户确认后更新或重命名文档；
- 在回答中引用稳定 Document UUID，而不是依赖易变路径。

### 2.2 明确不做

V1 不包含：

- 后台定时或无人值守 Agent；
- 远程网络访问 Companion；
- TUI 与 Web 各自独立的会话实现；
- 模型 API key 的录入或存储；
- 任意 shell、任意路径读写或仓库 coding-agent 权限；
- 多 Agent 协作；
- 语音、图片附件和拖拽文件；
- 对 Pi session JSONL 的自定义解析器作为主要消息模型；
- 将聊天记录复制到 Markdown；
- 云同步 Agent 会话；
- 在用户未确认时自动修改文档。

Pi RPC 已支持 steer、follow-up、fork、clone 和 tree navigation，但 V1 只实现新建、继续、普通 prompt 和 abort。排队、分叉与会话树属于后续能力。

## 3. 必须先满足的架构前置条件

Agent 写工具上线前，每个 `MEMBOX_HOME` 必须只有一个 mutation coordinator。仅保证“一个 HTTP server”不够：TUI、CLI、Companion 或 Agent 若分别调用 `bootstrap.Open()` 并执行“解析位置 → 改文件 → observe → 提交 DB”，仍会产生跨进程竞态。

实施门槛：

- 文档 create/update/rename/delete、annotation save、scan/reindex 等写流程必须路由到 Companion；或
- 使用覆盖完整业务事务的跨进程锁，锁范围必须包含路径解析、文件操作、重新观察和 SQLite commit；
- Agent 专用内部 API 只能调用上述协调器，不能复刻 filesystem/SQLite 写逻辑；
- 用跨进程集成测试证明 TUI/CLI/浏览器/Agent 并发写不会丢更新或造成身份分裂。

在该前置条件完成前，可以发布 **read-only Agent preview**，但必须完全不注册写工具。

## 4. 为什么选择 Pi RPC

### 4.1 选择

Companion 直接管理：

```bash
pi --mode rpc \
  --session-dir <MEMBOX_HOME>/agent/sessions \
  --no-builtin-tools \
  --no-extensions --extension <materialized-membox-extension.ts> \
  --no-skills --no-prompt-templates --no-context-files \
  --tools membox_search_documents,membox_read_document,...
```

实际参数由 `AgentManager` 构造，不经过 shell 拼接。

### 4.2 原因

- membox 的宿主是 Go，不是 Node；Pi 文档建议非 Node 宿主使用 RPC。
- RPC 已提供 prompt、abort、session、model、thinking、messages、stats、streaming events 和 extension UI 协议。
- 不需要再设计 Node HTTP 服务、健康检查、端口和第二套 session API。
- Pi 版本、崩溃和 stderr 都可以在一个 Go process manager 中统一处理。
- 自定义 extension 可以把 membox tools 和逐次确认接入 Pi，同时保持模型配置由 Pi 管理。

### 4.3 何时改用直接 SDK

只有出现以下需求时才重新评估 `AgentSession` / `AgentSessionRuntime` sidecar：

- 数十个并行会话使“一会话一进程”成本不可接受；
- 需要进程内共享 ModelRuntime 或自定义 provider；
- Pi RPC 缺少无法通过 extension 补齐的会话能力。

即使改用 SDK，本文定义的 Companion HTTP/SSE API 和前端协议也不改变。

## 5. 组件职责

### 5.1 Web Companion

Companion 是唯一公开的本地服务，负责：

- Agent HTTP API 与浏览器同源鉴权；
- AgentManager 生命周期；
- session catalog；
- prompt 串行化、幂等和控制权；
- Pi JSONL 命令/响应关联；
- Pi event → membox event 转换；
- SSE 广播、短期重放和快照；
- extension UI 确认路由；
- 写工具最终调用 mutation coordinator；
- worker 日志、健康状态和优雅关闭。

Companion 不负责直接调用模型，也不解析模型文本来判断工具权限。

### 5.2 AgentManager

建议新增 `internal/agent`：

```text
internal/agent/
├── manager.go          # workers、session registry、资源上限
├── worker.go           # exec、stdin/stdout/stderr、状态机
├── protocol.go         # Pi RPC command/response/event DTO
├── stream.go           # event normalization、ring buffer、subscribers
├── approval.go         # UI request ownership与超时
├── catalog.go          # agent_sessions repository port
├── errors.go
└── assets/
    └── membox.ts       # go:embed，启动时原子 materialize
```

`AgentManager` 不依赖 Bubble Tea 或浏览器 DOM。Web backend 只把 HTTP DTO 转成 manager command。

### 5.3 Pi RPC worker

每个 worker：

- 只绑定一个 membox Agent session；
- 只有一个 stdin writer goroutine；
- 有独立 LF-only stdout decoder；
- 将 stderr 写入有界 ring log，不混入协议；
- 同一时间最多运行一个 prompt；
- 使用 Companion 生命周期 context，而不是某个 HTTP request/TUI context；
- 保存 process group 信息，Companion 停止时回收整个组；
- stdin 关闭时必须退出；异常 orphan 由下次 Companion 启动按带 nonce 的 registry 保守清理。

### 5.4 membox Pi extension

extension 只负责 Pi 内部集成：

- 注册受限的 `membox_*` tools；
- 在 `before_agent_start` 获取并注入本轮文档上下文；
- 写工具执行前调用 `ctx.ui.confirm()`；
- 通过仅 worker 可用的内部 bearer token 调用 Companion；
- 将冲突、拒绝和业务错误作为结构化 tool result 返回。

extension 不打开 SQLite，不自行扫描目录，不使用 `fs.writeFile` 修改用户文档。

### 5.5 TUI 与 Web

两个 UI 都只是客户端：

- 调用同一 HTTP API；
- 消费同一规范化 SSE；
- 不理解 Pi 原始 event 类型；
- 不保存第二份权威 transcript；
- 重连时先取 snapshot，再续订 event stream。

## 6. 生命周期与进程模型

### 6.1 Companion 启动

AgentManager 采用 lazy start：Companion 启动不立即启动 Pi。第一次请求 Agent status 时执行轻量 capability probe；第一次打开或创建会话时才启动 worker。

状态：

```text
disabled -> probing -> ready
                    -> unavailable(reason)

session: unloaded -> starting -> idle -> running -> idle
                              \-> failed -> restarting/unloaded
```

`pi` 查找顺序：

1. membox setting `agent.pi_path`；
2. Companion 启动时的 `PATH`；
3. 平台标准可执行文件查找。

不通过 `bash -lc` 查找，不信任浏览器传入的可执行路径。

### 6.2 Session 与 Keep 模式

- Companion `keep`：TUI 退出不影响 worker；浏览器可继续查看正在运行的回答。
- Companion `session`：现有 controller leases 到期且无保留条件时，Companion 退出，并终止其 workers。
- 浏览器 tab presence 不是独立 Agent daemon 所有权；它只影响现有 Companion 规则。
- TUI 不能直接 kill Pi；`abort` 与“停止 Companion”是不同操作。

### 6.3 资源限制

默认：

- 最多 2 个 loaded workers；
- 每个 session 最多 1 个 worker；
- idle 10 分钟且无 SSE subscriber 的 worker 可卸载；
- running、等待确认或有订阅者的 worker不可驱逐；
- 达到上限时先驱逐最久 idle worker，无可驱逐项则返回 `429 agent_capacity_reached`。

这些值允许设置，但必须有合理硬上限，避免网页重复请求制造无限进程。

### 6.4 Companion 或 worker 崩溃

- Pi session JSONL 已落盘的内容保留；
- 未收到 `agent_settled` 的 run 标为 `interrupted`，不自动重发 prompt，避免重复写工具；
- 下一次打开会话用 `pi --session <validated-session-path>` 恢复；
- 客户端收到新的 stream epoch 和 snapshot reset；
- worker 连续崩溃使用有界退避，3 次后进入 failed，必须由用户重试；
- Companion 关闭先拒绝新 prompt，再请求当前 run abort，等待最多 5 秒，最后终止 process group。

## 7. 会话模型与持久化

### 7.1 Authority

```text
Pi session JSONL                         完整对话和会话树事实
SQLite agent_sessions                    membox 展示与定位元数据
内存 event ring / approvals / run state  瞬时状态
```

建议 migration：

```sql
CREATE TABLE agent_sessions (
    id TEXT PRIMARY KEY,
    pi_session_id TEXT UNIQUE,
    session_path TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL DEFAULT '',
    model_provider TEXT,
    model_id TEXT,
    thinking_level TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    archived INTEGER NOT NULL DEFAULT 0
);
```

约束：

- `id` 是 membox 对外 UUIDv7；HTTP 不暴露任意本地路径。
- `session_path` canonicalize 后必须位于 `<MEMBOX_HOME>/agent/sessions/`。
- 完整 messages 不复制进 SQLite。
- catalog 写入也走 Companion 单写者。
- session file 缺失时标记为 unavailable，不静默创建同 ID 新会话。

### 7.2 新建和恢复

新建：

1. manager 预留 membox session ID；
2. 启动无 `--session` 的 RPC worker；
3. 调用 `get_state` 获得 `sessionId/sessionFile`；
4. 验证 session path；
5. 事务写入 catalog；
6. 返回规范化 snapshot。

恢复：

1. 按 membox session ID 查 catalog；
2. 验证路径仍在 session root；
3. 原子地取得 session worker slot；
4. 使用 `--session <path>` 启动；
5. `get_state` 校验 Pi session ID；
6. 调用 `get_messages`、`get_session_stats` 构造 snapshot。

### 7.3 当前会话

“当前会话”属于客户端，不是全局值。TUI 和每个 Web tab 各自保存最近选择的 membox session ID；它们可以选择同一或不同会话。

同一会话的 transcript、run 和审批共享。选择会话不会调用 Pi 的 `switch_session` 去替换另一个客户端正在使用的 worker。

## 8. 并发、控制权与幂等

### 8.1 单会话串行

V1 中 session busy 时拒绝新的普通 prompt：

```http
409 Conflict
{"error":{"code":"agent_busy","message":"The session is already running."}}
```

不偷偷把消息映射成 Pi `steer` 或 `followUp`。这避免多客户端下上下文、审批所有权和队列语义不清。后续加入排队时必须在 API 中显式声明 queue mode。

### 8.2 控制客户端

每个 TUI instance/Web tab 生成随机 `client_id`。成功提交 prompt 的客户端取得该 run 的 controller lease：

- 其他订阅者可看流式输出；
- 只有 controller 可以回答审批；
- 任一同源已认证客户端可请求 abort，但 UI 必须明确显示它会中止共享 run；
- controller 断连后 lease 保留 30 秒；审批自身最多等待 60 秒；
- lease 过期后，其他客户端可通过显式“Take control”取得控制；
- 无人取得控制时审批默认拒绝，不允许默认同意。

### 8.3 请求幂等

所有创建 session、提交 prompt 和写操作要求：

```http
Idempotency-Key: <uuid>
X-Membox-Agent-Client: <client_id>
```

Companion 为每个 client/session 保留有界幂等记录。HTTP 超时后重复相同 key 返回原 `run_id`，不能生成第二轮模型调用。

API request context 取消只表示客户端停止等待；prompt 一旦返回 accepted，不随 HTTP 连接取消。中止必须调用显式 abort endpoint。

## 9. 本轮文档上下文

客户端提交 prompt 时可以附加：

```json
{
  "text": "这篇文章和我之前的笔记有什么冲突？",
  "context": {
    "document_id": "019...",
    "selection": null
  }
}
```

V1 只接受稳定 Document UUID；不接受客户端给出的 path 或任意文件内容。Companion 重新解析 title/status，并把最小上下文存入该 run 的内存 slot。

membox extension 在 `before_agent_start` 通过一次性内部 endpoint 取走本轮 context，并追加到**本轮 system prompt**：

```text
Current membox context:
- document_id: 019...
- title: Flash Attention
Use membox_read_document when body content is needed.
```

这样：

- 用户消息保持原文，不含隐藏前缀；
- context 不伪装成用户输入；
- 不把全文无条件塞入 token context；
- session 恢复后不会错误沿用旧的“当前文档”；
- extension 获取失败时本轮直接报错，不在错误上下文下继续。

V1 不传网页正文 selection。后续支持时必须传 `document_id + anchor/text hash`，由服务端验证 selection 确实来自该文档。

## 10. Agent 工具与权限

### 10.1 V1 工具集合

| Tool | 类别 | 行为 | 确认 |
| --- | --- | --- | --- |
| `membox_search_documents` | read | FTS/title 搜索，返回 UUID、title、snippet | 否 |
| `membox_read_document` | read | 按 UUID 读取 Markdown，支持 line/byte 上限 | 否 |
| `membox_list_related` | read | notes、links、backlinks、topics | 否 |
| `membox_get_document` | read | metadata、revision、status | 否 |
| `membox_create_note` | write | 创建普通 note，可选链接到 target | 是 |
| `membox_create_annotation_note` | write | 用经验证 anchor 创建 annotation note | 是 |
| `membox_update_document` | write | 基于 expected revision 替换完整内容或应用受限 patch | 是 |
| `membox_rename_document` | write | 保 UUID 的 title/path rename use case | 是 |

工具输出必须有结构化 JSON，且返回稳定 UUID。正文结果设置字符/字节上限，超限时返回 continuation cursor，防止一次读取把完整资料库送给模型。

### 10.2 默认禁用能力

启动参数必须禁用：

- built-in `bash/read/write/edit/grep/find/ls`；
- 自动发现的 extensions；
- skills 和 prompt templates；
- 项目 `AGENTS.md`/`CLAUDE.md` context；
- 从当前目录继承的 project-local 可执行逻辑。

只使用 `--extension` 显式加载由当前 membox build 内嵌并校验的 extension。高级“coding tools profile”若以后提供，必须是独立、显眼、可撤销的设置，并重新设计权限，不得顺带进入 V1。

### 10.3 写确认

写工具执行顺序：

1. 读取当前 revision；
2. 构造 mutation preview（目标、摘要、diff、影响的 links/annotations）；
3. extension 调用 `ctx.ui.confirm()`；
4. RPC 发出 `extension_ui_request`；
5. Companion 转为 `approval.requested`，只允许 controller 回答；
6. 拒绝/超时则工具返回 denied，绝不调用 mutation endpoint；
7. 同意后发送 `expected_revision`；
8. coordinator 在锁内再次校验 revision 并提交；
9. 冲突返回 `revision_conflict`，Agent 必须重新读取，不自动覆盖。

确认是 **Allow once / Deny**。V1 不提供“本会话始终允许”。diff 过长时 UI 展示摘要和可滚动详情，但确认消息本身有大小上限。

### 10.4 System prompt 原则

内嵌追加提示应明确：

- membox 是 Markdown identity/catalog，不是普通代码仓库；
- 使用 Document UUID，不猜测路径；
- 修改前先读取最新 revision；
- 写工具成功才可声称已修改；
- 回答引用使用 `membox://doc/<uuid>`；
- 不向用户显示内部 bearer token、session path 或隐藏 context；
- 未经工具结果不得臆造文档内容。

提示词不是安全边界；真正权限由工具 allowlist、内部鉴权和 coordinator 实现。

## 11. Pi JSONL 实现约束

Pi RPC 使用严格 LF (`\n`) JSONL。实现不得使用会把 `U+2028/U+2029` 当换行的通用 reader。

worker protocol 必须：

- 只按字节 `0x0A` 切帧，并去除结尾可选 `\r`；
- 限制单帧大小，默认 8 MiB；超限终止 worker 并报告 protocol error；
- 为每个 command 生成唯一 `id`，在 pending map 中关联 response；
- stdin 写入由单 goroutine 串行，处理 partial write；
- stdout parse error 不忽略，也不把原始行发给用户；
- stderr 与 stdout 完全分离；
- pending command 在进程退出时统一以 `worker_exited` 失败；
- 使用 `agent_settled` 判断一次 run 真正结束，不用较早的 `agent_end`；
- extension UI response 也通过同一 stdin writer；
- 所有 map、subscriber 和 pending channel 都有上限与清理路径。

## 12. Companion HTTP API

统一前缀：`/api/agent`。所有 JSON 响应包含版本 `v: 1`；错误使用稳定 code。

### 12.1 Status

```http
GET /api/agent/status
```

```json
{
  "v": 1,
  "enabled": true,
  "available": true,
  "pi_version": "...",
  "state": "ready",
  "loaded_workers": 1,
  "max_workers": 2,
  "error": null
}
```

### 12.2 Sessions

```http
GET  /api/agent/sessions?include_archived=false
POST /api/agent/sessions
GET  /api/agent/sessions/{session_id}
PATCH /api/agent/sessions/{session_id}
POST /api/agent/sessions/{session_id}/archive
```

创建 body：

```json
{"title":"Research notes","model":null,"thinking_level":"medium"}
```

snapshot：

```json
{
  "v": 1,
  "session": {
    "id": "019...",
    "title": "Research notes",
    "state": "idle",
    "model": {"provider":"anthropic","id":"..."},
    "thinking_level": "medium",
    "created_at": "...",
    "updated_at": "..."
  },
  "messages": [],
  "active_run": null,
  "last_event_id": "4f8c:27"
}
```

归档不删除 Pi session file。物理删除会话不进入 V1。

### 12.3 Prompt 与 abort

```http
POST /api/agent/sessions/{session_id}/messages
Idempotency-Key: ...
X-Membox-Agent-Client: ...
Content-Type: application/json

{"text":"...","context":{"document_id":"019..."}}
```

成功返回 `202 Accepted`：

```json
{"v":1,"run_id":"019...","accepted":true}
```

```http
POST /api/agent/sessions/{session_id}/runs/{run_id}/abort
POST /api/agent/sessions/{session_id}/runs/{run_id}/claim
```

### 12.4 Events

```http
GET /api/agent/sessions/{session_id}/events?after=4f8c:27
Last-Event-ID: 4f8c:27
Accept: text/event-stream
```

SSE 示例：

```text
id: 4f8c:28
event: agent
data: {"v":1,"session_id":"019...","run_id":"019...","type":"assistant.delta","at":"...","payload":{"text":"Hello"}}

```

服务端优先使用 `Last-Event-ID`，也接受 `after` 作为浏览器首次创建 `EventSource` 时的 cursor（原生 `EventSource` 不能自定义 request header）。`after` 只能携带 event ID，不能携带 token。服务端发送 15 秒 heartbeat comment。单 session ring 默认保留最近 1000 events 或 2 MiB，以先达到者为准。

如果 epoch 不同或 requested event 已被淘汰，先发送：

```json
{"type":"stream.reset","payload":{"reason":"replay_unavailable"}}
```

客户端随后重新 GET session snapshot，再以新 `Last-Event-ID` 订阅。SSE 不是 durable log。

### 12.5 Approvals

```http
POST /api/agent/sessions/{session_id}/runs/{run_id}/approvals/{approval_id}
X-Membox-Agent-Client: ...

{"decision":"allow_once"}
```

或：

```json
{"decision":"deny"}
```

重复相同决定幂等成功；相反决定返回 `409 approval_already_resolved`。非 controller 返回 `403 not_run_controller`。

### 12.6 Models

```http
GET   /api/agent/models
PATCH /api/agent/sessions/{session_id}/model
PATCH /api/agent/sessions/{session_id}/thinking
```

数据来自 Pi RPC `get_available_models`、`set_model`、`set_thinking_level`。running 时修改返回 409。membox 不保存 provider credential。

## 13. 公共事件模型

不得把 Pi 原始对象直接透传。V1 事件类型：

```text
stream.reset
session.state
run.started
run.settled
run.interrupted
user.accepted
assistant.started
assistant.delta
assistant.completed
tool.started
tool.progress
tool.completed
approval.requested
approval.resolved
queue.updated
model.changed
usage.updated
agent.error
```

每个 event 至少包含：

```json
{
  "v": 1,
  "session_id": "019...",
  "run_id": "019... or null",
  "type": "tool.started",
  "at": "RFC3339Nano",
  "payload": {}
}
```

规则：

- `assistant.delta` 只含追加文本，不重复发送累计全文；
- raw thinking delta 默认不对客户端输出，只显示普通 `Thinking…` 状态；
- tool arguments 按 schema 过滤，内部 token/path 永不出现；
- tool result 大字段截断，完整敏感输出不进入 event ring；
- `tool.completed` 明确 `success/denied/error`；
- `run.settled` 在 Pi `agent_settled` 后发出；
- snapshot 是最终事实，events 只是增量体验。

## 14. TUI 体验设计

### 14.1 进入方式

现有 `Ctrl+K` 保留，但行为从“打开占位输入”升级为：

1. 确保 Companion 正在运行；
2. 查询 Agent status；
3. 恢复该 TUI 最近会话，没有则创建；
4. 打开 Agent workspace 并聚焦 composer。

`AGENT` badge 永远反映真实状态：

```text
AGENT OFF · AGENT CONNECTING · AGENT READY · AGENT RUNNING · AGENT ERROR
```

不能再用 `agent is not connected yet` 作为正常提交结果。

### 14.2 布局

宽终端：

```text
Documents / board          │ Agent transcript
                            │ user / assistant / tool cards
                            │
                            │ [current document: Flash Attention ×]
                            │ composer...
```

Agent workspace 替换右侧 preview，不覆盖左侧文档导航。用户在左侧移动选择时，只更新“可附加的当前文档”，不会在未发送时改变正在运行的一轮上下文。

窄终端使用全屏 transcript；Esc 返回之前的文档视图。transcript 使用独立 viewport，新增 delta 只有在用户位于底部时自动跟随；用户向上阅读时显示 `new output ↓`，不得强制跳到底部。

### 14.3 输入和快捷键

- `Ctrl+K`：打开/聚焦 Agent；
- `Enter`：提交单行 prompt；
- `Ctrl+C`：Agent running 时弹出/执行 abort，不退出 TUI；
- `Esc`：关闭审批、取消输入焦点或返回文档视图，不隐式 abort；
- `:agent new`：新会话；
- `:agent sessions`：会话 picker；
- `:agent model`：模型 picker；
- `:agent thinking <level>`：thinking level；
- `:agent status`：诊断；
- `:agent abort`：中止当前 run。

V1 composer 可以继续使用单行 `textinput`。多行编辑后续再引入；不能为此阻塞核心集成。

### 14.4 TUI 状态实现

建议新增：

```text
internal/interfaces/tui/
├── agent.go          # AgentClient、HTTP/SSE commands
├── agent_model.go    # state/messages
├── agent_update.go   # Bubble Tea messages/state transitions
└── agent_view.go     # transcript、tool、approval UI
```

`Model` 增加独立 `agentState`，不要复用 `filterErr/rawContent` 保存聊天状态。所有 HTTP I/O 通过 `tea.Cmd`。SSE reader 在 goroutine 中写有界 channel，Bubble Tea 每次用 `waitAgentEventCmd` 取一个 event；退出或切换 session 时必须 cancel 并 drain。

测试必须覆盖旧 stream 的 sequence/session ID 不能覆盖新选择的会话，规则与现有异步 search sequence 相同。

### 14.5 审批

审批用居中 modal，至少显示：

- tool 名；
- 目标 document title + 短 UUID；
- mutation 摘要/diff；
- `Allow once` / `Deny`；
- 发起客户端和超时。

审批期间普通输入不提交。Esc 等价 Deny，不等价关闭而悬挂。

## 15. Web Companion 聊天窗口

### 15.1 代码边界

聊天是 membox integration，不属于 host-neutral Miru。实现放在：

```text
internal/web/frontend/adapters/companion/
├── integration.js            # 只负责挂载入口
├── companion.css
└── agent/
    ├── client.js             # fetch、SSE、重连
    ├── drawer.js             # DOM/state
    ├── render.js             # message/tool/approval rendering
    └── markdown.js           # safe rendering/link handling
```

不得把 Agent API、session 或 membox UUID 逻辑写入 `frontend/miru`。

### 15.2 入口和布局

在 topbar 右侧加入 chat icon，带状态点；点击打开固定右侧 drawer：

- 桌面宽度约 420px，覆盖而不重排 `.article` 与 `.annotation-layer`，避免破坏 margin-note layout；
- 小屏占满 viewport；
- header：session picker、New、model/status；
- body：virtualized/增量 transcript；
- footer：current document chip、composer、send/stop；
- drawer 关闭时 run 继续，状态点仍显示 running/approval/error。

不把聊天 DOM 放入 `.article` 或 `.annotation-layer`，因此正文复制和导出不会包含聊天。

### 15.3 消息渲染

- assistant Markdown 使用严格 sanitizer；禁止 raw HTML、script、inline event 和危险 URL；
- `membox://doc/<uuid>` 渲染为可点击内部链接，调用现有 document loader；
- 普通外链使用 `noopener noreferrer`；
- tool call 默认折叠，只显示名称、目标和结果；
- thinking 内容不显示；
- streaming 文本按 animation frame 合并更新，避免每个 token 触发布局；
- drawer 自己管理 scroll anchor，不调用 annotation `layoutMarginNotes()`。

### 15.4 多 tab

每个 tab 有自己的 `client_id` 和选中 session。多个 tab 订阅同一 session 时都看到输出，但只有 prompt 发起 tab 拥有 controller lease。另一个 tab 必须明确点击 `Take control`，不能因为最后一次打开而自动抢占。

## 16. 鉴权与安全

### 16.1 网络边界

- 继续只监听 `127.0.0.1`；
- Agent API 不启用宽泛 CORS；
- 非浏览器客户端使用 Companion bearer token；
- Web 使用同源、`HttpOnly`、`SameSite=Strict` 的短期 session cookie；
- 所有 mutation POST/PATCH 要求同源 `Origin` 校验及 CSRF header；
- SSE 使用 same-origin cookie，不把 token 放在 query string；
- `GET` 也不能产生 prompt、abort、approval 等副作用。

如果当前 Companion 的 CORS/auth 不能满足这些条件，Agent API 上线前必须先收紧，不能依赖“localhost 所以安全”。

### 16.2 内部 worker API

extension 使用与浏览器 token 不同的短期内部 token：

- 每个 worker 独立随机 token；
- 通过子进程 environment 传入，不写 session JSONL；
- endpoint 同时校验 worker ID、session ID 和 token；
- worker 退出立即吊销；
- token 不进入 stderr、tool result、SSE 或错误文本；
- 文档 selector 只能是 UUID，不接受 path traversal。

### 16.3 供应链和资源发现

- extension 源码内嵌在 `mm` build；materialize 使用 temp + fsync + atomic rename；
- 可选校验 build-time SHA-256；
- 不自动执行 `MEMBOX_HOME` 或文档目录中的 `.pi` 内容；
- launch args 固定使用 `--no-extensions` 等开关，再显式加载 membox extension；
- `pi_path` 只能来自本地设置/环境查找，浏览器不能在请求中指定；
- `mm agent doctor` 显示实际 path/version/flags，但不显示 secrets。

### 16.4 内容与提示注入

Markdown 是不可信模型输入。system prompt 要声明文档中的“执行命令、泄露密钥、绕过确认”等文本只是资料内容。更重要的是，模型无 bash/任意 filesystem 工具，所有写操作仍受 schema、revision 和人工确认约束。

## 17. 设置与诊断

建议增加设置：

```text
agent.enabled             true
agent.pi_path             ""        # empty = PATH lookup
agent.default_model       ""        # empty = Pi default/session restore
agent.thinking_level      medium
agent.max_workers         2
agent.idle_timeout        10m
```

不保存：provider API key、OAuth token、Pi auth file 内容。

CLI 增加：

```bash
mm agent status
mm agent doctor
```

两者查询/诊断 Companion，不独立启动第二个 Agent manager。`doctor` 检查：

- Companion 可达；
- Pi 可执行文件和版本；
- RPC capability probe；
- session directory 权限；
- extension materialization；
- 模型是否可用/是否需要用户在 Pi 中登录；
- mutation coordinator 是否允许写工具。

认证失败时 UI 提示用户在独立终端完成 Pi 的登录流程；membox 不嵌入供应商凭证表单。

版本策略：实现时锁定一个已通过 contract test 的最低 Pi 版本，并在代码中维护兼容范围。不能只检查 `pi` 命令存在；未知/过旧版本返回 `agent_incompatible_pi`，并给出检测到与要求的版本。

## 18. 错误恢复与可观测性

稳定错误 code 至少包括：

```text
agent_disabled
agent_unavailable
agent_incompatible_pi
agent_not_authenticated
agent_busy
agent_capacity_reached
session_not_found
session_file_missing
worker_start_failed
worker_exited
protocol_error
stream_replay_unavailable
not_run_controller
approval_expired
revision_conflict
mutation_denied
```

日志规则：

- Companion 日志记录 session ID、worker ID、run ID、event type 和耗时；
- 不记录完整 prompt、assistant 正文、Markdown 正文、API key 或 bearer token；
- stderr ring 有大小上限，诊断输出默认只返回最后若干脱敏行；
- tool metrics 记录名称、耗时、状态，不记录完整 arguments/result；
- HTTP 断连、SSE 重连和 dropped slow subscriber 可计数。

慢客户端不能阻塞 worker stdout。每个 subscriber 使用有界队列；溢出时断开该 subscriber，让其通过 snapshot/reset 恢复。

## 19. 关键 Go 接口草案

```go
type AgentManager interface {
    Status(ctx context.Context) (AgentStatus, error)
    ListSessions(ctx context.Context, q ListAgentSessionsQuery) ([]AgentSessionView, error)
    CreateSession(ctx context.Context, cmd CreateAgentSessionCommand) (AgentSessionSnapshot, error)
    Snapshot(ctx context.Context, sessionID string) (AgentSessionSnapshot, error)
    Prompt(ctx context.Context, cmd AgentPromptCommand) (AgentRunView, error)
    Abort(ctx context.Context, sessionID, runID string) error
    Subscribe(ctx context.Context, sessionID, afterEventID string) (AgentSubscription, error)
    ResolveApproval(ctx context.Context, cmd ResolveAgentApprovalCommand) error
    SetModel(ctx context.Context, sessionID string, model AgentModelRef) error
    SetThinking(ctx context.Context, sessionID, level string) error
    Shutdown(ctx context.Context) error
}
```

```go
type AgentPromptCommand struct {
    SessionID     string
    ClientID      string
    IdempotencyKey string
    Text          string
    DocumentID    string
}

type AgentEvent struct {
    Version   int
    ID        string
    SessionID string
    RunID     string
    Type      string
    At        time.Time
    Payload   json.RawMessage
}
```

工具 mutation 应依赖 application port，而不是 HTTP handler：

```go
type AgentDocumentTools interface {
    SearchDocuments(ctx context.Context, query string, limit int) ([]AgentDocumentHit, error)
    ReadDocument(ctx context.Context, id string, cursor string, limit int) (AgentDocumentChunk, error)
    GetDocument(ctx context.Context, id string) (AgentDocumentView, error)
    CreateNote(ctx context.Context, cmd CreateAgentNoteCommand) (AgentMutationResult, error)
    UpdateDocument(ctx context.Context, cmd UpdateAgentDocumentCommand) (AgentMutationResult, error)
    RenameDocument(ctx context.Context, cmd RenameAgentDocumentCommand) (AgentMutationResult, error)
}
```

内部 HTTP endpoint 只是 extension 到该 port 的进程边界适配器。

## 20. 实施文件规划

预计新增/修改：

```text
cmd/mm/main.go                               # 注入 Agent manager/client lifecycle
internal/agent/*                             # RPC manager 与协议
internal/agent/assets/membox.ts              # Pi extension
internal/infrastructure/sqlite/*             # agent_sessions migration/repository
internal/web/backend/agent_http.go            # public Agent API/SSE
internal/web/backend/agent_internal_http.go   # worker-only tools/context
internal/web/backend/server.go                # route/lifecycle wiring
internal/interfaces/cli/agent.go              # status/doctor
internal/interfaces/tui/agent*.go             # Agent state/update/view/client
internal/web/frontend/adapters/companion/agent/*        # Web drawer
internal/web/frontend/adapters/companion/integration.js # mount entry
internal/web/frontend/adapters/companion/companion.css  # drawer responsive styles
docs/pi-agent-integration.md                   # 本文
```

extension 的 TypeScript 应有自己的最小 build/typecheck 流程。若 Pi 可直接加载 `.ts`，发布物仍由 `go:embed` 固定；测试不能依赖开发机上未提交的 extension 文件。

## 21. 分阶段实施

### Phase 0：数据写入与安全前置

- 完成每个 home 单 mutation coordinator；
- 收紧 Agent endpoint 的 origin/CORS/cookie/bearer 规则；
- 增加跨进程 mutation 测试；
- 未完成前固定 `agent_write_tools=false`。

### Phase 1：RPC 核心与只读 API

- 实现 LF-only protocol、command correlation、worker state machine；
- capability/version probe；
- session catalog、新建/恢复/snapshot；
- event normalization、ring、SSE；
- read-only membox extension tools；
- fake Pi contract tests。

完成标准：curl/API 可以创建会话、流式回答、恢复并 abort，且模型只能读取 membox tools。

### Phase 2：TUI AGENT 模式

- `agentState`、workspace、transcript viewport；
- `Ctrl+K`、submit、abort、session picker；
- 当前文档 context chip；
- model/thinking 基础选择；
- reconnect/reset/error UI。

完成标准：删除现有占位错误，TUI 可以完成一轮真实对话并在重启后继续。

### Phase 3：Web chat drawer

- drawer DOM/CSS；
- session/model UI；
- SSE reconnect、scroll anchor、safe Markdown；
- `membox://` links；
- 多 tab observer/controller 行为。

完成标准：TUI 发起的 run 能在网页实时看到，反之亦然。

### Phase 4：写工具与审批

- mutation preview；
- RPC extension UI request 映射；
- controller lease；
- revision conflict；
- create/update/rename/annotation tools；
- TUI/Web approval UX。

完成标准：所有写入逐次确认、保 UUID、保 links/annotations，拒绝和超时零副作用。

### Phase 5：加固与发布

- worker eviction/orphan cleanup；
- crash/restart/Keep mode；
- doctor 和版本兼容；
- load/backpressure/security tests；
- 文档、帮助和用户故事更新。

## 22. 测试策略

### 22.1 Protocol unit tests

用 fake `pi` executable 测试：

- command id/response correlation；
- 多个异步 events 穿插 responses；
- partial read/write；
- CRLF 输入兼容；
- JSON 字符串内 `U+2028/U+2029` 不切帧；
- oversized/malformed frame；
- stderr 不污染 stdout；
- worker exit 释放所有 pending calls；
- `agent_end` 后 retry，只有 `agent_settled` 才结束 run。

### 22.2 Manager tests

- 同一 session 并发 open 只启动一个 worker；
- busy prompt 返回 409；
- idempotency retry 不重复 prompt；
- worker cap 和 idle eviction；
- running/approval worker 不被驱逐；
- crash 标记 interrupted，不自动重放；
- Companion shutdown 回收 workers；
- Keep 生命周期不受 TUI context cancellation；
- session path 越界被拒绝。

### 22.3 Stream tests

- monotonically increasing event IDs；
- reconnect replay；
- epoch mismatch/reset；
- ring 淘汰；
- slow subscriber 不阻塞 worker；
- snapshot + event 切换无丢失窗口；
- session switch 后旧 event 不进入新 transcript。

### 22.4 Tool/security tests

- 启动参数确实禁用 built-ins 和 discovery；
- read tool 大小限制/cursor；
- internal token 绑定 worker/session；
- browser 不能调用 internal endpoint；
- CSRF、cross-origin GET/POST/SSE 被拒绝；
- approval 非 controller 被拒绝；
- timeout 默认 deny；
- revision conflict 不覆盖新内容；
- prompt injection 不能获得未注册工具；
- logs/events 不泄漏 token 或绝对 session path。

### 22.5 TUI tests

- `Ctrl+K` 状态转换；
- loading/ready/running/error badges；
- delta append 与 scroll anchor；
- submit/abort；
- approval allow/deny/Esc；
- narrow/wide layout；
- canceled stream goroutine 不泄漏；
- Agent I/O 不在 `Update` 阻塞。

### 22.6 Web tests

- drawer mount 不修改 Miru article/annotation DOM；
- 正文 copy/export 不含 chat；
- safe Markdown/XSS；
- SSE reconnect/reset；
- requestAnimationFrame delta batching；
- mobile fullscreen；
- 多 tab controller；
- current document chip 随 reader 切换但不改变已提交 run context。

### 22.7 端到端测试

在临时 `MEMBOX_HOME` 和 fake/真实测试模型下验证：

1. TUI 创建 session 并发送 prompt；
2. Web 打开同 session，收到相同 transcript；
3. Web 发起读取当前 document 的问题；
4. TUI 看到 tool events；
5. 写工具请求在 controller UI 审批；
6. allow 后 UUID、links、annotations 保持；
7. deny 后文件和 DB 完全不变；
8. Companion 重启后恢复 session；
9. TUI 退出、Keep Companion 下 run 继续；
10. 多进程并发 mutation 不产生 split-brain。

真实 Pi contract tests 可作为带 build tag/环境变量的 CI job；普通单元测试不能要求模型 credential 或网络。

## 23. 验收标准

Feature 只有同时满足以下条件才算完成：

1. TUI `Ctrl+K` 可以发送真实 Pi prompt，不再返回占位错误。
2. TUI 和 Web 可选择并共享同一个持久化 session。
3. assistant 文本以增量方式显示，断线后可通过 replay 或 snapshot 恢复。
4. 同一 session 不会由两个 Pi workers 同时拥有。
5. HTTP retry 不会重复创建 run。
6. abort、worker crash、Companion restart 都有明确状态，且不自动重复写操作。
7. 当前 Document 通过服务端验证的 UUID 作为每轮 context 注入。
8. 默认没有 bash、任意 filesystem write 或第三方 extension/skill 权限。
9. 每个写工具逐次确认；拒绝、超时和 revision conflict 均无副作用。
10. Agent mutations 与 TUI/CLI/Web mutations 共用一个 coordinator。
11. Web drawer 不进入 `.article`/`.annotation-layer`，不污染复制与导出。
12. 多 TUI/多 tab 不会相互抢夺审批控制权。
13. Companion Keep/Session 生命周期语义对 Agent 同样成立，没有隐藏的常驻 Agent daemon。
14. Pi 缺失、版本不兼容或未认证时，TUI/Web 显示可操作诊断而不是崩溃。
15. `go test ./...`、race tests、JS/TS checks、协议测试和端到端测试通过。

## 24. 后续能力

V1 稳定后可以按独立设计增加：

- 显式 steer/follow-up queue；
- fork/clone 和会话树 UI；
- selection anchor 上下文；
- 图片输入；
- 用户可安装但逐项授权的 membox skills；
- tool diff 的独立全屏审阅器；
- session export；
- 更高密度的 Node SDK worker pool。

这些能力不能削弱本文的基本不变量：**Companion 是控制面、session 单 owner、事件有版本、写入单协调、权限默认最小、审批默认拒绝。**
