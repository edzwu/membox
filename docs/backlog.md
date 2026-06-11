# Backlog: membox — 统一个人知识 workspace

> 组织方式：Epic → User Story → Acceptance Criteria  
> 优先级：P0（MVP 必须）、P1（MVP 最好有）、P2（Phase 2 跨机器）、P3（Phase 3+）  
> 架构参考：`docs/01-architecture.md`（Hub-and-Spoke）、`docs/mvp.md`（单机 MVP）

---

## Epic 1: 本地 Backend 管理
> **目标**：把任意本地路径注册进 VFS，文件一行不动。  
> **MVP 核心**：`mm backend add/ls/rm` 是 P0，这是"向后兼容"的入口。

### Story 1.1 — 初始化配置
**As a** user, **I want** 一条命令初始化配置目录，**so that** 不用手动写 TOML。

- **AC1**: `mm init` 创建 `~/.config/mm/`，生成空的 `backends.toml` 和 `settings.toml`。
- **AC2**: 交互式询问默认 backend 名和路径，自动执行 `mm backend add`。
- **AC3**: 已初始化时再次运行不覆盖已有配置，提示当前状态。
- **Priority**: P0

### Story 1.2 — 注册本地 Backend
**As a** user, **I want** 把任意本地路径加入 VFS，**so that** 不同目录的文件可以统一搜索和管理。

- **AC1**: `mm backend add <name> <path> [--git]` 写入 `backends.toml`，不移动文件。
- **AC2**: `mm backend ls` 列出所有 backend（名称、路径、git 状态、文件数量）。
- **AC3**: `mm backend rm <name>` 删除配置（不删物理文件），若有未同步的索引给出提示。
- **AC4**: 同名 backend 不可重复注册，路径不存在时报错。
- **Priority**: P0
- **Notes**: 这是"向后兼容"的核心。用户已有的 `~/work/notes/`、`~/diary/` 直接 `add` 进来，零迁移成本。

### Story 1.3 — 路径解析
**As a** the system, **I want** 把 `<backend>/<rel_path>` 解析到物理绝对路径，**so that** 上层命令不感知 backend 的存在。

- **AC1**: `work/daily/foo.md` → `~/work/notes/daily/foo.md`。
- **AC2**: 写入时自动 `mkdir -p` 父目录。
- **AC3**: backend 不存在或路径越权时返回明确错误。
- **AC4**: 代码预留 `ws://` 前缀支持，Phase 2 开放多机器时无需改核心逻辑。
- **Priority**: P0

### Story 1.4 — 故障隔离
**As a** user, **I want** 某个 backend 目录损坏或离线时其余 backend 不受影响，**so that** 数据不会全军覆没。

- **AC1**: `registry.sqlite` 存放在 `~/.cache/mm/`，与各 backend 物理目录解耦。
- **AC2**: `work` backend 目录被移走时，`mm search` 仍返回 `personal` 的结果，并对 `work` 给出警告。
- **AC3**: 目录恢复后 `mm index --rebuild` 秒级重建。
- **Priority**: P0

---

## Epic 2: Note Lifecycle
> **目标**：想到就能记，文件永远可直接 vim 编辑。

### Story 2.1 — 快速创建笔记
**As a** user, **I want** 一条命令生成带 UUID frontmatter 的 markdown 并打开编辑器，**so that** 不用想文件名和路径。

- **AC1**: `mm new [title]` 生成 UUID v7，取前 8 位为 `short_id`。
- **AC2**: 默认落到 `settings.toml` 中 `defaults.backend` 的 `inbox/<year>/<month>/<short_id>.md`。
- **AC3**: `--to work/daily` 指定 backend + 子目录。
- **AC4**: frontmatter 包含 `id`、`title`、`created`、`modified`、`tags: []`。
- **AC5**: 调用 `$EDITOR` 打开，显示路径格式 `work/inbox/2026/06/<short_id>.md`。
- **Priority**: P0

### Story 2.2 — 读取笔记
**As a** user, **I want** 通过 `backend/path` 读取文件内容，**so that** 不需要记物理路径。

