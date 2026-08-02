# membox MVP 开发文档

> 状态：Draft  
> 目标：使用领域驱动设计（DDD）实现一个以外部 Markdown 为内容事实、以 SQLite 为身份与索引事实的本地文档身份层，并通过同一个 `mm` 二进制同时提供 CLI 和 TUI。

## 1. 产品边界

membox MVP 不保存 Markdown 内容副本，也不引入 blob storage。

```text
Markdown 文件          内容事实（content authority）
SQLite documents.id   身份事实（identity authority）
文件系统路径            当前位置（location）
SQLite FTS5            可重建的搜索索引
```

一个文档的 UUID 不因内容修改而改变：

```text
Document ID: 019...
Location:    ~/notes/a.md
SHA-256:     当前字节内容的指纹
```

路径重命名后，目标状态是：

```text
Document ID: 019...          # 不变
Location:    ~/notes/b.md    # 更新
SHA-256:     可能不变，也可能改变
```

### 1.1 MVP 包含

- 注册和查看 Markdown 扫描路径
- 递归发现 Markdown 文件
- 为文档分配稳定 UUID
- 将 UUID 映射到当前文件位置
- 更新 metadata 和 FTS5 索引
- 按关键词搜索
- 按 Document ID 读取、编辑和打开真实文件
- Cobra CLI
- Bubble Tea TUI

### 1.2 MVP 不包含

- blob storage
- 文件内容副本
- daemon 或 HTTP server
- filesystem watcher
- 文档版本历史
- 双向同步
- frontmatter UUID
- embedding/vector search
- 自动修改外部 Markdown
- 基于相似度的自动身份合并

## 2. 核心术语

### Document

逻辑文档。`documents.id` 是稳定 UUID，与文件名、目录、路径和内容 hash 无关。

### Path

用户注册到 membox 的扫描根目录，例如：

```text
~/notes
~/repo/project/docs
```

CLI 使用 `path` 作为名词：

```bash
mm path add ~/notes
mm path list
```

### Location

一个 Document 当前对应的真实文件位置，由已注册 Path 和相对路径组成。

### Index State

membox 最近一次观察到的文件状态，包括 `mtime`、`size`、`sha256` 和 FTS 内容。它不等于 Document identity。

## 3. DDD 系统架构

membox 使用务实的领域驱动设计。MVP 采用一个进程、一个 SQLite 数据库和一个主要限界上下文，不因为采用 DDD 而引入微服务、消息队列、event sourcing 或不必要的抽象。

### 3.1 限界上下文

MVP 只有一个主要限界上下文：**Document Catalog**。

它负责：

- Document 稳定身份
- IndexedPath 注册与扫描状态
- Document 当前 Location
- 文件观察状态与 missing 状态
- rename reconciliation
- 搜索索引投影

FTS5 是 Document Catalog 的可重建 read projection，不是另一个身份权威，也不单独拆成微服务。

### 3.2 分层与依赖方向

```text
                         mm
                          |
             +------------+------------+
             |                         |
      CLI Interface Adapter      TUI Interface Adapter
          Cobra                      Bubble Tea
             |                         |
             +------------+------------+
                          |
                 Application Facade
                          |
                  Application Layer
            Commands · Queries · Use Cases
                    |             |
                    v             v
              Domain Layer   Outbound Ports
       Aggregates · Values   Repositories · Scanner · FTS
                                  ^
                                  |
                         Infrastructure Adapters
                    SQLite · Filesystem · Platform
```

依赖规则：

```text
interfaces     -> facade/application contracts
application    -> domain + outbound port interfaces
infrastructure -> outbound port interfaces + domain mapping types
bootstrap      -> all concrete packages for dependency wiring
```

Domain 禁止依赖：

- SQLite/SQL
- filesystem API
- Cobra/Bubble Tea
- JSON 或终端格式
- editor/opener
- 具体 UUID、clock 实现

Application 负责组织 use case 和事务边界；Domain 负责业务规则；Infrastructure 实现持久化和外部系统 ports；CLI/TUI 只负责输入输出与交互。

### 3.3 Ubiquitous Language

代码、命令、测试和文档统一使用以下语言：

