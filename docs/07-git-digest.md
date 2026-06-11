# Git 日报：AI Digest

AI 日报（digest）的目标是：**汇总你最近一段时间在所有 git-tracked backend 上的笔记活动**。

## 为什么放在 backend 层做

因为 workspace 是虚拟视图，本身没有 git 历史。物理 backend 才有 `git log`。

## 执行流程

```bash
$ mm digest
```

1. 读取 workspace，找出所有 `git_tracked = true` 的 backend。
2. 对每个 backend 执行 `git log --since="24 hours ago" --name-only`。
3. 合并变更文件列表，按 UUID 去重。
4. 读取这些文件当前内容（从 workspace 视图）。
5. 构造 prompt，调用 LLM 生成日报。
6. 输出保存到 `ws://daily/<date>.md`。

## Prompt 模板思路

```markdown
你是一位严谨的笔记整理助手。请根据用户过去 24 小时在笔记系统中创建和修改的内容，生成一份日报。

要求：
- 按主题分组，不要简单按时间罗列。
- 提取关键想法、决策、待办事项。
- 对不完整或需要后续展开的想法，标注 "TODO" 或 "待展开"。
- 每个结论都要标注来源 UUID。

内容：
{{files}}
```

## 日报文件格式

生成的日报本身也是一篇 UUID 笔记：

```markdown
---
id: d4e5f6a7-...
title: Daily Digest 2024-06-10
created: 2024-06-10T23:00:00+08:00
type: digest
period_start: 2024-06-09T23:00:00+08:00
period_end: 2024-06-10T23:00:00+08:00
sources:
  - a3f7b2c1-...
  - b2c3d4e5-...
---

# Daily Digest 2024-06-10

## 主题一：VFS / Workspace 设计

- 决定将 workspace 类比为虚拟内存，backend 类比为物理内存
  - [→ a3f7b2c1](../inbox/2024/06/a3f7b2c1.md)
- 明确 UUID 是主键，标题和路径只是视图
  - [→ b2c3d4e5](../docs/03-uuid-allocation.md)

## 待办

- [ ] 实现 `mm ws mount` 命令
- [ ] 给 VS Code 写插件解析 `ws://`
```

## 定时执行

通过 cron 或 launchd：

```bash
# crontab: 每晚 23:00 生成日报
0 23 * * * /usr/local/bin/mm digest --auto-commit
```

`--auto-commit` 选项：生成日报后自动 `git add daily/ && git commit -m "digest: 2024-06-10"`。

## 非 git backend 怎么办？

对于 `git_tracked = false` 的 backend（如 iCloud inbox）：

- 不纳入 git digest。
- 但通过 **文件 mtime** 仍然可以被搜索/QA 索引。
- 推荐做法：定期把 inbox 里的笔记 **promote** 到 git-tracked backend，然后删除原文件。

## 跨机器 digest

如果你在公司电脑和工作电脑都有 git-tracked backend：

- 公司电脑的 backend 在家里可能是只读缓存。
- `mm digest` 在家里执行时，会尝试 `git fetch` 公司 backend 的最新历史。
- 如果无法连接，使用本地缓存的历史，并在日报里标注 "workbox 数据截至 xxx"。
