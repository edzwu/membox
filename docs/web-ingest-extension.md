# Web Ingest Extension — 设计方案

> 状态：Draft · **P0/M1 已落地骨架**（`/api/ingest`、`mm web start --fg`、`extensions/clipper`）  
> 相关：[`development.md`](development.md)、[`user-stories.md`](user-stories.md)、MarkSnip Agent Bridge（参考实现）  
> 目标：用 **WXT** 做浏览器扩展，把任意网页转为 Markdown，**ingest 进 membox（分配 UUID）**，并用现有 **Miru** 前端打开阅读/批注。

---

## 1. 问题与目标

### 1.1 要解决什么

知识工作里大量材料仍在浏览器：文档站、博客、Issue、论文页、内部 wiki。  
membox 已经具备：

- 稳定 **Document UUID**
- 本地 Markdown 为内容权威
- 本机 HTTP + **Miru** 阅读器（`/?id=<logical-id>`）
- `POST /api/save` / `POST /api/sync` 创建笔记并回写索引

缺的是一条 **从「正在看的网页」到「membox 文档」** 的低摩擦导入路径。

### 1.2 非目标（MVP）

- 不做通用网页归档器 / 离线整站爬虫
- 不做云同步、账号体系
- 不把「回到 terminal 敲 clip」当人类主路径（MarkSnip CLI 演示路径明确降级）
- 不在扩展里重做 Miru 阅读体验（阅读与批注仍在 membox 的 Miru）

### 1.3 成功标准

| 指标 | MVP 期望 |
| --- | --- |
| 主路径次数 | 用户在网页上 **一次手势** → 已入库且 Miru 已打开（或可一键打开） |
| 身份 | 每次成功 ingest 得到 **新的稳定 UUID**（或明确的去重策略，见 §5） |
| 内容 | Markdown 可读、带来源 front matter（title / url / clipped_at） |
| 本机边界 | 仅 `127.0.0.1`；带会话 token；无公网服务 |
| 技术债 | 扩展用 WXT；转换逻辑可测；不与 membox DDD 内核缠死 |

---

## 2. 核心产品叙事（先纠正工作流）

MarkSnip Agent Bridge 的合理内核是：

```text
机器（agent/脚本） ↔ localhost HTTP ↔ native host ↔ 扩展 ↔ 当前页
```

**不合理**的是把「人回 terminal 剪页」当主 story。

本方案的叙事拆成两条路径：

| 路径 | 谁操作 | 体验 |
| --- | --- | --- |
| **Human ingest（P0）** | 人 | 留在浏览器：剪页 → 写入 membox → 打开 Miru |
| **Agent bridge（P1）** | agent/脚本 | 本机 API 触发「剪当前页并入库」，人不必切 terminal |

人的心智模型：

```text
浏览任意网页
    →（扩展）一键 Save to membox
    → membox 分配 UUID，写入 notes 目录并索引
    → 新标签打开 Miru：http://127.0.0.1:<port>/?id=<logical-id>
    → 继续阅读 / 折叠 / 批注（现有能力）
```

---

## 3. 高层架构

### 3.1 组件

```text
┌─────────────────────────────────────────────────────────────┐
│  Browser (Chrome / Firefox)                                 │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  WXT Extension  (membox-clipper)                      │  │
│  │  - popup / 快捷键 / 右键菜单                            │  │
│  │  - content script / offscreen：DOM → Markdown         │  │
│  │  - background：鉴权、调用 localhost、打开 Miru 标签     │  │
│  └───────────────────────────┬───────────────────────────┘  │
│                              │ HTTP 127.0.0.1 + token        │
└──────────────────────────────┼──────────────────────────────┘
                               ▼
┌─────────────────────────────────────────────────────────────┐
│  membox (Go)  —— 已有或扩展的 localhost server                │
│  - POST /api/ingest   （新建：网页 clip 专用，见 §6）         │
│  - POST /api/sync     （已有：通用创建/更新）                  │
│  - GET  /api/doc/:id  （已有）                               │
│  - GET  /?id=:id      （已有 Miru）                          │
│  - GET  /api/bridge/status                                  │
│  - session/token 文件 或 mm web start --fg 打印的 endpoint             │
└─────────────────────────────────────────────────────────────┘
                               │
                               ▼
                    ~/.membox + 已注册 notes 路径
                    Document UUID + foo.md + FTS
```

### 3.2 与 MarkSnip 的异同

