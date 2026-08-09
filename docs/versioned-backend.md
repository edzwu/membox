# membox 前后端、版本与多端备份架构

> 状态：Proposed
>
> 目标：在保留 Markdown 可见、可用普通编辑器修改这一产品特性的同时，把 membox 从“文件系统 + 本地索引”演进为“后端权威版本库 + 多个 Markdown workspace 客户端”。
>
> 参考：`~/repo/docbank/` 的 single-owner daemon、immutable version、content-addressed storage、optimistic concurrency、logical snapshot 和 staged restore 设计。本文不会直接复制 docbank 的通用文档归档、pack storage 或 audited-history 复杂度。

## 1. 结论

目标架构采用以下不变量：

1. **后端是 Document identity、当前 head、版本历史、关系元数据和备份状态的权威。**
2. **本地 Markdown 目录是可编辑的 workspace projection，不再是唯一内容权威。** 用户仍可用 Vim、VS Code 等普通编辑器直接修改文件。
3. **每次已提交内容修改都创建 immutable `DocumentVersion`。** 旧版本永不原地改写。
4. **一个 `MEMBOX_HOME` 只有一个后端进程打开 SQLite 和 object store。** CLI、TUI、Web、browser extension、sync worker 和 Agent 都通过同一 API 操作。
5. **写入使用 stable Document UUID + expected revision/head。** stale write 返回冲突，不允许 Agent 或另一台设备静默覆盖新内容。
6. **备份在后端执行。** 后端生成 representation-neutral logical snapshot，再独立复制到 local directory、S3-compatible storage、GitHub 等多个 destination。
7. **GitHub 是 backup/replica adapter，不是 membox 的事务数据库，也不是多设备同步协议。**
8. **sync、version history 和 backup 是三个不同问题：** sync 传播当前变化，version history 保存每次提交，backup 在后端损坏或丢失时恢复整个 vault。

这意味着 `docs/development.md` 中的下述 MVP 边界将成为历史阶段，而不是长期架构：

```text
旧：Markdown file = content authority
新：backend head/version = content authority
    Markdown file = editable workspace projection
```

## 2. 为什么不能只给现有 Markdown 目录加 Git

直接在每个扫描目录运行 Git 有几个问题：

- 并非所有目录都属于同一个 repository；
- 自动 commit、用户手工 Git 操作和多设备 push/pull 会互相干扰；
- SQLite 中的 UUID、links、topics、annotation anchors、trash 等身份信息无法只靠 Markdown 恢复；
- Agent 的 read-modify-write 需要明确的 stale-write precondition，普通文件写入和 Git commit 不能提供这一 API 契约；
- 多个 Git remote 解决的是 repository replication，不是运行中后端的一致 snapshot；
- force push、rebase、shallow clone 或未提交工作区都可能破坏恢复假设。

GitHub 很适合成为一个可审阅的异地副本，但不应成为在线写入路径的基础。

## 3. 领域模型与 authority

### 3.1 核心术语

| 术语 | 含义 |
| --- | --- |
| Vault | 一个独立的 membox 后端，具有稳定 `vault_id` |
| Collection | 后端中的逻辑根目录；替代机器相关的 absolute indexed path |
| Document | 稳定逻辑 Markdown 文档，UUID 在 rename、move、trash、restore、content update 后不变 |
| Document Revision | Document 的 sync-relevant logical state 版本号；用于 `If-Match` |
| DocumentVersion | 一次 immutable Markdown 内容状态，具有独立 UUID |
| Head | Document 当前选中的 DocumentVersion |
| Blob | 一组准确 bytes，以 SHA-256 寻址；相同 bytes 可以被多个版本共享 |
| Workspace | 某台设备上 Collection 到本地目录的映射 |
| Workspace Base | 本地文件最后一次成功同步对应的 server version |
| Conflict Version | 从 stale base 产生、已安全保存但未成为 head 的候选版本 |
| Snapshot | 后端某个一致逻辑时点的 metadata + 所有 retained blobs |
| Backup Destination | 接收 immutable snapshots 的 local/S3/GitHub adapter |
| Replica | 便于浏览或协作的副本，不一定满足完整恢复契约 |

