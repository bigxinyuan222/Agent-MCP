# AI-Agent

基于 Go、Bubble Tea 和千问 `qwen-plus` 的终端聊天 Agent，支持本地工具和 MCP stdio 工具。

## 启动

需要 Go 1.26.4 或更高版本。在项目目录打开 PowerShell：

```powershell
$env:DASHSCOPE_API_KEY = "你的 DashScope API Key"
go run .
```

进入对话后输入：

```text
打开浏览器搜索天气
打开浏览器搜索北京今天天气
打开 https://www.example.com
```

Agent 会选择 MCP 工具，在这台电脑的默认浏览器打开必应搜索结果或指定网页。未指定城市时直接搜索“天气”。界面会显示已加载的 MCP 工具数量，以及每次调用的参数和结果。浏览器服务无需额外安装 Node.js、Python 或浏览器驱动。

调用链：用户消息 → 千问选择工具 → MCP 客户端 → stdio 浏览器 MCP 子进程 → 系统默认浏览器 → 工具结果回传千问 → 回答。

浏览器工具只负责打开页面，不读取网页内容，因此打开成功不代表 Agent 已获取实时天气数据。

## MCP 配置

默认读取当前工作目录的 [mcp.json](mcp.json)。该文件不存在时，自动启用内置浏览器服务：

```json
{
  "mcpServers": {
    "browser": {
      "command": "@self",
      "args": ["--mcp-browser-server"]
    }
  }
}
```

`@self` 指当前 Agent 可执行文件，`go run .` 和编译后运行均可使用。`--mcp-browser-server` 单独运行时仅通过 stdin/stdout 提供 MCP 服务，不启动聊天界面，也不需要 API Key。

可以指定其他配置文件：

```powershell
go run . --mcp-config .\mcp.local.json
```

在 `mcpServers` 中添加其他 stdio 服务即可自动发现并注册其工具：

```json
{
  "mcpServers": {
    "browser": {
      "command": "@self",
      "args": ["--mcp-browser-server"]
    },
    "custom": {
      "command": "C:\\path\\to\\your-mcp-server.exe",
      "args": ["--stdio"],
      "env": {"EXAMPLE_SETTING": "value"},
      "disabled": false
    }
  }
}
```

`command` 是可执行文件名或完整路径，参数逐项写入 `args`，不经过 shell。Windows 的 `.cmd` / `.bat` 包装脚本需要显式配置对应解释器，或改用其实际可执行文件。`env` 覆盖子进程环境变量；`disabled: true` 跳过该服务；`{"mcpServers": {}}` 关闭全部 MCP 服务。当前支持 stdio 传输。

单个服务连接失败会在聊天界面提示，其余工具仍可使用。连接与发现限时 15 秒，每次 MCP 调用限时 30 秒，退出程序时会关闭 MCP 子进程。

## 编译和验证

```powershell
go test ./...
go vet ./...
go build -o ai-agent.exe .
.\ai-agent.exe
```

测试覆盖真实 MCP stdio 子进程通信、浏览器搜索参数编码、无效网址与启动失败、服务发现与关闭，以及模拟模型选择 MCP 工具后接收调用结果的完整对话。自动测试使用模拟浏览器，不会弹出网页，也不调用付费模型 API。

MCP 实现使用[官方 Go SDK](https://github.com/modelcontextprotocol/go-sdk)。
