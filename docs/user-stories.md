# membox MVP User Stories 与 TDD 验收规范

> 状态：Draft  
> 依据：[`docs/development.md`](development.md)  
> 用途：把产品行为转换为可追踪、可先写失败测试的 User Stories、Acceptance Criteria 和 Given/When/Then 场景。

## 1. 文档目标

本文档描述 membox MVP 的用户可见行为和领域规则。开发时应遵循：

```text
User Story
    -> Acceptance Scenario
    -> Failing Test
    -> Minimal Implementation
    -> Refactor
```

本文不替代架构文档：

- `development.md` 定义 DDD 边界、依赖方向和实现结构
- 本文定义系统应表现出的行为
- 每个测试应能追溯到一个 Story ID 和 Scenario ID

关键字含义：

- **必须**：MVP 验收条件
- **应该**：默认实现目标，可以在明确记录原因后推迟
- **不得**：架构或产品约束

## 2. Personas

### Knowledge Worker

拥有散落在本地目录中的 Markdown，希望稳定引用、搜索和编辑这些文档，不希望 membox 改写原文件。

### CLI User

希望通过一致的名词 + 动词命令完成精确操作，并只看到与当前 subcommand 有关的错误帮助。

### TUI User

希望在终端里搜索、预览、编辑和打开文档，不需要记住完整 UUID 或文件路径。

### Automation Client

希望获得稳定 Document ID、确定的退出码、干净的 stdout/stderr 和结构化 JSON。

### Maintainer

希望通过 DDD 和 TDD 保证身份规则不会被 SQLite、CLI 或 TUI 实现细节破坏。

## 3. TDD 测试层级

每个场景应放在最低成本且能够证明行为的测试层。

| 标记 | 层级 | 外部依赖 | 目的 |
| --- | --- | --- | --- |
| `D` | Domain unit | 无 | Aggregate、Value Object、Domain Service 规则 |
| `A` | Application unit | Fake ports | Use case orchestration 和事务边界 |
| `I` | Infrastructure integration | 临时 SQLite/目录 | Repository、FTS、filesystem adapter |
| `C` | CLI acceptance | 临时 home + command | Cobra 输出、错误、退出码 |
| `T` | TUI model | Fake facade/messages | Bubble Tea 状态转换 |
| `E` | End-to-end | 编译后的 `mm` | 最小关键用户路径 |

测试约定：

- Domain/Application 测试不得使用真实 home directory
- 时间使用 fake clock，不以 `time.Sleep` 判断业务结果
- UUID 使用 fake ID generator，保证断言稳定
- filesystem 测试使用 `t.TempDir()`
- CLI 测试分别捕获 stdout 和 stderr
- TUI 测试优先测试 `Init/Update/View`，不依赖真实终端
- 除平台专属 adapter 测试外，测试不得启动真实 Vim 或桌面应用

建议测试命名：

```text
Test<Subject>_<ScenarioID>_<ExpectedBehavior>
```

例如：

```go
func TestDocument_ID001_ScanContentChangePreservesID(t *testing.T)
func TestAddPathHandler_PATH001_DuplicateCanonicalPathIsIdempotent(t *testing.T)
func TestCLI_CLI002_MissingAddArgumentPrintsOnlyLocalUsage(t *testing.T)
```

## 4. Epic：管理 Indexed Paths

### US-PATH-001：添加扫描路径

**作为** Knowledge Worker，  
**我希望** 使用 `mm path add <directory>` 注册一个目录，  
**从而** 让目录中的 Markdown 获得稳定身份并可被搜索。

优先级：P0

#### PATH-001-A：添加有效目录 `[A][I][C][E]`

```gherkin
Given 一个可读目录，其中包含 a.md、nested/b.markdown 和 c.txt
And membox 中尚未注册该目录
When 用户执行 mm path add <directory>
Then 系统保存该目录的 canonical absolute path
And 系统为 a.md 和 nested/b.markdown 建立 Document
And 系统忽略 c.txt
And 系统执行首次索引
And 命令退出码为 0
And stdout 显示 Path ID 和扫描摘要
```

#### PATH-001-B：规范化输入路径 `[A][I][C]`

```gherkin
Given 同一个目录可以通过相对路径、包含 .. 的路径或 symlink 访问
When 用户添加其中任意一种表示
Then 系统保存解析后的 canonical path
And 后续表示不会创建重复 IndexedPath
```

#### PATH-001-C：重复添加是幂等的 `[A][I][C]`

```gherkin
Given canonical path 已经注册为 Path 7
When 用户再次执行 mm path add 指向同一 canonical path
Then 不创建新的 IndexedPath
And 不改变已有 Document ID
And stdout 提示 Path 7 已存在
And 命令退出码为 0
```

