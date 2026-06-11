# VFS / Workspace 设计文档

这是为个人笔记系统设计的 **虚拟文件系统（Virtual Workspace）** 方案。

## 一句话总结

> 把笔记系统比作操作系统的虚拟内存：**mm 只看见 workspace（虚拟地址空间），backend 是物理存储（内存/磁盘/远程）。** 通过 mount table 做映射，通过 UUID 做主键，实现 "到处记笔记、统一搜索、AI 日报跨路径"。

## 文档索引

| 文档 | 内容 |
|------|------|
| [01-architecture.md](./01-architecture.md) | 整体架构，虚拟内存类比，三层模型 |
| [02-workspace.md](./02-workspace.md) | workspace 抽象，mount table，路径解析 |
| [03-uuid-allocation.md](./03-uuid-allocation.md) | UUID 主键策略，frontmatter 规范 |
| [04-mount-table.md](./04-mount-table.md) | backend 定义，mount 规则，缓存 |
| [05-mm-integration.md](./05-mm-integration.md) | mm CLI 与编辑器集成 |
| [06-search-index.md](./06-search-index.md) | 统一搜索与 QA/RAG 层 |
| [07-git-digest.md](./07-git-digest.md) | AI 日报与 git 集成 |
| [08-cross-machine.md](./08-cross-machine.md) | 跨机器工作流 |
| [09-roadmap.md](./09-roadmap.md) | 实施路线图与最小原型 |

## 核心设计原则

1. **Quick Notes accessibility 第一**：`mm new` 打开即写，UUID 和路径自动处理。
2. **Workspace 是唯一视图**：所有工具（mm、search、QA、digest）只消费 `ws://`。
3. **物理位置可任意组合**：本地、iCloud、公司机器、NAS，通过 mount table 接入。
4. **Git 是 backend 属性**：digest 遍历所有 git-tracked backend，而非假设只有一个 repo。
5. **UUID 是主键**：标题和路径都是视图，可以任意变更而不破坏链接。

## 快速启动

```bash
# 1. 注册 backend
mm backend add main local ~/repo/notes --git

# 2. mount 到 workspace 根
mm ws mount / main /

# 3. 开始记笔记
mm new "我的第一个 UUID 笔记"

# 4. 搜索
mm search "UUID 笔记"

# 5. 生成日报
mm digest
```