| 术语 | 含义 |
| --- | --- |
| Document | 具有稳定 UUID 的逻辑 Markdown 文档 |
| DocumentID | Document 的不可变 UUID |
| IndexedPath | 用户通过 `mm path add` 注册的扫描根目录 |
| Location | Document 当前所在的 IndexedPath 和相对路径 |
| Observation | 一次扫描观察到的文件状态 |
| IndexState | 最近已提交的 mtime、size、hash、索引时间，以及面向用户的来源创建/修改时间 |
| Missing | 已知 Document 的当前 Location 未被扫描到 |
| Relocation | 保持 DocumentID 不变并更新 Location |
| RenameCandidate | 尚不能自动确认的可能 rename |

CLI 保留简洁名词 `path`；Go domain 中使用 `IndexedPath`，避免与 `path/filepath` 和普通文件路径混淆。

### 3.4 Aggregate boundaries

#### Document aggregate

`Document` 是 aggregate root，持有：

- `DocumentID`
- 当前 `Location`
- document status
- 当前 `IndexState`
- 用户固定状态 `pinned`
- `created_at`、`updated_at`

行为由 aggregate method 表达，而不是由 handler 任意修改字段：

```go
func (d *Document) Observe(observation Observation, now time.Time) error
func (d *Document) Relocate(location Location, observation Observation, now time.Time) error
func (d *Document) MarkMissing(now time.Time)
func (d *Document) MarkUntracked(now time.Time)
```

Document aggregate 自身的核心 invariants：

1. `DocumentID` 创建后不可改变
2. 内容变化不得创建新的 DocumentID
3. 已确认的 Location 变化不得创建新的 DocumentID
4. missing/untracked 不删除 Document identity
5. 相同 SHA-256 不自动改变 Document identity

跨 aggregate 的“两个 active Document 不能占用同一个 Location”由 application use case、repository 查询和数据库唯一约束共同保证，不能假装由单个 Document aggregate 独立验证。

#### IndexedPath aggregate

`IndexedPath` 是独立 aggregate root，持有：

- Path ID
- canonical root path
- registration status
- last scan summary/time/error

它不拥有 Document 生命周期；移除 IndexedPath 只能使 Location untracked，不能删除 Document aggregate。

### 3.5 Value Objects 与 Domain Services

以下对象应实现为不可变 value object：

- `DocumentID`
- `IndexedPathID`
- `Location`
- `RelativePath`
- `ContentFingerprint`
- `FileKey`
- `Observation`

`RenameReconciler` 是纯 domain service。它接收 missing locations 与 new observations，返回：

```text
ConfirmedRelocation
RenameCandidate
UnmatchedMissing
UnmatchedNew
```

它不访问 SQLite、不遍历文件系统、不直接写 FTS。

### 3.6 Application commands 与 queries

Application layer 使用轻量 CQRS 分离写 use case 和读 use case，但不使用 event sourcing。

Commands：

```text
AddPath
RemovePath
ScanPaths
ReindexDocument
```

Queries：

```text
ListPaths
SearchDocuments
GetDocument
ResolveDocumentLocation
GetIndexStatus
```

一个 CLI subcommand 对应一个 application use case。例如：

```text
mm path add  -> AddPath
mm path scan -> ScanPaths
mm doc show  -> GetDocument
mm doc edit  -> ResolveDocumentLocation + editor adapter + ReindexDocument
```

### 3.7 Public facade

根目录 package `membox` 是 application facade，作为 CLI、TUI、未来 Miru、Agent 或 daemon 的稳定 Go API。它暴露 DTO 和 use-case methods，但不暴露 SQL row、Cobra model 或 Bubble Tea message。

CLI 和 TUI 必须调用同一个 facade。禁止：

- TUI 调用 Cobra command
- CLI 通过子进程调用另一个 `mm` 命令
- 在 CLI/TUI 中直接拼接 SQL
- infrastructure 绕过 aggregate 业务规则写入非法状态
- domain 输出终端文本或依赖 Cobra/Bubble Tea

## 4. 推荐项目结构

