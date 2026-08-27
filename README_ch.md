<div align="center">

# ❄️ Arcus

**一个标准、易用的 Go 语言 Agent 开发套件（ADK）。**

[![Go Reference](https://pkg.go.dev/badge/github.com/arbureva/arcus.svg)](https://pkg.go.dev/github.com/arbureva/arcus) [![Go Version](https://img.shields.io/github/go-mod/go-version/Arbureva/arcus)](go.mod) [![Go Report Card](https://goreportcard.com/badge/github.com/arbureva/arcus)](https://goreportcard.com/report/github.com/arbureva/arcus) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE) ![Dependencies](https://img.shields.io/badge/dependencies-0-success) ![Status](https://img.shields.io/badge/status-active%20development-orange)

![OpenAI](https://img.shields.io/badge/OpenAI-supported-412991) ![Anthropic](https://img.shields.io/badge/Anthropic-supported-D4A27F) ![DeepSeek](https://img.shields.io/badge/DeepSeek-supported-4D6BFE)

[English](README.md) · **简体中文**

</div>

---

Arcus 为 Go 应用提供一种统一、干净的方式来对接大语言模型。它把每家厂商**原生、忠于协议的客户端**，与一个对请求、响应、流式、工具调用做归一化的**统一 chat 层**配合在一起——业务代码只写一次，即可在 OpenAI、Anthropic、DeepSeek 之间无改动切换。

它采用 `database/sql` 的驱动模型：核心不导入任何厂商包，应用通过空白导入（blank import）按需启用驱动。整个套件**仅依赖标准库——零第三方依赖。**

```go
cli := chat.New()
_ = cli.Use(adapter.OpenAI, openai.Config{APIKey: key})

msg, _ := cli.Chat(ctx, adapter.Request{
    Provider: adapter.OpenAI,
    Data:     &openai.Request{Model: "gpt-4o", Messages: []openai.Message{openai.UserMessage("hi")}},
})
out, _ := chat.Result(msg)
fmt.Println(out.Text)
```

## ✨ 为什么选择 Arcus

- **原生包，不做有损抽象。** `pkg/openai`、`pkg/anthropic`、`pkg/deepseek` 各自直接讲自己的线缆协议（content block、SSE 事件、`reasoning_content` 等）。既可单独使用，也可通过统一层使用——由你决定，而非框架强加。
- **一个入口，三家厂商。** `pkg/chat` 按 `Provider` 路由请求，返回归一化结果。换模型是改配置，不是重写代码。
- **驱动注册式接入。** 厂商驱动在 `init()` 中自注册，你用空白导入启用。无 build tag，无中心化的 switch，新增后端无需改动核心。
- **工具一次声明，处处可用。** 用极简的 `func(ctx, json.RawMessage) (*tool.Result, error)` 接口声明一个工具，各驱动会把它渲染成对应厂商的原生工具格式——同一套工具集驱动三家的 function calling。
- **流式归一化。** 无论哪个后端，都是一条带类型的 chunk 通道（`text`、`thinking`、`tool_call`、`stop`、`usage`、`error`）。
- **配置文件友好。** 厂商配置是带 `snake_case` JSON tag 的普通结构体；`Use` 也接受从应用配置直接解码出的原始 JSON。

## 📦 安装

```bash
go get github.com/arbureva/arcus
```

需要 Go 1.25+。

## 🗂 包结构

| 包 | 职责 |
| --- | --- |
| `pkg/chat` | 统一入口——`Client`、`Chat`、`ChatStream`、`Result` 及 chunk 辅助函数。不导入任何厂商包。 |
| `pkg/chat/drivers/{openai,anthropic,deepseek}` | 厂商驱动桥接。空白导入即启用，各自在 `init()` 中自注册。 |
| `pkg/adapter` | 中立信封——`Request`、`MessageAdapter`、`ChunkMessageAdapter` 以及 `Provider` 常量。 |
| `pkg/openai` · `pkg/anthropic` · `pkg/deepseek` | 原生协议客户端，可独立使用。 |
| `pkg/tool` | 与厂商无关的工具抽象——`Tool`、`Func`、`Reflect`、`Set`、`Result`。 |
| `pkg/agent` | Agent 循环——`Run` / `RunStream`、钩子、通过 `AsTool` 实现子 Agent。不导入任何厂商包。 |
| `pkg/agent/transcripts/{openai,anthropic,deepseek}` | 原生消息 Transcript。空白导入即启用，各自在 `init()` 中自注册。 |
| `pkg/toolbox` | 渐进式披露——把成组工具折叠在一个元工具背后，模型按需打开。 |
| `pkg/skill` | Skills——带可选工具的指令包，可用代码构建（`skill.New`）或从 `SKILL.md` 目录加载（`skill.LoadDir`）。 |
| `pkg/mcp` | MCP 客户端（stdio 与 Streamable HTTP）——远程工具以普通 `tool.Tool` 的形态呈现。 |
| `pkg/cli` | 把命令行程序变成工具——`cli.Command`（argv 数组、可加子命令白名单）与 `cli.Shell`。 |
| `pkg/ecode` | 共享的哨兵错误。 |

## 🚀 快速上手

### 配置厂商

用空白导入启用后端，再 `Use` 它：

```go
import (
    "github.com/arbureva/arcus/pkg/adapter"
    "github.com/arbureva/arcus/pkg/chat"
    "github.com/arbureva/arcus/pkg/openai"

    _ "github.com/arbureva/arcus/pkg/chat/drivers/openai"   // 注册 openai 驱动
    _ "github.com/arbureva/arcus/pkg/chat/drivers/anthropic"
    _ "github.com/arbureva/arcus/pkg/chat/drivers/deepseek"
)

cli := chat.New()
_ = cli.Use(adapter.OpenAI, openai.Config{APIKey: key, BaseURL: "https://api.openai.com/v1"})
```

### 非流式

```go
msg, _ := cli.Chat(ctx, adapter.Request{
    Provider: adapter.OpenAI,
    Data:     &openai.Request{Model: "gpt-4o", Messages: []openai.Message{openai.UserMessage("介绍一下你自己。")}},
})
out, _ := chat.Result(msg) // *chat.Completion：Text / Reasoning / ToolCalls / StopReason / Usage / Raw
fmt.Println(out.Text)
```

### 流式

```go
ch, _ := cli.ChatStream(ctx, adapter.Request{Provider: adapter.OpenAI, Data: req})
for c := range ch {
    switch c.Kind {
    case chat.ChunkText:
        fmt.Print(chat.MustText(&c))
    case chat.ChunkThinking:
        fmt.Print(chat.MustThinking(&c))
    case chat.ChunkUsage:
        fmt.Printf("\nUsage: %d\n", chat.MustUsage(&c).TotalTokens)
    case chat.ChunkError:
        return chat.MustError(&c)
    }
}
```

### 工具调用

工具只声明一次；同一个 `Set` 既用于向模型暴露工具，也用于派发模型发起的调用：

```go
type weatherArgs struct {
    City string `json:"city" desc:"城市名，例如 Shanghai"`
}

tools := tool.NewSet(tool.Func("get_weather", "查询某城市的当前天气",
    tool.Reflect(weatherArgs{}),
    func(ctx context.Context, raw json.RawMessage) (*tool.Result, error) {
        var a weatherArgs
        if err := json.Unmarshal(raw, &a); err != nil {
            return tool.Errf("参数有误: %v", err), nil
        }
        return tool.Textf("%s 当前 24°C，晴。", a.City), nil
    }))

msg, _ := cli.Chat(ctx, adapter.Request{
    Provider: adapter.OpenAI,
    Data:     &openai.Request{Model: "gpt-4o", Messages: msgs},
    Tools:    tools.RequestTools(),
})
out, _ := chat.Result(msg)
for _, call := range out.ToolCalls {
    res, _ := tools.Invoke(ctx, call.Name, call.Args)
    // 把 res.Content 作为该厂商的 tool / tool_result 消息回填，再发起下一轮请求
}
```

`chat.Result` 会把所有厂商的工具调用都归一成 `[]chat.ToolCall`，因此你的派发循环在各后端之间完全一致。只有“回填消息的重建”是厂商相关的（OpenAI/DeepSeek 用 `tool` 角色消息，Anthropic 用 `tool_use` / `tool_result` 内容块）。

### Agent

也可以完全省掉手写循环。`pkg/agent` 会自动执行“请求 → 派发工具 → 回填结果”的循环，直到模型不再调用工具。消息历史保存在 **Transcript** 中——按厂商各自实现、存放**原生**消息类型（Anthropic 的 thinking 块、DeepSeek 的 `reasoning_content`、工具调用轮次都能原样往返），注册方式与 chat 驱动完全一致：

```go
import (
    "github.com/arbureva/arcus/pkg/agent"
    _ "github.com/arbureva/arcus/pkg/agent/transcripts/openai" // 与驱动同款：空白导入即启用
)

bot := agent.New(cli, agent.WithTools(tools), agent.WithMaxSteps(8))

tr, _ := agent.NewTranscript(adapter.OpenAI, &openai.Request{
    Model:    "gpt-4o",
    Messages: []openai.Message{openai.SystemMessage("回答尽量简短。")},
})

tr.User("2+2 等于几？现在几点？")
out, _ := bot.Run(ctx, tr)
fmt.Println(out.Text()) // out.Steps / out.Usage 保留完整轨迹
```

你始终清楚自己注入的是哪家的原生类型——种子请求由你构造，`tr.Messages()` 也会以该厂商自己的消息类型交还全部历史。`agent.RunStream` 在归一化 chunk 通道上跑同一个循环，并在轮次之间发出 `tool_result` chunk。

**多 Agent 协同**只需一行：`agent.AsTool(name, desc, subAgent, seedFn)` 把专家 Agent 包装成 `tool.Tool`；每次委派都会通过 `seedFn` 拿到全新的 Transcript，上下文彼此隔离。

### 渐进式披露、Skills、MCP、CLI

循环之上的一切依然只是 `tool.Tool`：

```go
// 把成组工具折叠在一个元工具背后，模型按需打开。
box := toolbox.New().
    Add(clock).                                  // 常驻可见
    Namespace("git", "只读 git 检查",             // 打开之前对模型不可见
        toolbox.Tools(gitTool), toolbox.Instructions("优先使用 --stat 而非完整 diff。")).
    AddSkills(skills)                            // 每个 skill 折叠成独立命名空间
session := box.Clone()                           // 每个会话一份打开/折叠状态
bot := agent.New(cli, agent.WithTools(session))  // *toolbox.Box 直接满足 agent.Tools

// Skills：指令包，代码构建或从 SKILL.md 目录加载。
skills, _ := skill.LoadDir("skills") // 每个含 SKILL.md 的子目录即一个 skill

// MCP 服务器：远程工具与本地工具毫无二致。
srv, _ := mcp.Dial(ctx, mcp.Stdio("npx", "-y", "@modelcontextprotocol/server-filesystem", "."))
defer srv.Close()
remote, _ := srv.ToolSet(ctx) // *tool.Set——直接塞给 Agent

// 命令行程序：argv 进、stdout/stderr 回，中间没有 shell。
git := cli.Command("git", "检查仓库。",
    cli.AllowFirstArg("status", "log", "diff"), cli.Timeout(30*time.Second))
```

## 📂 示例

可运行示例位于 [`example/`](example/) 下：

- [`example/chat`](example/chat) —— 非流式对话，每个厂商一个文件。
- [`example/chat-stream`](example/chat-stream) —— 流式，每个厂商一个文件。
- [`example/chat-tool`](example/chat-tool) —— 两轮工具调用循环，每个厂商一个文件。
- [`example/agent`](example/agent) —— 围绕 `agent.Run` 的交互式 REPL，钩子实时播报每一次工具往返。
- [`example/agent-multi`](example/agent-multi) —— 多 Agent 协同：用 `agent.AsTool` 包装专家 Agent。
- [`example/mcp`](example/mcp) —— Agent 通过 stdio 驱动 MCP 文件系统服务器。
- [`example/skill-toolbox`](example/skill-toolbox) —— 从 `SKILL.md` 加载 skill，配合 toolbox 渐进式披露。
- [`example/http`](example/http) —— 接入模板：在 HTTP 服务中使用 arcus。标准 OpenAI 兼容层（`/v1/chat/completions`，Cherry Studio、LobeChat 等现成客户端直连，"模型名"即服务路由）+ 服务端管会话的自有 API，四个服务（只挂 Skills / 只挂 CLI / 只挂 MCP / 多 Agent 协同）、配置文件启动、SSE 流式。想把 SDK 接进自己后端的，从这里开始抄。

## 🗺 路线图

Arcus 致力于成为一个完整、标准的 Go 语言 ADK。所有上层能力都包裹同一个 `tool.Tool` 接口与 `chat` 入口——引入它们无需改动已基于 Arcus 写好的代码。

- [x] 原生厂商客户端 —— OpenAI · Anthropic · DeepSeek
- [x] 带驱动注册的统一 chat 入口
- [x] 归一化 chunk 的流式
- [x] 工具调用
- [x] **Agent** —— 带原生消息 Transcript、钩子、流式与子 Agent 的工具调用循环（`pkg/agent`）
- [x] **MCP** —— MCP 工具作为一等 `tool.Tool` 接入，支持 stdio 与 Streamable HTTP（`pkg/mcp`）
- [x] **Skills** —— 带工具的指令包，代码构建或 `SKILL.md` 目录加载（`pkg/skill`），可经 `pkg/toolbox` 折叠
- [x] **Cli** —— 命令行程序即工具（`pkg/cli`），另附用于运行与观察 Agent 的 REPL 示例
- [ ] 结构化输出辅助
- [ ] 更多厂商（Gemini、Qwen……）

## 📄 许可证

基于 [MIT 许可证](LICENSE) 发布。

---

<div align="center">

[English](README.md) · **简体中文**

</div>