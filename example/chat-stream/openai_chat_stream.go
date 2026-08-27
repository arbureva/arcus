package main

import (
	"context"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/openai"
)

// OpenAiChatStream opens a streaming completion against an OpenAI-compatible
// endpoint. Stream: true is required on the native request.
func OpenAiChatStream(cli *chat.Client) (<-chan adapter.ChunkMessageAdapter, error) {
	if err := cli.Use(adapter.OpenAI, openai.Config{
		APIKey:  "test",
		BaseURL: "http://studio:11434/v1",
	}); err != nil {
		return nil, err
	}

	return cli.ChatStream(context.Background(), adapter.Request{
		Provider: adapter.OpenAI,
		Data: &openai.Request{
			Model: "gpt-oss:20b",
			Messages: []openai.Message{
				openai.UserMessage("写一篇1000字的文章"),
			},
			Stream: true,
		},
	})
}