```text
membox/
├── cmd/
│   └── mm/
│       └── main.go                 # composition root
├── membox.go                       # public application facade
├── types.go                        # public DTOs
├── errors.go                       # public error contract
├── docs/
│   └── development.md
└── internal/
    ├── domain/
    │   └── catalog/
    │       ├── document.go         # Document aggregate
    │       ├── indexed_path.go     # IndexedPath aggregate
    │       ├── location.go         # value objects
    │       ├── observation.go
    │       ├── rename.go           # RenameReconciler domain service
    │       ├── status.go
    │       └── errors.go
    ├── application/
    │   ├── command/
    │   │   ├── add_path.go
    │   │   ├── remove_path.go
    │   │   ├── scan_paths.go
    │   │   └── reindex_document.go
    │   ├── query/
    │   │   ├── list_paths.go
    │   │   ├── search_documents.go
    │   │   ├── get_document.go
    │   │   ├── resolve_location.go
    │   │   └── index_status.go
    │   └── port/
    │       ├── repositories.go
    │       ├── scanner.go
    │       ├── content.go
    │       ├── search_index.go
    │       ├── transaction.go
    │       ├── clock.go
    │       └── id_generator.go
    ├── infrastructure/
    │   ├── sqlite/
    │   │   ├── database.go
    │   │   ├── migrations.go
    │   │   ├── document_repository.go
    │   │   ├── path_repository.go
    │   │   ├── search_index.go
    │   │   └── transaction.go
    │   ├── filesystem/
    │   │   ├── scanner.go
    │   │   ├── content_reader.go
    │   │   └── fingerprint.go
    │   └── platform/
    │       ├── editor.go
    │       └── opener.go
    ├── interfaces/
    │   ├── host/
    │   │   └── launcher.go        # editor/opener presentation port
    │   ├── cli/
    │   │   ├── root.go
    │   │   ├── errors.go
    │   │   ├── path.go
    │   │   ├── doc.go
    │   │   ├── index.go
    │   │   └── ui.go
    │   └── tui/
    │       ├── model.go
    │       ├── update.go
    │       ├── view.go
    │       ├── keys.go
    │       └── messages.go
    └── bootstrap/
        └── bootstrap.go            # construct adapters and facade
```

`cmd/mm/main.go` 只处理 process lifecycle、signal、stdio 和 exit code。依赖组装集中在 `bootstrap`，业务规则不得出现在 composition root。

## 5. CLI 设计原则

### 5.1 名词 + 动词

CLI 采用类似 GitHub `gh` 的多层 subcommand：

```text
mm <noun> <verb> [arguments] [flags]
```

MVP command tree：

```text
mm
├── path
│   ├── add
│   ├── list
│   ├── remove
│   └── scan
├── doc
│   ├── search
│   ├── list
│   ├── show
│   ├── cat
│   ├── edit
│   └── open
├── index
│   └── status
└── ui
```

具体命令：

```bash
mm path add <directory>
mm path list
mm path remove <path-id-or-directory>
mm path scan [path-id-or-directory]

mm doc list
mm doc search <query>
mm doc show <document-id>
mm doc cat <document-id>
mm doc edit <document-id>
mm doc open <document-id>

mm index status
```

`mm` 在交互式 TTY 中无参数运行时直接启动 TUI；非 TTY 环境无参数运行时打印 root usage 并以 usage error 退出。不提供额外的 `ui start` 入口，保持 `mm` 本身就是唯一 TUI 入口。

暂不提供扁平别名，例如 `mm search`、`mm cat`、`mm sync`。保持 command tree 一致，避免早期形成两套 CLI 语法。

### 5.2 Progressive disclosure

错误信息只展示用户当前所在层级的 usage，不一次打印完整 root help。

#### 参数错误

```console
$ mm path add
Error: missing directory

Usage:
  mm path add <directory> [flags]

Flags:
  -h, --help   help for add
```

不得附带 `doc`、`index`、`ui` 等无关命令。

#### 未知子命令

```console
$ mm path wat
Error: unknown command "wat" for "mm path"

Usage:
  mm path <command>

Available Commands:
  add       Add a Markdown scan path
  list      List configured paths
  remove    Remove a configured path
  scan      Scan configured paths
```

这里显示 `mm path` 的 usage，而不是完整 `mm --help`。

#### 缺少动词

```console
$ mm path
Error: a path command is required

Usage:
  mm path <command>

Available Commands:
  add
  list
  remove
  scan
```

#### 运行时错误

数据库错误、文件不存在、权限不足等运行时错误不打印 usage：

```console
$ mm doc edit 019abc
Error: document 019abc is missing at /Users/example/notes/a.md
```

Usage 不能帮助解决运行时错误，因此不应展示。

#### 显式帮助

只有用户明确执行以下操作时才展示完整的相应层级帮助：

