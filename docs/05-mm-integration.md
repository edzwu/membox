# Mm 集成：Quick Note 的第一性

Mm 是你的主要入口。它的设计必须保证 **"想到就能记"** 的 accessibility。

## 核心命令

```bash
# 最快方式：生成 UUID，打开编辑器
$ mm new
→ 打开 $EDITOR ws://inbox/2024/06/a3f7b2c1.md

# 带标题（只作为 frontmatter，不决定文件名）
$ mm new "开会想法"

# 指定位置
mm new --to daily
mm new --to projects/vfs
mm new --backend icloud --to inbox

# 直接追加到当日快速笔记（不创建新文件）
$ mm capture "刚才想到 VFS 的冲突规则应该用 LPM"
→ 追加到 ws://inbox/2024/06/daily-capture.md （自动创建或追加）
```

## 编辑器集成

Mm 不内置编辑器。它把 **workspace 路径** 传给 `$EDITOR`，同时提供 LSP/插件让编辑器能解析 `ws://`。

### VS Code / Cursor

配置 `mm://` URI handler：

```json
// settings.json
"workbench.editor.enablePreview": false,
"mm.workspacePath": "/Users/edward/.config/mm/workspace.toml"
```

插件功能：

- `Ctrl+Cmd+N`：调用 `mm new`，在当前编辑器打开生成的文件。
- 悬浮提示：把 `ws://...` 链接解析为真实路径。
- 自动补全：输入 `[[` 搜索笔记标题/UUID。

### Terminal

```bash
# zsh alias
alias tn='mm new'
alias tc='mm capture'
alias ts='mm search'
alias td='mm digest'
```

## Mobile / 跨端 Quick Capture

移动端不直接 mount workspace。采用 **inbox drop** 模式：

1. iOS Shortcuts → 调用 `mm capture` HTTP endpoint（如果本机运行 mm server）或直接写 iCloud `Notes/inbox/`。
2. 因为 iCloud 目录是 mount 进 workspace 的，所以桌面端 mm 立即可见。
3. 定期（或触发）把 inbox 里的内容整理到主 backend。

```
iOS Share Sheet
    ↓
~/icloud/Notes/inbox/<iso-timestamp>-<short-uuid>.md
    ↓
workspace mount /inbox → backend=icloud
    ↓
mm search / digest 能立即索引到
```

## 无网络时

Mm 写入本地 backend（main 或 scratch），不依赖远程 backend。远程 backend 在 sync 时合并。

## 文件命名：用户无感知

用户只和 `ws://` 以及标题打交道，永远不需要自己管理 UUID。

```bash
$ mm new "VFS 架构想法"
# mm 内部生成 id=a3f7b2c1-...
# 物理文件：~/repo/notes/inbox/2024/06/a3f7b2c1.md
# 打开时编辑器标题栏显示 frontmatter.title
```

## 从老 repo 迁移

你现在的 `notes/` 目录可以直接作为一个 backend：

```bash
# 1. 注册 backend
$ mm backend add main local /Users/edward/repo/notes --git

# 2. 把现有 notes 目录 mount 为 workspace 根
$ mm ws mount / main /

# 3. 可选：迁移旧文件到 UUID 命名
$ mm migrate --backend main --path /
```

如果暂时不想全量迁移，可以保留旧文件名，只对 **新文件** 启用 UUID。老文件通过 legacy index 记录。