### 3.2 Authority 表

| 数据 | 权威 | 说明 |
| --- | --- | --- |
| Document UUID | backend SQLite | 不可从 path/hash 重新推导 |
| Logical collection/path | backend SQLite | absolute local path 不进入全局 identity |
| Current head/revision | backend SQLite | 写事务中的并发权威 |
| Markdown versions | immutable version rows + blob store | retained history 的事实 |
| Blob physical bytes | backend object store catalog | 磁盘上 stray file 本身没有 authority |
| Links/topics/annotation relation/trash | backend SQLite | 与 UUID 一同备份 |
| Search index | backend FTS5 | 可从 current heads 重建 |
| Local Markdown file | workspace projection | 可以有 unsynced local edit，不自动成为 server head |
| Workspace base/cursor | client-local sync state | 每台设备独立，不是 vault 的全局 path |
| Backup snapshot | destination manifest + objects | 只读恢复点，不接受在线文档 mutation |

阅读状态、scroll progress、临时 UI 选择等不应推进 Document Revision；content、logical path、trash state 以及需要并发保护的关系 mutation 才推进 revision。

## 4. 组件拓扑

```text
                                  ┌─ Local workspace A (Markdown files)
                                  │
CLI / TUI / Web / Extension ─┐    ├─ Workspace sync client
Agent tools ─────────────────┼────┤
Remote client ───────────────┘    │   HTTPS / localhost HTTP
                                  ▼
                         Membox Backend API
                    single owner per vault/home
                                  │
          ┌───────────────────────┼───────────────────────┐
          ▼                       ▼                       ▼
   SQLite metadata         Immutable object store       FTS projection
 docs/versions/heads       objects/sha256/...         current heads only
 graph/trash/change log
          │
          ▼
   Backup supervisor ───── local directory
          ├─────────────── S3-compatible / R2 / MinIO
          ├─────────────── GitHub restore-grade backup
          └─────────────── GitHub readable mirror
```

### 4.1 一个后端，不增加第二个互相竞争的 daemon

当前 Web Companion 已经是本地 HTTP 进程，但 CLI/TUI 仍可分别 `bootstrap.Open()` 同一个 SQLite，并依赖 `mutation.lock` 协调。目标状态不是在 Companion 旁边再加一个 storage server，而是：

- 将 Web Companion 演进/吸收到统一的 **Membox Backend daemon**；
- daemon 独占 SQLite、object store、scanner/sync jobs、Agent manager 和 backup jobs；
- CLI/TUI/Web/Agent 全部成为 API client；
- `mutation.lock` 保留为 daemon ownership/启动保护，而不是每个客户端的业务事务方案；
- CLI 本身必须只使用公共 API，作为“Agent 也能完成所有操作”的架构测试。

建议目标命令：

```bash
mm daemon run
mm daemon start
mm daemon status
mm daemon stop
```

本地模式可以自动发现和启动 loopback daemon。远程模式只更换 endpoint/profile，不更换 Document/Version API。

### 4.2 Workspace client

Workspace client 负责普通文件体验：

- 将一个 server Collection 映射到本机目录，例如 `personal -> ~/notes`；
- watcher 只用于降低延迟，周期 scan 用于最终收敛；
- 记录每个文件的 `document_id`、`base_version_id`、local hash 和 change cursor；
- pull 时使用 temp file + fsync + atomic rename 发布本地文件；
- push 时携带 base version/revision；
- 不把机器 A 的 `/Users/a/notes` 同步成机器 B 的路径。

现有 `paths` 应逐步拆为：

```text
server: Collection(id, logical name)
client: Workspace(collection_id, local absolute root, device_id)
```

Document 的 server location 是 `(collection_id, relative_path)`。

