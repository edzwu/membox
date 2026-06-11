# MVP 架构启示：从 kabi-digest / xf / birdclaw / x-algorithm 学到的最重要设计

> 目标：回答 "存储的目的是为了检索"，把四个项目的核心设计映射到 VFS MVP 的架构决策上。

## 核心结论（一句话）

**文件是真理源（Source of Truth），SQLite 是派生索引，AI 是派生视图。** 所有设计都应保证：删除索引后能在数秒内从文件系统完全重建，且检索体验不因索引损坏而长期中断。

---

## 十大架构启示

### 1. 文件为源，DB 为索引 —— birdclaw / Rookery

| 来源 | 设计 |
|------|------|
| birdclaw | "SQLite 是 canonical truth"，但 archive 和 live 数据都收敛到同一模型；备份用 JSONL 文本分片 |
| Rookery PRD | ".md 文件是真理源，DB 只是索引，随时可重建" |

**对 VFS 的映射**：
- `~/.cache/mm/index.sqlite` 只是加速层，真正的 truth 是 `~/repo/notes/**/*.md`。
- 索引 Schema 必须包含 `content_hash` 和 `indexed_at`，支持幂等重建。
- 提供 `mm index --rebuild`：删除 SQLite，从所有 backend 的 `.md` 全量重建。

---

### 2. Content Hash 增量索引 —— xf

| 来源 | 设计 |
|------|------|
| xf `canonicalize.rs` | 索引前对文本做 canonicalization（去 markdown、normalize whitespace），计算 SHA256 hash；hash 未变则跳过 re-indexing |

**对 VFS 的映射**：
- MVP 阶段就把 `mtime + size` 判断升级为 **canonicalized content hash**。
- 理由：git checkout、批量脚本 touch 会改变 mtime 但内容不变；不做 hash 会导致无意义的全量重索引。
- Canonicalization 规则：去掉 frontmatter、collapse code block、normalize whitespace、Unicode NFC。

---

### 3. 双存储搜索架构：FTS5 + 预留专用引擎 —— xf

| 来源 | 设计 |
|------|------|
| xf `architecture.md` | Tantivy 做 BM25 全文（<1ms），SQLite 做结构化查询和 FTS5 fallback；两者互补，不互相替代 |

**对 VFS 的映射**：
- MVP 用 SQLite FTS5 够快够简单（十万文档 <100ms）。
- 但索引层接口要预留 `SearchBackend`：
  - `Fts5Backend`（MVP 默认）
  - `TantivyBackend`（Phase 2 可选，支持前缀匹配、BM25、phrase query）
- 搜索返回的永远是 `ws://` 路径，不暴露底层引擎差异。

---

### 4. 采集-处理-索引分离 —— kabi-digest / grox

| 来源 | 设计 |
|------|------|
| kabi-digest | `collect`（采集累积）与 `generate`（评分/摘要/渲染）显式分离；数据池按 ID upsert merge |
| grox `dispatcher.py` | Dispatcher 负责任务分发与重试，Engine 负责实际执行；fill loop / result loop / queue 三者隔离 |

**对 VFS 的映射**：
- `mm new` / `mm capture` 只负责写入文件系统（采集）。
- `mm index` 负责扫描文件系统并更新索引（处理）。
- `mm digest` 负责读索引 + git log + LLM 生成日报（渲染）。
- 三个命令独立运行，不互相阻塞。`mm new` 成功后**不强制**触发索引（可配置为 lazy）。
- 索引本身支持 upsert：同一 UUID 文件内容更新 → 覆盖旧索引行，不产生重复。

---

### 5. 配置分层与 Environment Fallback —— xf / kabi-digest / birdclaw

| 来源 | 设计 |
|------|------|
| xf `config.rs` | compiled defaults → `~/.config/xf/config.toml` → `XF_*` env → CLI args |
| kabi-digest `config.ts` | `config.yaml` + `deepMerge` + env fallback（`OPENAI_API_KEY` / `V2EX_TOKEN`） |
| birdclaw | `config.json` + env override（`OPENAI_API_KEY` / `BIRDCLAW_HOME`） |

**对 VFS 的映射**：
- 配置优先级：defaults → `~/.config/mm/settings.toml` → `MM_*` env → CLI flags。
- 关键 env：
  - `MM_LLM_API_KEY` / `MM_LLM_MODEL` / `MM_LLM_BASE_URL`
  - `MM_HOME`（覆盖 `~/.config/mm`）
  - `MM_DB_PATH` / `MM_INDEX_PATH`
- `workspace.toml` 和 `backends.toml` 作为**数据配置**（描述 mount 和 backend），`settings.toml` 作为**行为配置**（搜索默认值、AI provider）。

---

### 6. Transport / Backend 抽象与 Auto Fallback —— birdclaw

| 来源 | 设计 |
|------|------|
| birdclaw `data-architecture.md` | `TransportKind = "archive" | "xurl" | "bird" | "official" | "xweb"`；auto 模式按健康状态链式回退 |

**对 VFS 的映射**：
- `BackendDriver` 接口要足够抽象：
  ```go
  type BackendDriver interface {
      Type() string                    // "local" | "ssh" | "s3"
      Read(path) ([]byte, error)
      Write(path, data) error
      List(dir) ([]FileInfo, error)
      Stat(path) (FileInfo, error)
      GitLog(since) ([]Commit, error)  // VFS 特有
      IsWritable() bool
      IsHealthy() bool                 // 新增：心跳/可达性检测
  }
  ```