| | MarkSnip Bridge | 本方案 |
| --- | --- | --- |
| 转换 | 扩展内 Readability + Turndown | **同思路**（WXT 内实现或复用同类库） |
| 本机 HTTP | native host 随机端口 | **优先复用/扩展 membox web server** |
| Native Messaging | host ↔ 扩展双向 | MVP **可不做**；扩展主动连 membox 即可 |
| 输出 | stdout Markdown | **membox UUID + 文件 + Miru URL** |
| 人类主路径 | CLI clip（弱） | **扩展一键 ingest（强）** |
| Agent | CLI/HTTP clip | P1：`POST /api/bridge/clip-ingest` 同类能力 |

**关键决策：**  
MVP 不引入第二套 Go native-host 进程，除非「membox 未运行时也要剪页」。默认假设用户本机有 `mm` 守护或按需拉起（§7）。

### 3.3 为什么用 WXT

- 多浏览器一份代码（Chrome MV3 / Firefox）
- 类 Nuxt 的项目结构、类型友好、HMR
- background / content / popup / options 边界清晰
- 便于把「转换 pipeline」做成可单测模块，而不是糊在 background 里

建议仓库位置（二选一，方案阶段先定边界）：

```text
选项 A（推荐单体）：membox/extensions/clipper/   # WXT 项目，随 monorepo 发版
选项 B（独立仓）：  membox-clipper/               # 仅通过 HTTP 契约耦合
```

MVP 推荐 **A**，版本与 `/api/ingest` 契约一起演进。

---

## 4. 用户故事（Top）

### US-CLIP-001 — 一键入库并阅读（P0）

**作为** 正在浏览网页的 Knowledge Worker，  
**我希望** 点扩展图标或按快捷键，把当前页存进 membox 并用 Miru 打开，  
**从而** 不复制粘贴、不离开浏览器上下文，就能进入可批注的阅读态。

验收：

1. 成功时 toast/popup 显示标题 + 短 UUID  
2. 自动（或确认后）打开 `http://127.0.0.1:<port>/?id=<logical-id>`  
3. `mm doc show <uuid>` 可见；磁盘上有对应 `.md`  
4. Markdown 含来源 URL（front matter 或文首 meta）

### US-CLIP-002 — 连接状态可感知（P0）

**作为** 用户，  
**我希望** 扩展明确显示 membox 是否在线，  
**从而** 失败时知道是「没开 membox」而不是「剪页失败」。

验收：

- popup 显示 Connected / Offline  
- Offline 时提供：如何启动 `mm web start --fg`（或「打开 membox」按钮若 OS 集成允许）  
- 不出现无提示的静默失败

### US-CLIP-003 — 选区剪辑（P1）

**作为** 用户，  
**我希望** 只剪选中的段落，而不是整页，  
**从而** 避免导航/页脚噪音。

### US-CLIP-004 — Agent 静默 ingest（P1）

**作为** Automation / Agent，  
**我希望** 在本机调用 HTTP，把「用户当前前台标签页」入库并返回 UUID + Miru URL，  
**从而** 人不用回 terminal，agent 也能拿到稳定文档身份。

说明：此路径需要扩展在线 + membox 在线；由 background 接收 membox 的「请剪当前页」或由扩展轮询/长连接——P1 再定（§8）。

### US-CLIP-005 — 重复页策略可预期（P1）

同一 URL 再次剪辑时：默认 **总是新建 UUID**（归档快照），或可选「更新上次 clip」。  
MVP 先做 **总是新建**，产品最简单、身份最清晰。

---

## 5. 内容模型：网页 → Markdown → membox Document

### 5.1 转换 pipeline（扩展内）

顺序建议：

```text
1. 取得 DOM
   - 整页：document 或 Readability 主内容
   - 选区：用户 selection 的 HTML 片段
2. 可选清理：去掉 script/nav/footer、扩展自身 DOM
3. HTML → Markdown（Turndown + GFM 表/代码；或等价库）
4. 组装文档：
   - YAML front matter（推荐）或 Markdown 前言块
   - body
5. POST membox ingest API
```

Front matter 草案：

```yaml
---
title: "FlashAttention-3"
source_url: "https://example.com/blog/fa3"
clipped_at: "2026-03-28T12:00:00Z"
clipper: "membox-clipper"
clipper_version: "0.1.0"
---

# FlashAttention-3

…
```