#### PATH-001-D：拒绝无效路径 `[A][C]`

场景表：

| 输入 | 预期结果 |
| --- | --- |
| 不存在 | runtime error，不写数据库 |
| 普通文件 | runtime error，说明需要目录 |
| 不可读目录 | runtime error，不写数据库 |
| 空参数 | usage error，只显示 `mm path add` usage |

#### PATH-001-E：首次扫描部分失败 `[A][I][C]`

```gherkin
Given 一个目录包含可读和不可读 Markdown
When 用户添加该目录
Then IndexedPath 保持已注册
And 可读文档成功建立身份和索引
And Path status 为 partial
And last_error/scan report 描述失败数量
And 命令以 runtime error 退出
```

#### PATH-001-F：不得修改源文件 `[I][E]`

```gherkin
Given 扫描前记录所有源 Markdown 的内容、权限和 mtime
When 用户执行 mm path add
Then membox 不改写、不移动、不重命名任何源 Markdown
And ~/.membox 下不产生 Markdown 内容副本或 blob
```

---

### US-PATH-002：查看已注册路径

**作为** CLI User，  
**我希望** 使用 `mm path list` 查看所有已注册路径，  
**从而** 知道 membox 当前覆盖哪些目录及其索引状态。

优先级：P0

#### PATH-002-A：列出路径摘要 `[A][C]`

```gherkin
Given 系统中有两个 IndexedPath
When 用户执行 mm path list
Then stdout 为每个 Path 显示 ID、canonical path、Document 数、status 和 last scan
And 命令退出码为 0
```

#### PATH-002-B：空列表 `[A][C]`

```gherkin
Given 系统中没有 IndexedPath
When 用户执行 mm path list
Then 命令退出码为 0
And stdout 明确表示当前没有 configured paths
```

#### PATH-002-C：JSON 列表 `[A][C]`

```gherkin
Given 系统中存在 IndexedPath
When 用户执行 mm path list --json
Then stdout 是合法 JSON
And 时间使用 RFC3339 表示
And stdout 不包含进度或提示文本
```

---

### US-PATH-003：移除扫描路径

**作为** Knowledge Worker，  
**我希望** 停止跟踪一个目录，  
**从而** 在不删除文件和 Document identity 的情况下缩小 membox 范围。

优先级：P1

#### PATH-003-A：移除已注册路径 `[D][A][I][C]`

```gherkin
Given Path 3 下有多个 active Documents
When 用户执行 mm path remove 3
Then Path 不再参与后续扫描
And 相关 Locations 变为 untracked
And Documents 和 Document IDs 保留
And 实际 Markdown 文件保持不变
And untracked Documents 不出现在默认搜索结果中
```

#### PATH-003-B：重新添加恢复身份 `[A][I][E]`

```gherkin
Given 一个 Path 被移除且其 Documents 为 untracked
When 用户重新添加同一 canonical path
Then 系统重新观察现存文件
And 能确定为原 Location 的文件恢复原 Document ID
And 不创建重复 Document
```

#### PATH-003-C：不存在的 selector `[A][C]`

```gherkin
Given 不存在 Path selector 99
When 用户执行 mm path remove 99
Then 命令返回 runtime error
And 不打印 command usage
And 不修改任何状态
```

---

### US-PATH-004：显式扫描路径

**作为** Knowledge Worker，  
**我希望** 使用 `mm path scan` 刷新文件观察和搜索索引，  
**从而** 在没有 watcher/daemon 的情况下获得可预测更新。

优先级：P0

#### PATH-004-A：扫描全部路径 `[A][I][C][E]`

```gherkin
Given 系统有多个已注册 Path
When 用户执行 mm path scan
Then 系统扫描所有 active Paths
And 发现 added、updated、renamed、missing 和 unchanged 文件
And stdout 显示汇总数量
```

#### PATH-004-B：扫描指定路径 `[A][I][C]`

```gherkin
Given 系统有 Path 1 和 Path 2
When 用户执行 mm path scan 1
Then 只扫描 Path 1
And 不改变 Path 2 的 last_scan_at 和 Document 状态
```

#### PATH-004-C：取消扫描 `[A][I]`

```gherkin
Given 扫描正在进行
When context 被取消
Then scanner 尽快停止
And 已开始的数据库事务回滚
And 不留下 metadata 与 FTS 半更新状态
And 返回 cancellation error
```

## 5. Epic：Document Identity

### US-ID-001：首次发现时获得身份