## 5. Version 与 Blob 模型

### 5.1 建议 schema

下面是概念 schema，不要求一次 migration 完成：

```sql
CREATE TABLE vault_state (
    vault_id TEXT PRIMARY KEY,
    change_sequence INTEGER NOT NULL
);

CREATE TABLE collections (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    revision INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL
);

CREATE TABLE documents (
    id TEXT PRIMARY KEY,
    collection_id TEXT NOT NULL REFERENCES collections(id),
    relative_path TEXT NOT NULL,
    revision INTEGER NOT NULL,
    head_version_id TEXT,
    state TEXT NOT NULL CHECK(state IN ('live','trashed')),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(collection_id, relative_path)
);

CREATE TABLE blobs (
    sha256 TEXT PRIMARY KEY,
    size INTEGER NOT NULL,
    media_type TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE document_versions (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES documents(id),
    parent_version_id TEXT,
    merge_from_version_id TEXT,
    blob_sha256 TEXT NOT NULL REFERENCES blobs(sha256),
    size INTEGER NOT NULL,
    operation TEXT NOT NULL,
    state TEXT NOT NULL CHECK(state IN ('committed','conflict')),
    actor_kind TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    request_id TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    UNIQUE(document_id, request_id)
);

CREATE TABLE change_log (
    sequence INTEGER PRIMARY KEY,
    document_id TEXT,
    document_revision INTEGER,
    kind TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
```

必须额外通过 foreign key/transaction validation 保证：

- `head_version_id` 属于同一个 Document，且状态为 `committed`；
- Version、Blob 和 Document head 的授权关系完整；
- `size` 与 Blob catalog 一致；
- 一个 `request_id` 重试不会创建第二个版本；
- FTS 只索引 current head，不混入历史或 conflict body。

### 5.2 写入顺序

所有 content write 使用与 docbank 相同的关键顺序：**先证明并发布 bytes，再提交引用它的 metadata。**

```text
1. client 发送 document_id、expected revision/head、size、sha256、request_id
2. backend 写入 private staging，同时独立计算 size + SHA-256
3. mismatch -> 拒绝，不创建 Version/head authority
4. fsync staging，按 digest 原子发布到 objects/sha256/...
5. SQLite transaction:
   - 再次检查 expected revision/head
   - INSERT blob catalog authority
   - INSERT immutable DocumentVersion
   - UPDATE Document head + revision
   - UPDATE FTS
   - APPEND change_log
6. commit 后返回完整 receipt + ETag
```

metadata commit 失败后可能留下一个未授权 object；它不可被普通读取，后续 GC 清理。反向状态——metadata 已指向尚未 durable publish 的 bytes——必须被顺序设计阻止。

### 5.3 Revision 与 `If-Match`

建议 HTTP 使用：

```http
GET /api/v1/documents/{id}
ETag: "doc:<uuid>:17"

PUT /api/v1/documents/{id}/content
If-Match: "doc:<uuid>:17"
X-Membox-Base-Version: <version-uuid>
X-Membox-Blob-Hash: <sha256>
X-Membox-Blob-Size: <bytes>
Idempotency-Key: <uuid>
```

- missing precondition：`428 precondition_required`；
- stale revision/head：`412 stale_revision`；
- hash/size mismatch：`422 digest_mismatch` / `size_mismatch`；
- duplicate request ID：返回第一次 mutation 的 receipt；
- 不允许客户端通过“先读取 path、再按 path 修改”跨越两个事务。

Agent 写操作必须使用 Document ID + revision，不能使用 path 作为长期 authority。

### 5.4 Revert、prune、trash 与 GC 分离

| 操作 | 行为 |
| --- | --- |
| Revert | 创建一个新的 head version，复用所选历史 Blob；不倒转或删除历史 |
| Version prune | 显式删除 selected non-head version authority；默认 dry-run |
| Trash | 只改变 logical state/location，保留 UUID、versions、links 和 bytes |
| Trash empty | 删除被选中的 logical metadata；不承诺立即回收物理 bytes |
| GC | 删除没有任何 current/history/conflict/trash reference 的 blob authority 与 loose object |
| Repack | 未来优化；MVP 不需要先实现 pack storage |

