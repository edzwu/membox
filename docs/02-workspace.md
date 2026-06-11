# Workspace 抽象

Workspace 是 mm 唯一可见的全局命名空间。它本身不存储文件，只是一张 **Mount Table** 加上一套 **路径解析规则**。

## 数据结构：`workspace.toml`

```toml
[workspace]
name = "edward-notes"
version = 1
# 默认 backend：当 mm new 不指定落点时，文件写到这里
default_backend = "main"

# 冲突解决策略：later | manual | backend-priority
conflict_resolution = "later"

[[mounts]]
# 逻辑路径
path = "/"
# 物理后端
backend = "main"
# 物理后端里的子目录
subpath = "/"
# 是否参与 git digest
git_tracked = true
# 优先级（冲突时用）
priority = 10

[[mounts]]
path = "/inbox"
backend = "icloud"
subpath = "/Notes/inbox"
git_tracked = false
priority = 5

[[mounts]]
path = "/work"
backend = "workbox"
subpath = "/home/edward/notes"
git_tracked = true
priority = 8

[[mounts]]
path = "/scratch"
backend = "local-scratch"
subpath = "/"
git_tracked = false
priority = 1
```

## 路径解析规则

给定 workspace 路径 `ws://inbox/2024/06/a3f7b2c1.md`：

1. 按最长前缀匹配 mount：`/inbox` 命中。
2. 剥离前缀，得到相对路径 `2024/06/a3f7b2c1.md`。
3. 拼接到 backend 的 subpath：`~/icloud/Notes/inbox/2024/06/a3f7b2c1.md`。

### 多 mount 重叠示例

```toml
[[mounts]]
path = "/"
backend = "main"
subpath = "/"
priority = 10

[[mounts]]
path = "/daily"
backend = "secondary"
subpath = "/daily"
priority = 20
```

`ws://daily/2024-06-10.md` 优先命中 `/daily`，而不是 `/`。

### 文件不存在的处理

- `mm read ws://daily/x.md`：按 mount 解析，文件不存在 → 404。
- `mm new ws://daily/x.md`：按 mount 解析，在对应 backend 创建。
- `mm new ws://unknown/x.md`：无 mount 命中 → 报错或 fallback 到 default backend 根目录（可配置）。

## 特殊目录约定

| Workspace 路径 | 语义 |
|---------------|------|
| `/inbox` | 快速捕获，高 accessibility，不一定 git 跟踪 |
| `/notes` | 主笔记库，git 跟踪 |
| `/daily` | AI 日报输出目录 |
| `/projects/<name>` | 项目笔记 |
| `/archive/<year>` | 归档 |
| `/scratch` | 临时想法，定期 review 并迁移 |

## 挂载状态命令

```bash
$ mm ws ls
PATH        BACKEND       SUBPATH                 GIT  PRIO
/           main          /                       yes  10
/inbox      icloud        /Notes/inbox            no   5
/work       workbox       /home/edward/notes      yes  8
/scratch    local-scratch /                       no   1

$ mm ws mount /mobile ios-notes /Notes --priority 3
```

## Workspace 不是 Git Repo

重要：**workspace 层本身不做版本控制**。版本控制是 backend 的属性：

- `main` backend 是一个 git repo。
- `icloud` backend 可能被 iCloud 同步，但不 git。
- `workbox` backend 是公司机器上的 git repo。

AI digest 会遍历所有 `git_tracked = true` 的 backend，分别执行 git log，再合并结果。
