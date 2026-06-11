- baidu disk 以后每个人可以用自己的
- [] mario 那篇文章需要看了, agent native
- [] 结合 LinWei 和另外两个做 ai 记忆的
- [] xai 的双塔模型
- 为了避免 hub 的单点故障, 还需要 gitee hub 来做全量的备份
- 第一步可能是 hub-and-spoke 后续要变成不管几台机器下线也没有关系的模型
- 应该是真正的 GFS 和 sqlite, add backend 实际上做的是同步, 然后是缓存, 这样来做
- 搞不好需要的是服务端, 大的 pdf 什么的, 需要 api 暴露出来

## principle
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