初期建议无限保留 version，不实现自动 retention。错误的自动清理比存储增长更危险。

## 6. 多设备同步与冲突

### 6.1 Pull

Client 保存 server `change_sequence` cursor：

```http
GET /api/v1/changes?after=1842&limit=500
```

每条 change 至少包含 Document ID、revision、kind、logical path 和 head version。Client 对每个文件执行：

1. 比较 local hash 与 workspace base hash；
2. local 未修改：下载并验证 head，原子替换文件，推进 base；
3. local 已修改：不覆盖，进入 push/conflict 路径；
4. rename/delete 同样先检查 local dirty state。

Cursor 只是同步增量；完整 reconcile 仍必须可通过 inventory API 完成，避免 change log retention 或客户端长期离线造成永久缺口。

### 6.2 Push

本地文件改变后：

1. client 读取自己的 `base_version_id`；
2. hash 本地 bytes；
3. 用 base/revision 提交；
4. 成功后更新 base；
5. stale 时绝不自动覆盖 server head。

### 6.3 Conflict 不丢数据

普通 Agent/API mutation 在 stale 时只返回 `412`。Workspace sync client 在收到 stale 后可显式调用 conflict endpoint，把本地候选保存为 immutable conflict version：

```http
POST /api/v1/documents/{id}/conflicts
X-Membox-Base-Version: <old-base>
...
```

Conflict Version：

- 保存 local bytes、base、actor/device 和时间；
- 不推进 Document head；
- 在 TUI/Web 中显示 base ↔ current head ↔ candidate 的 Markdown diff；
- 用户或 Agent 经确认合并后，以当前 head 为 expected revision 创建新 committed version，并记录 `merge_from_version_id`；
- 在 conflict 已被安全上传前，不删除或覆盖本地 dirty file。

不建议用自动生成 `filename (conflicted copy).md` 作为唯一冲突模型；它会创建错误的新 Document identity。可以生成这种文件作为人类可见导出，但 conflict identity 必须先在后端保存。

## 7. Agent 修改文件的契约

版本后端是 Agent 写能力的前置条件。安全流程：

```text
1. GET Document -> id, revision, head_version, hash
2. Agent 形成 patch/new body
3. 服务端生成 preview/diff
4. 用户 allow once（若产品策略要求审批）
5. PUT with If-Match + Idempotency-Key
6. backend 验证并创建 immutable version
7. 返回 version ID、new revision、hash 和 actor provenance
```

Version 至少记录：

```text
actor_kind = agent
actor_id   = <agent session id>
request_id = <tool call / idempotency id>
message    = <bounded mutation summary>
```

可选 provenance 扩展记录 `run_id`、`tool_call_id`、model/provider，但不得把 secret、完整 prompt 或 chain-of-thought 写进版本 metadata。

Agent 遇到 `412` 必须重新读取、重新评估并再次展示 diff；不得自动拿新 revision 重放旧 patch。Agent 只能在收到 committed receipt 后声称文件已修改。

## 8. Backup 设计

### 8.1 Backup 不是复制 live SQLite

后端 snapshot 应捕获逻辑状态，而不是依赖某个 SQLite page/WAL 布局：

```text
Snapshot
├── manifest.json
├── metadata.jsonl[.zst]
└── objects/sha256/...  # snapshot 引用的 logical bytes
```

`metadata.jsonl` 包括：

- vault ID、schema/export version、change high-water；
- collections、documents、locations、revisions 和 heads；
- all retained document versions 与 blob references；
- links、topics、annotations、sources/provenance、trash；
- 必要 settings；
- 不包括 FTS rows、runtime records、locks、logs、client-local workspace path。

FTS 在 restore 后从 current heads 重建。

### 8.2 一致 capture

