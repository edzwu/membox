# Sync MVP 开发规划

> 目标：一个双向文件同步工具的最小可用版本。借鉴 Unison 的同步思想、Mutagen 的实现结构。第一版刻意简单：冲突只报不合，靠人手动解决。

## 0. 定位：不是全量同步，是「离线子集」的引擎

membox 的整体架构是三层，本同步引擎只服务最底一层：

1. **Server 权威**：唯一全量副本，浏览器/CLI 默认在线直连（对应 sprint0 的大云盘），在线读写零冲突
2. **读缓存**：客户端 LRU 缓存 + 元数据索引缓存，解决「快」，content-addressed，失效即重取，不需要三态比对
3. **离线子集（本引擎）**：用户显式 pin 的小范围内容，支持离线写，是唯一会产生产生冲突的场景，也只在这里付出同步复杂度

推论：

- 不做全量同步（内容增长后全量既无必要也不可行，尤其 PDF 库）；全量 = 「pin 全部」的极端情况，允许但不推荐
- 未 pin 的内容完全绕开同步层，走 server API
- 冲突面被 pin 范围天然限缩，archive 也小

**分端策略**：同步/缓存策略按端的能力和角色分派，不是所有端一个打法

| 端 | 角色 | 策略 |
|----|------|------|
| macOS 桌面 | 创作主场（编辑器/grep/agent 需要本地文件，磁盘富余，有离线场景） | **本地优先**：跑同步引擎，默认 pin 全部 markdown（很小），PDF 按需拉取缓存（类 iCloud 优化存储） |
| 浏览器/手机 | 薄客户端（浏览/速记为主，存储有限） | 直连 server + 读缓存，不做本地优先，在线写零冲突 |

桌面上「pin 子集」退化为容量策略（控制 PDF 占多少盘），而非功能开关。单用户多设备场景下冲突实际很罕见，偶尔撞上手动 resolve 即可。

**设计支柱：元数据全量，字节按需。** 一致性来自「状态的完整复制」，而状态（`{exists, name, sha256}`，~100B/文档）极小，每个端都全量持有全局目录；只有字节（PDF 等大对象）按 pin/缓存惰性物化。由此：

- reconcile 永远在全量元数据上运行，部分同步不产生知识盲区，冲突检测全局完整
- 冲突只能产生于 pinned 集合内（无字节无从离线编辑）——冲突面小是构造出来的
- 字节 content-addressed：交付前校验 sha256，缓存最坏是 miss，不会错
- tombstone 随元数据传播，删除可达从未持有字节的端
- server 永不清除被任何当前状态引用的字节（本地驱逐的字节永远可取回）

**正确性不变量（验收标准）**：同步静默后非冲突条目满足 `α.metadata == β.metadata == archive`；字节只在 tombstone 已传播或可从 server 取回时本地消失。reconcile 是纯函数，用 property-based testing 验证收敛（随机两端操作序列 → 同步 → 断言不变量）。

## 1. 一句话定义

给定两个 membox workspace（α 和 β），记住上次同步时的状态（archive），每次运行时对比三方，把单边的变更复制到另一边，双边都改过的文档报冲突，由用户选择方向强制覆盖。

关键前提：**同步的主体是 UUID 组成的虚拟文件系统**，且分两个独立的层：

- **元数据层（全量、权威）**：`UUID → {exists, name, sha256}`，每个端完整持有，reconcile 只在这一层运行。一致性是这一层的义务
- **字节层（按需、可弃）**：content-addressed 的 blob 存储，`sha256 → bytes`。只在需要物化时拉取，随时可驱逐、可取回、可校验。容量是这一层的问题

目录路径是各 membox workspace 的内部实现，不下场；**文件名（basename）是文档本身的属性**，在元数据层参与同步（rename 即元数据变更）。其余元数据由接收方从字节重新派生。

## 2. 范围

### 做（In scope）