**作为** Knowledge Worker，  
**我希望** 每个首次进入 membox 的 Markdown 获得 UUID，  
**从而** 可以不依赖路径长期引用它。

优先级：P0

#### ID-001-A：生成新 Document ID `[D][A][I]`

```gherkin
Given 一个从未被观察过的 Markdown Location
When ScanPaths 处理该 Observation
Then 创建一个新的 Document aggregate
And 通过 IDGenerator 分配 UUID
And UUID 不由 path、filename 或 hash 计算得出
```

#### ID-001-B：相同内容可以有不同身份 `[D][A][I]`

```gherkin
Given two.md 和 three.md 同时存在且字节完全相同
When 首次扫描它们
Then 两个 Location 获得不同 Document IDs
And SHA-256 可以相同
```

---

### US-ID-002：内容变化保持身份

**作为** Knowledge Worker，  
**我希望** 编辑 Markdown 后 Document ID 不变，  
**从而** 已保存的引用继续有效。

优先级：P0

#### ID-002-A：外部编辑后重新扫描 `[D][A][I][E]`

```gherkin
Given a.md 已绑定 Document 019-A
When 外部工具修改 a.md 内容
And 用户执行 mm path scan
Then a.md 仍绑定 Document 019-A
And size、mtime、sha256 和 FTS body 更新
```

#### ID-002-B：重复扫描不改变身份 `[D][A][I]`

```gherkin
Given 文件内容和 Location 均未变化
When 连续执行多次扫描
Then Document ID 和 created_at 不变
And 扫描结果将该文件计为 unchanged
```

---

### US-ID-003：文件缺失时保留身份

**作为** Knowledge Worker，  
**我希望** 暂时缺失的文件仍保留 Document ID，  
**从而** 文件恢复或 rename reconciliation 后可以继续使用原引用。

优先级：P0

#### ID-003-A：标记 missing `[D][A][I]`

```gherkin
Given a.md 绑定 Document 019-A
When a.md 在下一次完整 Path scan 中未出现
Then Document 019-A 状态变为 missing
And Document row 不被删除
And 已知 Location 和最后 IndexState 保留用于诊断和 rename matching
```

#### ID-003-B：原位置恢复 `[D][A][I]`

```gherkin
Given Document 019-A 因 a.md 消失而 missing
When a.md 重新出现在原 Location
And 用户执行扫描
Then Document 019-A 恢复 active
And 不创建新的 Document ID
```

#### ID-003-C：missing 文档操作 `[A][C][T]`

```gherkin
Given Document 019-A 为 missing
When 用户执行 doc cat、edit 或 open
Then 命令返回包含最后已知路径的 runtime error
And 不打印 usage
And 不尝试操作其他同名文件
```

---

### US-ID-004：使用 ID selector

**作为** CLI/TUI User，  
**我希望** 使用完整 UUID 或唯一 UUID prefix 定位文档，  
**从而** 在保持稳定性的同时减少输入长度。

优先级：P0

#### ID-004-A：完整 UUID `[A][C]`

```gherkin
Given 一个 active Document
When 用户使用完整 UUID 查询它
Then 系统返回该 Document
```

#### ID-004-B：唯一 prefix `[A][C]`

```gherkin
Given prefix 019abc 只匹配一个 Document
When 用户使用 019abc
Then 系统解析到该 Document
```

#### ID-004-C：歧义 prefix `[A][C]`

```gherkin
Given prefix 019 同时匹配多个 Documents
When 用户使用 019
Then 系统返回 ambiguous selector runtime error
And 要求输入更长 prefix
And 不任选其中一个 Document
```

## 6. Epic：Rename Reconciliation

### US-REN-001：通过 file key 识别 rename

**作为** Knowledge Worker，  
**我希望** 普通 filesystem rename 后 UUID 保持不变，  
**从而** 通过旧 Document ID 仍能打开文件。

优先级：P0

#### REN-001-A：唯一 file-key relocation `[D][A][I][E]`

```gherkin
Given a.md 绑定 Document 019-A 和 file key K
And 下一次扫描中 a.md missing
And b.md 是唯一具有 file key K 的 new Observation
When RenameReconciler 运行
Then 返回 ConfirmedRelocation(a.md -> b.md)
And Document 019-A Location 更新为 b.md
And Document ID 不变
```

---

### US-REN-002：通过 exact hash 识别纯 rename

优先级：P0

#### REN-002-A：唯一一对一 exact hash `[D][A][I]`

```gherkin
Given 一个 old Location missing
And 同一 IndexedPath 有一个 new Location
And 二者 SHA-256 相同
And 没有其他同 hash 候选
When RenameReconciler 运行
Then 将其确认为 Relocation
And 保持原 Document ID
```