`title`：`document.title` 清理后；失败则 hostname + path。  
`body`：API 字段可与 front matter 合并后一次写入，或 title/body 分离由服务端拼——**推荐扩展侧拼好完整 Markdown**，服务端只负责落盘 + UUID + 索引（与现有 `CreateNote` 一致）。

### 5.2 与 membox 身份模型对齐

沿用现有规则（见 `development.md`）：

```text
Markdown 文件     内容权威
documents.id      身份权威（UUID）
path              当前路径（notes 目录下）
FTS5              可重建
```

Ingest = **CreateNote 语义**：

- 生成 UUID  
- 在已配置的 notes/默认写入路径写 `.md`  
- 扫描/索引  
- 返回 `{ id, path, created: true }`

已有能力对照：

| API | 现状 | Ingest 用法 |
| --- | --- | --- |
| `POST /api/save` | `{title,body}` → CreateNote | 可用，但缺来源元数据约定 |
| `POST /api/sync` | 无 id 时创建；可带 annotations | 更适合「创建后立刻可注」 |
| `GET /?id=` | Miru 打开 | ingest 成功后跳转 |

建议新增 **`POST /api/ingest`**（语义更清晰，便于鉴权与限流），内部仍调 `CreateNote`：

```json
// request
{
  "title": "FlashAttention-3",
  "body": "---\ntitle: ...\n---\n\n# ...\n",
  "source_url": "https://...",
  "open": true
}

// response
{
  "id": "019...",
  "path": "/Users/…/notes/flashattention-3.md",
  "created": true,
  "view_url": "http://127.0.0.1:8787/?id=019..."
}
```

`view_url` 由 server 用已有 `ViewURL` 生成，扩展不必拼端口细节（仍需知道 base URL）。

### 5.3 去重（非 MVP 默认）

| 策略 | 行为 | 何时 |
| --- | --- | --- |
| `always_new`（默认） | 每次新 UUID | 快照归档 |
| `upsert_by_url` | 同 source_url 更新同一文档 | 跟踪演进中的文档 |
| `skip_if_same_hash` | 内容 hash 相同则返回旧 id | 防手抖双击 |

MVP 只实现 `always_new`；API 预留 `mode` 字段。

---

## 6. 本机通信与安全

### 6.1 传输

- 仅 `http://127.0.0.1:<port>`（与现有 membox web 一致）  
- CORS：只允许 extension origin（Chrome `chrome-extension://…` / Firefox `moz-extension://…`）  
- 每个 membox home 一份 **bridge token**（随机），扩展 options 里粘贴一次，或通过「配对码」写入

### 6.2 鉴权

请求头：

```http
POST /api/ingest HTTP/1.1
Host: 127.0.0.1:8787
Content-Type: application/json
X-Membox-Token: <token>
```

- `/api/status` 可无 token 返回 `{ "connected": true, "version": "…" }` 或弱化信息  
- 写接口（ingest/sync/save/annotations）**必须** token（迁移期可先 warn 后 enforce）

Token 存放：

```text
$MEMBOX_HOME/bridge.json
{
  "token": "...",
  "port": 8787,
  "host_version": "..."
}
```

扩展 options：读取用户粘贴的 token + base URL；或 `mm web start --fg --write-extension-config` 生成说明。

### 6.3 威胁模型（本地）

| 威胁 | 缓解 |
| --- | --- |
| 本机恶意网页调 `127.0.0.1` | token + CORS；浏览器扩展才有权带 token |
| 端口扫描瞎写 | token；可选绑定随机端口 + 状态文件 |
| 大 body DoS | 已有 MaxBytes 量级限制（与 sync 对齐，如 16MiB） |
| 打开钓鱼 view_url | view_url 仅由本机 server 生成 |

---

## 7. membox 侧改动（Go）

### 7.1 进程模型

推荐：

```bash
mm web start --fg          # 前台或用户服务：启动 web + bridge 配置
# 或 TUI/桌面首次 OpenDocumentWeb 时已懒启动 —— 扩展场景需要「常驻」
```

扩展场景下 **懒启动不够**：用户在任意网页点保存时，membox 可能没在跑。

已落地策略：

1. **TUI 启动时自动 `StartWebServer`**（优先 `:8787`，占用则换临时端口），并写 `~/.membox/bridge.json`  
2. **`mm web start --fg`** 仍可用（无 TUI 时单独挂 bridge）  
3. 扩展开发才需要 `npm run dev`（构建扩展，不是 membox 的 localhost）  
4. 后可做 login item / daemon（P2）

