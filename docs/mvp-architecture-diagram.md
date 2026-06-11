# VFS MVP 架构图

## 图 1：整体分层架构（静态视角）

```
┌─────────────────────────────────────────────────────────────────┐
│                     Application Layer                            │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────────────┐    │
│  │ mm new │  │mm read │  │mm search│  │  mm digest     │    │
│  │         │  │         │  │         │  │  (git + LLM)    │    │
│  └────┬────┘  └────┬────┘  └────┬────┘  └────────┬────────┘    │
│       │            │            │                 │              │
│       └────────────┴─────┬──────┴─────────────────┘              │
│                          ▼                                       │
│              "ws://" —— 唯一命名空间                              │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                  VFS Control Plane (Metadata Only)               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐   │
│  │ Mount Table  │  │ UUID Registry│  │   Path Resolution    │   │
│  │(workspace)   │  │(frontmatter) │  │   (LPM + fallback)   │   │
│  │              │  │              │  │                      │   │
│  │ /        → main│  │uuid → ws_path │  │ws://inbox/a.md      │   │
│  │ /inbox   → scr │  │     → backend │  │   → ~/notes/inbox/...│   │
│  │ /work    → wrk │  │     → physical│  │                      │   │
│  └──────────────┘  └──────────────┘  └──────────────────────┘   │
│                              │                                   │
│                              ▼                                   │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              BackendDriver Interface                      │   │
│  │  Read() | Write() | List() | Stat() | GitLog() | IsHealthy()│   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐
│   Data Plane    │ │   Data Plane    │ │   Data Plane    │
│  ┌───────────┐  │ │  ┌───────────┐  │ │  ┌───────────┐  │
│  │  local    │  │ │  │  local    │  │ │  │  local    │  │
│  │  backend  │  │ │  │  backend  │  │ │  │  backend  │  │
│  │  "main"   │  │ │  │ "scratch" │  │ │  │  "work"   │  │
│  │           │  │ │  │           │  │ │  │ (ssh/ro)  │  │
│  │~/repo/    │  │ │  │~/.mm/    │  │ │  │(cache)    │  │
│  │  notes/   │  │ │  │scratch/   │  │ │  │           │  │
│  │  (git)    │  │ │  │ (no git)  │  │ │  │           │  │
│  └───────────┘  │ │  └───────────┘  │ │  └───────────┘  │
│      ▲          │ │      ▲          │ │      ▲          │
└──────┼──────────┘ └──────┼──────────┘ └──────┼──────────┘
       │                   │                   │
       └───────────────────┴───────────────────┘
                           │
                           ▼
              ┌─────────────────────┐
              │   Physical Storage  │
              │   (File System)     │
              │                     │
              │  inbox/2025/06/     │
              │    a3f7b2c1.md      │  ◄─── Source of Truth
              │    (UUID + yaml)    │       文件是真理源
              │                     │
              └─────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│              Index Layer (Derived, Rebuildable)                  │
│  ┌─────────────────┐  ┌─────────────────┐  ┌────────────────┐  │
│  │  registry.sqlite│  │  notes_fts      │  │  content_hash  │  │
│  │                 │  │  (FTS5虚拟表)    │  │  (SHA256)      │  │
│  │ uuid → ws_path  │  │                 │  │                │  │
│  │      → backend  │  │  BM25-ish       │  │  hash未变      │  │
│  │      → physical │  │  全文索引        │  │  → 跳过重索引  │  │
│  │      → title    │  │                 │  │                │  │
│  │      → tags     │  │  增量 + 可重建   │  │                │  │
│  │      → links    │  │                 │  │                │  │
│  │      → hash     │  │                 │  │                │  │
│  └─────────────────┘  └─────────────────┘  └────────────────┘  │
│                              ▲                                  │
│                              │                                  │
│                    mm index --rebuild                          │
│                    (秒级从文件重建)                              │
└─────────────────────────────────────────────────────────────────┘
```

**关键设计**：
- Control Plane 只碰 metadata，不碰实际字节。
- Data Plane 通过 `BackendDriver` 接口隔离，MVP 只有 `local` 实现。
- Index Layer 完全在文件系统之上，可以删除后秒级重建。

**元数据权威来源（必须严守的边界）**：

| 权威层级 | 位置 | 说明 |
|----------|------|------|
| **真理源** | `.md` frontmatter + `workspace.toml` | 用户直接可编辑的文本，唯一可信状态 |
| **派生缓存** | `registry.sqlite` + `notes_fts` | 从真理源物化出来的查询加速层，冲突时无条件服从真理源 |

图中的 "UUID Registry (frontmatter)" 指的是 Control Plane **读取并解析** frontmatter 的能力，而不是 frontmatter 本身在 Control Plane 里。frontmatter 的物理位置始终在 `.md` 文件中。

---

## 图 2：写路径数据流（mm new）

```
User: $ mm new "开会想法"

        │
        ▼
┌───────────────┐
│ 1. 分配 UUID  │  UUID v7 (时间有序)
│    (Control)  │  short-id = 前8位
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 2. 决定落点   │  default_backend = "main"
│    (Control)  │  ws_path = /inbox/2025/06/<short-id>.md
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 3. 路径解析   │  LPM: /inbox → backend "main"
│    (Control)  │  physical = ~/repo/notes/inbox/2025/06/
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 4. 写入文件   │  BackendDriver.Write()
│   (Data Plane)│  mkdir -p + os.WriteFile
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 5. 打开编辑器 │  $EDITOR <physical_path>
│  (Application)│
└───────┬───────┘
        │ 用户保存退出
        ▼
┌───────────────┐
│ 6. Lazy Index │  不强制触发！
│    (可选)     │  由 `mm index` 定时/手动增量更新
│               │  或后台 watcher（MVP 后）
└───────────────┘
```