#### REN-002-B：不同 IndexedPath 不用 hash 自动重绑定 `[D][A]`

```gherkin
Given Path A 的文件 missing
And Path B 出现相同 hash 文件
When RenameReconciler 运行
Then 不自动将 Path B 文件绑定到 Path A 的 Document
```

---

### US-REN-003：歧义时保护身份

**作为** Maintainer，  
**我希望** rename matching 在不确定时拒绝自动合并，  
**从而** 避免两个真实文档错误共享一个身份。

优先级：P0

#### REN-003-A：一个 missing 对多个相同 hash `[D][A]`

```gherkin
Given old/a.md missing 且 hash 为 H
And new/b.md 与 new/c.md 的 hash 都为 H
When RenameReconciler 运行
Then 不产生 ConfirmedRelocation
And 返回 RenameCandidate 或 unmatched 结果
And 不把 019-A 任意分配给 b.md 或 c.md
```

#### REN-003-B：多个 missing 对一个相同 hash `[D][A]`

```gherkin
Given 两个 missing Documents 的 hash 都为 H
And 只有一个 new file 的 hash 为 H
When RenameReconciler 运行
Then 不产生 ConfirmedRelocation
And 不任意选择旧 Document ID
```

#### REN-003-C：原文件仍在时相同内容是 copy `[D][A][I]`

```gherkin
Given a.md 仍然 active
And 新出现 b.md 与 a.md 内容相同
When 扫描运行
Then b.md 获得新的 Document ID
And a.md 的 Document ID 不变
```

#### REN-003-D：相似度只产生候选 `[D][A][C]`

```gherkin
Given old/a.md missing
And new/b.md 与其内容高度相似但 hash 不同
And file key 也不同
When rename analysis 运行
Then 不自动继承 old/a.md 的 Document ID
And scan report 可以显示 possible rename
```

## 7. Epic：搜索与索引

### US-SEARCH-001：搜索文档

**作为** Knowledge Worker，  
**我希望** 搜索 title、path 和 Markdown body，  
**从而** 找到文档并继续通过 UUID 操作。

优先级：P0

#### SEARCH-001-A：匹配 Markdown body `[A][I][C][T][E]`

```gherkin
Given active 文档 body 包含 "online softmax maintains"
And FTS 已更新
When 用户执行 mm doc search "online softmax"
Then 结果包含该 Document ID、当前 path 和 snippet
```

#### SEARCH-001-B：匹配 title/path `[A][I][C]`

```gherkin
Given query 只出现在 title 或 relative path
When 用户搜索该 query
Then 对应 Document 出现在结果中
```

#### SEARCH-001-C：排除 unavailable 文档 `[A][I][C]`

```gherkin
Given 一个 Document missing，另一个 untracked
When 用户执行默认搜索
Then 两个 Document 都不出现在结果中
```

#### SEARCH-001-D：无结果 `[A][C][T]`

```gherkin
Given 没有匹配文档
When 用户搜索 query
Then 返回空结果
And CLI 退出码为 0
And TUI 显示 no results
```

#### SEARCH-001-E：限制结果数 `[A][I][C]`

```gherkin
Given 匹配文档超过 limit
When 用户传入 --limit 20
Then 最多返回 20 条结果
```

---

### US-SEARCH-002：索引刷新语义

**作为** CLI User，  
**我希望** 搜索基于明确的最近索引状态，  
**从而** 命令不会因隐式全盘扫描而变慢或产生意外副作用。

优先级：P0

#### SEARCH-002-A：search 不隐式扫描 `[A][C]`

```gherkin
Given 文件被外部修改但尚未 scan
When 用户执行 mm doc search
Then SearchDocuments 不调用 MarkdownScanner
And 结果仍反映最近已提交的 FTS projection
```

#### SEARCH-002-B：scan 后结果更新 `[A][I][E]`

```gherkin
Given 外部文件增加新关键词
When 用户执行 mm path scan
And 再次搜索新关键词
Then 结果包含该 Document
And Document ID 保持不变
```

## 8. Epic：读取、编辑和打开

### US-DOC-001：查看文档身份与状态

优先级：P0

#### DOC-001-A：show active Document `[A][C][T]`

```gherkin
Given 一个 active Document
When 用户执行 mm doc show <id>
Then 显示完整 Document ID、Location、status、title、mtime、size、sha256 和 indexed_at
```

#### DOC-001-B：show missing Document `[A][C]`

```gherkin
Given 一个 missing Document
When 用户执行 mm doc show <id>
Then 仍显示身份、missing status 和最后已知 Location
```

