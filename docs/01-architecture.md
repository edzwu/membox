# 系统架构

## 核心原则

**VFS 是路由层，不是存储层。**

文件永远留在原始物理路径（`~/work/notes/`、`~/diary/` 等），不迁移，不复制。VFS 只负责：知道文件在哪台机器的哪个路径，并把读写请求路由过去。

这保证了：
- **向后兼容**：已有的任意目录直接 `mm backend add` 加入，文件一行不动
- **就地编辑**：`vim ~/work/notes/foo.md` 永远有效
- **统一搜索**：跨机器的全文搜索，不需要把文件聚合到一处

---

## 整体架构：Hub-and-Spoke

```
                    ┌─────────────────────────┐
                    │          Hub            │
                    │  ┌───────────────────┐  │
                    │  │   SQLite          │  │
                    │  │   全局 FTS 索引    │  │
                    │  │   路由配置        │  │
                    │  └───────────────────┘  │
                    │  监听一个端口 :8080      │
                    └────────────┬────────────┘
                                 │
              ┌──────────────────┼──────────────────┐
              │ WebSocket        │ WebSocket        │ HTTP
              │ (出站，不开端口) │ (出站，不开端口) │
              │                  │                  │
        Spoke A              Spoke B           Client
        mm-agent             mm-agent          mm CLI
        ┌──────────┐         ┌──────────┐      (无状态
        │work      │         │personal  │       或注册
        │backend   │         │backend   │       scratch
        │~/work/   │         │~/diary/  │       backend)
        │notes/    │         │          │
        │SQLite    │         │SQLite    │
        └──────────┘         └──────────┘
        文件留在原地          文件留在原地
```

---

## 三个角色

### Hub
- **唯一开放端口的机器**（一台有固定地址的 VPS 或机器）
- 存全局 FTS 索引（从各 Spoke 推送汇总而来）
- 存路由配置（backend name → Spoke 的 WebSocket 连接）
- 不存文件内容本身

### Spoke
- **有 backend 的机器**，运行 `mm-agent`
- 管理本地真实文件（读、写、fsnotify 监控）
- 主动出站 WebSocket 连接到 Hub，**不开任何端口**
- 有本地 SQLite（本地 FTS + 文件索引）

### Client
- **纯访问型**，无状态，搜索和编辑都通过 Hub 路由
- 不需要本地 DB
- 如果是常用机器，建议注册一个 `scratch` backend，自动变成 Spoke

---

## 网络层

```
Spoke 启动：
  Spoke ──── WebSocket connect ────→ Hub:8080
  注册：{ backend: "work", root: "~/work/notes" }
  保持连接，等待 Hub 转发来的请求

客户端请求：
  Client ──── HTTP ────→ Hub:8080
  Hub 查路由表：work → Spoke A 的 WS 连接
  Hub ──── WS 转发 ────→ Spoke A
  Spoke A 处理本地文件，结果原路返回
```

**端口汇总：**

| 角色 | 开放端口 |
|------|---------|
| Hub | 一个（如 8080） |
| Spoke | 零 |
| Client | 零 |

NAT 友好：Spoke 只需要出站连接，任何在 NAT 后面的机器都可以作为 Spoke。

---

## 数据库设计

### Hub SQLite（全局索引，物化视图）

```sql
-- Spoke/Backend 注册信息（持久化，hub 重启后 spoke 重连可恢复）
CREATE TABLE backends (
    name          TEXT PRIMARY KEY,
    machine_id    TEXT,
    physical_root TEXT,
    git_tracked   BOOLEAN,
    last_seen     TEXT
);

-- 全局文件元数据（从各 Spoke 推送汇总）
CREATE TABLE documents (
    uuid         TEXT PRIMARY KEY,
    backend      TEXT,
    rel_path     TEXT,
    title        TEXT,
    modified     TEXT,
    content_hash TEXT
);

-- 全局 FTS（所有 Spoke 的内容在这里统一搜索）
CREATE VIRTUAL TABLE documents_fts USING fts5(
    title,
    content,
    content_rowid = rowid,
    tokenize = 'porter'
);

-- 变更日志（用于断线重连的差量补偿）
CREATE TABLE changelog (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    op         TEXT,   -- 'upsert' / 'delete'
    uuid       TEXT,
    backend    TEXT,
    hash       TEXT,
    changed_at TEXT
);
```

**Hub SQLite 是派生的**，所有 Spoke 重新推送索引即可重建，不是权威数据。

### Spoke SQLite（本地索引，Source of Truth 是文件）