- **AC1**: `mm read work/daily/foo.md` 把内容 cat 到 stdout。
- **AC2**: 文件不存在时返回清晰错误。
- **Priority**: P0

### Story 2.3 — 兼容老文件
**As a** user with existing notes, **I want** 已有的非 UUID 命名文件直接可用，**so that** 不需要立刻全量迁移。

- **AC1**: `mm backend add` 后，老文件在 registry 中以 `legacy` 标记，仍可搜索和读取。
- **AC2**: 只有 `mm new` 创建的新文件强制 UUID 命名，老文件保持原样。
- **Priority**: P0

### Story 2.4 — Quick Capture
**As a** user, **I want** 快速追加一段文字到当日 capture 文件，**so that** 碎片想法不用开新文件。

- **AC1**: `mm capture "想法"` 追加到当前 backend 的 `inbox/<year>/<month>/capture.md`。
- **AC2**: 文件不存在则自动创建（带 frontmatter）。
- **AC3**: 追加内容带时间戳前缀（`- 14:35 想法`）。
- **Priority**: P1

---

## Epic 3: Content Indexing
> **目标**：把本地文件变成可搜索的索引。  
> **既有资产**：`mcore/schema.sql`、`mcore/indexer.py`、`mcore/ingest_pdf.py`。

### Story 3.1 — 扫描索引 Markdown
**As a** user, **I want** `mm index` 扫描所有 backend 的 `.md` 文件并建立 FTS 索引，**so that** 笔记可被全文检索。

- **AC1**: `mm index` 遍历所有已注册 backend，递归找 `.md`。
- **AC2**: 提取 frontmatter（uuid、title、tags）存入 `notes` 表。
- **AC3**: 正文进入 `notes_fts`（porter tokenizer）。
- **AC4**: `--rebuild` 全量重建，`--backend work` 只扫描指定 backend。
- **Priority**: P0

### Story 3.2 — 增量索引
**As a** user, **I want** 重复 `mm index` 只处理变更文件，**so that** 速度够快。

- **AC1**: 比较 `(mtime, size)` 与 registry，未变更跳过。
- **AC2**: 手动 `mv` 文件后，uuid 不变则只更新路径映射。
- **AC3**: 文件删除后标记 `orphaned`，搜索不返回，`--rebuild` 时清理。
- **Priority**: P0

### Story 3.3 — 索引 PDF（复用既有）
**As a** user, **I want** PDF 也纳入统一索引，**so that** 阅读材料和笔记在同一个搜索空间。

- **AC1**: `mm index <pdf-or-dir>` 提取文本 → chunking → 存入 `chunk_fts`（复用 `mcore/`）。
- **AC2**: PDF 在 registry 中标记 `type=doc`，`.md` 标记 `type=note`，搜索结果标明类型。
- **Priority**: P0
- **Notes**: 直接复用 `mcore/ingest_pdf.py`、`mcore/chunking.py`、`mcore/indexer.py`。

### Story 3.4 — 新建后自动索引
**As a** user, **I want** `mm new` 后不需要手动跑 `mm index`，**so that** 新笔记立即可搜。

- **AC1**: `mm new` 成功后自动更新 registry + FTS。
- **AC2**: 自动索引失败只警告，不阻塞主操作。
- **Priority**: P1

---

## Epic 4: Search & Discovery
> **既有资产**：`mcore/db.py` FTS5 查询、`mcore/embedder.py`、`service/routes.py`。

### Story 4.1 — 全文搜索
**As a** user, **I want** 关键词跨所有 backend 搜索，**so that** 快速找到任意笔记或 PDF。

- **AC1**: `mm search <query>` 返回 `backend/rel_path`、title、snippet，按 BM25 排序。
- **AC2**: `--backend work` 限定范围，`--json` 输出 JSON。
- **AC3**: 默认 top 10，`-n` 调整。
- **Priority**: P0

### Story 4.2 — 语义相关（复用 embedding）
**As a** user, **I want** 基于语义找到相关段落，**so that** 发现隐性关联。