- 两个本地 membox workspace 之间的双向同步（α、β 都是本机可访问；远端通过挂载，后续再加 agent/SSH）
- 文档同步：新增、修改、删除（tombstone 传播）、rename（basename 视为内容的一部分；目录不传播）
- 冲突检测：同一文件两边都改（相对 archive）→ 报冲突，本轮跳过该文件
- 手动解决：`sync resolve <path> --from α|β` 强制用一边覆盖另一边；或用户自己改好后 `sync resolve <path> --mark` 标记已解决
- 命令行：`sync run`（一次同步）、`sync status`（看变更和冲突）、`sync resolve`
- archive 持久化：上次同步的元数据清单（UUID → Entry），存 JSON 或 BoltDB

### 不做（Out of scope，后续版本再谈）

- 远程 agent / SSH 传输（Mutagen 的核心，但第一版跳过）
- fsnotify 实时监控、守护进程
- 三方 merge、rsync 增量传输、压缩

  **三方 merge 的扩展路径已预留**（现在不做，将来按此接入）：冲突发生时三个版本的指纹已齐——base = `archive[uuid].ContentSha256`，ours/theirs = `Conflict.Alpha/Beta.ContentSha256`（git 同构：CAS blob 层 = object store，archive = merge base）。唯一配套策略：**archive 或进行中冲突引用的 blob 不得 GC**（MVP 不做 GC，天然满足）。实现位置：reconcile 之后加独立的 autoMerge pass（需要读字节，不污染纯函数），仅处理 `both-modified`：diff3 干净 → 降级为 Change；有冲突块 → 保留 Conflict（或写冲突标记文件）。文本类可合并，PDF 永远人工。rename-vs-edit 将来可从「保守冲突」升级为「改名 + 内容合并」。
- 符号链接、权限、ACL、大小写折叠等边界情况
- `.syncignore`（可后置，先用硬编码忽略列表：`.git`、`.DS_Store`、临时文件）

## 3. 核心算法：元数据层的三态比对（Unison 思想）

**术语约定（全文档统一）**：

- **快照（snapshot）**：某一侧**当前**状态的清单，`ScanCatalog` 每次运行现算，纯内存、用完即弃。有两个：α、β
- **存档（archive）**：上次同步**双方达成一致**的状态清单，唯一一份，持久化。它是双方的共同历史（类比 git merge base）——没有它，「α 无 β 有」分不清是 β 新建还是 α 删除

两者是**同一类型的不同角色**：`map[UUID]Entry`，三份实例，无冗余抽象。archive 更新规则：成功传播的条目才更新；冲突条目保留旧记录，下轮重报。

reconcile **只在元数据层运行**——输入是三份 `UUID → Entry` 清单（archive、α、β），完全不碰字节；字节传输是它的执行后果，由 hash 驱动（见第 4 节）。

对每个文档（以 UUID 为身份），取三份状态：

- **archive(uuid)**：上次同步成功时该文档的样子
- **α(uuid)** **β(uuid)**：现在该文档的样子

每个 UUID 的状态：

- **存在性**：本侧该 UUID 是否存在（含 trash 语义）
- **指纹**：`{name, size, mtime, sha256}`——name（basename）与内容同属一个比对维度，任一不同即「变了」（快速路径：name + size + mtime 相同即认为没变；否则算 sha256）

**三态比对表**：

| α 变了 | β 变了 | 动作 |
|--------|--------|------|
| 否     | 否     | 跳过 |
| 是     | 否     | α → β 传播该维度 |
| 否     | 是     | β → α 传播该维度 |
| 是     | 是     | **冲突**：记录，跳过该文档，退出码非零提示 |

传播的具体动作分两层（通过接收方的「带 UUID 导入」接口执行）：

- **元数据层**：新增 UUID / 改名 / 删除（tombstone）→ 只传 `Entry`，接收方更新 catalog、rename 或 trash，**不碰字节**
- **字节层**：仅当接收方需要物化某内容且本地 blob 存储没有该 `sha256` 时才传字节；已有该 hash（他文档同内容、或缓存命中）则零传输，直接链接

推论：