建议 capture 流程：

1. 短暂取得 mutation freeze；
2. 开启并固定 SQLite read transaction，记录 change high-water；
3. 释放 mutation freeze，让普通写继续进入 WAL；
4. 从同一个 pinned view deterministic 地导出 metadata；
5. 读取该 view 授权的 blobs，逐个验证 size/SHA-256；
6. destination 先上传缺失 objects；
7. 最后原子发布 immutable manifest。

在 manifest 发布前中断的 snapshot 不可见。备份期间 GC/retention 需要 preservation lease，不能删除 pinned snapshot 仍引用的对象。

### 8.3 多 destination 语义

一个 logical snapshot 可以 fan out 到多个 destination，但不做跨云 distributed transaction：

```text
snapshot S42
  local-nas     complete
  github        complete
  s3            retrying
```

- Document commit 不等待 remote backup；
- 每个 destination 有独立 cursor、重试、last success 和 verify state；
- S42 只有在某 destination 的 manifest 发布后，才对该 destination 算成功；
- “全局成功”由 policy 定义，例如 `required = [local-nas, s3]`；
- UI 必须显示 per-destination lag，不能用一个模糊的“已备份”状态掩盖部分失败。

建议 adapter contract：

```go
type BackupDestination interface {
    HasObject(ctx context.Context, digest string) (bool, error)
    PutObject(ctx context.Context, digest string, r io.Reader, size int64) error
    PutMetadata(ctx context.Context, snapshotID string, r io.Reader, size int64) error
    CommitManifest(ctx context.Context, manifest SnapshotManifest) error
    ListSnapshots(ctx context.Context, page Page) (SnapshotPage, error)
    VerifySnapshot(ctx context.Context, snapshotID string, full bool) (VerifyReport, error)
}
```

### 8.4 GitHub adapter

GitHub 连接由 backend 保存/读取 credential；browser 和 Agent 不接触 token。推荐区分两个明确模式：

#### Restore-grade backup

专用 private repository/branch，每个 snapshot 对应 append-only commit：

```text
.membox/manifest.json
.membox/metadata.jsonl.zst
.membox/objects/sha256/ab/<digest>
files/<collection>/<relative-path>.md
```

- `.membox/objects` 包含 snapshot 所有 retained unique blobs，确保单个 snapshot 可恢复完整 history；
- `files/` 是 current head 的可读 projection；
- Git object database 会复用 unchanged content；adapter 不需要反复上传相同 Git objects；
- 禁止 force push 和 history rewrite；backend 记录 remote commit OID；
- restore 同时验证 membox manifest hash、metadata relations 和每个 logical blob hash，不能只相信 Git transport 成功。

#### Readable mirror

只提交 current Markdown tree 和最小 ID manifest，适合浏览、review 和普通 Git clone。它可以不包含每个中间版本，因此必须标记为 `replica`，不能计入 full-fidelity backup policy。

GitHub 限制：

- private repository 仍是明文托管；敏感 vault 需要 encrypted archive destination；
- GitHub 对大文件和 repository size 有限制；Markdown 很适合，未来附件未必适合；
- API rate limit、branch protection 和 token expiry 必须成为 destination health；
- 不允许把 repository working copy当作后端 object store。

### 8.5 Restore

Restore 永远不覆盖正在运行的 vault：

1. 选择 snapshot 并下载到 private staging；
2. 验证 manifest、metadata hash、所有 referenced blobs；
3. 将 JSONL import 到 fresh current-schema SQLite；
4. 验证 foreign keys、head/version ownership、sizes 和 counts；
5. 重建 object catalog 和 FTS；
6. 运行完整 verify；
7. 最后原子 publish 为一个新 vault/home，或在后端停止后显式替换。

恢复测试是 backup feature 的一部分。不能只测试“上传成功”。

## 9. API 面

建议公开版本化 API `/api/v1`，并生成 OpenAPI：