```bash
mm --help
mm path --help
mm path add --help
```

### 5.3 Error 分类与退出码

```text
0    成功；搜索无结果也属于成功
1    运行时/业务错误
2    command、flag 或 argument usage error
130  用户中断
```

建议定义带 command 上下文的错误类型：

```go
type UsageError struct {
    Command *cobra.Command
    Err     error
}
```

Cobra root 设置：

```go
root.SilenceErrors = true
root.SilenceUsage = true
```

由顶层统一渲染：

1. 输出 `Error: ...`
2. 如果是 `UsageError`，打印该错误对应的最深层已识别 command usage
3. 如果是运行时错误，不打印 usage
4. 返回对应退出码

argument validator 和 flag parser 必须将错误包装成 `UsageError`。未知子命令错误需要从 argv 中找到最深层已识别 command，例如 `mm path wat` 应绑定到 `mm path`。

### 5.4 输出约定

- 正常结果写 stdout
- 错误、警告和扫描进度写 stderr
- `cat` 的 stdout 只包含 Markdown 原文
- human output 默认简洁
- 数据查询命令从 MVP 开始支持 `--json`
- JSON 成功输出不得混入进度和提示文本

示例：

```bash
mm path list --json
mm doc search "flash attention" --json
mm doc show 019abc --json
mm index status --json
```

## 6. Path commands

### 6.1 `mm path add`

```bash
mm path add <directory>
```

行为：

1. 展开 `~`
2. 转换为 clean absolute path
3. 验证路径存在、是目录且可读取
4. 解析 symlink 后保存 canonical path
5. 检查 canonical path 是否已经注册
6. 写入 `paths`
7. 执行该 Path 的首次扫描
8. 输出 Path ID 和扫描摘要

示例：

```console
$ mm path add ~/notes
Added path 1: /Users/example/notes
Scanned 124 Markdown files: 124 added, 0 updated, 0 missing
```

重复添加同一个 canonical path 是幂等成功：

```console
$ mm path add ~/notes
Path 1 is already configured: /Users/example/notes
```

扫描部分失败时 Path 仍保持注册，成功索引的文档保留；命令输出摘要并以运行时错误退出。后续可以执行 `mm path scan 1` 重试。

Markdown 扩展名 MVP 支持：

```text
.md
.markdown
```

扩展名比较遵循目标操作系统通常规则；数据库保存实际相对路径。

### 6.2 `mm path list`

```bash
mm path list
```

默认表格：

```text
ID  PATH                          DOCS  STATUS   LAST SCAN
1   /Users/example/notes          124   ready    2m ago
2   /Users/example/project/docs   38    partial  1d ago
```

`--json` 输出完整字段和 RFC3339 时间，不使用 `2m ago` 等相对时间。

### 6.3 `mm path remove`

```bash
mm path remove <path-id-or-directory>
```

MVP 语义：

- 停止后续扫描该 Path
- 不删除 `documents` 身份记录
- 不删除实际 Markdown
- 对只属于该 Path 的 location 标记为 `untracked`
- 默认搜索排除 `untracked` 文档
- 重新添加同一 canonical path 后可以恢复原有 location 与 Document ID

该操作必须明确打印影响数量。MVP 不提供 destructive purge。

### 6.4 `mm path scan`

```bash
mm path scan
mm path scan 1
mm path scan /Users/example/notes
```

无 selector 时扫描所有已注册 Path。它不是内容同步，只执行：

- 发现新 Markdown
- 分配 Document UUID
- 更新 location
- 更新 metadata/hash
- 更新 FTS5
- 标记 missing
- 保守识别 rename

扫描结果：

```text
Scanned 2 paths and 162 Markdown files
  added:             3
  updated:           7
  renamed:           1
  unchanged:       149
  missing:           2
  possible renames:  1
  errors:             0
```

## 7. Rename 规则

扫描只在“本次消失的旧 location”和“本次新增的文件”之间匹配：

1. 同一 filesystem file key 且唯一匹配
2. 同一 Path 内 SHA-256 完全相同且唯一的一对一匹配
3. 内容相似度只报告 `possible rename`，不自动继承 UUID
4. 无法确认时，旧 Document 标记 missing，新文件分配新 UUID

身份系统优先避免 false positive：错误地把两个文档合并为同一 UUID，比漏掉一次 rename 更危险。

