# Mount Table 与路径解析

Mount Table 是 workspace 的核心。它回答一个问题：**`ws://x` 实际在哪里？**

## 设计决策

### 1. 前缀最长匹配（Longest Prefix Match）

和 URL route、操作系统 mount 一样。保证子目录可以被单独映射到不同 backend。

### 2. 只支持目录级 Mount

不支持单文件 mount。简化实现，避免 inode 式复杂度。

### 3. Mount 是本地配置

`workspace.toml` 保存在 `~/.config/mm/workspace.toml` 或当前机器的 `~/repo/vfs/` 里。

跨机器时，每台机器维护自己的 mount 表，但共享 **backend registry**（比如通过 git 同步 `backends.toml`）。

## Backend 定义

backend 是物理存储的抽象：

```toml
# ~/.config/mm/backends.toml

[backends.main]
type = "local"
root = "/Users/edward/repo/notes"
# 是否可被当前机器写入
writable = true
# 是否参与 git digest
git_tracked = true
# 同步方式：git | rsync | icloud | manual
sync = "git"

[backends.icloud]
type = "local"
root = "/Users/edward/Library/Mobile Documents/com~apple~CloudDocs/Notes"
writable = true
git_tracked = false
sync = "icloud"

[backends.workbox]
type = "ssh"
host = "workbox.local"
root = "/home/edward/notes"
writable = false  # 在公司外只读
git_tracked = true
sync = "git"

[backends.local-scratch]
type = "local"
root = "/Users/edward/.mm/scratch"
writable = true
git_tracked = false
sync = "none"
```

## 路径解析伪代码

```python
def resolve(ws_path: str) -> ResolvedPath:
    # ws_path = "/inbox/2024/06/a3f7b2c1.md"
    candidates = []
    for mount in mounts:
        if ws_path.startswith(mount.path):
            candidates.append(mount)
    
    # 最长前缀匹配
    best = max(candidates, key=lambda m: len(m.path))
    
    rel = ws_path[len(best.path):]
    if not rel.startswith("/"):
        rel = "/" + rel
    
    backend = backends[best.backend]
    physical = backend.root + best.subpath + rel
    
    return ResolvedPath(
        ws_path=ws_path,
        backend=backend,
        physical=physical,
        git_tracked=best.git_tracked,
    )
```

## 写操作时的路径选择

```bash
$ mm new "想法"                    # 写到 default_backend
$ mm new "想法" --to inbox         # 写到 ws://inbox/...
$ mm new "想法" --backend icloud   # 显式指定 backend
```

如果 `--to inbox`，先找命中 `/inbox` 的 mount；如果该 mount 的 backend 不可写（如 workbox 在只读模式），报错。

## 只读 Mount

某些 backend 在当前机器上可以是只读的，比如公司电脑的 `workbox` 在家里：

- `mm read ws://work/xxx.md`：允许，按需通过 ssh/rsync 拉取（或读本地缓存）。
- `mm new ws://work/xxx.md`：拒绝，提示 "backend workbox is read-only on this machine"。

## 缓存层

对于远程 backend，本地维护一个 cache：

```
~/.cache/mm/backends/
  workbox/
    notes/
      projects/
        a3f7b2c1.md
```

缓存策略：

- `read`：miss 时拉取，按 TTL 失效。
- `index`：批量 sync（如 `mm sync workbox`）。
- `write`：只写本地 writable backend，远程 backend 通过 git pull 更新。