- **AC1**: `mm related --query "..."` 用 hashed-bow embedding 找最相似 chunk。
- **AC2**: `--doc <pdf> --page 12` 以 PDF 页为种子。
- **AC3**: `--bootstrap` 首次计算所有 chunk 的 embedding。
- **Priority**: P1

### Story 4.3 — HTTP API
**As a** client developer, **I want** 本地 HTTP API，**so that** 编辑器插件不需要直接读 SQLite。

- **AC1**: `mm api serve` 启动服务，提供 `/search`、`/ingest`、`/reindex`（复用 `service/routes.py`）。
- **AC2**: 返回路径格式为 `backend/rel_path`。
- **Priority**: P0

---

## Epic 5: AI Digest
> **目标**：系统帮我回顾今日变更，不用手动翻 git log。

### Story 5.1 — Git 变更日报
**As a** user, **I want** 自动读取 git-tracked backend 的今日变更并生成日报，**so that** 不用手动 git log。

- **AC1**: `mm digest [--since 24h]` 扫描所有 `git_tracked=true` 的 backend。
- **AC2**: `git log --since=<period> --name-only` 收集变更 `.md`，按 uuid 去重。
- **AC3**: 调用 LLM API（OpenAI-compatible，读 `settings.toml` 的 llm 配置）。
- **AC4**: 日报保存到 `<default_backend>/daily/<date>.md`，`--stdout` 直接打印。
- **AC5**: `--backend work` 只看指定 backend。
- **Priority**: P0

### Story 5.2 — Prompt 模板
**As a** user, **I want** 自定义日报 prompt，**so that** 风格符合习惯。

- **AC1**: `~/.config/mm/digest-prompt.md` 存在时优先使用。
- **AC2**: 模板变量：`{{date}}`、`{{sources}}`、`{{content}}`。
- **Priority**: P3

---

## Epic 6: Hub-and-Spoke 架构
> **目标**：Hub 是整个系统的核心，单机时跑 localhost，多机时搬到公网机器，同一套代码。  
> **架构详情**：见 `docs/01-architecture.md`。

### Story 6.1 — Hub 服务
**As a** user, **I want** 启动一个 Hub 服务作为路由和搜索中心，**so that** 所有读写和搜索都经过统一入口。

- **AC1**: `mm hub start [--port 8080]` 启动 Hub，监听指定端口。
- **AC2**: 维护路由表：`backend name → Spoke WS 连接`（内存，Spoke 重连后自动恢复）。
- **AC3**: 持久化 backend 注册信息到 Hub SQLite（Spoke 离线期间保留记录）。
- **AC4**: 维护全局 FTS 索引（从各 Spoke 推送汇总）。
- **AC5**: Hub SQLite 完全丢失时，所有 Spoke 重连后重推索引即可重建。
- **Priority**: P0

### Story 6.2 — mm-agent（Spoke 守护进程）
**As a** user, **I want** 每台有 backend 的机器运行一个 agent，**so that** 文件变化能实时同步到 Hub。

- **AC1**: `mm agent start [--hub <url>]` 启动，出站 WebSocket 连接到 Hub。
- **AC2**: 向 Hub 注册本机所有 backend（name、physical_root、git_tracked）。
- **AC3**: fsnotify 监控所有 backend 目录，文件变化时计算 hash，变化才推送索引。
- **AC4**: 连接断开时自动重连（指数退避）。
- **AC5**: 不开任何入站端口。
- **Priority**: P0

### Story 6.3 — 文件读写（Hub 路由）
**As a** user, **I want** 通过 `mm read/edit` 访问任意 backend 的文件，**so that** 不需要知道文件的物理位置。

- **AC1**: `mm read work/foo.md`：Hub 查路由 → WS 转发 Spoke → Spoke 读本地文件 → 返回。
- **AC2**: `mm edit work/foo.md`：read 流程 + 写入本地临时文件 → `$EDITOR` → 保存后 PUT 回 Hub → Spoke 写原始文件。
- **AC3**: MVP 阶段 last-write-wins，不做冲突检测。
- **Priority**: P0

