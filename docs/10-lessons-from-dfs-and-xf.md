# 从 DFS 与 xf 获得的架构启发

## 背景

读了两个项目：

- **`~/repo/distributed-file-system`** — Go 实现的分布式文件系统（类似 GFS/HDFS 的教学实现）。核心：Coordinator + DataNode 分离、chunk 存储、gRPC 流传输、双 session 模型。
- **`~/repo/xf`** — Rust 实现的本地 X (Twitter) 归档搜索 CLI。核心：SQLite 元数据 + Tantivy 全文索引 + 向量索引、混合搜索 RRF、增量索引、sub-millisecond 延迟。

两者虽然目标完全不同，但对 VFS/Workspace 设计有直接的借鉴价值。

---

## 一、Control Plane / Data Plane 分离（来自 DFS）

### DFS 的做法

| 层级 | 职责 | 对应组件 |
|------|------|---------|
| Control Plane | 元数据管理、节点发现、chunk 规划 | Coordinator |
| Data Plane | 实际字节存储、流传输、复制 | DataNode |

Client 只和 Coordinator 交互获取 "计划"，实际数据流直接到 DataNode。

### 对 VFS 的映射

我们的设计天然有这个结构，但可以更明确地定义边界：

| VFS 层级 | 职责 | 对应概念 |
|----------|------|---------|
| **Control Plane** | Mount Table、UUID Registry、路径解析、冲突规则、索引管理 | Workspace Layer |
| **Data Plane** | 实际文件读写、目录遍历、Git 操作、远程同步 | Backend Layer |
| **Client** | `mm` CLI、编辑器插件、AI Digest | Application Layer |

**启发**：把 "workspace 只做 metadata，不碰实际字节" 这个原则写死。`mm new` 时 workspace 负责：
1. 分配 UUID
2. 决定 `ws://` 落点（按 mount table + default_backend）
3. 写入 frontmatter 规范
4. 交给 backend driver 做实际的 `os.WriteFile`

---

## 二、两层 Session 模型（来自 DFS）

### DFS 的做法

- **Metadata Session**（Coordinator 管理）：覆盖整个文件操作生命周期（UploadFile → ... → ConfirmUpload），保证原子性。
- **Streaming Session**（DataNode 管理）：覆盖单个 chunk 的传输，处理反压、超时、重试。

### 对 VFS 的启发

VFS 写入笔记也可以类比成 "上传文件"：

```
Metadata Session (Workspace)
  ├── 分配 UUID
  ├── 解析 ws:// 到 physical path
  ├── 确保 frontmatter 完整性
  └── 确认索引更新

Streaming Session (Backend)
  ├── 打开物理文件
  ├── 写入 bytes
  ├── fsync
  └── 返回写入结果
```

**具体收益**：
- 如果 backend 写入成功但索引更新失败，metadata session 可以感知到不一致。
- 远程 backend（如 workbox ssh）可以有自己的 session timeout，不阻塞 workspace 层。
- 将来做 "批量迁移笔记" 时，可以复用 batch metadata session 概念。

---

## 三、Storage Backend Interface（来自 DFS）

### DFS 的做法

DFS 内部抽象了 `Storage Interface`，支持：
- Local Disk Backend
- S3 Backend
- Hybrid Backend（热/冷分层，自动迁移）

### 对 VFS 的启发

我们目前的 backend.toml 定义了 `type = "local" | "ssh" | "s3"`，但还没有明确 interface。可以借鉴 DFS 定义一组 backend 必须实现的操作：

```go
// 伪代码
interface BackendDriver {
    Read(path string) ([]byte, error)
    Write(path string, data []byte) error
    List(dir string) ([]FileInfo, error)
    Delete(path string) error
    Stat(path string) (FileInfo, error)
    // VFS 特有
    GitLog(since time.Time) ([]Commit, error)  // 用于 digest
    IsWritable() bool
    Sync() error  // 远程 backend 的拉取/同步
}
```

**特别值得借鉴**：DFS 的 **Hybrid Backend（热/冷分层）** 概念。VFS 中 `scratch` backend 就是 "冷/临时"，`main` 是 "热/主存储"。未来可以自动化：
- `scratch` 里的笔记超过 7 天未访问 → 自动 promote 到 `main`
- `main` 里的旧笔记 → 压缩归档到 `archive` backend

