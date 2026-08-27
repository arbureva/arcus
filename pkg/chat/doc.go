// Package chat is Arcus's unified chat entry point. Business code talks only
// to chat with adapter types and never selects a provider package directly:
//
//   - Input is an adapter.Request whose Data carries the provider-native
//     request (e.g. *openai.Request) — request parameters are inherently
//     provider-specific, so they stay native and are routed by Provider.
//   - Non-streaming output is an adapter.MessageAdapter whose Data is a
//     normalized *Completion (Text / Reasoning / ToolCalls / StopReason /
//     Usage, plus the native object on Raw).
//   - Streaming output is a channel of adapter.ChunkMessageAdapter with a
//     normalized Kind and a Kind-specific Data payload.
//
// Providers are wired with the database/sql-style driver pattern: each bridge
// registers itself from init, and the application selects providers with blank
// imports. The chat package itself imports no provider package.
//
//	import (
//		"github.com/arbureva/arcus/pkg/chat"
//		"github.com/arbureva/arcus/pkg/openai"
//		_ "github.com/arbureva/arcus/pkg/chat/drivers/openai"   // registers the openai driver
//		_ "github.com/arbureva/arcus/pkg/chat/drivers/deepseek" // registers the deepseek driver
//	)
//
//	cli := chat.New()
//	_ = cli.Use(adapter.OpenAI, openai.Config{APIKey: cfg.OpenAI.Key})
//
//	// Non-streaming
//	msg, _ := cli.Chat(ctx, adapter.Request{
//		Provider: adapter.OpenAI,
//		Data:     &openai.Request{Model: "gpt-4o", Messages: []openai.Message{openai.UserMessage("hi")}},
//	})
//	out, _ := chat.Result(msg)
//	fmt.Println(out.Text)
//
//	// Streaming
//	ch, _ := cli.ChatStream(ctx, adapter.Request{Provider: adapter.OpenAI, Data: req})
//	for c := range ch {
//		switch c.Kind {
//		case chat.ChunkText:
//			fmt.Print(c.Data.(string))
//		case chat.ChunkToolCall:
//			frag := c.Data.(*chat.ToolCallChunk)
//			_ = frag // correlate by frag.Index, concatenate frag.ArgsDelta
//		case chat.ChunkError:
//			return c.Data.(error)
//		}
//	}
package chat
