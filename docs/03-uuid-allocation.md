# UUID 分配策略

为了支持 "到处记笔记" 且最终能归并到统一命名空间，所有笔记文件以 **UUID 为主键**。

## 文件名格式

```
<uuid-short>.md
```

- 完整 UUID：`a3f7b2c1-8d4e-4f5a-9b2c-1d3e4f5a6b7c`
- 短 ID（文件名用）：`a3f7b2c1`（前 8 位）
- 完整 UUID 放在 frontmatter 的 `id` 字段

## Frontmatter 规范

```markdown
---
id: a3f7b2c1-8d4e-4f5a-9b2c-1d3e4f5a6b7c
title: 开会时关于 VFS 的灵感
created: 2024-06-10T09:23:00+08:00
modified: 2024-06-10T09:45:00+08:00
backend: main
source: mm-cli
tags: [vfs, idea, inbox]
---

# 开会时关于 VFS 的灵感

...
```

## 分配流程

```bash
$ mm new "开会想法"
```

1. 生成 UUID v7（时间排序，对索引和 git 更友好）。
2. 根据标题或默认规则决定 workspace 路径：
   - 无分类 → `ws://inbox/<year>/<month>/<short-uuid>.md`
3. 解析 mount，确定 backend 和物理路径。
4. 创建文件，写入 frontmatter，打开编辑器。
5. 用户保存后，mm 触发 index update。

## 冲突避免

UUID v7 基本不会冲突。如果真冲突：

1. 扫描目标目录，发现同名文件。
2. 读取文件 frontmatter，比较完整 `id`。
3. 若完整 `id` 不同但短 ID 相同（极小概率）：
   - 在文件名使用 12 位或 16 位短 ID。
   - 或在文件名后加 `-2` 后缀，但 frontmatter 保持完整 UUID。

## UUID 与路径无关

UUID 是逻辑主键，物理路径可以变：

```
ws://inbox/2024/06/a3f7b2c1.md   # 今天放在 inbox
mv ws://inbox/2024/06/a3f7b2c1.md ws://projects/vfs/a3f7b2c1.md
```

移动只是改 mount/path，不改文件名和 frontmatter。Index 通过 UUID 更新位置映射。

## 标题变更不影响链接

因为主键是 UUID，改名很自由：

```markdown
# 旧标题
→ 改为 →
# 新的更好的标题
```

内部链接使用 `uuid://a3f7b2c1-...` 或 `ws://<path>/<short-uuid>.md`，不依赖标题。

## 批量导入旧笔记

已有的大量非 UUID 笔记可以通过 migration 导入：

```bash
$ mm migrate --from ~/old-notes --to backend=main --path=/archive/legacy
```

迁移时会为每个 `.md` 文件生成 UUID，文件名改为 `<short-uuid>.md`，frontmatter 保留原 `title`，并加 `legacy_name: original-filename.md`。