```text
GET    /vault
GET    /collections
GET    /documents/{id}
GET    /documents/{id}/content
PUT    /documents/{id}/content
GET    /documents/{id}/versions
GET    /versions/{version_id}
GET    /versions/{version_id}/content
POST   /documents/{id}/revert
POST   /documents/{id}/versions/prune
POST   /documents/{id}/conflicts
PATCH  /documents/{id}                 # rename/move metadata
POST   /documents/{id}/trash
POST   /documents/{id}/restore
GET    /changes
GET    /search
POST   /backup/snapshots
GET    /backup/snapshots
GET    /backup/destinations
POST   /backup/snapshots/{id}/verify
POST   /backup/snapshots/{id}/restore
```

所有错误使用 RFC 7807 + stable `code`。所有 list/search/change endpoints 必须 bounded/paginated，并显式报告 truncation/cursor。

CLI 对应能力：

```bash
mm doc versions <document-id>
mm doc version show <version-id>
mm doc version cat <version-id>
mm doc diff <document-id> [<from> <to>]
mm doc revert <document-id> <version-id>

mm sync status
mm sync now
mm sync conflicts

mm backup destination list
mm backup create
mm backup list
mm backup status
mm backup verify <snapshot-id> --full
mm backup restore <snapshot-id> --to <new-home>
```

## 10. 安全与完整性

最低要求：

- backend 默认仅 loopback；remote deployment 必须 TLS，不复用明文 local token；
- daemon runtime record 和 local API token 仅 owner 可读；
- remote backup credentials 进入 OS keychain/secret file，不进入 Markdown、Git mirror、logs 或 API response；
- bytes 在 write、read、backup、restore、verify 的边界都核对 size + SHA-256；
- object publication 使用 private temp + fsync + atomic rename；
- SQLite 使用 foreign keys、WAL、busy timeout，但并发 authority 来自 single owner + transaction + revision，不来自 WAL 本身；
- destructive prune/trash-empty/GC 默认 dry-run 或显式 `--run`；
- backup manifest 可增加 HMAC/signature；若需要 rollback detection，trusted snapshot evidence 必须另存于 vault 外部。

## 11. 与 docbank 的关系

直接借用：

- single-owner daemon，CLI 也只走 HTTP；
- stable logical identity、immutable version、content-addressed blob 分层；
- bytes durable publish before metadata authority；
- revision/ETag 保护 Agent read-modify-write；
- logical deterministic snapshot，而非热拷贝 SQLite；
- restore 在 staging 中验证后才发布；
- edit、revert、prune、trash、GC 分离。

暂不借用：

- 立即把所有本地文件收进纯 virtual vault；membox 仍提供普通 Markdown workspace；
- 通用 binary archive、MIME ingest、pack/repack；第一阶段只需 Markdown loose objects；
- audited permanent history 和复杂证据链；先做普通 immutable history + provenance；
- server-side arbitrary filesystem ingest 作为 remote API；远程 workspace 应上传 bytes，不让 server 读取任意 client path。

## 12. 分阶段迁移

不要直接进行一次不可逆重写。

### Phase 0：ADR 与边界冻结

- 确认本文 authority flip；
- 定义 Vault/Collection/Version/Blob/Workspace ubiquitous language；
- 冻结 `/api/v1` error、revision、receipt 和 idempotency contract；
- 为当前数据库和 Markdown 根目录制作迁移前备份。

### Phase 1：本地 version kernel（仍兼容现有文件 authority）

- 新增 `blobs`、`document_versions`、head/revision 和 object store；
- 将当前 active Markdown 导入为 `baseline` version，保留现有 Document UUID；
- 所有 membox 内部 create/sync/rename-body/annotation-note writes 同时创建 version；
- scanner 发现外部修改时创建 `external_edit` version；
- 实现 versions/list/cat/diff/revert；
- 文件仍留在原位置，因此旧工作流不中断。

Migration 必须逐文件验证 `document_index.sha256` 与实际 bytes。missing 文档保留 identity，但不伪造 baseline content。