---

### US-DOC-002：读取真实 Markdown

**作为** CLI User，  
**我希望** `mm doc cat` 输出真实文件内容，  
**从而** 可以安全地用于 shell pipeline。

优先级：P0

#### DOC-002-A：纯净 stdout `[A][I][C][E]`

```gherkin
Given active Document 指向一个 Markdown 文件
When 用户执行 mm doc cat <id>
Then stdout 与文件字节完全一致
And stdout 不包含标题、ID、进度或提示
And stderr 为空
```

#### DOC-002-B：读取失败 `[A][I][C]`

```gherkin
Given Location 在数据库中 active 但读取时文件不可访问
When 用户执行 mm doc cat <id>
Then stdout 不输出部分诊断文本
And stderr 输出 runtime error
And 命令退出码为 1
```

---

### US-DOC-003：通过 Document ID 编辑

**作为** Knowledge Worker，  
**我希望** 使用 `mm doc edit <id>` 打开真实文件，  
**从而** 不必记住当前位置。

优先级：P0

#### DOC-003-A：编辑成功后局部 reindex `[A][C][T]`

```gherkin
Given Document 019-A 为 active
And editor adapter 会成功修改其文件
When 用户执行 mm doc edit 019-A
Then 系统在启动 editor 前释放数据库事务
And editor 收到真实 absolute path
And editor 成功退出后只 reindex Document 019-A
And Document ID 不变
```

#### DOC-003-B：editor 选择顺序 `[C]`

```gherkin
Given VISUAL 已设置
When 用户执行 doc edit
Then 使用 VISUAL

Given VISUAL 未设置且 EDITOR 已设置
Then 使用 EDITOR

Given 两者都未设置
Then fallback 为 vi
```

#### DOC-003-C：editor 非零退出 `[A][C][T]`

```gherkin
Given editor 返回非零退出码
When edit use flow 完成
Then 命令返回 runtime error
And 不将该次操作报告为成功
And 不自动执行成功路径的 ReindexDocument
```

---

### US-DOC-004：用系统默认应用打开

优先级：P1

#### DOC-004-A：传递真实路径 `[C][T]`

```gherkin
Given active Document
When 用户执行 mm doc open <id>
Then platform opener 收到该 Document 的真实 absolute path
And core 不修改文件或 Document metadata
```

平台命令通过 fake opener 测试；macOS/Linux/Windows 各自 adapter 只在对应平台运行 integration test。

## 9. Epic：状态与可观察性

### US-STATUS-001：查看索引状态

**作为** Knowledge Worker，  
**我希望** 查看当前 catalog/index 状态，  
**从而** 知道是否需要重新扫描或处理 missing 文件。

优先级：P1

#### STATUS-001-A：human status `[A][C][T]`

```gherkin
Given catalog 中存在 active、missing 和 untracked Documents
When 用户执行 mm index status
Then 显示 Path 数和各 Document status 数量
And 显示最近扫描时间与 FTS 状态
```

#### STATUS-001-B：JSON status `[A][C]`

```gherkin
When 用户执行 mm index status --json
Then stdout 为合法 JSON
And 数量是 number
And 时间是 RFC3339 或 null
```

## 10. Epic：CLI 命令体验

### US-CLI-001：名词 + 动词命令树

**作为** CLI User，  
**我希望** 命令按资源名词和动作组织，  
**从而** 可以像使用 `gh` 一样发现功能。

优先级：P0

#### CLI-001-A：支持规定 command tree `[C]`

必须支持：

```text
mm path add/list/remove/scan
mm doc list/search/show/cat/edit/open
mm index status
```

#### CLI-001-B：不提供扁平影子命令 `[C]`

```gherkin
When 用户执行 mm search 或 mm cat
Then 返回 root-level unknown command usage error
And 不静默映射到 mm doc 子命令
```

---

### US-CLI-002：Progressive disclosure errors

**作为** CLI User，  
**我希望** 参数错误只显示最近 subcommand 的 usage，  
**从而** 不被完整 root help 淹没。

优先级：P0

#### CLI-002-A：leaf argument error `[C][E]`

```gherkin
When 用户执行 mm path add
Then stderr 包含 Error 和 mm path add usage
And stderr 不列出 doc、index、ui groups
And 退出码为 2
```

#### CLI-002-B：unknown nested command `[C]`

```gherkin
When 用户执行 mm path wat
Then stderr 显示 mm path usage
And 只列出 path 的 add/list/remove/scan
And 不显示完整 root help
And 退出码为 2
```

#### CLI-002-C：missing noun verb `[C]`