- `default_backend` 不可写时，`mm new` 自动 fallback 到下一个 writable backend（可配置）。
- 远程 backend（如 workbox ssh）在不可达时标记为 offline，索引扫描自动跳过，不阻塞全局搜索。

---

### 7. AI 是 Overlay，不是 Source of Truth —— birdclaw

| 来源 | 设计 |
|------|------|
| birdclaw `inbox.md` | "OpenAI scoring 是 overlay，不是 verdict。原始 mention 或 DM 是 source of truth。" |

**对 VFS 的映射**：
- `mm digest` 生成的日报是 **派生文档**，必须：
  - 包含 `type: digest` frontmatter。
  - 在 `sources` 中列出所有引用的原始 UUID。
  - 不修改、不删除原始笔记。
- AI 标签建议、分类、inbox scoring 都作为索引层的 overlay 字段，原始 frontmatter 不变。
- 用户随时可以用 `mm digest --stdout` 只看不写。

---

### 8. Inbox 作为渐进式整理入口 —— birdclaw / Rookery

| 来源 | 设计 |
|------|------|
| birdclaw | `inbox` 是统一 triage queue，混合 mentions + DMs，AI-ranked，可 dismiss/acted-on |
| Rookery PRD | 区分 `inbox/`（未整理）与 `corpus/`（已整理），支持渐进式归档 |

**对 VFS 的映射**：
- `ws://inbox/` 是**快速捕获区**，高 accessibility，不一定 git tracked。
- `ws://notes/` / `ws://projects/` 是**知识本体区**，git tracked，有完整 frontmatter。
- `mm new` 默认写到 inbox；`mm move ws://inbox/xxx.md ws://projects/vfs/` 是 promote 操作。
- 索引层给 inbox 笔记打 `status: seed`；promote 后改为 `status: evergreen`。

---

### 9. 健康检查与可观测性 —— xf / grox

| 来源 | 设计 |
|------|------|
| xf `doctor.rs` | 系统化 health check：archive、database、index、performance；Pass/Warning/Error 三级状态 |
| grox | 全链路 metrics：histogram（处理时长）、counter（成功/失败）、gauge（in-flight）、tracer（调用链） |

**对 VFS 的映射**：
- MVP 引入 `mm doctor`：
  ```
  ✓ backend 'main' accessible (~/repo/notes)
  ✗ backend 'workbox' offline (last seen 2h ago)
  ✓ index registry: 1247 notes
  ⚠ orphan index entries: 3
  ⚠ broken links: 2
  ```
- 检查项：backend 可达性、index-document count 一致性、FTS5 integrity、schema version、sample query latency。
- 性能基准：首次 `mm search` <100ms（十万文档 FTS5）。

---

### 10. 两阶段召回-精排 —— phoenix / xf

| 来源 | 设计 |
|------|------|
| phoenix | Retrieval（Two-Tower ANN，百万→千）→ Ranking（Transformer 独立评分，千→排序） |
| xf `hybrid.rs` | lexical 召回 + semantic 召回 → RRF fusion（K=60）→ 最终排序 |

**对 VFS 的映射**：
- MVP 阶段只有**单阶段 FTS5 召回**，但架构预留**精排层**：
  - **召回（Retrieval）**：FTS5 按 BM25 或相关性取 Top-100。
  - **精排（Ranking）**：按 recency + git activity + 链接关系 + 标签匹配度重新排序。
- 这样 `mm search "虚拟内存"` 先召回含关键词的 100 篇，再按最近修改和链接密度把最相关的 10 篇排到前面。
- 未来接入 embedding 时，semantic 召回作为第二路召回，与 FTS5 做 RRF fusion（K=60），无需改动精排层。

---

## 对 MVP 文档的修改建议

基于以上启示，建议在 `mvp.md` 中做以下调整：

1. **索引策略**：把 `mtime + size` 改为 `canonicalized content hash`，增加 `canonicalize.rs` 等价的文本清洗规则。
2. **配置系统**：把 `settings.toml` 纳入配置分层，明确 `MM_*` 环境变量列表。
3. **BackendDriver 接口**：增加 `IsHealthy()` 方法，为跨机器场景检测 backend 可达性留接口（不是自动故障转移，只是避免阻塞全局搜索）。
4. **`mm new` 与索引解耦**：`mm new` 不强制触发索引更新，由 `mm index` 或后台 lazy index 处理。
5. **AI Overlay 规范**：digest 生成的文件必须带 `type: digest` 和 `sources` frontmatter，且原始笔记不可变。
6. **Inbox 语义**：明确 `ws://inbox/` 是 seed 状态，`ws://notes/` / `ws://projects/` 是 evergreen 状态，`mm move` 即 promote。
7. **引入 `mm doctor`**：作为 MVP 的可靠性保障命令，检查 backend 可达性、index 一致性、FTS5 integrity。
8. **搜索接口预留**：`mm search` 的返回结构预留 `score` 和 `ranking_signals` 字段，为后续精排层留位置。
9. **备份策略**：除了 `mm index --rebuild`，还应提供 `mm export --jsonl`（类似 birdclaw），让笔记数据可以用文本形式 Git 备份。
10. **双存储索引接口**：在代码中抽象出 `IndexBackend` 接口，`SqliteFtsBackend` 实现 MVP 版本，`TantivyBackend` 作为 Phase 2 的 drop-in replacement。

---

**一句话总结**：从四个项目学到的最关键设计是 —— **以文件为真理源、以 hash 为增量锚点、以配置分层为灵活度、以健康检查为可靠性、以召回-精排为检索架构**。把这些原则写入 VFS 的代码契约，MVP 就不会在未来扩展时伤筋动骨。 