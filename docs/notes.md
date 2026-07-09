## sprint0

- 双击 space bubble tea
- 和远端的 perkeep 做整合

## backlog 

- grok 可以在后台, 找到这节课相关的 pdf
- ai agentic search 应该起作用, 复习的时候, 很有用
- 现在在 note new 的时候, 需要查看已有笔记, 关联 or 显示打开建议
- 现在的 uuid 都是怎么做的?
- tags 需要一个系统
- 需要 alphaxiv-open 模仿, 打上 index
- 把当前的 changes commit
	root.AddCommand(noteCmd())
	root.AddCommand(tuiCmd())
- bugfix: 当前的 rename take args 错误
- 需要有 migrate 的能力, 然后开始做 link

## learning

- cobra 的 cli 的包需要学
- goreleaser 需要弄懂 go 的这个开发的链条


## 价值点

- llm wiki 的 ingest 的能力
- 搜索能力
- 多个 tabs 在一起的总结成看板
- 总结 / 收藏
- 最重要的 是怎么利用大模型, 搜索, 怎么建立卡片墙, 帮助我复习!!!
  - birdclaw

## todo

- [] 可以做到 curl 接收 markdown 保存在远端
- [] 可以做到截图发给后端

## idea

- baidu disk 以后每个人可以用自己的
- [] mario 那篇文章需要看了, agent native
  - 有一个运行在 browser 的 ai 的
- [] 结合 LinWei 和另外两个做 ai 记忆的
- [] xai 的双塔模型
- 为了避免 hub 的单点故障, 还需要 gitee hub 来做全量的备份
- 第一步可能是 hub-and-spoke 后续要变成不管几台机器下线也没有关系的模型
- 应该是真正的 GFS 和 sqlite, add backend 实际上做的是同步, 然后是缓存, 这样来做
- 搞不好需要的是服务端, 大的 pdf 什么的, 需要 api 暴露出来

## principle

**使用 go 是看中分布式的能力, CLI 保持 Go，但心态调整：它不是"CLI 工具"，是分布式文件系统的本地控制平面**
- 做存储层抽象, 不同的应用都可以存内容进来, 只要满足接口
- 有 workspace 概念, 其实也就是变相的文件夹
- 具有分布式容灾特质, 某个机器坏掉, 不影响访问
- 有 git 做全量文本文件的版本管理

---

# Membox 原有笔记

- 应该是有 topic-slug, 然后文档和 slug 可以 link

## MVP V0.5
1. POST /ingest：导入 PDF（先支持本地 path，后面再支持 upload）
2. POST /search：query + topk → 返回 chunks + score + page
3. POST /reindex：重建/更新索引（最开始可以同步执行）
