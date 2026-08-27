package chat_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/anthropic"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/deepseek"
	"github.com/arbureva/arcus/pkg/openai"

	// register drivers (the only thing business code blank-imports)
	_ "github.com/arbureva/arcus/pkg/chat/drivers/anthropic"
	_ "github.com/arbureva/arcus/pkg/chat/drivers/deepseek"
	_ "github.com/arbureva/arcus/pkg/chat/drivers/openai"
)

func sse(w http.ResponseWriter, lines ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	for _, l := range lines {
		io.WriteString(w, l)
		if fl != nil {
			fl.Flush()
		}
	}
}

func TestDriversRegistered(t *testing.T) {
	got := chat.Providers()
	want := map[adapter.Provider]bool{adapter.OpenAI: true, adapter.Anthropic: true, adapter.Deepseek: true}
	for _, p := range got {
		delete(want, p)
	}
	if len(want) != 0 {
		t.Fatalf("missing drivers: %v (have %v)", want, got)
	}
}

func TestOpenAIChatThroughChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"id":"1","object":"chat.completion","model":"gpt-4o",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hi back"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)
	}))
	defer srv.Close()

	cli := chat.New()
	if err := cli.Use(adapter.OpenAI, openai.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1}); err != nil {
		t.Fatal(err)
	}

	msg, err := cli.Chat(context.Background(), adapter.Request{
		Provider: adapter.OpenAI,
		Data:     &openai.Request{Model: "gpt-4o", Messages: []openai.Message{openai.UserMessage("hi")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Provider != adapter.OpenAI || msg.Role != adapter.RoleAssistant {
		t.Errorf("envelope = %+v", msg)
	}
	out, ok := chat.Result(msg)
	if !ok {
		t.Fatal("Result not a *Completion")
	}
	if out.Text != "hi back" || out.StopReason != "stop" {
		t.Errorf("completion = %+v", out)
	}
	if out.Usage == nil || out.Usage.TotalTokens != 5 {
		t.Errorf("usage = %+v", out.Usage)
	}
}

func TestOpenAIStreamThroughChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n",
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\"},\"finish_reason\":null}]}\n\n",
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"wx\",\"arguments\":\"{\\\"loc\\\"\"}}]},\"finish_reason\":null}]}\n\n",
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"SF\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n",
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n",
			"data: [DONE]\n\n",
		)
	}))
	defer srv.Close()

	cli := chat.New()
	cli.Use(adapter.OpenAI, openai.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})

	ch, err := cli.ChatStream(context.Background(), adapter.Request{
		Provider: adapter.OpenAI,
		Data:     &openai.Request{Model: "gpt-4o", Messages: []openai.Message{openai.UserMessage("hi")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	argsByIdx := map[int]*strings.Builder{}
	var toolName string
	var stop string
	var gotUsage bool
	for c := range ch {
		switch c.Kind {
		case chat.ChunkText:
			text.WriteString(c.Data.(string))
		case chat.ChunkToolCall:
			f := c.Data.(*chat.ToolCallChunk)
			if argsByIdx[f.Index] == nil {
				argsByIdx[f.Index] = &strings.Builder{}
			}
			argsByIdx[f.Index].WriteString(f.ArgsDelta)
			if f.Name != "" {
				toolName = f.Name
			}
		case chat.ChunkStop:
			stop = c.Data.(string)
		case chat.ChunkUsage:
			if c.Data.(*chat.Usage).TotalTokens == 3 {
				gotUsage = true
			}
		case chat.ChunkError:
			t.Fatalf("stream error: %v", c.Data.(error))
		}
	}
	if text.String() != "Hello" {
		t.Errorf("text = %q", text.String())
	}
	if toolName != "wx" || argsByIdx[0].String() != `{"loc":"SF"}` {
		t.Errorf("tool: name=%q args=%q", toolName, argsByIdx[0].String())
	}
	if stop != "tool_calls" || !gotUsage {
		t.Errorf("stop=%q usage=%v", stop, gotUsage)
	}
}

func TestDeepSeekReasoningThroughChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"think\"},\"finish_reason\":null}]}\n\n",
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"ing\"},\"finish_reason\":null}]}\n\n",
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\n",
			"data: [DONE]\n\n",
		)
	}))
	defer srv.Close()

	cli := chat.New()
	cli.Use(adapter.Deepseek, deepseek.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})

	ch, _ := cli.ChatStream(context.Background(), adapter.Request{
		Provider: adapter.Deepseek,
		Data:     &deepseek.Request{Model: "deepseek-reasoner", Messages: []deepseek.Message{deepseek.UserMessage("hi")}},
	})

	var think, text strings.Builder
	for c := range ch {
		switch c.Kind {
		case chat.ChunkThinking:
			think.WriteString(c.Data.(string))
		case chat.ChunkText:
			text.WriteString(c.Data.(string))
		case chat.ChunkError:
			t.Fatalf("stream error: %v", c.Data.(error))
		}
	}
	if think.String() != "thinking" {
		t.Errorf("thinking = %q", think.String())
	}
	if text.String() != "answer" {
		t.Errorf("text = %q", text.String())
	}
}

func TestAnthropicToolStreamThroughChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sse(w,
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":7,\"output_tokens\":0}}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tu_1\",\"name\":\"wx\",\"input\":{}}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"loc\\\":\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"SF\\\"}\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":11}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		)
	}))
	defer srv.Close()

	cli := chat.New()
	cli.Use(adapter.Anthropic, anthropic.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})

	ch, _ := cli.ChatStream(context.Background(), adapter.Request{
		Provider: adapter.Anthropic,
		Data:     &anthropic.Request{Model: "claude", MaxTokens: 100, Messages: []anthropic.Message{anthropic.UserText("weather?")}},
	})

	var name, args, stop string
	var usage *chat.Usage
	for c := range ch {
		switch c.Kind {
		case chat.ChunkToolCall:
			f := c.Data.(*chat.ToolCallChunk)
			if f.Name != "" {
				name = f.Name
			}
			args += f.ArgsDelta
		case chat.ChunkStop:
			stop = c.Data.(string)
		case chat.ChunkUsage:
			usage = c.Data.(*chat.Usage)
		case chat.ChunkError:
			t.Fatalf("stream error: %v", c.Data.(error))
		}
	}
	if name != "wx" || args != `{"loc":"SF"}` {
		t.Errorf("tool: name=%q args=%q", name, args)
	}
	if stop != "tool_use" {
		t.Errorf("stop = %q", stop)
	}
	if usage == nil || usage.InputTokens != 7 || usage.OutputTokens != 11 || usage.TotalTokens != 18 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestUnconfiguredAndMismatch(t *testing.T) {
	cli := chat.New()
	// provider not configured -> synchronous error
	if _, err := cli.Chat(context.Background(), adapter.Request{Provider: adapter.OpenAI, Data: &openai.Request{}}); err == nil {
		t.Error("expected error for unconfigured provider")
	}
	// configured, but wrong native request type -> type mismatch
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	cli.Use(adapter.OpenAI, openai.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})
	if _, err := cli.Chat(context.Background(), adapter.Request{Provider: adapter.OpenAI, Data: &anthropic.Request{}}); err == nil {
		t.Error("expected type mismatch for wrong request type")
	}
}