- 冲突只有一种：**同一 UUID 双边都变了**（相对 archive，name 或内容任一）。α 改名 + β 改内容也算冲突，保守但简单
- **删除 vs 修改** 也算冲突（一边删了、一边改了 → 报冲突，不自动删）。第一版保守

## 4. 执行流水线（Mutagen 结构，两平面分离）

```
                     ┌─────────── 元数据平面（全量、权威）───────────┐
scan(α) ──┐          │                                              │
          ├─→ reconcile(archive, α, β) → []Change / []Conflict ─→ 更新 archive（逐条落盘）
scan(β) ──┘          └──────────────┬───────────────────────────────┘
                                    │ Change 里需要新字节的条目
                     ┌───────────────▼── 字节平面（按需、可弃）───────────────┐
                     │  对方缺 hash? ──是──→ stage → 校验 → apply（物化）      │
                     │        │                                              │
                     │        否（本地已有该 blob）→ 直接链接，零传输          │
                     └────────────────────────────────────────────────────────┘
```

1. **Scan**：从 membox DB 读出两侧 `UUID → Entry` 清单（纯元数据，内存中即可，MVP 全量扫，不增量）。
2. **Reconcile**：纯函数，输入三份元数据清单，输出动作列表和冲突列表。有冲突不阻塞其他条目。
3. **字节平面的 Stage + Apply**（Mutagen 的两阶段写入，仅对需要传输的 blob 执行）：
   - **Stage（备料）**：写到接收方暂存区 `.sync/staging/<sha256>`，fsync，校验 digest。此阶段目标目录完全不动。按 digest 命名 → 多文档同内容只传/只写一次
   - **Apply（落子）**：校验通过后原子 `rename` 到最终路径。这是唯一的提交点：观察者要么看到旧文件、要么看到新文件，无中间态
   - 两阶段分离的意义：慢且易失败的 IO（复制）与瞬时提交（rename）解耦 → 中断只留孤儿暂存文件，绝不写出半个文件；先验 digest 再提交 → 不会用好文件换坏字节
   - **先元数据后字节**：archive 在元数据变更落盘后即更新；字节传输失败只影响物化，不影响一致性（下轮重试即可）
   - 删除：映射为接收方 trash 语义；blob 的 GC 由 server 统一负责（见第 0 节不变量）
4. **冲突**：打印 + 写入状态文件，等用户 `resolve`。

## 5. 数据模型

```go
// ── 元数据层（全量复制，reconcile 的唯一输入）──
// 判别标准：name 是文档本身的属性（rename 即元数据变更），随 UUID 同步；
// 目录是 workspace 内部实现，不同步；
// 能从字节派生的（title/authors）接收方自己重新提取；
// workspace 本地状态（pinned/read_status/title override/trash）不同步。
type Entry struct {
    Exists        bool   // false = tombstone（已删除，需传播）
    Name          string // basename，与内容同属一个比对维度
    ContentSha256 string // 指向字节层的钥匙
    Size          int64
    Mtime         int64
}

type Archive struct {
    Version int
    Entries map[string]Entry // UUID → 上次同步状态（身份即 UUID，全局一致）
}

type Conflict struct {
    UUID    string
    Reason  string // "both-modified" | "delete-vs-modify"
    Alpha, Beta Entry
}

// ── 字节层（按需物化，content-addressed，可弃）──
// 接口语义：has(sha256) / get(sha256) / stage→put(sha256, bytes)
// 实现：本地 CAS 目录（MVP）→ 后续换 HTTP range fetch + LRU 驱逐
type BlobStore interface {
    Has(sha256 string) bool
    Get(sha256 string) (io.ReadCloser, error)  // 交付前校验 digest
    Put(sha256 string, r io.Reader) error       // stage + 校验 + 原子落位
}
```

存储：

- archive：`<workspace>/.sync/archive.json`（MVP 用 JSON，量大再换 BoltDB）
- 冲突列表：`<workspace>/.sync/conflicts.json`

## 6. CLI 草案