### 7.2 API 增量

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/bridge/status` | connected、version、需要 token 与否 |
| POST | `/api/ingest` | clip 专用创建；返回 id + view_url |
| GET/POST | `/api/resources*` | URL resource 入库、去重、分类、列表与 review 游标 |
| 现有 | `/api/sync`、`/api/doc/…`、`/` | 不变 |

`/api/resources/ingest` 接收来源行，或读取 `source_document_id` / 已索引的 `source_file`，并在 Membox 内执行 URL 提取与 canonical 去重；它不会读取任意未索引路径。`/api/resources/assess` 写知识价值分类；`/api/resources` 返回排名。

`/api/questions/ingest` 同构：从来源行确定性抽取 open question（`Q:`/`问：` 前缀，或足够长的 `?`/`？` 结尾行），按 `canonical_body` 去重入库；`/api/questions` 返回问题清单（可按 `source_file` 过滤）。Timension `/inbox scan` 在 URL 旁路之外并行调用 question ingest。Pi/Timension extension 只做命令适配和 agent 交互，不直接写 SQLite。

Application 层：优先复用 `CreateNote`；可选把 `source_url` 记入 metadata 表（若尚无字段，MVP 只进 front matter）。

### 7.3 打开 Miru

扩展：

```js
const { id, view_url } = await ingest(payload)
await browser.tabs.create({ url: view_url })
```

用户也可在 popup 关「自动打开」，仅入库。

---

## 8. 扩展侧模块划分（WXT）

```text
extensions/clipper/
  wxt.config.ts
  entrypoints/
    background.ts       # 消息总线、ingest、开 tab、右键菜单
    popup/              # 连接状态、Save、Open last、选项入口
    options/            # base URL、token、自动打开、默认整页/选区
    content.ts          # 选区 HTML、页面 title/url（若需）
    (optional) offscreen.html  # 重转换放到 offscreen 避免卡 SW
  lib/
    readability.ts
    turndown.ts
    assemble.ts         # front matter + body
    membox-client.ts    # status/ingest
    types.ts
  locales/
```

### 8.1 主序列（Human P0）

```text
User: 点击 Save to membox
  → background 请求 content/offscreen 拿到 { title, url, markdown }
  → POST /api/ingest + token
  → 成功：toast + tabs.create(view_url)
  → 失败：popup 展示 offline / 401 / 转换错误
```

### 8.2 Agent P1（两种实现，择一）

**A. 扩展轮询 / 长轮询 membox**  
`GET /api/bridge/jobs` → 扩展执行 clip → `POST /api/bridge/jobs/:id/result`  
（类似 MarkSnip 反向：HTTP 在 membox，扩展是 worker）

**B. Native messaging host 薄封装**  
仅当必须「浏览器未开后台 SW」时；成本高，不作 MVP。

推荐 P1 走 **A**，与「单一 membox server」一致，避免第二进程。

---

## 9. 里程碑

### M0 — 契约与 spike（约数天）

- [x] 冻结 `POST /api/ingest` JSON schema  
- [x] WXT 扩展 + ingest 通路  
- [x] 确认 notes 写入路径与 UUID 返回

### M1 — Human ingest MVP

- [x] 扩展：整页 Readability → MD → ingest → 打开 Miru  
- [x] membox：`/api/ingest` + token + CORS  
- [x] `mm web start --fg`；popup 连接状态  
- [x] API 测试（ingest + token）  
- [ ] 转换 fixture 快照测试（后续）

### M2 — 体验打磨

- [ ] 选区剪辑  
- [ ] 快捷键、右键菜单  
- [ ] 失败重试、最近一次 clip 列表（扩展 local）  
- [ ] front matter 与文件名 slug 规则对齐 CreateNote

### M3 — Agent bridge

- [ ] job 队列或等价 API  
- [ ] 返回 `{ id, view_url, title, source_url }` 供 agent  
- [ ] 与 CLI `mm clip`（可选薄封装，调用同一 API）

---

## 10. 测试策略

| 层 | 内容 |
| --- | --- |
| 转换 unit | HTML fixtures → Markdown 快照（表格、代码、列表、相对链接） |
| API | `TestIngest_AssignsUUIDAndWritesFile`；无 token → 401 |
| 扩展 e2e（后） | Playwright + 固定页；可先手动脚本 |
| 回归 | 既有 Miru `/?id=`、annotations sync 不被破坏 |

---

## 10.5 Markdown Annotation Note（已实现）

**原则：`*-note.md` 是唯一笔记内容权威；SQLite 保存 UUID 关系和锚点；Miru sidecar 只是在读取时生成的 DTO。**

```text
Extension 或 Miru 选区 Save
  ├─ 建/更新 *-note.md（blockquote 摘录 + 用户笔记）
  ├─ annotation_notes：page UUID → note UUID + anchor/style
  └─ Miru 打开 page
       ├─ 读取关联 note Markdown
       ├─ 与 DB anchor 组合成临时 miru-annotations JSON
       └─ 前端原生渲染；不持久化 sidecar 副本