### Phase 2：single-owner backend

- 将 Web Companion 扩展为统一 daemon；
- daemon 独占 SQLite/object store；
- CLI/TUI/Web/Agent 使用同一个 typed client；
- API 引入 ETag、If-Match、idempotency 和 structured errors；
- 删除客户端直接 `bootstrap.Open()` 的数据路径；
- 增加并发 stale-write、crash ordering 和 receipt tests。

### Phase 3：backup engine

- deterministic metadata JSONL；
- local directory destination；
- snapshot verify + staged restore；
- S3-compatible destination；
- GitHub restore-grade adapter与 readable mirror；
- per-destination policy、retry、lag 和 health UI。

先证明 local snapshot 能完整 restore，再添加 cloud adapter。

### Phase 4：Workspace sync 与多设备

- Collection + per-device Workspace mapping；
- change feed、inventory reconcile、push/pull；
- conflict versions 与三方 diff；
- remote backend profile/TLS/auth；
- 从“文件 authority”正式切换为“backend head authority”。

### Phase 5：Agent write hardening

- Agent mutation preview 使用 version diff；
- expected revision + idempotency；
- actor/session/run provenance；
- conflict 后重新读取/重新审批；
- version history UI 标记 human、sync、agent、revert 来源。

Agent read-only 可以更早上线；无人值守写入应等 Phase 2 的 revision contract 完成。

## 13. 测试与验收

关键测试：

### Version/store

- baseline migration 保留 Document UUID；
- 内容变化创建新 Version，不改旧 Blob；
- 相同 bytes 共享 Blob 但 Version ID 不同；
- crash before metadata commit 不产生可读 head；
- revert 创建新 head，不删除历史；
- FTS 永远对应 current head。

### Concurrency/Agent

- 两个 client 从 revision 7 写入，只有一个可推进 head；
- stale Agent patch 返回 412，不能静默 retry；
- Idempotency-Key retry 只产生一个 Version；
- rename + content update 可以作为一个有完整 receipt 的 logical mutation；
- read-state update 不造成 content conflict。

### Sync

- clean workspace pull 原子更新；
- dirty workspace 不被 remote head 覆盖；
- stale local bytes 可上传为 conflict version；
- conflict resolve 保留 base/current/candidate；
- 多机器不同 absolute root 映射同一个 Collection。

### Backup/restore

- pinned snapshot 与并发写产生一致旧视图或新视图，不能混合；
- destination 失败不阻止 Document commit；
- 多 destination 独立重试且状态准确；
- GitHub mirror 不被误报为 full backup；
- restore 后 UUID、heads、all versions、links、topics、annotations、trash 一致；
- restore 后重建 FTS 并能读取/验证所有 retained blobs；
- corrupt/truncated snapshot 永不发布为可用 vault。

### 最终验收

1. 普通编辑器修改本地 `.md` 后形成可见版本；
2. TUI 能查看 diff、历史、actor，并 revert；
3. Agent 修改必须基于 revision，所有修改有 Version ID 和 provenance；
4. 两台设备并发编辑不会丢掉任一方 bytes；
5. 后端可向至少两个 destinations 备份并显示各自 lag；
6. 从任一 restore-grade destination 可恢复到新 home；
7. GitHub 可作为可选 full backup 或明确标记的 readable replica；
8. 停掉后端时没有其他进程绕过 API 修改 SQLite/object authority。

## 14. 当前最优先的实现切片

第一刀不应先做 GitHub，也不应先做远程 UI。最小高价值 vertical slice 是：

```text
existing Document
  -> baseline immutable Version
  -> update with expected revision
  -> list/diff/revert versions
  -> local logical snapshot
  -> restore to a new temporary home
```

它先建立 Agent 安全修改和真实备份共同依赖的 version authority。完成这个切片后，再把现有 Companion 收敛为 single-owner backend，最后添加 GitHub/S3 adapters 和多设备 workspace sync。