### Story 6.4 — 全局全文搜索
**As a** user, **I want** `mm search` 搜索所有 Spoke 上所有 backend 的内容，**so that** 真正统一搜索。

- **AC1**: `mm search <query>` 查 Hub 全局 FTS，返回 `backend/rel_path` + snippet。
- **AC2**: Spoke 离线时，Hub 仍返回上次同步的索引内容（标注该 backend 离线）。
- **Priority**: P0

### Story 6.5 — 实时索引推送（Spoke → Hub）
**As a** user, **I want** 文件改动后 Hub 的索引自动更新，**so that** 搜索结果始终是最新的。

- **AC1**: 文件变化 → fsnotify → 计算 content_hash → hash 变化才推送。
- **AC2**: 推送内容：`{ uuid, backend, rel_path, title, content, hash }`。
- **AC3**: Hub 更新 `documents_fts` + `changelog`，Spoke 更新 `hub_synced_hash`。
- **Priority**: P0

### Story 6.6 — 新机器接入
**As a** user, **I want** 在新机器上快速加入已有的 VFS，**so that** 不用手动配置。

- **AC1**: `mm init --hub <hub-url>` 连接 Hub，拉取已有 backend 列表。
- **AC2**: 提示用户哪些 backend 在本机有对应路径（注册为 Spoke），哪些只作 client 访问。
- **AC3**: 注册本机新 backend 时自动推送初始索引到 Hub。
- **Priority**: P0

### Story 6.7 — 断线重连补偿
**As a** user, **I want** Spoke 断线重连后自动补偿期间的文件变化，**so that** Hub 索引不会长期 stale。

- **AC1**: Spoke 重连时发送本地文件清单 `[{uuid, hash}]` 给 Hub。
- **AC2**: Hub 对比，返回差异列表（hash 不同或 Hub 没有的条目）。
- **AC3**: Spoke 只推送差异文件（rsync 思想，不全量重传）。
- **Priority**: P2

### Story 6.8 — 客户端按需缓存
**As a** client machine user, **I want** 编辑过的文件在本地缓存，**so that** 下次打开秒开不重新下载。

- **AC1**: `mm edit` 下载的文件存入 `~/.cache/mm/files/<uuid>.md`，记录 `{uuid, hash, cached_at}`。
- **AC2**: 再次打开时先校验 Hub hash，未变化则直接读缓存。
- **AC3**: LRU 淘汰（可配置最大缓存大小）。
- **Priority**: P2

### Story 6.9 — 冲突检测
**As a** user, **I want** 两台机器同时编辑同一文件时不丢数据，**so that** 不会静默覆盖。

- **AC1**: PUT 时携带 `base_hash`，Spoke 检测 `current_hash ≠ base_hash` 时返回 409。
- **AC2**: 冲突时生成 `foo.conflict.md`，提示用户手动解决。
- **Priority**: P2

---

## Epic 7: Client Integration
> **目标**：TUI 是 MVP 第一阶段必须交付的核心交互界面，不是附加功能。  
> **技术栈**：`charmbracelet/bubbletea` + `charmbracelet/bubbles` + `charmbracelet/lipgloss`。  
> **既有资产**：`service/routes.py`、`mcore/tools/commands/api.py`、TUI 分支（`br_ed_extension_system_m0_0110`）、nvim 分支（`br_membox_as_nvim_ext`）。

### Story 7.1 — HTTP API 服务
**As a** client developer, **I want** 本地 HTTP API，**so that** TUI 和编辑器插件不需要直接读 SQLite。

- **AC1**: `mm api serve` 启动服务，提供 `/search`、`/ingest`、`/reindex`（复用 `service/routes.py`）。
- **AC2**: 返回路径格式为 `backend/rel_path`。
- **AC3**: TUI 内部通过此 API 与 Hub 通信。
- **Priority**: P0

### Story 7.2 — CLI API Client
**As a** user, **I want** 通过 CLI 调用 API，**so that** 不必写 curl。

- **AC1**: `mm api search/ingest/reindex` 复用 `mcore/tools/commands/api.py`。
- **AC2**: `--base` 支持连接远程 Hub。
- **Priority**: P0

