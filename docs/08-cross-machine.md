# 跨机器工作流

Workspace 是 **per-machine 配置**，backend 是 **物理存在**。跨机器时，核心问题是：

> 同一组 UUID 笔记，如何在不同设备上有不同的物理布局，但在 workspace 中呈现一致的视图？

## 配置分层

```
~/.config/mm/
  workspace.toml          # 本机独有： mount 表
  backends.toml           # 可多机共享：backend 列表
  settings.toml           # 本机偏好

~/repo/vfs/               # 可以是一个 git repo
  docs/                   # 本文档
  shared/
    backends.toml         # 共享 backend registry
    default-mounts.toml   # 新机器初始化用的默认 mount 表模板
```

## 典型多机器场景

### 场景 A：家里 Mac

```toml
# workspace.toml
[[mounts]]
path = "/"
backend = "main"
subpath = "/"

[[mounts]]
path = "/inbox"
backend = "icloud"
subpath = "/Notes/inbox"

[[mounts]]
path = "/work"
backend = "workbox"
subpath = "/home/edward/notes"
```

`workbox` 配置为 **ssh + 只读**：

```toml
[backends.workbox]
type = "ssh"
host = "vpn-to-workbox"
writable = false
```

### 场景 B：公司 Linux

```toml
# workspace.toml
[[mounts]]
path = "/"
backend = "main"
subpath = "/"

[[mounts]]
path = "/work"
backend = "workbox"
subpath = "/home/edward/notes"
```

公司机器上没有 `icloud` mount，但可以有 `mobile-inbox`（通过 Syncthing 或手动同步）。

## Sync 策略矩阵

| Backend | Sync 方式 | 冲突解决 |
|---------|----------|---------|
| main | git + 定期 push/pull | git merge，mm 辅助 diff |
| icloud | Apple iCloud | 以最新 mtime 为准，手动 review |
| workbox | git（vpn 时 fetch） | git merge |
| scratch | 不 sync，本地暂存 | 定期手动 promote |

## 初始化新机器

```bash
$ mm init --from-git git@github.com:edward/vfs-config.git
→ clone 共享配置
→ 根据当前机器 hostname 选择对应的 workspace.toml
→ 提示输入本地 backend 根目录
```

## 无冲突原则

1. **UUID 不冲突**：新文件永远用 UUID，跨机器创建同名文件概率为零。
2. **移动不产生冲突**：移动笔记是 workspace 层操作，不改物理 backend 内容时无 git diff。
3. **编辑冲突按 git 走**：同一文件在 A、B 机器都编辑，push/pull 时用标准 git 冲突解决。

## 离线场景

- 所有写入都落在本地 writable backend。
- 远程 backend 在离线时退化为本地缓存。
- sync 恢复后，先 `mm sync`，再 `mm digest`，再 `mm index --rebuild`。

## 未来扩展： cloud backend

可以加入一个中心化的 cloud backend：

```toml
[backends.central]
type = "s3"
bucket = "mm-notes"
writable = true
git_tracked = false
sync = "s3"
```

它作为跨机器的 "交换分区"，不替代 git backend，但用于临时中转。
