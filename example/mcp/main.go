// Command mcp connects to an MCP server and hands its tools to an agent.
// From the loop's point of view an MCP tool is indistinguishable from a local
// tool.Func — Definition() advertises it, Invoke() proxies tools/call over
// the wire. Swap Stdio for mcp.HTTP("https://...") to talk to a remote
// Streamable-HTTP server; nothing else changes.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Arbureva/ice-adk/pkg/adapter"
	"github.com/Arbureva/ice-adk/pkg/agent"
	"github.com/Arbureva/ice-adk/pkg/chat"
	"github.com/Arbureva/ice-adk/pkg/mcp"
	"github.com/Arbureva/ice-adk/pkg/openai"

	_ "github.com/Arbureva/ice-adk/pkg/agent/transcripts/openai"
	_ "github.com/Arbureva/ice-adk/pkg/chat/drivers/openai"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Launch the reference filesystem server as a subprocess (stdio
	// transport). WithToolPrefix namespaces its tools — read_text_file
	// becomes fs_read_text_file — so several servers can coexist in one Set.
	srv, err := mcp.Dial(ctx,
		mcp.Stdio("npx", "-y", "@modelcontextprotocol/server-filesystem", "."),
		mcp.WithToolPrefix("fs_"),
	)
	if err != nil {
		panic(err)
	}
	defer srv.Close()
	fmt.Printf("connected: %s %s (protocol %s)\n", srv.ServerInfo().Name, srv.ServerInfo().Version, srv.NegotiatedVersion())

	// ToolSet lists the server's tools once and wraps them into a *tool.Set —
	// which already satisfies agent.Tools.
	tools, err := srv.ToolSet(ctx)
	if err != nil {
		panic(err)
	}

	cli := chat.New()
	if err := cli.Use(adapter.OpenAI, openai.Config{
		APIKey:  "test",
		BaseURL: "http://studio:11434/v1",
	}); err != nil {
		panic(err)
	}

	bot := agent.New(cli, agent.WithTools(tools), agent.WithMaxSteps(100))

	system := "You have filesystem tools. Be precise and read before you claim."
	if extra := srv.Instructions(); extra != "" {
		system += "\n\n" + extra // many servers ship usage notes in the handshake
	}

	tr, err := agent.NewTranscript(adapter.OpenAI, &openai.Request{
		Model: "gpt-oss:20b",
		Messages: []openai.Message{
			openai.SystemMessage(system),
			openai.UserMessage("List the Go files in the current directory and summarize what this project is."),
		},
	})
	if err != nil {
		panic(err)
	}

	out, err := bot.Run(ctx, tr)
	if err != nil {
		panic(err)
	}
	fmt.Println(out.Text())
}
