# MVP：最小可行产品

> Hub-and-Spoke 架构，Hub 可以跑在 localhost（单机）或公网机器（多机），两者是同一套代码的不同部署方式，不是两个版本。  
> 架构详情见 `docs/01-architecture.md`。

---

## 一句话定义

MVP = **Hub + 至少一个 Spoke + 本地 backend** 跑通 `mm new` → `mm index` → `mm search` → `mm digest`。

单机部署时 Hub 和 Spoke 都在 localhost，用户感知不到区别。多机部署时把 Hub 搬到公网机器，其余代码不变。

---

## 必须达成的目标

1. **Hub 是核心**：所有路由、搜索、文件读写都经过 Hub。
2. **文件留在原地**：`mm backend add` 注册任意本地路径，文件一行不动。
3. **Spoke 零端口**：mm-agent 只出站连接 Hub，不开任何入站端口。
4. **统一搜索**：`mm search` 查 Hub 全局 FTS，跨所有 Spoke 的所有 backend。
5. **索引与存储解耦**：Hub SQLite 是物化视图，完全丢失后所有 Spoke 重推即可重建。

---

## 明确不做

| 功能 | 理由 |
|------|------|
| Hub 高可用 / 多 Hub | MVP Hub 是单点，挂了重启即可，个人工具够用 |
| 断线重连补偿（hash 清单对比） | Phase 2，MVP 重连后全量重推 |
| 客户端缓存 LRU | Phase 2，MVP 每次 edit 重新拉取 |
| 冲突检测 / conflict file | Phase 2，MVP last-write-wins |
| SSH / S3 remote backend | Phase 2+，接口预留 |
| Embedding / 语义搜索 | `mm related` 保留 hashed-bow，不作默认 |
| TUI / nvim 插件 | TUI 是 MVP P0； nvim 插件 Phase 2+ |
| Content hash 增量 | `mtime + size` 够用 |

---

## 架构（Hub + Spoke，单机部署示例）

```
┌─────────────────────────────────────────────┐
│  本机（localhost）                            │
│                                             │
│  ┌──────────────────────────────────────┐   │
│  │  Hub（mm hub start）                 │   │
│  │  - 全局 FTS（SQLite）                 │   │
│  │  - 路由表（backend → WS 连接）        │   │
│  │  - 监听 localhost:8080               │   │
│  └──────────────┬───────────────────────┘   │
│                 │ WebSocket                 │
│  ┌──────────────┴───────────────────────┐   │
│  │  Spoke（mm agent start）             │   │
│  │  - fsnotify 监控本地 backend 目录     │   │
│  │  - 本地 SQLite（本地 FTS）            │   │
│  │  - 出站连接 Hub，不开入站端口         │   │
│  │                                      │   │
│  │  backend: work     → ~/work/notes/   │   │
│  │  backend: personal → ~/diary/        │   │
│  └──────────────────────────────────────┘   │
│                                             │
│  mm CLI → HTTP → localhost:8080（Hub）       │
└─────────────────────────────────────────────┘
```

多机部署时：Hub 搬到公网机器，Spoke 在各自机器上出站连接，代码不变。

---

## 配置文件

### `~/.config/mm/backends.toml`

```toml
[backends.work]
type        = "local"
root        = "/Users/edward/work/notes"
writable    = true
git_tracked = true

[backends.personal]
type        = "local"
root        = "/Users/edward/personal/diary"
writable    = true
git_tracked = false
```

### `~/.config/mm/settings.toml`

```toml
[hub]
url = "http://localhost:8080"   # 单机时 localhost，多机时改为公网地址

[defaults]
backend = "work"

[editor]
bin = "nvim"

[llm]
api_key  = "sk-..."
model    = "gpt-4o-mini"
endpoint = "https://api.openai.com/v1"
```

---

## Hub SQLite Schema

```sql
-- backend 注册（Spoke 连接时写入，持久化）
CREATE TABLE backends (
    name          TEXT PRIMARY KEY,
    machine_id    TEXT,
    physical_root TEXT,
    git_tracked   BOOLEAN,
    last_seen     TEXT
);

-- 全局文件元数据（从 Spoke 推送汇总）
CREATE TABLE documents (
    uuid         TEXT PRIMARY KEY,
    backend      TEXT,
    rel_path     TEXT,
    title        TEXT,
    modified     TEXT,
    content_hash TEXT
);

-- 全局 FTS
CREATE VIRTUAL TABLE documents_fts USING fts5(
    title, content,
    content_rowid = rowid,
    tokenize = 'porter'
);

-- 变更日志（Phase 2 断线补偿用，MVP 预留）
CREATE TABLE changelog (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    op         TEXT,
    uuid       TEXT,
    backend    TEXT,
    hash       TEXT,
    changed_at TEXT
);
```

## Spoke SQLite Schema

```sql
-- 本地文件注册
CREATE TABLE local_notes (
    uuid             TEXT PRIMARY KEY,
    backend          TEXT,
    rel_path         TEXT,
    physical_path    TEXT,
    title            TEXT,
    mtime            INTEGER,
    content_hash     TEXT,
    hub_synced_hash  TEXT   -- 上次推送到 Hub 时的 hash
);

-- 本地 FTS（离线搜索）
CREATE VIRTUAL TABLE local_notes_fts USING fts5(
    title, content, content_rowid = rowid
);

-- Hub 同步状态（Phase 2 断线补偿用，MVP 预留）
CREATE TABLE hub_sync_state (
    hub_url           TEXT PRIMARY KEY,
    last_changelog_id INTEGER,
    last_synced_at    TEXT
);
```

