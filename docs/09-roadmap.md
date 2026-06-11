# 实施路线图

## Phase 0：现有 repo 兼容（本周）

目标：不破坏现有 `notes/` 目录，先让 mm 能工作。

1. 创建 `~/.config/mm/` 目录。
2. 把 `~/repo/notes` 注册为 `main` backend。
3. mount 到 workspace 根 `/`。
4. 实现最小 mm CLI：
   - `mm new [title]`
   - `mm read <ws-path>`
   - `mm search <query>`
5. 新文件采用 UUID 命名，老文件保持原样。

## Phase 1：Workspace 核心（2 周）

1. 实现 mount table 和路径解析。
2. 实现 `mm ws mount/ls/rm`。
3. 实现 `mm backend add/ls/rm`。
4. UUID registry 和 frontmatter 规范落地。
5. 基础索引：`mm index` 和 `mm search`。

## Phase 2：AI 日报（2 周）

1. `mm digest`：遍历 git-tracked backend，读 git log，生成日报。
2. 日报保存到 `ws://daily/<date>.md`。
3. 配置 cron/launchd 定时执行。
4. Prompt 调优，让日报真正可用。

## Phase 3：跨机器与移动（1 个月）

1. 通过 git 共享 `backends.toml` 和 mount 模板。
2. iCloud / Syncthing inbox 集成。
3. 远程 backend 只读缓存（ssh / s3）。
4. VS Code 插件解析 `ws://`。

## Phase 4：智能化（长期）

1. Embedding + 语义搜索。
2. 链接图可视化。
3. 自动分类 / inbox 整理建议。
4. 语音/图片 Quick Capture。

## 最小可运行结构

```
~/.config/mm/
  workspace.toml
  backends.toml
  settings.toml

~/repo/notes/          # main backend
  inbox/
    2024/
      06/
        a3f7b2c1.md   # UUID 新文件
      old-note.md     # 兼容老文件
  daily/
    2024-06-10.md

~/repo/vfs/            # 本设计文档
  docs/
    01-architecture.md
    ...
```

## 第一个要写的代码

```bash
#!/usr/bin/env bash
# mm new 的最小原型

TITLE="${1:-Untitled}"
UUID=$(uuidgen | tr '[:upper:]' '[:lower:]')
SHORT=${UUID:0:8}
DATE_DIR=$(date +%Y/%m)
FILE="$HOME/repo/notes/inbox/$DATE_DIR/$SHORT.md"

mkdir -p "$(dirname "$FILE")"

cat > "$FILE" <<EOF
---
id: $UUID
title: $TITLE
created: $(date -Iseconds)
---

# $TITLE

EOF

$EDITOR "$FILE"
```

先让这个脚本跑起来，再逐步替换为完整的 workspace 实现。