---

## 四、Heartbeat & 增量状态（来自 DFS）

### DFS 的做法

- DataNode 每 2 秒向 Coordinator 发送 heartbeat。
- Heartbeat 携带增量版本号，避免全量状态传输。
- Coordinator 据此维护 cluster 拓扑和节点健康。

### 对 VFS 的启发

VFS 没有多节点 cluster，但有 **多 backend 可用性** 问题：

```toml
[backends.workbox]
type = "ssh"
host = "workbox.local"
```

家里 Mac 连不上公司 VPN 时，`workbox` backend 不可用。可以借鉴 heartbeat：

```
# mm 内部维护 backend health map
backend     status      last_seen      version
main        online      now            -
icloud      online      now            -
workbox     offline     2h ago         -
```

- 启动时或定时探测各 backend 可达性。
- 索引更新时跳过 offline backend，避免阻塞。
- workbox 恢复在线后，自动 `git fetch` + 增量索引更新。

**增量索引版本号**：backend 本地维护一个 `index_version`（如 git HEAD + mtime hash），只有 version 变化时才触发重索引。

---

## 五、Garbage Collection（来自 DFS）

### DFS 的做法

- **Soft Delete**：Coordinator 标记文件删除，立即返回成功。
- **Chunk Cleanup**：后台异步删除各 DataNode 上的 chunk。
- **Orphaned Cleanup**：DataNode 本地扫描 inventory，发现 metadata 中不存在的 chunk → 自动清理。

### 对 VFS 的启发

笔记系统也需要 GC：

1. **Inbox GC**：`icloud` inbox 里的笔记被 promote 到 `main` 后，原文件可以软删除，定期清理。
2. **Orphan Index Cleanup**：索引数据库里有条目，但物理文件已被删除（用户手动 `rm`）→ 定期扫描发现 orphan 索引并清理。
3. **Broken Link GC**：笔记 A 链接到笔记 B（UUID），但 B 已不存在 → `mm doctor` 扫描并报告 broken links。

```bash
$ mm gc --dry-run
[inbox] 3 notes promoted, can delete originals
[index] 5 orphan entries found
[links] 2 broken links detected
```

---

## 六、SQLite + Tantivy 双存储（来自 xf）

### xf 的做法

| 存储 | 用途 | 技术 |
|------|------|------|
| SQLite | 结构化元数据、统计、FTS5 fallback | SQLite (WAL mode) |
| Tantivy | 全文倒排索引、BM25 排名 | Tantivy |
| Vector Index | 语义相似度搜索 | F16 向量 + SIMD dot product |

### 对 VFS 的启发

**我们之前的文档说索引存在 `~/.cache/mm/index/notes.sqlite`，但 xf 的经验表明应该分离：**

```
~/.cache/mm/
  registry.sqlite        # UUID → ws_path → backend → physical_path
  tantivy/               # 全文倒排索引
  vectors/               # 语义向量（可选）
```

**为什么分离？**
- SQLite 做关系查询（找某 backend 的所有文件、按日期范围过滤）极佳。
- Tantivy 做 BM25 全文搜索比 SQLite FTS5 更快、更精确。
- 向量索引格式与两者都不同，单独存更灵活。

**xf 的具体配置值得抄**：

```sql
PRAGMA journal_mode = WAL;      -- 读写并发
PRAGMA synchronous = NORMAL;    -- 速度优先
PRAGMA cache_size = -64000;     -- 64MB page cache
PRAGMA temp_store = MEMORY;     -- 临时表放内存
```

---

## 七、增量索引与 Content Hash（来自 xf）

### xf 的做法

- 索引前对文本做 canonicalization（去 markdown、normalize whitespace）。
- 计算 canonicalized text 的 SHA256 hash。
- hash 未变 → 跳过 re-embedding，直接复用旧向量。

### 对 VFS 的启发

笔记索引也可以完全增量：

```python
def should_index(file_path, uuid):
    current_hash = sha256(read(file_path))
    last_hash = registry.get_content_hash(uuid)
    if current_hash == last_hash:
        return False  # 跳过
    
    # 更新索引
    index_document(file_path)
    registry.update_content_hash(uuid, current_hash)
```

**这比 mtime 更可靠**：有时用户用脚本批量 touch 文件，mtime 会变但内容没变。content hash 保证只索引真正变化的内容。

