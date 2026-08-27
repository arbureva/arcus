// Command agent-multi shows multi-agent coordination the arcus way: a
// sub-agent is just another tool. agent.AsTool wraps a specialist Agent as a
// tool.Tool; when the coordinator delegates, the seed function builds a fresh
// Transcript so every delegation runs in its own isolated context. No routing
// framework, no message bus — it is function calling all the way down.
package main

import (
	"context"
	"fmt"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/agent"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/openai"
	"github.com/arbureva/arcus/pkg/tool"

	_ "github.com/arbureva/arcus/pkg/agent/transcripts/openai"
	_ "github.com/arbureva/arcus/pkg/chat/drivers/openai"
)

const model = "gpt-oss:20b"

func main() {
	cli := chat.New()
	if err := cli.Use(adapter.OpenAI, openai.Config{
		APIKey:  "test",
		BaseURL: "http://studio:11434/v1",
	}); err != nil {
		panic(err)
	}

	// The specialist: no tools of its own, just a tight system prompt. Its
	// seed function is called once per delegation, so parallel or repeated
	// tasks never leak context into each other.
	translator := agent.New(cli, agent.WithMaxSteps(2))
	translatorSeed := func() (agent.Transcript, error) {
		return agent.NewTranscript(adapter.OpenAI, &openai.Request{
			Model: model,
			Messages: []openai.Message{
				openai.SystemMessage("You translate the given task text into elegant Classical Chinese (文言文). Reply with the translation only."),
			},
		})
	}

	// The coordinator sees the specialist as one more function to call.
	tools := tool.NewSet(
		agent.AsTool("translate_classical",
			"Translate a passage into Classical Chinese. Input: the passage.",
			translator, translatorSeed),
	)

	coordinator := agent.New(cli,
		agent.WithTools(tools),
		agent.WithHooks(agent.Hooks{
			OnToolCall: func(step int, call chat.ToolCall) {
				fmt.Printf("[delegate] %s %s\n", call.Name, call.Args)
			},
		}),
	)

	tr, err := agent.NewTranscript(adapter.OpenAI, &openai.Request{
		Model: model,
		Messages: []openai.Message{
			openai.SystemMessage("You are an editor. When the user wants Classical Chinese, delegate to your translator tool, then present its output with one sentence of commentary."),
			openai.UserMessage("请把“代码写得越少，睡得越香”翻成文言文。"),
		},
	})
	if err != nil {
		panic(err)
	}

	out, err := coordinator.Run(context.Background(), tr)
	if err != nil {
		panic(err)
	}
	fmt.Println(out.Text())
}