```gherkin
When 用户执行 mm path
Then stderr 显示 mm path usage
And 说明需要 path command
And 退出码为 2
```

#### CLI-002-D：runtime error 不打印 usage `[C][E]`

```gherkin
Given Document selector 不存在
When 用户执行 mm doc show <selector>
Then stderr 显示 runtime error
And stderr 不包含 "Usage:"
And 退出码为 1
```

#### CLI-002-E：显式 help `[C]`

```gherkin
When 用户执行 mm path add --help
Then stdout 显示完整 leaf help
And 退出码为 0
```

---

### US-CLI-003：stdout、stderr 与 JSON 稳定性

**作为** Automation Client，  
**我希望** 数据和诊断严格分离，  
**从而** 可以可靠解析命令结果。

优先级：P0

#### CLI-003-A：成功数据写 stdout `[C]`

#### CLI-003-B：错误和进度写 stderr `[C]`

#### CLI-003-C：JSON 不混入 human 文本 `[C][E]`

```gherkin
When 用户执行支持 --json 的查询命令
Then stdout 可以直接被标准 JSON parser 解析
And progress/warning 只出现在 stderr
```

#### CLI-003-D：退出码 `[C]`

| 情况 | 退出码 |
| --- | --- |
| 成功 | 0 |
| 搜索无结果 | 0 |
| runtime/business error | 1 |
| command/flag/argument error | 2 |
| 用户中断 | 130 |

## 11. Epic：TUI

### US-TUI-001：启动 TUI

**作为** TUI User，  
**我希望** 在交互终端中快速进入 TUI，  
**从而** 浏览和搜索文档。

优先级：P0

#### TUI-001-A：TTY 无参数启动 `[C][T]`

```gherkin
Given stdin/stdout 是 TTY
When 用户执行 mm
Then 启动 Bubble Tea program
```

#### TUI-001-B：不支持 ui start 影子命令 `[C]`

```gherkin
When 用户执行 mm ui start
Then 返回 root-level unknown command usage error
```

#### TUI-001-C：非 TTY 无参数 `[C]`

```gherkin
Given stdin 或 stdout 不是 TTY
When 用户执行 mm
Then 不启动 TUI
And 显示 root usage
And 以 usage error 退出
```

---

### US-TUI-002：搜索、过滤和预览

优先级：P0

#### TUI-002-A：显示搜索结果 `[T]`

```gherkin
Given facade 返回 SearchResults
When search completion message 到达
Then model 显示结果列表
And 当前选中项显示 Markdown 预览
```

#### TUI-002-B：忽略过期异步结果 `[T]`

```gherkin
Given query A 的 sequence 小于当前 query B
When query A 的结果晚于 query B 到达
Then model 忽略 query A 结果
And 当前界面仍对应 query B
```

#### TUI-002-C：Update 不执行阻塞 I/O `[T][architecture]`

```gherkin
When 用户触发 search 或 scan
Then Update 返回 tea.Cmd
And facade 调用发生在 tea.Cmd 中而不是 Update 调用栈中
```

#### TUI-002-D：响应式布局 `[T]`

```gherkin
Given 终端宽度低于单栏阈值
Then 列表和预览采用页面切换

Given 终端宽度达到双栏阈值
Then 同时显示列表和预览
```

#### TUI-002-E：双击 space 打开 rg/fzf 式过滤框 `[T]`

```gherkin
Given 主输入框已获得焦点
When 用户快速按两次 space
Then 加载 active Document 全量列表
And 底部输入框进入 filter mode
And 输入内容按空格分词进行包含过滤
```

#### TUI-002-F：过滤选择恢复搜索框 `[T]`

```gherkin
Given filter mode 中有匹配结果
When 用户按 Enter
Then filter mode 退出
And 当前选中 Document 保持预览
And 底部输入框恢复为空 search input
```

#### TUI-002-G：过滤取消 `[T]`

```gherkin
Given filter mode 已获得焦点
When 用户按 Esc
Then filter mode 退出
And 恢复之前的 Document 列表
```

#### TUI-002-H：输入框固定在底部且有清晰边框 `[T]`

```gherkin
Given TUI 正在显示任意页面
Then 内容区域位于上方
And 主输入框固定在最底部
And 未聚焦时使用 muted border
And 聚焦时使用 accent border
```

---

### US-TUI-003：从 TUI 编辑和打开

优先级：P1

#### TUI-003-A：暂停并恢复终端 `[T]`

```gherkin
Given 用户选中 active Document
When 用户按 e
Then TUI 通过 ExecProcess 暂停
And editor 退出后恢复 TUI
And 成功退出触发该 Document reindex
```