```bash
sync init --alpha /path/a --beta /path/b   # 建立 workspace，首次全量比对
sync run                                    # 同步一轮，打印动作 + 冲突
sync status                                 # 只扫不改，预览变更/冲突
sync resolve <uuid-or-name> --from alpha    # 用 α 的该文档覆盖 β（元数据+字节整体以胜方为准）
sync resolve <uuid-or-name> --mark          # 我已手动改好两边，按现状写回 archive
```

## 7. 里程碑

| # | 任务 | 验收 |
|---|------|------|
| M1 | 元数据层 Scan：从 membox DB 读出 UUID → Entry 清单 | 单元测试：构造 workspace 能出正确清单 |
| M2 | Archive 存取 + Reconcile 纯函数（不碰字节） | 单测覆盖三态表所有分支（新增/修改/删除/rename × 单边/双边）+ property test 收敛性 |
| M3 | 字节层 BlobStore：本地 CAS + stage/apply 原子物化 + hash 命中零传输 | 集成测试：同内容多文档只传一次；中途 kill 后重跑收敛 |
| M4 | 冲突报告 + `resolve --from/--mark` | 双边改同一文档 → 报冲突且其余正常同步；resolve 后收敛 |
| M5 | CLI 串起来 + 简单日志输出 | 手动 demo 全流程 |

## 8. 技术选型

- 语言：Go（对齐 Mutagen，方便后续直接参考/引入其 `pkg/synchronization/core` 的 reconcile 思路）
- hash：sha256（`crypto/sha256`，流式读文件）
- CLI：cobra 或标准库 flag 均可，MVP 无所谓
- 测试：标准 `testing` + `t.TempDir()` 构造场景
- syncx 包纪律：**纯库，无 ctx、无 IO**。对外只暴露纯函数——`EntryFromRecord(port.DocumentRecord) Entry`（单条映射）+ `ScanCatalog([]port.DocumentRecord) map[string]Entry`（薄包装）+ reconcile。取数/IO 一律由调用方（CLI 经 `Box` 门面）负责；将来枚举改流式分页时调用方逐条喂 `EntryFromRecord`，syncx 不变。**不为 mock 引入接口**：`Box` 是具体类型但已隔离存储细节，DIP 已成立；等出现第二种 catalog 来源再提取接口

## 9. 跨 workspace 的 UUID 身份（关键决策点）

membox 的 UUID 是随机 UUIDv7（全局唯一，不撞号），但**身份绑定是 workspace 本地的**：UUID 不存在文件字节里，只存在各 workspace 自己的 DB；scan 发现新文件时现场 `NewDocumentID()` 生成。因此同步把字节写到 β 后，β rescan 会绑定一个全新的本地 UUID —— 同一文档在两侧 UUID 不同。

注意：membox scan 内的 `ReconcileRenames` 会用 file-key / exact-hash 配对「消失 ↔ 新增」，所以 sync 以 **rename 语义**（而非 delete+create）操作时，β 侧自己的 UUID 能稳定，但仍无法对齐到 α 的 UUID。

三选项：

1. 映射表：archive 里存 α UUID ↔ β UUID，不改 membox
2. **带 UUID 导入（采用）**：membox 加接口，写入时保留源 UUID，身份真正全局
3. UUID 入 frontmatter：身份随字节走，但侵入文件内容

**方案 2 的适用边界（关键）**：保留 UUID 只在「存在共同历史」时良定义。随机碰撞（UUIDv7, 122 bit）不可能；真风险是**语义碰撞**——同一 UUID 在两边独立演化出无共同祖先的内容，三态比对失去依据。因此：

- **Sync**（同一用户、自己的多个 workspace，有共享 archive）→ 保留 UUID
- **Share**（跨用户 / 跨所有权边界）→ **重新铸号**：导入方生成新 UUID，可记 `forked_from` 溯源。分享即 fork，此后各自独立演化，互不覆盖
- 跨用户共同编辑同一文档 = CRDT/版本向量问题，不属于同步层，后续再说

## 10. 依赖项（对 membox 的改动）

