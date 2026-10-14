# membox Remote Stage 1 — 架构决策记录（ADR）

> 本文档是 `docs/membox-remote-stage1-plan.md` M0 T01 的产出：仓库审计结果 + 首版架构决策。
> 审计日期：2026-10（基于 main @ bebb56a）。

## 1. 审计结果摘要

| 审计项 | 现状 | 可复用性 |
|---|---|---|
| Document 模型 | `internal/domain/catalog/types.go`：Document 聚合（DocumentID、Location、ContentFingerprint{SHA256,Size}、IndexState、状态机 active/missing/untracked） | **部分复用**。身份语义（DocumentID、ContentFingerprint）可直接用；Location 是 `(PathID, RelativePath)` 的 filesystem-centric 概念，远端以 blob 为中心，**不搬** Location/IndexedPath |
| UUIDv7 | `internal/infrastructure/system/services.go`：`uuid.NewV7()`（google/uuid），实现 `port.IDGenerator` | **直接复用**。远端铸造文档 UUID 用同一实现，保证两节点身份同构 |
| BlobStore | `internal/infrastructure/blobstore/store.go`：内容寻址（sha256 分片目录）、原子发布（temp+rename+fsync）、去重时校验哈希；对应 port 是 `ImmutableContentStore`（**只有 Put**），`ContentObject{SHA256, ObjectHash, Size}` | **语义复用，接口需扩展**。内容寻址 + "Put 成功必须持久可见后才提交元数据"的契约与 plan 完全一致；但 plan T05 需要 Get/Head/Delete，现有 port 没有，需新增远端向接口 |
| 版本模型 | SQLite 已有 `contents(sha256 PK, representation, object_hash)` + `document_versions(parent_version_id, reason)` + `document_heads` 不可变版本链 | **概念 1:1 映射**。plan 的 `document_versions` 不可变 revision 快照不是新发明，是把本地已验证的模型搬到 PG；差异仅是本地版本由扫描触发、远端由 API 写入触发 |
| HTTP Handler | `internal/web/backend/server.go`：stdlib `http.NewServeMux`、每端点 `mux.HandleFunc`、单 Server 结构体持有依赖、薄 handler 调 application service、token 认证（`SetToken`，空=关闭） | **模式复用，代码不复用**。Companion server 绑定 loopback、服务 Miru 资产、单用户 token，与远端多用户 server 职责不同；新 server 照同样的薄 handler 模式写，不 import web/backend |
| 配置加载 | `MEMBOX_HOME` env + `~/.membox` home + 功能级 JSON 配置（`pdf-converter.json`、`bridge.json`）。无 .env、无第三方配置库 | **新约定，有意分叉**。远端是 12-factor 服务端进程，用 env/`.env`（plan T03）；本地约定不动 |
| Migration | SQLite 单文件 in-code `migrate()`，`CREATE TABLE IF NOT EXISTS` 幂等 schema，无版本化迁移文件 | **新约定，有意分叉**。PG + 多用户 + 需要 down 迁移，用 golang-migrate（plan T08）；本地 SQLite 保持现状 |

## 2. 架构决策

### ADR-001: 远端 Document 身份复用本地语义，不搬文件系统概念

- **状态**: accepted
- **背景**: 远端 schema（plan T09/T10）需要 documents + document_versions；本地 domain 已有 Document 聚合。
- **决策**: 远端复用 `catalog.DocumentID`（UUIDv7）、`ContentFingerprint` 语义和 `system/services.go` 的 ID 生成器。**不**复用 `Location`、`IndexedPath`、扫描状态机——远端文档的"位置"是 `blob_key`，没有 scan path 概念。
- **依据**: 本地 Location 强制 `(PathID>0, 合法相对路径)`，是文件系统扫描的产物；远端文档由 API 创建，没有对应的观察/重命名调和流程。
- **后果**: 未来本地↔远端 sync 时，映射点是 `(DocumentID, sha256, blob_key)` 三元组，而不是路径。路径只属于本地节点。

### ADR-002: BlobStore 语义复用，远端接口独立扩展

- **状态**: accepted
- **背景**: plan T05 要求 R2 adapter 提供 `Put/Get/Head/Delete`；现有 `port.ImmutableContentStore` 只有 `Put`。
- **决策**: 新增远端向 port（如 `internal/application/port` 中的 `RemoteContentStore`：`Put/Get/Head/Delete`，key 为 sha256），由 `internal/infrastructure/r2` 实现。**不改** `ImmutableContentStore`，本地 blobstore 不动。内容寻址（key=sha256）、先持久 blob 后提交元数据的顺序契约沿用本地注释中的约定。
- **依据**: `blobstore/store.go` 的 Put 契约（dedup、持久可见）正是远端需要的；但本地 store 是 append-only 语义，给它加 Delete 会鼓励在本地做远端才有的 GC 操作。
- **后果**: `ContentObject` 可共享；R2 adapter 的 Put 应接收 reader+size（流式，plan T15 的 MaxBytesReader 路径），而非本地 `[]byte` 全量内存签名——这是两个 port 的另一个分叉理由。