```sql
-- 本地文件注册
CREATE TABLE local_notes (
    uuid             TEXT PRIMARY KEY,
    physical_path    TEXT,
    title            TEXT,
    mtime            INTEGER,
    content_hash     TEXT,
    hub_synced_hash  TEXT   -- 上次推送到 Hub 时的 hash，NULL 表示未推送
);

-- 本地 FTS（离线搜索用）
CREATE VIRTUAL TABLE local_notes_fts USING fts5(
    title, content, content_rowid = rowid
);

-- Hub 同步状态
CREATE TABLE hub_sync_state (
    hub_url            TEXT PRIMARY KEY,
    last_changelog_id  INTEGER,
    last_synced_at     TEXT
);
```

> **为什么用 SQLite 不用 PostgreSQL**：个人使用，写操作基本串行，几千个 markdown 文件，SQLite WAL 模式完全够用。PostgreSQL 是 overkill，等并发真的成问题再换。

---

## Spoke → Hub 同步

同步方向是**单向**的：Spoke → Hub。Hub 是物化的搜索索引，不是权威数据。

### 实时同步（Spoke 在线时）

```
~/diary/foo.md 被修改
       │ fsnotify 检测到
       ▼
Spoke mm-agent 计算新 content_hash
       │ hash 变化才推送（避免重复）
       ▼
WebSocket push 到 Hub
{ uuid, backend, rel_path, title, content, hash }
       │
       ▼
Hub 更新 documents + documents_fts + changelog
Spoke 更新本地 hub_synced_hash
```

### 断线重连补偿

```
Spoke 重新连接到 Hub
       │
       │ 发送本地文件清单
       │ [{ uuid, hash }, ...]
       ▼
Hub 对比：hash 不同 or 不存在的条目
       │ 回复差异列表 [uuid-a, uuid-c, ...]
       ▼
Spoke 只推送这些文件的 title + content
       │
       ▼
Hub 更新 FTS 索引，同步完成
```

只传差量，不全量重传（rsync 思想）。

### Hub 完全丢失

所有 Spoke 重新推送全量索引，Hub SQLite 秒级重建。

---

## 搜索流程

```
A 机器：mm search "keyword"
       │ HTTP → Hub
       ▼
Hub 查全局 FTS
       │ 返回 [{ backend:"personal", path:"diary/foo.md", snippet }]
       ▼
A 机器显示结果
（到这里没有碰任何 Spoke）

A 机器：mm read personal/diary/foo.md
       │ HTTP → Hub
       ▼
Hub 查路由表：personal → Spoke B 的 WS 连接
       │ 通过 WS 转发到 Spoke B
       ▼
Spoke B 读取 ~/diary/foo.md
       │ 内容原路返回
       ▼
A 机器拿到文件内容
```

搜索和读取是分离的：**搜索只查 Hub，读文件才访问 Spoke**。

---

## 文件 CRUD

| 操作 | 流程 |
|------|------|
| `mm new` | Client → Hub → 路由到目标 Spoke → Spoke 写本地文件 → fsnotify 触发索引推送 |
| `mm read` | Client → Hub → 路由到 Spoke → 返回文件内容 |
| `mm edit` | read 流程 + 下载到本地缓存 → 打开 `$EDITOR` → 保存后 PUT 回 Hub → 路由到 Spoke 写文件 |
| `mm delete` | Client → Hub → 路由到 Spoke → Spoke 删文件 → Hub 移除索引 |
| `mm search` | Client → Hub FTS → 返回结果（不访问任何 Spoke） |

---

## add backend 流程

```bash
# 在 Machine B 上执行
mm backend add personal ~/diary

# 发生了什么：
# 1. Machine B 启动 mm-agent（如未运行）
# 2. mm-agent 出站 WebSocket 连接到 Hub
# 3. 注册：{ backend:"personal", root:"~/diary", machine:"machine-b" }
# 4. mm-agent 扫描 ~/diary，建立本地 SQLite 索引
# 5. 把所有文件的 title + content 推送到 Hub 建立全局 FTS
# 6. 完成——文件一行未动，还在 ~/diary/
```

---

## 与 MVP 的关系

当前 MVP 是单机版本（本地 FTS + SQLite），不涉及 Hub-and-Spoke。

| 阶段 | 内容 |
|------|------|
| MVP（当前） | 单机，本地多 backend，本地 SQLite FTS，`mm new/index/search/digest` |
| Phase 2 | mm-agent + Hub + WebSocket + 跨机器路由 |
| Phase 3 | 断线重连补偿 + hash 清单对比 + conflict 处理 |
| Phase 4 | Client 缓存（LRU）+ 离线编辑队列 |

MVP 的所有代码（UUID、frontmatter、FTS schema、pdf ingest）在 Phase 2 里全部复用。Phase 2 本质上是在 MVP 之上加一个网络路由层。
