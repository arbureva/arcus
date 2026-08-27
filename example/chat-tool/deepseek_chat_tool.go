package main

import (
	"context"
	"os"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/deepseek"
	"github.com/arbureva/arcus/pkg/tool"
)

// DeepseekChatTool mirrors OpenAiChatTool: DeepSeek speaks the same
// assistant-tool_calls / tool-role-message protocol, so only the request type
// and the message constructors differ. The shared chat.ToolCall shape returned
// by chat.Result means the dispatch loop is identical across the two.
func DeepseekChatTool(cli *chat.Client, tools *tool.Set) (*chat.Completion, error) {
	if err := cli.Use(adapter.Deepseek, deepseek.Config{
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		BaseURL: "https://api.deepseek.com",
	}); err != nil {
		return nil, err
	}

	messages := []deepseek.Message{
		deepseek.UserMessage("上海的天气怎么样？另外你叫什么名字？"),
	}

	newReq := func() adapter.Request {
		return adapter.Request{
			Provider: adapter.Deepseek,
			Data:     &deepseek.Request{Model: "deepseek-v4-flash", Messages: messages},
			Tools:    tools.RequestTools(),
		}
	}

	res, err := cli.Chat(context.Background(), newReq())
	if err != nil {
		return nil, err
	}
	out, _ := chat.Result(res)

	if len(out.ToolCalls) > 0 {
		asst := deepseek.Message{Role: deepseek.RoleAssistant}
		for _, call := range out.ToolCalls {
			asst.ToolCalls = append(asst.ToolCalls, deepseek.ToolCall{
				ID:       call.ID,
				Type:     "function",
				Function: deepseek.FunctionCall{Name: call.Name, Arguments: string(call.Args)},
			})
		}
		messages = append(messages, asst)

		for _, call := range out.ToolCalls {
			result, err := tools.Invoke(context.Background(), call.Name, call.Args)
			if err != nil {
				result = tool.Errf("tool %q could not be run: %v", call.Name, err)
			}
			messages = append(messages, deepseek.ToolMessage(call.ID, result.Content))
		}

		res, err = cli.Chat(context.Background(), newReq())
		if err != nil {
			return nil, err
		}
		out, _ = chat.Result(res)
	}

	return out, nil
}
