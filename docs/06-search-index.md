# 搜索与 QA：统一抽象层

搜索和 QA 只消费 workspace 视图，不感知 backend。

## 索引设计

### 索引存储

```
~/.cache/mm/index/
  notes.sqlite          # 元数据 + 标题 + tags + 链接图
  embeddings/           # 向量索引（可选）
  fulltext/             # FTS5 或 meilisearch 索引
```

### 索引内容

| 字段 | 来源 |
|------|------|
| `uuid` | frontmatter.id |
| `ws_path` | mount table 解析出的 workspace 路径 |
| `backend` | 物理 backend 名 |
| `physical_path` | 真实绝对路径 |
| `title` | frontmatter.title 或 H1 |
| `content` | markdown body |
| `tags` | frontmatter.tags |
| `created` | frontmatter.created |
| `modified` | frontmatter.modified 或 mtime |
| `links` | 文中 `[[uuid]]` 或 `ws://...` |
| `git_commits` | 该文件在最近 digest 周期内的 commits |

### 索引更新策略

1. **事件触发**：mm new/edit/save 时增量更新对应文档。
2. **定时全量**：`mm index --rebuild` 扫描所有 mount。
3. **backend sync 后**：远程 backend pull 成功后触发增量更新。

## 搜索接口

```bash
# 全文搜索
$ mm search "虚拟内存"
ws://docs/01-architecture.md      "...把笔记系统做成 虚拟内存 ..."
ws://inbox/2024/06/a3f7b2c1.md    "...VFS 的MMU类比..."

# 按 tag
$ mm search '#vfs'

# 按日期范围
$ mm search --after 2024-06-01 --before 2024-06-10

# 语义搜索（如果配了 embedding）
$ mm search --semantic "我关于 workspace 的设计"

# 复合搜索
$ mm search '#vfs 虚拟内存' --after yesterday
```

## QA / RAG 接口

```bash
$ mm ask "我前两天关于 workspace mount 的想法是什么？"
```

实现流程：

1. Query 解析：识别时间范围（"前两天" → `after:2024-06-08`）、主题关键词。
2. 召回：用 FTS + embedding 召回 Top-K。
3. 重排：按 recency + 链接关系 + git activity 排序。
4. 生成：把 Top-K 文档塞进 LLM prompt，生成回答。
5. 溯源：每个结论标注来源 `ws://...`。

## 链接图（Graph View）

基于 indexed links 构建有向图：

```bash
$ mm graph --of a3f7b2c1
a3f7b2c1.md
  → b2c3d4e5.md  (ws://docs/02-workspace.md)
  ← c3d4e5f6.md  (ws://inbox/2024/06/another.md)
```

可用于：

- 可视化知识关联
- 发现 orphan notes
- QA 时做多跳召回

## 与物理路径解耦

索引层的关键是：**所有结果返回 `ws://` 路径**。

这样即使 backend 被重新 mount、文件被移动，只要 UUID 和 frontmatter 不变，索引无需重建链接关系。