相同 hash 不代表相同 Document。只有“一个旧 location 消失 + 一个新 location 出现 + 匹配唯一”时，hash 才可作为 rename 信号。

## 8. Document commands

Document selector 接受完整 UUID 或唯一 UUID prefix。prefix 匹配不唯一时返回运行时错误并要求更长 selector。

### `mm doc search`

```bash
mm doc search "flash attention" [--limit 20] [--json]
```

查询 SQLite FTS5，不隐式全量扫描文件系统。结果反映最近一次扫描或局部 reindex 的状态。

### `mm doc show`

显示：

- 完整 Document ID
- 当前 path/location
- status
- title
- mtime、size、sha256
- indexed_at

### `mm doc cat`

从当前真实 location 读取文件。stdout 只能包含文件字节，诊断写 stderr。

### `mm doc edit`

流程：

1. 解析 Document ID
2. 查询当前 location
3. 结束数据库事务
4. 启动 `$VISUAL`、`$EDITOR` 或 `vi`
5. 等待 editor 退出
6. editor 成功退出后只 reindex 当前 Document

禁止在 editor 运行期间持有 SQLite transaction。

### `mm doc open`

使用系统默认程序打开当前真实文件：

```text
macOS    open
Linux    xdg-open
Windows  platform opener
```

## 9. TUI

### 9.1 启动

```bash
mm
```

`mm` 是唯一 TUI 入口。非交互式管道中执行 `mm` 不启动 TUI，而是返回 root usage error。

### 9.2 MVP 页面

#### Search

参考 pi 的底部输入区设计：内容滚动区位于上方，search/filter input 固定在底部；未聚焦时使用 muted border，聚焦时使用 accent border。双击 `space` 进入 rg/fzf 式 Document filter：加载 active Document 列表，按空格分词做包含过滤，Enter 选择并恢复搜索输入，Esc 取消。

```text
Results                 │ Preview
> attention.md          │ # Flash Attention
                        │ Online softmax maintains...
╭──────────────────────────────────────────────────────────────────────────╮
❯ flash attention                                               search
0 shown / 0 loaded      space×2 filter • enter search • ↑↓ select • q quit
```

#### Paths

- 查看 `mm path list` 对应数据
- 添加 Path
- 触发单个或全部 Path scan
- 查看 last scan/error

#### Status

- Path 数
- active/missing/untracked Document 数
- FTS 状态
- 最近扫描时间

### 9.3 Bubble Tea 约束

- SQLite 查询和 filesystem scan 必须通过 `tea.Cmd` 执行
- 禁止在 `Update` 中执行阻塞 I/O
- scan 时显示 spinner
- 搜索请求携带 sequence，旧结果不得覆盖新查询
- 使用 `tea.ExecProcess` 暂停 TUI 并进入 Vim，退出后恢复并 reindex
- 窄终端使用列表/预览单栏切换；宽终端使用双栏
- MVP 预览 Markdown 原文，不要求完整 Markdown renderer

推荐组件：

```text
bubbletea
bubbles/textinput
bubbles/list
bubbles/viewport
bubbles/spinner
bubbles/help
lipgloss
```

## 10. Application Facade 与 Ports 草案

public facade 保持面向 use case，而不是暴露 repository：

```go
type Box struct{}

func Open(config Config) (*Box, error)
func (b *Box) Close() error

func (b *Box) AddPath(ctx context.Context, cmd AddPathCommand) (AddPathResult, error)
func (b *Box) RemovePath(ctx context.Context, cmd RemovePathCommand) (RemovePathResult, error)
func (b *Box) ScanPaths(ctx context.Context, cmd ScanPathsCommand) (ScanReport, error)
func (b *Box) ReindexDocument(ctx context.Context, cmd ReindexDocumentCommand) error

func (b *Box) ListPaths(ctx context.Context, query ListPathsQuery) ([]PathView, error)
func (b *Box) SearchDocuments(ctx context.Context, query SearchDocumentsQuery) ([]SearchResult, error)
func (b *Box) GetDocument(ctx context.Context, query GetDocumentQuery) (DocumentView, error)
func (b *Box) ResolveDocumentLocation(ctx context.Context, query ResolveLocationQuery) (LocationView, error)
func (b *Box) ReadDocument(ctx context.Context, query ReadDocumentQuery) ([]byte, error)
func (b *Box) GetIndexStatus(ctx context.Context) (IndexStatusView, error)
```

