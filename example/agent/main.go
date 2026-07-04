// Command agent is a minimal REPL around agent.Run: type a line, watch the
// model think / call tools / answer, repeat. It doubles as the "CLI" story
// from the observing side — pkg/cli turns command-line programs into tools
// the model can run, while this example is a command-line program you run to
// observe the agent.
//
// Point Config at any OpenAI-compatible endpoint (Ollama, vLLM, the real
// thing) and swap the provider by changing three lines — the agent loop and
// the tools never change.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Arbureva/ice-adk/pkg/adapter"
	"github.com/Arbureva/ice-adk/pkg/agent"
	"github.com/Arbureva/ice-adk/pkg/chat"
	"github.com/Arbureva/ice-adk/pkg/openai"
	"github.com/Arbureva/ice-adk/pkg/tool"

	// Drivers and transcripts self-register via init(); pick the ones you use.
	_ "github.com/Arbureva/ice-adk/pkg/agent/transcripts/openai"
	_ "github.com/Arbureva/ice-adk/pkg/chat/drivers/openai"
)

type clockArgs struct{}

type addArgs struct {
	A float64 `json:"a" desc:"first addend"`
	B float64 `json:"b" desc:"second addend"`
}

func main() {
	cli := chat.New()
	if err := cli.Use(adapter.OpenAI, openai.Config{
		APIKey:  "test",
		BaseURL: "http://studio:11434/v1",
	}); err != nil {
		panic(err)
	}

	tools := tool.NewSet(
		tool.Func("now", "Current local time.", tool.Reflect(clockArgs{}),
			func(context.Context, json.RawMessage) (*tool.Result, error) {
				return tool.Text(time.Now().Format(time.RFC3339)), nil
			}),
		tool.Func("add", "Add two numbers exactly.", tool.Reflect(addArgs{}),
			func(_ context.Context, raw json.RawMessage) (*tool.Result, error) {
				var a addArgs
				if err := json.Unmarshal(raw, &a); err != nil {
					return tool.Errf("bad arguments: %v", err), nil
				}
				return tool.Textf("%g", a.A+a.B), nil
			}),
	)

	// Hooks are the observation surface: every completion and every tool
	// round-trip flows through here, so the REPL can narrate the loop.
	bot := agent.New(cli,
		agent.WithTools(tools),
		agent.WithMaxSteps(8),
		agent.WithHooks(agent.Hooks{
			OnToolCall: func(step int, call chat.ToolCall) {
				fmt.Printf("  [step %d] -> %s(%s)\n", step, call.Name, call.Args)
			},
			OnToolResult: func(step int, call chat.ToolCall, res *tool.Result, err error) {
				switch {
				case err != nil:
					fmt.Printf("  [step %d] <- %s failed: %v\n", step, call.Name, err)
				case res.IsError:
					fmt.Printf("  [step %d] <- %s error: %s\n", step, call.Name, res.Content)
				default:
					fmt.Printf("  [step %d] <- %s: %s\n", step, call.Name, res.Content)
				}
			},
		}),
	)

	// One transcript per conversation. The seed request is the template —
	// model, system prompt, sampling parameters — and the transcript owns the
	// message history from here on, in the provider's own native types.
	tr, err := agent.NewTranscript(adapter.OpenAI, &openai.Request{
		Model: "gpt-oss:20b",
		Messages: []openai.Message{
			openai.SystemMessage("You are a terse assistant. Use tools when they help."),
		},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("agent REPL — empty line to quit")
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("you> ")
		if !sc.Scan() {
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			break
		}

		tr.User(line)
		out, err := bot.Run(context.Background(), tr)
		if err != nil {
			fmt.Println("error:", err)
			continue
		}
		fmt.Printf("bot> %s\n", out.Text())
		fmt.Printf("     (%d step(s), %d tokens)\n", len(out.Steps), out.Usage.TotalTokens)
	}
}