**canonicalization 也适用**：索引前把 markdown 转为纯文本， stripping frontmatter，这样搜索 `"---"` 不会匹配到所有文件的 frontmatter 分隔符。

---

## 八、Hybrid Search 与 RRF Fusion（来自 xf）

### xf 的做法

xf 实现三种搜索模式：

| 模式 | 原理 | 适用场景 |
|------|------|---------|
| lexical | Tantivy BM25 | 精确关键词 |
| semantic | 向量余弦相似度 | 意思相近但用词不同 |
| hybrid | RRF fusion | 默认，兼顾两者 |

**RRF (Reciprocal Rank Fusion) 公式**：

```
Score(doc) = Σ 1/(K + rank_i + 1)
```

K=60 是经验常数。BM25 排第 3 名 + 向量排第 5 名 的文档，比单一榜单第 1 名的文档得分更高。

### 对 VFS 的启发

`mm search` 应该直接提供这三种模式：

```bash
$ mm search "虚拟内存" --mode lexical   # 精确匹配
$ mm search "笔记系统设计" --mode semantic  # 意思相关
$ mm search "workspace" --mode hybrid    # 默认
```

**默认 embedding 策略也可以学 xf**：

- **默认**：轻量 hash-based embedder（FNV-1a 或 tf-idf），零依赖、亚毫秒。
- **可选**：`mm index --semantic` 时下载 MiniLM 等模型，做真正的语义匹配。

个人笔记场景下，hash-based 可能已经足够好 —— 因为你通常记得自己用过哪些词。

---

## 九、Agent-Friendly CLI（来自 xf）

### xf 的做法

```bash
# stdout = 数据（机器可读）
# stderr = 诊断/日志（人类可读）
xf search "query" --format json
```

严格遵守：exit 0 = 成功，json 输出可直接被脚本/AI agent 消费。

### 对 VFS 的启发

`mm` 所有命令都应该保证：**`--format json` 时 stdout 是纯 JSON，stderr 是日志。**

这对 AI Digest 极其重要：

```bash
# AI digest 脚本内部调用
FILES=$(mm digest --since yesterday --format json)
# FILES 是结构化的变更列表，直接喂给 LLM prompt
```

另外可以学 xf 的 `doctor` 命令：

```bash
$ mm doctor
✓ backend 'main' accessible (~/repo/notes)
✓ backend 'icloud' accessible
✗ backend 'workbox' offline (last seen 2h ago)
✓ index registry: 1247 notes
⚠ orphan index entries: 3
⚠ broken links: 2
```

---

## 十、Lazy Initialization & Memory-Mapped Index（来自 xf）

### xf 的做法

- Tantivy index reader 懒加载，首次搜索时才初始化。
- Index 文件使用 memory-mapped I/O，由 OS 自动缓存。
- 首次搜索可能 ~100ms（加载），后续 <1ms。

### 对 VFS 的启发

- `mm` 启动时**不**打开索引，第一次 `search`/`ask` 时才加载 reader。
- 索引文件放 `~/.cache/mm/` 并用 mmap（如果底层索引库支持）。
- 远程 backend 的缓存文件也用类似策略：首次访问 pull，后续读本地。

---

## 总结：对 VFS 文档的修改建议

基于以上启发，建议对已有 VFS 设计做以下增强：

1. **明确 Control Plane / Data Plane 边界**（更新 `01-architecture.md`）。
2. **定义 BackendDriver Interface**（更新 `04-mount-table.md`）。
3. **索引层拆分为 SQLite + Tantivy + 可选向量**（重写 `06-search-index.md` 部分）。
4. **引入 content hash 增量索引**（更新 `06-search-index.md`）。
5. **支持三种搜索模式（lexical/semantic/hybrid）和 RRF**（更新 `06-search-index.md`）。
6. **加入 `mm doctor` 和 `mm gc`**（更新 `05-mm-integration.md`）。
7. **backend heartbeat / health map**（更新 `08-cross-machine.md`）。
8. **所有 CLI 命令支持 `--format json`，stdout/stderr 分离**（更新 `05-mm-integration.md`）。
9. **默认轻量 embedding，可选 `--semantic`**（更新 `06-search-index.md`）。
10. **SQLite WAL mode 和性能调优**（更新 `06-search-index.md`）。
