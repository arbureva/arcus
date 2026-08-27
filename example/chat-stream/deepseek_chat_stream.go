package main

import (
	"context"
	"os"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/deepseek"
)

// DeepseekChatStream opens a streaming completion against DeepSeek. Two
// provider-specific details worth noting:
//
//   - StreamOptions.IncludeUsage must be set to receive the trailing usage
//     chunk; without it the stream ends after the content with no ChunkUsage.
//   - thinking mode is on by default for V4 models, so the consume loop will
//     see ChunkThinking fragments before the answer text.
func DeepseekChatStream(cli *chat.Client) (<-chan adapter.ChunkMessageAdapter, error) {
	if err := cli.Use(adapter.Deepseek, deepseek.Config{
		APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
		BaseURL: "https://api.deepseek.com",
	}); err != nil {
		return nil, err
	}

	return cli.ChatStream(context.Background(), adapter.Request{
		Provider: adapter.Deepseek,
		Data: &deepseek.Request{
			Model: "deepseek-v4-flash",
			Messages: []deepseek.Message{
				deepseek.UserMessage("写一篇1000字的文章"),
			},
			Stream:        true,
			StreamOptions: &deepseek.StreamOptions{IncludeUsage: true},
		},
	})
}
