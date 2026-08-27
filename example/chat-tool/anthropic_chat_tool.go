package main

import (
	"context"
	"os"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/anthropic"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/tool"
)

// AnthropicChatTool runs the same logical loop as the OpenAI/DeepSeek versions
// but over Anthropic's block-structured protocol, which differs in two places:
//
//   - the assistant's request to call a tool is echoed back as tool_use content
//     blocks inside an assistant message (not an OpenAI-style tool_calls array);
//   - every tool_result block goes back together in a *single* user message —
//     Anthropic pairs results to calls by tool_use_id, so they are not separate
//     tool-role messages.
//
// Because chat.Result already normalizes tool calls into []chat.ToolCall, the
// only provider-specific work here is rebuilding those two message turns.
func AnthropicChatTool(cli *chat.Client, tools *tool.Set) (*chat.Completion, error) {
	if err := cli.Use(adapter.Anthropic, anthropic.Config{
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		BaseURL: "https://api.deepseek.com/anthropic",
	}); err != nil {
		return nil, err
	}

	messages := []anthropic.Message{
		anthropic.UserText("上海的天气怎么样？另外你叫什么名字？"),
	}

	newReq := func() adapter.Request {
		return adapter.Request{
			Provider: adapter.Anthropic,
			Data: &anthropic.Request{
				Model:     "deepseek-v4-flash",
				Messages:  messages,
				MaxTokens: 1000,
			},
			Tools: tools.RequestTools(),
		}
	}

	res, err := cli.Chat(context.Background(), newReq())
	if err != nil {
		return nil, err
	}
	out, _ := chat.Result(res)

	if len(out.ToolCalls) > 0 {
		// 1. echo the assistant turn as tool_use blocks (id/name/input must
		//    match what the model produced so results can be correlated).
		useBlocks := make([]anthropic.ContentBlock, 0, len(out.ToolCalls))
		for _, call := range out.ToolCalls {
			useBlocks = append(useBlocks, anthropic.ToolUseBlock(call.ID, call.Name, call.Args))
		}
		messages = append(messages, anthropic.AssistantBlocks(useBlocks...))

		// 2. run each tool and collect tool_result blocks; IsError lets the
		//    model see a failure without aborting the turn.
		resultBlocks := make([]anthropic.ContentBlock, 0, len(out.ToolCalls))
		for _, call := range out.ToolCalls {
			result, err := tools.Invoke(context.Background(), call.Name, call.Args)
			if err != nil {
				result = tool.Errf("tool %q could not be run: %v", call.Name, err)
			}
			resultBlocks = append(resultBlocks, anthropic.ToolResultText(call.ID, result.Content, result.IsError))
		}
		// all results ride back in one user message
		messages = append(messages, anthropic.UserBlocks(resultBlocks...))

		res, err = cli.Chat(context.Background(), newReq())
		if err != nil {
			return nil, err
		}
		out, _ = chat.Result(res)
	}

	return out, nil
}
