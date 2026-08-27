package main

import (
	"context"
	"os"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/anthropic"
	"github.com/arbureva/arcus/pkg/chat"
)

// AnthropicChatStream opens a streaming completion over Anthropic's Messages
// API. MaxTokens is mandatory; size it for the expected answer length. Stream:
// true is required on the native request.
func AnthropicChatStream(cli *chat.Client) (<-chan adapter.ChunkMessageAdapter, error) {
	if err := cli.Use(adapter.Anthropic, anthropic.Config{
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		BaseURL: "https://api.deepseek.com/anthropic",
	}); err != nil {
		return nil, err
	}

	return cli.ChatStream(context.Background(), adapter.Request{
		Provider: adapter.Anthropic,
		Data: &anthropic.Request{
			Model: "deepseek-v4-flash",
			Messages: []anthropic.Message{
				anthropic.UserText("写一篇1000字的文章"),
			},
			MaxTokens: 2000,
			Stream:    true,
		},
	})
}
