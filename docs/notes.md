## M0
先把 TUI ↔ Host 的: “字节流通道 + framing + JSON-RPC ping” 跑通

- sys design: mm-sdk / mm-protocol 作为插件作者唯一依赖 
- view pane 得是一个 编辑器
- 弄懂 vim 的 lsp 之类的插件结构来借鉴
- extension - lsp ai 的 gdb
- extension - mm web get
- 需要 把 lsp 先看了
- 未来需要 redis 和 sqlite 的 db 的知识
- 知识库, 分散在 每个文件的每句话, 加上一个 tag 就能汇总到 inbox 里, 这可能也不错, 至少知道 context
- 搜索功能
  - 模糊搜索
    - 编辑距离, 词向量距离
    - RAG 的 ai 的方法
  - 应该是有一个类似 redis 的 cache 系统, 避免海量搜索
    - 但是远端数据更新了怎么办? 要想好什么情况下用缓存
      - 缓存可以做成, 看看有没有更新的文档, 如果没有的话, 直接返回上次的结果
---