- membox 需新增「带 UUID 导入」接口：接收 `{UUID, name, 内容}`，直接在 catalog 中按指定 UUID 建档（跳过 `NewDocumentID()`），按本 workspace 自己的目录约定物化文件，并触发正常索引。已存在的 UUID 走「原地更新」路径：name 变了则 rename（目录不动），内容变了则更新字节。同步层通过该接口写入，不再走「写裸文件 → rescan」

**存储引擎中立性（原则）**：同步协议只携带引擎中立的三样东西——元数据清单（`UUID → Entry`）、blob（`sha256 → bytes`）、导入命令。各端通过自己的 `CatalogStore` port 读写本地 DB：桌面端 SQLite、server Postgres，只是同一 port 的两个 adapter（`internal/infrastructure/sqlite` / `postgres` 已存在），同步层不感知引擎，server 换 Postgres 零改动。禁止的方向：同步 DB 文件本身（二进制、含搜索索引/已读等本地状态、页级冲突不可 merge）或 DB 级复制（Litestream / logical replication，工作在领域层之下，复制本地垃圾状态且跨不了引擎）。archive 是同步层自己的本地账本，不属于任何一端 DB 的被复制状态。

## 11. 未来方案：server 托管 archive（任意端发起）

目标：任何端随手发起同步，保住账本唯一性。做法：archive 托管给关系里永远在线的 hub（server）。

- **存储**：`sync_archives(account_id, device_id, archive_json, fencing_token, updated_at)`；key 是「账号×设备」，每对关系一本账；archive 对 server 不透明
- **协议（三段式会话）**：① `begin`：加锁（按 account 串行化）+ 返回 archive 与 server 快照 + fence token；② 客户端本地 reconcile（纯函数不动），执行变更（拉/推 blob、带 UUID 导入）；③ `commit`：校验 fence token，原子写回 archive，放锁
- **锁**：粒度 = 按 account 串行化（同步秒级低频，代价为零）；fencing token 防僵尸持锁者（单调递增序号，写回必须携带，过期拒绝）
- **崩溃语义与本地版同构**：所有操作幂等（带 UUID upsert、CAS blob、Apply 可重放），中途死亡 → 下轮用旧 archive 重放/自动收敛
- **客户端改动面**：`LoadArchive/SaveArchive` 抽象为窄接口 `ArchiveStore`（LocalFile / Server 两个真实实现，符合「真实多实现才收接口」的规矩），reconcile 零改动

与 Unison 双账本的取舍：我们世界里有永远在线的 hub，让 hub 当裁判比无单点的双账本提交协议简单；Unison 的方案适合无 hub 的直连世界。

## 12. 已知坑（先记着，不处理）

- **驱动方与账本唯一性**：一对同步关系的 archive 是唯一且连续的账本，必须每次同步读写同一本。MVP 采用固定驱动方（账本在驱动方本地），把问题简化为配置约定。「错开时间但各用各的账本」会导致历史断裂、满屏假冲突。任意端发起的升级路径见第 12 节。另外：驱动方本地丢失 archive 会退化为「首次同步」语义——一致内容自动收敛，不一致报假冲突，可恢复但不丢数据
- **untracked 的同步语义（已拍板）**：`path remove` = 该目录下文档批量退出共享 VFS = **批量删除语义**，tombstone 正常传播。安全性由「字节永不硬删」保证：α 本地文件不动，β 侧进回收站可恢复。连带行为：remove 后 re-add 同一目录，文档会以新 UUID 重新入库（`ReconcileRenames` 不配对 untracked），即这批文档换一次身份——MVP 接受（低频、结果正确，仅历史断裂）
- name 冲突：接收方物化目录里已有同名文件（属于另一个 UUID）。import 接口自行 slug 去重（如 `-2` 后缀），本地名与同步的 name 允许短暂不一致，下一轮以胜方 name 为准再 rename
- mtime 精度/时钟问题：仅作快路径，最终信任 sha256
- 同一文件一轮内被外部并发修改：不处理，靠 sha256 在 copy 前后再校验一次（不匹配则本轮报冲突）
- 大小写不敏感文件系统上的重命名：不处理