#### TUI-003-B：打开文档 `[T]`

```gherkin
When 用户按 o
Then opener 收到选中 Document 的真实 Location
And TUI 保持可继续操作
```

---

### US-TUI-004：管理 Paths 与查看状态

优先级：P1

#### TUI-004-A：Paths 页面 `[T]`

- 显示与 `ListPaths` query 相同的数据
- 可以添加 Path
- 可以扫描单个或全部 Path
- 显示 scan spinner、summary 和 error

#### TUI-004-B：Status 页面 `[T]`

- 显示与 `GetIndexStatus` query 相同的数据
- 不自行读取 SQLite

## 12. Epic：一致性与恢复

### US-INTEGRITY-001：身份和 FTS 原子更新

**作为** Maintainer，  
**我希望** metadata 和搜索索引同成同败，  
**从而** 搜索结果不会指向未提交或错误身份。

优先级：P0

#### INTEGRITY-001-A：事务成功 `[A][I]`

```gherkin
Given 一个 changed Observation
When aggregate 和 FTS 都成功保存
Then transaction commit
And Document view 与搜索结果反映同一状态
```

#### INTEGRITY-001-B：FTS 失败回滚 `[A][I]`

```gherkin
Given aggregate save 成功但 FTS Replace 返回错误
When ScanPaths 执行
Then transaction rollback
And Document metadata 保持扫描前状态
```

#### INTEGRITY-001-C：metadata 失败不更新 FTS `[A][I]`

```gherkin
Given DocumentRepository Save 失败
When ScanPaths 执行
Then FTS 不提交新内容
And 返回 runtime error
```

---

### US-INTEGRITY-002：SQLite 是身份权威

优先级：P0

#### INTEGRITY-002-A：missing 不删除 identity `[D][I]`

已由 `ID-003-A` 覆盖，Infrastructure 必须额外验证 row 仍存在。

#### INTEGRITY-002-B：非法持久化状态 `[I]`

```gherkin
Given SQLite 中存在违反 aggregate invariant 的数据
When repository 重建 aggregate
Then 返回 integrity error
And 不静默生成新 UUID 或修正 Location
```

#### INTEGRITY-002-C：无内容存储副作用 `[I][E]`

```gherkin
When 执行 add、scan、search、show 和 status
Then membox home 只包含数据库及必要 lock/temp 文件
And 不包含源 Markdown 副本、blob 或 version storage
```

## 13. Epic：DDD 架构约束

### US-ARCH-001：Domain 保持纯净

**作为** Maintainer，  
**我希望** Domain 不依赖技术框架，  
**从而** 核心身份规则可以快速、确定地进行单元测试。

优先级：P0

#### ARCH-001-A：Domain import rule `[architecture]`

`internal/domain/...` 不得 import：

- `internal/application`
- `internal/infrastructure`
- `internal/interfaces`
- Cobra
- Bubble Tea
- SQLite driver

#### ARCH-001-B：Domain tests 无 I/O `[D]`

Document、IndexedPath 和 RenameReconciler 的所有规则都可以只通过内存对象测试。

---

### US-ARCH-002：Application 依赖 Ports

优先级：P0

#### ARCH-002-A：Application 不依赖 concrete adapters `[architecture]`

`internal/application/...` 不得 import SQLite、filesystem adapter、Cobra 或 Bubble Tea。

#### ARCH-002-B：Use case 可使用 fake ports `[A]`

所有 command/query handler 必须能在不打开数据库和真实目录的情况下测试。

---

### US-ARCH-003：Interface adapters 共享 Facade

优先级：P0

#### ARCH-003-A：CLI/TUI 不复制业务逻辑 `[architecture]`

- CLI/TUI 不直接执行 SQL
- CLI/TUI 不自行决定 rename identity
- CLI/TUI 不生成 Document ID
- CLI/TUI 使用同一 public `membox` facade

## 14. Story 与测试层追踪矩阵

