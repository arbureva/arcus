package main

import (
	"context"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/openai"
)

func OpenAiChat(cli *chat.Client) (*adapter.MessageAdapter, error) {
	err := cli.Use(adapter.OpenAI, openai.Config{
		APIKey:  "test",
		BaseURL: "http://studio:11434/v1",
	})
	if err != nil {
		return nil, err
	}

	res, err := cli.Chat(context.Background(), adapter.Request{
		Provider: adapter.OpenAI,
		Data: &openai.Request{
			Model: "gpt-oss:20b",
			Messages: []openai.Message{
				openai.UserMessage("Introduce yourself in one sentence."),
			},
		},
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}
