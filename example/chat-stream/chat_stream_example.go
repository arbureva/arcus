package main

import (
	"fmt"

	"github.com/arbureva/arcus/pkg/chat"

	_ "github.com/arbureva/arcus/pkg/chat/drivers/anthropic"
	_ "github.com/arbureva/arcus/pkg/chat/drivers/deepseek"
	_ "github.com/arbureva/arcus/pkg/chat/drivers/openai"
)

func main() {
	cli := chat.New()

	ch, err := AnthropicChatStream(cli)
	//ch, err := OpenAiChatStream(cli)
	//ch, err := DeepseekChatStream(cli)
	if err != nil {
		panic(err)
	}

	// The consume loop is provider-agnostic: the Kind discriminates the chunk
	// and the Must* helpers assert the matching payload type. ChunkThinking is
	// handled too, since reasoning models (DeepSeek V4, Anthropic thinking)
	// stream chain-of-thought ahead of the answer.
	for c := range ch {
		switch c.Kind {
		case chat.ChunkText:
			fmt.Print(chat.MustText(&c))

		case chat.ChunkThinking:
			fmt.Print(chat.MustThinking(&c))

		case chat.ChunkToolCall:
			f := chat.MustToolCall(&c)
			fmt.Printf("Use Tool Call: %s\n", f.Name)

		case chat.ChunkStop:
			fmt.Printf("\nChunkStop: %s\n", chat.MustStop(&c))

		case chat.ChunkUsage:
			usg := chat.MustUsage(&c)
			fmt.Printf("\nUsage: %d\n", usg.TotalTokens)

		case chat.ChunkError:
			err := chat.MustError(&c)
			fmt.Printf("\nError: %s\n", err)
		}
	}
}