```

| 场景 | 体验 |
| --- | --- |
| 原始网页 | 浮动卡片（扩展渲染），**默认禁用，popup 里点 Enable 才生效** |
| Miru 阅读页 | 原生 annotation：摘录样式 + 编号上标 + 边栏笔记卡 |

实现要点：

- `*-note.md` 只保存用户可读内容；不复制 `annotates`、anchor、style 等机器 metadata 到 front matter。
- 文件名 = `slug(摘录前缀)-base26(sha256(正文))[:10]-note.md`：内容 hash 区分同前缀摘录；片段只用字母（不用 hex），避免与 UUIDv7 hex ID 在 FTS/名称搜索中互相误命中；重名 `-2` 后缀只作为极端兜底。
- `annotation_notes` 以 note document UUID 为主键，保存 target UUID、start、prefix/suffix 和样式。
- `GET /api/doc/:id/annotations` 读取时组合 Markdown + DB，并按当前 page Markdown 动态计算 `sourceHash/sourceLength`。
- `POST /api/doc/:id/annotations` 把 Miru 新笔记物化成 `*-note.md`；编辑更新该文件；删除移除该文件与关系行。
- 阅读进度独立存入 `document_read_state`，滚动不会重写笔记文件或 annotation JSON。
- 被标注文档的“最后修改时间”会提升到其 annotation note 的最新活动时间（保存或笔记文件更新），newest 排序与日期过滤会把刚做过笔记的文档视为 modified。
- 笔记输入支持多行：`Enter` 换行，`Command+Enter` 或纸飞机按钮发送；`*-note.md` 解析保留笔记内空行。
- 旧 `document_annotations` 仅作为迁移读取源；下次保存会物化成 Markdown 并清除旧 blob。
- Extension ingest 直接建立关系；先有 selection、后有 page 时，在 page ingest 时 backfill。
- Miru/bridge server 启动时自动发现尚未关联的存量 `*-note.md` 并建立关系；已迁移记录会被跳过，不需要用户命令。
- 锚定失败的前端 annotation 仍作为 pending 合并，避免一次渲染失败被误判为删除。

已知限制：

- Extension 浮动卡片中的本地编辑仍未回写 membox。
- 摘录含公式/表格时可能无法锚定；Markdown note 与 UUID 关系不会丢失。

---

## 11. 开放问题

1. **常驻方式**：仅文档约定 `mm web start --fg`，还是要 daemon/launchd？  
2. **默认写入路径**：必须已 `path add` 某个 notes 根，还是 membox 提供默认 inbox 目录？  
3. **图片**：MVP 只保留 URL，还是下载到附件目录？（建议 MVP 外链）  
4. **付费墙/登录页**：扩展可读当前 DOM（已登录态）——产品上要在隐私说明里写清「仅本机、用户触发」。  
5. **Firefox/Chrome 扩展 ID** 与 CORS 白名单如何在开发/发行间切换。

---

## 12. 建议的立即下一步

1. 在 membox 实现 `POST /api/ingest`（可先无 token，用现有 CreateNote）+ 测试。  
2. 脚手架 `extensions/clipper`（WXT），popup 一个按钮：硬编码 markdown → ingest → `tabs.create`。  
3. 再接入 Readability/Turndown 与 token。  
4. 文档：`mm web start --fg` + 扩展 options 配对流程写进 `user-stories.md` 新 Epic。

---

## 13. 一句话

**WXT 扩展负责「把当前页变成 Markdown」；membox 本机 HTTP 负责「UUID + 落盘 + 索引 + Miru」；Agent Bridge 是同一 server 上的机器入口，不是让人回 terminal 的主工作流。**
