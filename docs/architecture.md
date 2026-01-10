+------------------+                                   +------------------+
|     Client       |   JSON-RPC 2.0                    |      Server      |
| (TUI Process)    |<-----------framed bytes---------> | (Host Process)   |
+------------------+                                   +------------------+
| presentation/tui |                                   | presentation/rpc |
| application/*    |                                   | application/*    |
| infra/process    |                                   | infra/stdio      |
+------------------+                                   +------------------+
           \___________ shared/proto (contract, framing, types) _________/

## file tree

repo/
  README.md
  pyproject.toml  # 统一管理所有包
  poetry.lock  # 如果用 poetry
  
  client/        # 客户端专用代码
    __init__.py
    transport.py  # 进程通信（启动server）
    tui.py        # Textual UI
    cli.py        # 命令行入口
    
  server/        # 服务器端专用代码
    __init__.py
    handlers.py   # 请求处理
    plugins/      # 插件系统
      __init__.py
      loader.py   # 插件加载
      registry.py # 插件注册表
    main.py       # 服务器入口
    
  shared/        # 客户端和服务器共享代码
    __init__.py
    protocol.py   # JSON-RPC 消息编码/解码
    framing.py    # Content-Length 消息帧
    models.py     # 通用数据模型
    errors.py     # 协议错误定义
    
  plugins/       # 可选：示例插件目录
    example/
      __init__.py
      commands.py
  
  scripts/       # 工具脚本
    run_client.py
    run_server.py
    
  tests/         # 测试
    test_protocol.py
    test_server.py
    test_client.py
