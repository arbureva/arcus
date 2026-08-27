package main

import (
	"context"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/openai"
	"github.com/arbureva/arcus/pkg/tool"
)

// OpenAiChatTool runs the standard two-turn function-calling loop against an
// OpenAI-compatible endpoint:
//
//  1. advertise the tools and let the model decide;
//  2. echo the assistant's tool_calls turn, run each tool, append the results
//     as native tool-role messages, then call again for the final answer.
func OpenAiChatTool(cli *chat.Client, tools *tool.Set) (*chat.Completion, error) {
	if err := cli.Use(adapter.OpenAI, openai.Config{
		APIKey:  "test",
		BaseURL: "http://studio:11434/v1",
	}); err != nil {
		return nil, err
	}

	messages := []openai.Message{
		openai.UserMessage("What's the weather in Shanghai? And what's your name? "),
	}

	// newReq snapshots the current message history; the driver renders
	// tools.RequestTools() into OpenAI's native function-tool list.
	newReq := func() adapter.Request {
		return adapter.Request{
			Provider: adapter.OpenAI,
			Data:     &openai.Request{Model: "gpt-oss:20b", Messages: messages},
			Tools:    tools.RequestTools(),
		}
	}

	res, err := cli.Chat(context.Background(), newReq())
	if err != nil {
		return nil, err
	}
	out, _ := chat.Result(res)

	if len(out.ToolCalls) > 0 {
		// echo the assistant's tool-call turn back into the history
		asst := openai.Message{Role: openai.RoleAssistant}
		for _, call := range out.ToolCalls {
			asst.ToolCalls = append(asst.ToolCalls, openai.ToolCall{
				ID:       call.ID,
				Type:     "function",
				Function: openai.FunctionCall{Name: call.Name, Arguments: string(call.Args)},
			})
		}
		messages = append(messages, asst)

		// run each tool via the same Set and append a native tool message
		for _, call := range out.ToolCalls {
			result, err := tools.Invoke(context.Background(), call.Name, call.Args)
			if err != nil {
				// unknown tool / host failure — surface it to the model as a result
				result = tool.Errf("tool %q could not be run: %v", call.Name, err)
			}
			messages = append(messages, openai.ToolMessage(call.ID, result.Content))
		}

		res, err = cli.Chat(context.Background(), newReq())
		if err != nil {
			return nil, err
		}
		out, _ = chat.Result(res)
	}

	return out, nil
}
