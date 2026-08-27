package main

import (
	"context"
	"os"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/anthropic"
	"github.com/arbureva/arcus/pkg/chat"
)

func AnthropicChat(cli *chat.Client) (*adapter.MessageAdapter, error) {
	err := cli.Use(adapter.Anthropic, anthropic.Config{
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		BaseURL: "https://api.deepseek.com/anthropic",
	})
	if err != nil {
		return nil, err
	}

	res, err := cli.Chat(context.Background(), adapter.Request{
		Provider: adapter.Anthropic,
		Data: &anthropic.Request{
			Model: "deepseek-v4-flash",
			Messages: []anthropic.Message{
				anthropic.UserText("介绍一下你自己？"),
			},
			MaxTokens: 1000,
		},
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}
