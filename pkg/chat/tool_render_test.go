package chat_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/anthropic"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/deepseek"
	"github.com/arbureva/arcus/pkg/openai"
	"github.com/arbureva/arcus/pkg/tool"

	_ "github.com/arbureva/arcus/pkg/chat/drivers/anthropic"
	_ "github.com/arbureva/arcus/pkg/chat/drivers/deepseek"
	_ "github.com/arbureva/arcus/pkg/chat/drivers/openai"
)

type echoArgs struct {
	Text string `json:"text" desc:"text to echo"`
}

func echoTool() tool.Tool {
	return tool.Func("echo", "Echo text back", tool.Reflect(echoArgs{}),
		func(ctx context.Context, raw json.RawMessage) (*tool.Result, error) {
			var a echoArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return tool.Errf("bad args: %v", err), nil
			}
			return tool.Text(a.Text), nil
		})
}

// captureBody runs an httptest server that records the request body and replies
// with a minimal valid completion for the given provider so the driver's render
// path is exercised end-to-end through chat.Client.
func TestToolsRenderIntoNativeRequest(t *testing.T) {
	set := tool.NewSet(echoTool())

	cases := []struct {
		name     string
		provider adapter.Provider
		reply    string
		data     func() interface{}
		// toolNameAt extracts the rendered tool name from the captured body.
		toolNameAt func(body map[string]any) (string, bool)
	}{
		{
			name:     "openai",
			provider: adapter.OpenAI,
			reply:    `{"id":"1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
			data: func() interface{} {
				return &openai.Request{Model: "gpt-4o", Messages: []openai.Message{openai.UserMessage("hi")}}
			},
			toolNameAt: func(b map[string]any) (string, bool) {
				tools, _ := b["tools"].([]any)
				if len(tools) == 0 {
					return "", false
				}
				fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
				n, ok := fn["name"].(string)
				return n, ok
			},
		},
		{
			name:     "deepseek",
			provider: adapter.Deepseek,
			reply:    `{"id":"1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
			data: func() interface{} {
				return &deepseek.Request{Model: "deepseek-chat", Messages: []deepseek.Message{deepseek.UserMessage("hi")}}
			},
			toolNameAt: func(b map[string]any) (string, bool) {
				tools, _ := b["tools"].([]any)
				if len(tools) == 0 {
					return "", false
				}
				fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
				n, ok := fn["name"].(string)
				return n, ok
			},
		},
		{
			name:     "anthropic",
			provider: adapter.Anthropic,
			reply:    `{"id":"m1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
			data: func() interface{} {
				return &anthropic.Request{Model: "claude", MaxTokens: 50, Messages: []anthropic.Message{anthropic.UserText("hi")}}
			},
			toolNameAt: func(b map[string]any) (string, bool) {
				tools, _ := b["tools"].([]any)
				if len(tools) == 0 {
					return "", false
				}
				n, ok := tools[0].(map[string]any)["name"].(string)
				return n, ok
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &captured)
				io.WriteString(w, tc.reply)
			}))
			defer srv.Close()

			cli := chat.New()
			var cfgErr error
			switch tc.provider {
			case adapter.OpenAI:
				cfgErr = cli.Use(tc.provider, openai.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})
			case adapter.Deepseek:
				cfgErr = cli.Use(tc.provider, deepseek.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})
			case adapter.Anthropic:
				cfgErr = cli.Use(tc.provider, anthropic.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})
			}
			if cfgErr != nil {
				t.Fatal(cfgErr)
			}

			_, err := cli.Chat(context.Background(), adapter.Request{
				Provider: tc.provider,
				Data:     tc.data(),
				Tools:    set.RequestTools(),
			})
			if err != nil {
				t.Fatal(err)
			}

			name, ok := tc.toolNameAt(captured)
			if !ok || name != "echo" {
				t.Fatalf("rendered tool name = %q ok=%v; body=%v", name, ok, captured)
			}
		})
	}
}

// A foreign value on Request.Tools is rejected as a type mismatch.
func TestToolsTypeMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	cli := chat.New()
	cli.Use(adapter.OpenAI, openai.Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})

	_, err := cli.Chat(context.Background(), adapter.Request{
		Provider: adapter.OpenAI,
		Data:     &openai.Request{Model: "gpt-4o", Messages: []openai.Message{openai.UserMessage("hi")}},
		Tools:    []interface{}{"not a tool"},
	})
	if err == nil {
		t.Error("expected type mismatch error for foreign tool value")
	}
}