| Story | Domain | Application | Infrastructure | CLI | TUI | E2E |
| --- | --- | --- | --- | --- | --- | --- |
| PATH-001 Add |  | ✓ | ✓ | ✓ |  | ✓ |
| PATH-002 List |  | ✓ |  | ✓ |  |  |
| PATH-003 Remove | ✓ | ✓ | ✓ | ✓ |  | ✓ |
| PATH-004 Scan |  | ✓ | ✓ | ✓ |  | ✓ |
| ID-001 Create | ✓ | ✓ | ✓ |  |  |  |
| ID-002 Preserve | ✓ | ✓ | ✓ |  |  | ✓ |
| ID-003 Missing | ✓ | ✓ | ✓ | ✓ | ✓ |  |
| ID-004 Selector |  | ✓ |  | ✓ |  |  |
| REN-001 File key | ✓ | ✓ | ✓ |  |  | ✓ |
| REN-002 Hash | ✓ | ✓ | ✓ |  |  |  |
| REN-003 Ambiguous | ✓ | ✓ | ✓ | ✓ |  |  |
| SEARCH-001 Search |  | ✓ | ✓ | ✓ | ✓ | ✓ |
| SEARCH-002 Refresh |  | ✓ | ✓ | ✓ |  | ✓ |
| DOC-001 Show |  | ✓ |  | ✓ | ✓ |  |
| DOC-002 Cat |  | ✓ | ✓ | ✓ |  | ✓ |
| DOC-003 Edit |  | ✓ |  | ✓ | ✓ |  |
| DOC-004 Open |  |  | platform | ✓ | ✓ |  |
| STATUS-001 |  | ✓ | ✓ | ✓ | ✓ |  |
| CLI-001/002/003 |  |  |  | ✓ |  | selected |
| TUI-001/002/003/004 |  |  |  | selected | ✓ |  |
| INTEGRITY-001/002 | ✓ | ✓ | ✓ |  |  | selected |
| ARCH-001/002/003 | ✓ | ✓ |  | ✓ | ✓ |  |

## 15. 推荐 TDD 实现批次

### Batch 1：Domain identity

先写：

- ID-001
- ID-002
- ID-003
- REN-001/002/003
- ARCH-001

完成 Document、Value Objects 和 RenameReconciler，不接 SQLite。

### Batch 2：Application paths 与 scan

先写：

- PATH-001/002/003/004 application tests
- INTEGRITY-001 fake transaction tests
- ARCH-002

完成 Commands、Queries 和 Ports，不接 Cobra。

### Batch 3：SQLite 与 Filesystem

先写：

- repository round-trip
- scan fixtures
- FTS transaction rollback
- source non-modification

完成 Infrastructure adapters。

### Batch 4：CLI

先写：

- CLI-001 command tree
- CLI-002 progressive disclosure
- CLI-003 stdout/stderr/JSON
- PATH/DOC command acceptance tests

再实现 Cobra commands。

### Batch 5：TUI

先写 Bubble Tea model tests，再实现 View：

- sequence handling
- search/loading/error states
- editor return message
- responsive layout

### Batch 6：E2E

只保留少量高价值完整流程：

```text
path add -> search -> cat
external edit -> path scan -> same UUID -> updated search
filesystem rename -> path scan -> same UUID -> edit
usage error -> local usage only
```

## 16. Definition of Done

一个 Story 完成必须满足：

1. 所有 P0 acceptance scenarios 已转化为自动化测试
2. 测试先失败，再通过最小实现
3. Domain 规则优先由 Domain unit test 证明
4. Application orchestration 由 fake ports 证明
5. 技术 adapter 行为由 integration test 证明
6. CLI/TUI 不重复测试已经由低层证明的所有组合
7. 测试不依赖用户真实 home、真实 editor 或网络
8. 新行为有 Story ID 和 Scenario ID
9. `go test ./...` 通过
10. architecture dependency rules 通过
11. 文档与最终命令输出一致

## 17. 当前实现决策与剩余事项

首版实现已经采用以下可测试决策：

1. title 使用第一个 `# H1`；不存在时使用不含扩展名的 filename；不解析 frontmatter
2. directory symlink 不跟随，从而避免目录循环
3. hidden directories、`.git` 和 `node_modules` 当前不默认忽略
4. `.md`、`.markdown` 及其大小写变体均接受
5. 普通查询词被转义为 FTS5 prefix terms，不直接暴露原始 FTS5 query syntax
6. 搜索使用 BM25，title/path/body 权重依次降低
7. human `path list` 按本地 Path ID 升序
8. Document ID 使用 `google/uuid` 生成 UUIDv7；selector 接受完整 UUID 或唯一 prefix
9. possible rename 只在 scan report 中显示数量，MVP 暂无人工 reconcile command
10. `path add` 首次扫描部分失败时保留 Path 和成功结果，并返回 runtime error
11. editor 非零退出时不执行成功路径的自动 reindex

仍需后续 ADR 决定：

1. 父目录和子目录同时注册时，如何避免同一物理文件获得重复 Document identity
2. BM25 分数相同时的稳定二级排序规则
3. 大型 Markdown 的读取、hash 和 FTS body size 上限
4. possible rename 的详情查看和人工 reconcile UX
5. 是否增加默认 ignore 规则以及 `.gitignore` 兼容