---

## CLI 命令集

### 服务启动
```bash
mm hub start [--port 8080]       # 启动 Hub 服务
mm agent start [--hub <url>]     # 启动 Spoke agent，连接 Hub
```

### 配置
```bash
mm init                          # 创建配置目录，交互式引导
mm backend add <name> <path> [--git]
mm backend ls
mm backend rm <name>
```

### 读写（经过 Hub 路由）
```bash
mm new [title] [--to work/daily] # 新建笔记，默认 backend 的 inbox/
mm read <backend>/<path>         # 通过 Hub 路由到 Spoke 读文件
mm edit <backend>/<path>         # 拉取 → 本地编辑 → 推回
```

### 索引与搜索
```bash
mm index                         # Spoke 扫描所有 backend，推送索引到 Hub
mm index --rebuild               # 全量重建
mm index <pdf-or-dir>            # 索引 PDF（复用既有能力）
mm search <query>                # 查 Hub 全局 FTS
mm search <query> --backend work # 限定 backend
mm search <query> --json
mm related --query "..."         # 语义相关（hashed-bow）
```

### 日报
```bash
mm digest [--since 24h]          # 扫描 git_tracked backend，LLM 生成日报
mm digest --stdout
```

### TUI
```bash
mm tui                           # 启动 TUI　搜索 + 结果列表 + 预览 + 编辑
mm tui --backend work            # 限定到指定 backend
```

---

## 数据流

### 索引流程
```
Spoke fsnotify 检测到文件变化
  → 计算 content_hash
  → hash 变化才推送（避免重复）
  → WebSocket push 到 Hub
     { uuid, backend, rel_path, title, content, hash }
  → Hub 更新 documents_fts + changelog
  → Spoke 更新 hub_synced_hash
```

### 搜索流程
```
mm search "keyword"
  → HTTP → Hub
  → Hub 查 documents_fts
  → 返回 [{backend, rel_path, snippet}]
  （全程不访问 Spoke）

mm read work/foo.md
  → HTTP → Hub
  → Hub 查路由：work → Spoke WS 连接
  → WS 转发到 Spoke
  → Spoke 读 ~/work/notes/foo.md
  → 内容原路返回
```

---

## 实现顺序（建议 3 周）

### Week 1：Hub + Spoke 骨架
1. Hub 服务：WebSocket server + 路由表 + SQLite schema
2. mm-agent：出站 WebSocket + backend 注册 + 心跳
3. `mm backend add/ls/rm` + `backends.toml` 解析
4. `BackendDriver` 接口 + `local` 实现
5. 路径解析（`backend/rel_path` → 物理路径）

### Week 2：文件操作 + 索引
6. `mm new`：UUID v7 + frontmatter → Spoke 写文件 → 推送 Hub
7. `mm read` / `mm edit`：Hub 路由 → Spoke 读写
8. `mm index`：Spoke 扫描 `.md` → 推送 Hub FTS
9. fsnotify：文件变化实时推送
10. 复用既有 PDF ingest → `chunk_fts`

### Week 3：搜索 + 日报 + TUI + 收尾
11. `mm search`：查 Hub FTS，返回 `backend/rel_path`
12. `mm digest`：git log + LLM（复用既有逻辑）
13. `mm api serve`：Hub 暴露 HTTP API（复用 `service/routes.py`）
14. `mm tui`：textinput + list + viewport，对接 Hub FTS
15. 端到端验收

---

## 验收标准

```bash
# 1. 启动服务（单机，localhost）
$ mm hub start &
$ mm agent start &

# 2. 注册 backend
$ mm backend add work ~/work/notes --git
$ mm backend add personal ~/diary
# → Spoke 注册到 Hub，扫描目录，推送初始索引

# 3. 新建笔记
$ mm new "GFS 阅读笔记" --to work/reading
# → ~/work/notes/reading/<short-id>.md
# → Hub 全局 FTS 立即可搜

# 4. 跨 backend 搜索（查 Hub FTS）
$ mm search "GFS"
# → work/reading/<short-id>.md

$ mm index ./papers/gfs.pdf
$ mm search "chunkserver"
# → PDF chunk 结果，带 page 号

$ mm search "想法"
# → 同时返回 work/ 和 personal/ 的结果

# 5. 读取和编辑（Hub 路由到 Spoke）
$ mm read work/reading/<short-id>.md
$ mm edit work/reading/<short-id>.md
# → 拉取 → nvim → 保存 → 推回 Spoke

# 6. 日报
$ mm digest --since 24h
# → work/daily/2026-06-10.md

# 7. TUI
$ mm tui
# → 默认显示最近修改的文件
# 输入 "GFS" → 300ms 后刷新结果列表
# 上下键切换选中项，右侧预览自动更新
# Enter 打开 nvim，退出后返回 TUI
# Ctrl+N 新建笔记，退出编辑器后列表刷新
$ mm tui --backend work
# → 只搜索 work backend

# 8. 多机器部署（Phase 2，只改配置）
# Hub 搬到公网机器，settings.toml 改 hub.url
# Spoke 出站连接公网 Hub，其余代码不变
```

---

## 与 Phase 2 的边界

| MVP | Phase 2 |
|-----|---------|
| Hub 跑 localhost | Hub 跑公网机器 |
| 重连后全量重推索引 | hash 清单对比，只传差量 |
| last-write-wins | content hash 冲突检测 + `.conflict.md` |
| 每次 edit 重新拉取 | 本地缓存 LRU |
| mtime 增量 | content hash 增量 |
| local backend only | + ssh / s3 / remote |