**关键设计**：
- 采集（写文件）与处理（建索引）分离，kabi-digest / grox 的启示。
- `mm new` 不阻塞在索引更新上，保证 "想到就能记" 的 accessibility。

---

## 图 3：读/检索路径数据流（mm search → mm digest）

```
┌────────────────────────────────────────────────────────────────────┐
│                        检索链路（Search）                            │
└────────────────────────────────────────────────────────────────────┘

$ mm search "虚拟内存"

        │
        ▼
┌───────────────┐
│ 1. 查询解析   │  "虚拟内存" → FTS5 query
│               │  可选：--tag, --after, --before
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 2. FTS5 召回   │  SELECT docid FROM notes_fts WHERE body MATCH ?
│  (Index Layer) │  LIMIT 100
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 3. Registry   │  JOIN notes 表取 ws_path + title + tags
│   精排/过滤   │  按 recency + git activity 重排（MVP 后）
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 4. 返回结果   │  永远返回 ws:// 路径
│               │  ws://inbox/2025/06/a3f7b2c1.md
│               │  ws://docs/01-architecture.md
└───────────────┘

┌────────────────────────────────────────────────────────────────────┐
│                        日报链路（Digest）                            │
└────────────────────────────────────────────────────────────────────┘

$ mm digest --since 24h

        │
        ▼
┌───────────────┐
│ 1. 扫描 Mount │  筛选 git_tracked = true 的 backend
│    Table      │  main ✓, scratch ✗, work ✓
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 2. Git Log    │  BackendDriver.GitLog(since)
│   每个 backend │  git log --since=24h --name-only
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 3. 去重/读内容 │  按 UUID 去重，通过 registry 读当前 ws_path
│               │  通过 BackendDriver.Read() 读文件内容
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 4. LLM Prompt │  拼接变更文件内容 + system prompt
│               │  调用 OpenAI/Anthropic API
└───────┬───────┘
        │
        ▼
┌───────────────┐
│ 5. 生成日报   │  frontmatter: type: digest, sources: [uuid list]
│   写入文件    │  ws_path = /daily/2025-06-10.md
│               │  → resolve → BackendDriver.Write()
└───────────────┘

        │
        ▼
┌───────────────┐
│ 6. 可选 git   │  --auto-commit: git add + commit
│    commit     │  "digest: 2025-06-10"
└───────────────┘
```

---

## 图 4：容灾视角（为什么一个 backend 坏了不影响全局）

```
                    正常状态
                    ────────
    ┌──────────┐    ┌──────────┐    ┌──────────┐
    │  main    │◄──►│  index   │◄──►│ scratch  │
    │ (git)    │    │ (sqlite) │    │ (no git) │
    └──────────┘    └──────────┘    └──────────┘
         ▲                               ▲
         │                               │
    mm new / search                 mm new (inbox)
    mm digest                       (快速捕获)


                    main backend 损坏
                    ───────────────
    ┌──────────┐    ┌──────────┐    ┌──────────┐
    │  main    │✗   │  index   │◄──►│ scratch  │
    │ (offline)│    │ (sqlite) │    │ (online) │
    └──────────┘    └──────────┘    └──────────┘
         ✗               ▲               ▲
                         │               │
                    search 仍可用    mm new 仍可用
                    （返回其余 backend 的结果）

                    index.sqlite 损坏
                    ─────────────────
    ┌──────────┐    ┌──────────┐    ┌──────────┐
    │  main    │◄──►│  index   │✗   │ scratch  │
    │ (files)  │    │ (deleted)│    │ (files)  │
    └──────────┘    └──────────┘    └──────────┘
         ▲                               ▲
         │                               │
    mm index --rebuild              search 短暂不可用
    （秒级重建）                      （重建后恢复）
```

**关键设计**：
- MVP **不是**高可用/副本容灾系统。单个文件通常只存一份，backend 坏了就是数据丢失（除非 git 有历史）。
- 保证的是**架构隔离**：单 backend 故障不拖垮全局——backend 离线 = 搜索跳过它，其他 backend 仍可用；索引损坏 = 删除重建，因为文件才是 truth。

---

## 为什么这么画？

1. **三层分离一目了然**：Application 只认识 `ws://`，Control Plane 只做 metadata 决策，Data Plane 只负责字节读写。这是从 DFS 学到的 Control/Data Plane 分离。

2. **索引层画在文件系统旁边而非下面**：强调索引是"影子"，不是地基。删除影子不影响地基。

3. **写路径画成单向箭头，读路径画成闭环**：写是 fire-and-forget（快速记笔记），读是 query-and-enrich（搜索+日报）。

4. **单 backend 故障隔离单独成图**：因为这是 notes.md 的目标之一，必须让用户一眼看出"某个 backend 坏了不拖垮全局"在 MVP 层面是怎么保证的——注意这里保证的是"不拖垮全局"，不是"自动恢复丢失的数据"。

5. **没有画 SSH/S3/Embedding/Tantivy**：MVP 砍掉的都不画，避免架构图变成愿景图。