### ADR-003: 远端 HTTP server 新建，遵循现有薄 handler 模式

- **状态**: accepted
- **背景**: plan 建议 `internal/interfaces/httpserver`；现有 HTTP 代码在 `internal/web/backend`。
- **决策**: 新建 `cmd/membox-server` + `internal/interfaces/httpserver`。沿用现有模式：stdlib mux、handler 只做解析/授权/调 application service、错误响应统一。**不** import `internal/web/backend`（它属于 Companion 进程，绑 loopback、单用户 token、嵌 Miru 资产）。
- **依据**: plan 明确"不为了远端复用而使本地模块依赖远端数据库"；反向同理——companion 的 server 职责（Miru 资产、bridge、presence）与远端 API server 完全不同。
- **后果**: 鉴权中间件（session cookie + CSRF，plan T14）是 httpserver 包的内部关注点，application 层只见 `owner_id`。

### ADR-004: 远端配置用 env/.env，本地 JSON 约定不动

- **状态**: accepted
- **决策**: `cmd/membox-server` 只从环境变量读配置，提供 `.env.example`（plan T03 字段清单）；不引入 Viper 等配置库，stdlib `os.Getenv` + 启动时校验缺失项并清晰报错。真实凭据不入 Git。
- **依据**: 本地 `~/.membox/*.json` 约定服务于"桌面单用户、配置由工具自己写回"的场景；远端是 systemd 托管的服务端进程，env 是部署标准（compose、systemd EnvironmentFile 都原生支持）。

### ADR-005: 远端 migration 用 golang-migrate，本地 in-code migrate 不动

- **状态**: accepted
- **决策**: `internal/infrastructure/postgres/migrations/` 放 `NNNNNN_name.up/down.sql`，Makefile 加 `remote-db-up` / `remote-migrate`；server 启动时**校验** schema 版本，不自动执行破坏性迁移。本地 SQLite 的 in-code `migrate()` 保持不变。
- **依据**: PG 多用户场景需要可回滚、可审查的版本化迁移；本地 SQLite 是单用户嵌入式，in-code 幂等 schema 已服役良好，无动机改。
- **后果**: 两套 schema 独立演进。它们共享的只是**概念模型**（documents + 不可变版本 + 软删除 tombstone），不是 DDL。

### ADR-006: 依赖方向——shared kernel 只能向下

- **状态**: accepted
- **决策**: 允许远端依赖的共享层仅限：`internal/domain/catalog`（身份类型）、`internal/application/port`（接口与 DTO）、`internal/infrastructure/system`（UUID/clock/mutation lock）。新增 remote 专属代码（`internal/infrastructure/postgres`、`internal/infrastructure/r2`、`internal/remoteauth`、`internal/interfaces/httpserver`）**不得**被 `cmd/mm`、`cmd/mmd`、`internal/web` 依赖。
- **依据**: plan 的硬性约束；且 `internal/web/frontend_boundary_test.go` 已有"边界测试"的先例，后续可照此为 remote 包加依赖方向测试。
- **后果**: 如果某天本地 sync 客户端需要调远端 API，它以 HTTP client 身份出现（新包，如 `internal/remotesync`），而不是 import 远端 server 的 repository。

## 3. 必须新增的接口/包清单（T02 起按此施工）

| 新增 | 位置 | 说明 |
|---|---|---|
| `cmd/membox-server` | 新 | 启动入口、env 配置加载、healthz/readyz、优雅关闭 |
| `RemoteContentStore` port | `internal/application/port` | Put(reader,size)/Get/Head/Delete，key=sha256 |
| R2 adapter | `internal/infrastructure/r2` | aws-sdk-go-v2，S3 兼容端点 |
| PG repository | `internal/infrastructure/postgres` | pgx；所有文档查询强制 `owner_id` 过滤 |
| migrations | `internal/infrastructure/postgres/migrations` | golang-migrate |
| 鉴权 | `internal/remoteauth` | OTP（HMAC 存储）、session（哈希存储）、allowlist |
| HTTP API | `internal/interfaces/httpserver` | `/v1/...`，薄 handler，session 中间件 + CSRF |

## 4. 明确不做的事

- 不移动现有任何本地代码（plan 原话："不要未经审查移动现有代码"）。
- 不给本地 SQLite schema 加 remote 字段；本地 sync 状态是 Phase 4+ 的事。
- 不让远端 server 主动抓取 `source_url`（SSRF 风险，plan T19 已定）。
- 不在 R2 上 ListObjects 当文档列表（列表从 PG 查，plan T05）。