### Story 7.3 — TUI 搜索与预览
**As a** user, **I want** 一个终端界面能搜索、浏览、预览所有笔记，**so that** 不用记命令、不用离开终端。

- **Priority**: P0
- **技术栈**: `bubbletea` + `bubbles/textinput` + `bubbles/list` + `bubbles/viewport` + `lipgloss`

#### 布局

```
┌─────────────────────────────────────────────────────────┐
│  🔍 > gfs chunkserver_                                  │  ← textinput
├──────────────────────────┬──────────────────────────────┤
│  work/reading/a3f7.md    │  # GFS 阅读笔记               │
│  ▶ GFS 阅读笔记   [work] │                              │
│                          │  chunkserver 负责存储实际     │
│  work/daily/b2c1.md      │  的 chunk 数据，每个 chunk    │
│    今日笔记       [work] │  默认 64MB...                │
│                          │                              │
│  books/gfs.pdf  p.3      │  ---                         │
│    ...chunkserver [pdf]  │  来源: work/reading/a3f7.md  │
│                          │  修改: 2026-06-10 14:32      │
│  12 results              │                              │
├──────────────────────────┴──────────────────────────────┤
│  enter:打开  ctrl+n:新建  ctrl+d:删除  tab:切换  q:退出  │
└─────────────────────────────────────────────────────────┘
```

#### Acceptance Criteria

**启动**
- **AC1**: `mm tui` 进入 TUI，`mm tui --backend work` 限定搜索范围。
- **AC2**: 启动时默认显示最近修改的文件列表（搜索框为空时）。
- **AC3**: 底部状态栏显示 Hub 连接状态和结果总数。

**搜索框（textinput）**
- **AC4**: 实时输入，300ms debounce 后查询 Hub FTS，避免每次击键都发请求。
- **AC5**: 搜索框始终保持焦点，直接键入即可搜索，无需先点击。
- **AC6**: 支持 CJK（中文/日文/韩文）输入。

**结果列表（list）**
- **AC7**: 每条结果显示：标题、`[backend]` 标签、路径，PDF 结果额外显示页码。
- **AC8**: 上下方向键或 `j/k` 切换选中项，列表自动滚动。
- **AC9**: 选中项变化时自动拉取文件内容更新右侧预览。
- **AC10**: 搜索无结果时显示友好提示（"No results. Press ctrl+n to create."）。

**预览窗口（viewport）**
- **AC11**: 右侧 viewport 显示选中文件的文本内容，从 Hub 路由拉取。
- **AC12**: `Page Up/Down` 或 `ctrl+u/d` 在预览窗口内滚动。
- **AC13**: PDF 结果预览显示对应 chunk 的文本内容，标注页码。
- **AC14**: 预览加载时显示 spinner，加载失败显示错误信息。

**操作**
- **AC15**: `Enter`：暂停 TUI → 用 `$EDITOR` 打开选中文件的物理路径 → 退出编辑器后恢复 TUI。
- **AC16**: `Ctrl+N`：弹出输入框填写标题 → 调用 `mm new` → 打开编辑器 → 返回 TUI 后刷新列表。
- **AC17**: `Tab`：在搜索框和列表之间切换焦点。
- **AC18**: `q` 或 `Esc`：退出 TUI。
- **Notes**: 参考 `br_ed_extension_system_m0_0110` 分支的 `src/client/tui.py` 中的交互设计。

### Story 7.4 — TUI 文件树浏览
**As a** user, **I want** 在 TUI 中按目录结构浏览所有 backend，**so that** 不只依赖搜索也能导航。

- **AC1**: `Ctrl+B` 或独立的 `mm tui --browse` 模式，切换到文件树视图。
- **AC2**: 树形展示所有 backend 及其目录结构，可折叠/展开。
- **AC3**: 选中文件时右侧 viewport 显示预览，`Enter` 打开编辑器。
- **AC4**: Phase 2 中文件树能展示跨机器所有 Spoke 的 backend。
- **Priority**: P1