Facade 返回 application DTO，例如 `DocumentView`，而不是允许调用者持有和修改 `Document` aggregate。

Application ports 由 use case 需要定义，由 Infrastructure 实现：

```go
type DocumentRepository interface {
    Get(ctx context.Context, id catalog.DocumentID) (*catalog.Document, error)
    Save(ctx context.Context, document *catalog.Document) error
}

type IndexedPathRepository interface {
    Get(ctx context.Context, id catalog.IndexedPathID) (*catalog.IndexedPath, error)
    FindByRoot(ctx context.Context, root string) (*catalog.IndexedPath, error)
    List(ctx context.Context) ([]*catalog.IndexedPath, error)
    Save(ctx context.Context, path *catalog.IndexedPath) error
}

type MarkdownScanner interface {
    Scan(ctx context.Context, path catalog.IndexedPath) ([]catalog.Observation, error)
}

type SearchIndex interface {
    Replace(ctx context.Context, document catalog.Document, body []byte) error
    Remove(ctx context.Context, id catalog.DocumentID) error
    Search(ctx context.Context, query string, limit int) ([]SearchHit, error)
}
```

事务由 application use case 控制。`ScanPaths` 的主要流程：

```text
scan filesystem through port
        -> build Observations
        -> call RenameReconciler
        -> invoke aggregate behavior
        -> save aggregates + update FTS in one transaction
        -> return ScanReport DTO
```

Facade/Application 约束：

- 接受 `context.Context`
- 返回 typed results 和 errors
- command/query DTO 不绑定 Cobra flags
- 不打印文本
- 不读取终端状态
- 不启动 editor/opener
- 不依赖 Cobra/Bubble Tea
- application 不包含 SQL、platform command 或路径遍历实现

## 11. SQLite 初始模型

```sql
CREATE TABLE paths (
    id INTEGER PRIMARY KEY,
    root_path TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    last_scan_at INTEGER,
    status TEXT NOT NULL,
    last_error TEXT
);

CREATE TABLE documents (
    id TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    pinned INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE document_locations (
    document_id TEXT PRIMARY KEY REFERENCES documents(id),
    path_id INTEGER NOT NULL REFERENCES paths(id),
    relative_path TEXT NOT NULL,
    file_key TEXT,
    status TEXT NOT NULL,
    last_seen_at INTEGER,
    UNIQUE(path_id, relative_path)
);

CREATE TABLE document_index (
    document_id TEXT PRIMARY KEY REFERENCES documents(id),
    title TEXT,
    mtime INTEGER,
    size INTEGER,
    sha256 TEXT,
    indexed_at INTEGER,
    source_created_at INTEGER,
    source_updated_at INTEGER
);

CREATE VIRTUAL TABLE document_fts USING fts5(
    document_id UNINDEXED,
    title,
    path,
    body
);
```

