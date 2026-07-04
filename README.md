<div align="center">

# ❄️ IceADK

**A standard, easy-to-use Agent Development Kit for Go.**

[![Go Reference](https://pkg.go.dev/badge/github.com/Arbureva/ice-adk.svg)](https://pkg.go.dev/github.com/Arbureva/ice-adk) [![Go Version](https://img.shields.io/github/go-mod/go-version/Arbureva/ice-adk)](go.mod) [![Go Report Card](https://goreportcard.com/badge/github.com/Arbureva/ice-adk)](https://goreportcard.com/report/github.com/Arbureva/ice-adk) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE) ![Dependencies](https://img.shields.io/badge/dependencies-0-success) ![Status](https://img.shields.io/badge/status-active%20development-orange)

![OpenAI](https://img.shields.io/badge/OpenAI-supported-412991) ![Anthropic](https://img.shields.io/badge/Anthropic-supported-D4A27F) ![DeepSeek](https://img.shields.io/badge/DeepSeek-supported-4D6BFE)

**English** · [简体中文](README_ch.md)

</div>

---

IceADK gives Go applications one clean way to talk to large language models. It pairs **native, protocol-faithful clients** for each provider with a **unified chat layer** that normalizes requests, responses, streaming, and tool calls — so business code is written once and runs against OpenAI, Anthropic, or DeepSeek without change.

It is built on the `database/sql` driver model: the core imports no provider package, and applications wire providers in with blank imports. The whole kit is **standard-library only — zero third-party dependencies.**

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

## ✨ Why IceADK

- **Native packages, no leaky abstraction.** `pkg/openai`, `pkg/anthropic`, and `pkg/deepseek` each speak their wire protocol directly (content blocks, SSE events, `reasoning_content`, …). Use them standalone, or through the unified layer — your choice, not the framework's.
- **One entry point, three providers.** `pkg/chat` routes a request by `Provider` and hands back a normalized result. Switching models is a config change, not a rewrite.
- **Driver-registry wiring.** Providers register themselves from `init()`; you enable them with blank imports. No build tags, no central switch statement, no edits to the core to add a backend.
- **Tools that work everywhere.** Declare a tool once against a tiny `func(ctx, json.RawMessage) (*tool.Result, error)` interface. Each driver renders it into that provider's native tool shape — the same tool set drives function-calling on all three.
- **Streaming, normalized.** A single channel of typed chunks (`text`, `thinking`, `tool_call`, `stop`, `usage`, `error`) regardless of backend.
- **Config-file friendly.** Provider configs are plain structs with `snake_case` JSON tags; `Use` also accepts raw JSON decoded straight from your app config.

## 📦 Install

```bash
go get github.com/Arbureva/ice-adk
```

Requires Go 1.25+.

## 🗂 Package layout

| Package | Role |
| --- | --- |
| `pkg/chat` | Unified entry point — `Client`, `Chat`, `ChatStream`, `Result`, chunk helpers. Imports no provider package. |
| `pkg/chat/drivers/{openai,anthropic,deepseek}` | Provider bridges. Blank-import to enable; each registers itself from `init()`. |
| `pkg/adapter` | Neutral envelopes — `Request`, `MessageAdapter`, `ChunkMessageAdapter`, and the `Provider` constants. |
| `pkg/openai` · `pkg/anthropic` · `pkg/deepseek` | Native protocol clients, usable on their own. |
| `pkg/tool` | Provider-agnostic tool abstraction — `Tool`, `Func`, `Reflect`, `Set`, `Result`. |
| `pkg/agent` | The agent loop — `Run` / `RunStream`, hooks, sub-agents via `AsTool`. Imports no provider package. |
| `pkg/agent/transcripts/{openai,anthropic,deepseek}` | Native-message transcripts. Blank-import to enable; each registers itself from `init()`. |
| `pkg/toolbox` | Progressive disclosure — fold tool groups behind one meta-tool the model opens on demand. |
| `pkg/skill` | Skills — instruction packs with optional tools, from code (`skill.New`) or `SKILL.md` directories (`skill.LoadDir`). |
| `pkg/mcp` | MCP client (stdio & Streamable HTTP) — remote tools surfaced as ordinary `tool.Tool` values. |
| `pkg/cli` | Command-line programs as tools — `cli.Command` (argv, allowlisted) and `cli.Shell`. |
| `pkg/ecode` | Shared sentinel errors. |

## 🚀 Quick start

### Configure providers

Enable each backend with a blank import, then `Use` it:

```go
import (
    "github.com/Arbureva/ice-adk/pkg/adapter"
    "github.com/Arbureva/ice-adk/pkg/chat"
    "github.com/Arbureva/ice-adk/pkg/openai"

    _ "github.com/Arbureva/ice-adk/pkg/chat/drivers/openai"   // registers the openai driver
    _ "github.com/Arbureva/ice-adk/pkg/chat/drivers/anthropic"
    _ "github.com/Arbureva/ice-adk/pkg/chat/drivers/deepseek"
)

cli := chat.New()
_ = cli.Use(adapter.OpenAI, openai.Config{APIKey: key, BaseURL: "https://api.openai.com/v1"})
```

### Non-streaming

```go
msg, _ := cli.Chat(ctx, adapter.Request{
    Provider: adapter.OpenAI,
    Data:     &openai.Request{Model: "gpt-4o", Messages: []openai.Message{openai.UserMessage("Introduce yourself.")}},
})
out, _ := chat.Result(msg) // *chat.Completion: Text / Reasoning / ToolCalls / StopReason / Usage / Raw
fmt.Println(out.Text)
```

### Streaming

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

### Tool calling

Declare a tool once; the same `Set` is advertised to the model and used to dispatch its calls:

```go
type weatherArgs struct {
    City string `json:"city" desc:"City name, e.g. Shanghai"`
}

tools := tool.NewSet(tool.Func("get_weather", "Get the current weather for a city",
    tool.Reflect(weatherArgs{}),
    func(ctx context.Context, raw json.RawMessage) (*tool.Result, error) {
        var a weatherArgs
        if err := json.Unmarshal(raw, &a); err != nil {
            return tool.Errf("bad arguments: %v", err), nil
        }
        return tool.Textf("It is 24°C and sunny in %s.", a.City), nil
    }))

msg, _ := cli.Chat(ctx, adapter.Request{
    Provider: adapter.OpenAI,
    Data:     &openai.Request{Model: "gpt-4o", Messages: msgs},
    Tools:    tools.RequestTools(),
})
out, _ := chat.Result(msg)
for _, call := range out.ToolCalls {
    res, _ := tools.Invoke(ctx, call.Name, call.Args)
    // feed res.Content back as the provider's tool / tool_result message, then call again
}
```

`chat.Result` normalizes tool calls into `[]chat.ToolCall` for every provider, so your dispatch loop is identical across backends. Only the follow-up message reconstruction is provider-shaped (OpenAI/DeepSeek `tool` messages vs. Anthropic `tool_use` / `tool_result` blocks).

### Agent

Or skip the manual loop entirely. `pkg/agent` runs the call → dispatch → append cycle until the model stops asking for tools. Message history lives in a **Transcript** — a per-provider implementation that stores *native* messages (so Anthropic thinking blocks, DeepSeek `reasoning_content`, and tool-call turns all round-trip exactly), registered the same way chat drivers are:

```go
import (
    "github.com/Arbureva/ice-adk/pkg/agent"
    _ "github.com/Arbureva/ice-adk/pkg/agent/transcripts/openai" // like drivers: blank-import to enable
)

bot := agent.New(cli, agent.WithTools(tools), agent.WithMaxSteps(8))

tr, _ := agent.NewTranscript(adapter.OpenAI, &openai.Request{
    Model:    "gpt-4o",
    Messages: []openai.Message{openai.SystemMessage("Be terse.")},
})

tr.User("What's 2+2, and what time is it?")
out, _ := bot.Run(ctx, tr)
fmt.Println(out.Text()) // out.Steps / out.Usage hold the full trace
```

You always know exactly which native types you injected — the seed request is yours, and `tr.Messages()` hands the history back in the provider's own message type. `agent.RunStream` does the same loop over the normalized chunk channel, emitting `tool_result` chunks between turns.

**Multi-agent** is one line: `agent.AsTool(name, desc, subAgent, seedFn)` wraps a specialist agent as a `tool.Tool`; every delegation gets a fresh transcript from `seedFn`, so contexts stay isolated.

### Progressive disclosure, skills, MCP, CLI

Everything above the loop is still just `tool.Tool`:

```go
// Fold tool groups behind one meta-tool; the model opens what it needs.
box := toolbox.New().
    Add(clock).                                  // always visible
    Namespace("git", "Read-only git inspection", // folded until opened
        toolbox.Tools(gitTool), toolbox.Instructions("Prefer --stat over full diffs.")).
    AddSkills(skills)                            // each skill folds into its own namespace
session := box.Clone()                           // per-conversation open/closed state
bot := agent.New(cli, agent.WithTools(session))  // *toolbox.Box satisfies agent.Tools

// Skills: instruction packs, from code or from SKILL.md directories.
skills, _ := skill.LoadDir("skills") // each subdir with a SKILL.md becomes one skill

// MCP servers: remote tools indistinguishable from local ones.
srv, _ := mcp.Dial(ctx, mcp.Stdio("npx", "-y", "@modelcontextprotocol/server-filesystem", "."))
defer srv.Close()
remote, _ := srv.ToolSet(ctx) // *tool.Set — plug straight into an agent

// Command-line programs: argv in, stdout/stderr back, no shell in between.
git := cli.Command("git", "Inspect the repo.",
    cli.AllowFirstArg("status", "log", "diff"), cli.Timeout(30*time.Second))
```

## 📂 Examples

Runnable examples live under [`example/`](example/):

- [`example/chat`](example/chat) — non-streaming chat, one file per provider.
- [`example/chat-stream`](example/chat-stream) — streaming, one file per provider.
- [`example/chat-tool`](example/chat-tool) — the two-turn tool-calling loop, one file per provider.
- [`example/agent`](example/agent) — an interactive REPL around `agent.Run`, with hooks narrating every tool round-trip.
- [`example/agent-multi`](example/agent-multi) — multi-agent coordination: a specialist wrapped by `agent.AsTool`.
- [`example/mcp`](example/mcp) — an agent driving the MCP filesystem server over stdio.
- [`example/skill-toolbox`](example/skill-toolbox) — skills loaded from `SKILL.md` plus toolbox progressive disclosure.
- [`example/http`](example/http) — the integration template: ice-adk inside an HTTP service. An OpenAI-compatible layer (`/v1/chat/completions` — point Cherry Studio, LobeChat, or any OpenAI client at it; the "model" name selects the service) plus a session-owning API, four services (skills-only, cli-only, mcp-only, multi-agent team), config-file startup, SSE streaming. Start here if you're wiring the SDK into your own backend.

## 🗺 Roadmap

IceADK aims to be a complete, standard ADK for Go. Every higher-level capability wraps the same `tool.Tool` interface and `chat` entry point — adopting them requires no change to code already written against IceADK.

- [x] Native provider clients — OpenAI · Anthropic · DeepSeek
- [x] Unified chat entry point with driver registry
- [x] Streaming with normalized chunks
- [x] Tool calling
- [x] **Agent** — the tool-calling loop with native-message transcripts, hooks, streaming, and sub-agents (`pkg/agent`)
- [x] **MCP** — Model Context Protocol tools as first-class `tool.Tool` values, stdio & Streamable HTTP (`pkg/mcp`)
- [x] **Skills** — instruction packs with tools, from code or `SKILL.md` directories (`pkg/skill`), foldable via `pkg/toolbox`
- [x] **CLI** — command-line programs as tools (`pkg/cli`), plus a REPL example for running and inspecting agents
- [ ] Structured output helpers
- [ ] More providers (Gemini, Qwen, …)

## 📄 License

Released under the [MIT License](LICENSE).

---

<div align="center">

**English** · [简体中文](README_ch.md)

</div>