// Command skill-toolbox demonstrates progressive disclosure. A toolbox.Box
// keeps most tools folded behind a single meta-tool ("open_toolbox"): the
// model first sees one-line summaries, and only after opening a namespace do
// its tools and instructions enter the context. Skills — instruction packs
// loaded from SKILL.md directories or built with skill.New — plug into the
// same mechanism: AddSkill folds each skill into its own namespace, and
// opening it returns the skill's instructions as the tool result.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Arbureva/ice-adk/pkg/adapter"
	"github.com/Arbureva/ice-adk/pkg/agent"
	"github.com/Arbureva/ice-adk/pkg/chat"
	"github.com/Arbureva/ice-adk/pkg/openai"
	"github.com/Arbureva/ice-adk/pkg/skill"
	"github.com/Arbureva/ice-adk/pkg/tool"
	"github.com/Arbureva/ice-adk/pkg/toolbox"

	_ "github.com/Arbureva/ice-adk/pkg/agent/transcripts/openai"
	_ "github.com/Arbureva/ice-adk/pkg/chat/drivers/openai"
)

type wordCountArgs struct {
	Text string `json:"text" desc:"text to measure"`
}

func main() {
	// Skills load from disk: every subdirectory with a SKILL.md becomes one
	// skill (front-matter -> Name/Description, body -> Instructions).
	skills, err := skill.LoadDir("skills")
	if err != nil {
		panic(err)
	}

	// Or build one in code, with tools attached.
	counter := skill.MustNew(skill.Skill{
		Name:         "word-count",
		Description:  "Measure article length before submitting.",
		Instructions: "Always report both rune count and a reading-time estimate (300 chars/min).",
		Tools: []tool.Tool{
			tool.Func("count_runes", "Count runes in a text.", tool.Reflect(wordCountArgs{}),
				func(_ context.Context, raw json.RawMessage) (*tool.Result, error) {
					var a wordCountArgs
					if err := json.Unmarshal(raw, &a); err != nil {
						return tool.Errf("bad arguments: %v", err), nil
					}
					return tool.Textf("%d runes", len([]rune(a.Text))), nil
				}),
		},
	})

	// The Box: one always-visible meta-tool plus folded namespaces. Until a
	// namespace is opened, the model pays zero context for its contents —
	// only the one-line summary inside open_toolbox's own description.
	box := toolbox.New().
		AddSkills(skills).
		AddSkill(counter)

	// Boxes hold per-conversation state (what is open); Clone one per session.
	session := box.Clone()

	cli := chat.New()
	if err := cli.Use(adapter.OpenAI, openai.Config{
		APIKey:  "test",
		BaseURL: "http://studio:11434/v1",
	}); err != nil {
		panic(err)
	}

	bot := agent.New(cli,
		agent.WithTools(session), // *toolbox.Box satisfies agent.Tools directly
		agent.WithMaxSteps(8),
		agent.WithHooks(agent.Hooks{
			OnToolCall: func(step int, call chat.ToolCall) {
				fmt.Printf("[step %d] %s %s (open: %v)\n", step, call.Name, call.Args, session.Active())
			},
		}),
	)

	tr, err := agent.NewTranscript(adapter.OpenAI, &openai.Request{
		Model: "gpt-oss:20b",
		Messages: []openai.Message{
			openai.SystemMessage("You draft WeChat articles. Consult your toolbox for house style and measurement tools before answering."),
			openai.UserMessage("为「用 Go 写一个零依赖 LLM SDK」写一段公众号文章开头，并给出字数。"),
		},
	})
	if err != nil {
		panic(err)
	}

	out, err := bot.Run(context.Background(), tr)
	if err != nil {
		panic(err)
	}
	fmt.Println(out.Text())
}