初始化连接：

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
```

这些表是 Infrastructure 对 domain aggregates 和 search projection 的持久化映射，不是 domain model 本身。不得让 SQLite row struct 直接充当 `Document` 或 `IndexedPath` aggregate。

Document metadata 与 FTS 更新必须处于同一 application transaction。SQLite adapter 负责把 aggregate state 映射到表，并在读取时重建合法 aggregate；发现非法持久化状态时必须返回 integrity error，不能静默修正。

SQLite 是 identity authority，不是完全可丢弃的 cache。数据库丢失后 Markdown 内容仍然存在，但 external Document UUID 无法可靠恢复，因此必须保守处理数据库生命周期并支持普通文件备份。

## 12. 依赖建议

- CLI：`github.com/spf13/cobra`
- TUI：`github.com/charmbracelet/bubbletea`
- Components：`github.com/charmbracelet/bubbles`
- Styling：`github.com/charmbracelet/lipgloss`
- SQLite：`modernc.org/sqlite`，优先纯 Go、无 CGO 分发
- UUID：支持 UUIDv7 的稳定 Go library

具体版本在初始化 `go.mod` 时锁定，不在设计文档中写死。

## 13. 测试策略

### Domain unit tests

Domain tests 不使用 SQLite、filesystem 或 Cobra，直接验证：

- DocumentID 创建后不可改变
- `Observe` 更新 IndexState 但保持 DocumentID
- `Relocate` 更新 Location 但保持 DocumentID
- `MarkMissing` 和 `MarkUntracked` 保留身份
- 两个 active Document 不能占用同一 Location
- RenameReconciler 的 file-key、exact-hash、ambiguous matching 规则
- 相同 hash 不自动代表同一 Document

### Application use-case tests

使用 fake ports 测试：

- AddPath 的幂等行为与首次扫描 orchestration
- ScanPaths 的 transaction boundary
- aggregate save 与 FTS update 同成同败
- RemovePath 不删除 Document
- ReindexDocument 只更新目标 Document
- query handler 返回 DTO，不泄漏 domain mutable state
- context cancellation 和 adapter error 能正确传播

### Infrastructure integration tests

使用临时目录和临时 SQLite：

- migration 能从空数据库建立 schema
- repository 能正确 round-trip aggregates
- 首次扫描分配 UUID
- 重复扫描保持 UUID
- 内容修改后 UUID 不变且 FTS 更新
- 唯一 inode/file-key rename 保持 UUID
- 唯一 exact-hash rename 保持 UUID
- hash 匹配不唯一时不自动合并
- missing 文档保留身份
- Path remove 不删除文件和 Document
- SQLite transaction rollback 不留下 metadata/FTS 半更新状态

### Cobra tests

通过 `NewRootCommand(deps)` 注入 buffer 和 fake dependencies：

- `mm path add` 缺参数只显示 add usage
- `mm path wat` 只显示 path usage
- `mm path` 只显示 path usage
- runtime error 不显示 usage
- `--help` 显示当前层级完整帮助
- stdout/stderr 严格分离
- `cat` stdout 不混入提示文本
- JSON 输出不混入 progress

### Bubble Tea tests

- `Update` 状态转换
- 异步搜索只接受最新 sequence
- scan success/error message
- editor 返回后触发 reindex
- 不同终端宽度布局

### Architecture conformance tests

通过 import 检查或 `depguard` 固化 DDD dependency rule：

- `internal/domain/...` 不得 import application、infrastructure、interfaces、Cobra、Bubble Tea 或 SQLite
- `internal/application/...` 不得 import infrastructure、interfaces、Cobra 或 Bubble Tea
- `internal/interfaces/...` 不得直接 import SQLite repository
- concrete adapter 只能在 `bootstrap`/composition root 中组装
- public `membox` facade 不暴露 internal domain pointer 或 persistence row

## 14. 实现顺序

1. 初始化 Go module、`cmd/mm` 和 DDD package boundaries
2. 定义 ubiquitous language、domain value objects 和 errors
3. 实现 Document 与 IndexedPath aggregates 及 domain unit tests
4. 实现 RenameReconciler domain service 及纯单元测试
5. 定义 application commands、queries 和 ports
6. 实现 AddPath/ListPaths/ScanPaths use cases，使用 fake ports 测试
7. 实现 SQLite repositories、transaction adapter 和 migrations
8. 实现 filesystem scanner、fingerprint 和 content reader adapters
9. 接通 `path add/list/scan/remove`
10. 实现 SearchIndex adapter 和 document query handlers
11. 接通 `doc search/show/cat`
12. 实现 `doc edit/open` platform adapters
13. 实现 progressive-disclosure Cobra error renderer
14. 接通 `index status`
15. 实现 Bubble Tea Search 页面
16. 实现 TUI Paths/Status 页面
17. 完成 JSON 输出、架构检查与端到端验收

## 15. MVP 验收标准

```bash
mm path add ~/notes
mm path list
mm path scan
mm doc list
mm doc search "keyword"
mm doc show <id>
mm doc cat <id>
mm doc edit <id>
mm doc open <id>
mm index status
mm
```

必须满足：

1. 任意已注册 Path 下的 Markdown 能获得 UUID
2. 重复扫描和内容修改不改变 UUID
3. CLI 可以通过 UUID 读取和编辑真实文件
4. TUI 可以搜索、预览、编辑和打开文档
5. 参数错误只打印最近 subcommand usage
6. 运行时错误不打印无关帮助
7. CLI 和 TUI 共享 application facade，不复制业务逻辑
8. Domain 不依赖 SQLite、filesystem、Cobra 或 Bubble Tea
9. Application use case 通过 ports 访问外部能力，并控制事务边界
10. Infrastructure 不绕过 aggregate invariants
11. 不创建 Markdown 内容副本或 blob
