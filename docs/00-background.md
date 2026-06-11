# 背景

## 痛点分析

- agent 工具本质上是 stateless 的, 真正的记忆应该落盘
- agent 现在更多是双塔模型的 ‘item’ 和 “人物画像“ 的差距还是很大
- 多入口同步:
  - 文件有可读性要求, 阅读的时候, Editor or Browser 但是都有记笔记, 储存笔记的需求
  - 不区分入口, 方便记录, 但是区分入口, 方便查找
  - accessibility: 跨越不同 repo, 记忆系统是和 agent (人物画像) 绑定的
- backlink 很不方便, 文件系统应该有 workspace 和 topic 的抽象, 一个 workspace 可能多个 topic
- 需要 “背压”, 生产的速度, 可能远大于消费的速度, ai 需要帮人化整为零
- 搜索: 快速 agentic search
- 推荐系统: ai 充当 content creator, 根据长期的用户画像创建 for you