### Story 7.5 — Neovim 插件
**As a** nvim user, **I want** 在编辑器内调用 mm，**so that** 不需要切换终端。

- **AC1**: `:MmNew`、`:MmSearch`（浮动窗口展示结果）、`ws://` 链接跳转解析。
- **Priority**: P3
- **Notes**: 参考 `br_membox_as_nvim_ext` 分支的 `clients/nvim/lua/membox/init.lua`。

---

## 附录 A: 既有代码资产

| 资产 | 位置 | 复用方式 |
|------|------|---------|
| PDF 文本提取 | `mcore/ingest_pdf.py` | 直接复用（fitz → pypdf → pdftotext fallback） |
| 文本切分 | `mcore/chunking.py` | 复用 merge-short-pages 逻辑 |
| SQLite 连接 | `mcore/db.py` | 复用 WAL + foreign_keys 模式 |
| Schema | `mcore/schema.sql` | 扩展加 `notes/notes_fts`，保留 `doc/chunk/chunk_fts` |
| 索引器 | `mcore/indexer.py` | 复用 sha256 去重、增量判断 |
| Embedding | `mcore/embedder.py` | 复用 hashed-bow，后续可换模型 |
| 向量存储 | `mcore/vector_store.py` | 复用 cosine similarity |
| API 路由 | `service/routes.py` | 复用，返回值路径格式更新 |
| API Client | `mcore/tools/commands/api.py` | 复用 |
| TUI 探索 | `br_ed_extension_system_m0_0110` | 参考交互设计 |
| Nvim 探索 | `br_membox_as_nvim_ext` | 参考 lua 插件结构 |

## 附录 B: 不做清单

| 功能 | 理由 |
|------|------|
| GFS / 分布式块存储 | markdown 是小文件，GFS 是大文件引擎，完全不匹配 |
| 中心化文件内容存储 | 文件留在原地，VFS 是路由层不是存储层 |
| gRPC / 消息队列 | Hub-Spoke 用 WebSocket 够用，不过度设计 |
| CRDT / OT 实时协作 | 个人工具，hash + conflict file 足够 |
| 自动 merge | 冲突时生成 `.conflict.md`，用户手动解决 |
| Tailscale 依赖 | 自实现 hub-and-spoke，300 行 Go，不依赖第三方 VPN |
| PostgreSQL | 个人使用，SQLite WAL 完全够，并发不是问题 |

## 附录 C: 验收里程碑

### Milestone 1: Hub + Spoke 起动
```bash
mm hub start &
mm agent start &
mm backend add work ~/work/notes --git
mm backend add personal ~/diary
mm backend ls
# → Spoke 自动注册到 Hub，推送初始索引
```

### Milestone 2: 文件读写
```bash
mm new "Hello" --to work/inbox
# → Hub 路由 → Spoke 写文件 → fsnotify → Hub 索引
mm read work/inbox/<short-id>.md
mm edit work/inbox/<short-id>.md
```

### Milestone 3: 索引与搜索
```bash
mm index
mm search "Hello"            # 查 Hub 全局 FTS
mm index ./papers/gfs.pdf
mm search "chunkserver"       # 跨 .md 和 PDF
mm search "想法"              # 跨 work 和 personal
```

### Milestone 4: 日报
```bash
mm digest --since 24h --stdout
mm digest --since 24h         # 生成 work/daily/2026-06-10.md
```

### Milestone 5: TUI
```bash
mm tui
# → 进入 TUI，显示最近修改的文件
# → 输入 "gfs" 搜索，300ms 后刷新结果列表
# → 上下键选中结果，右侧预览自动更新
# → Enter 打开 nvim 编辑，退出后返回 TUI
# → Ctrl+N 新建笔记，退出编辑器后列表刷新
```

### Milestone 6: 多机器部署（只改配置）
```bash
# settings.toml 里把 hub.url 改为公网地址
# 其余代码不变

# Machine B
mm init --hub http://<hub-ip>:8080
mm backend add personal ~/diary
# → Spoke 连接公网 Hub
mm search "Hello"
# → 搜到 Machine A 上的文件
```
