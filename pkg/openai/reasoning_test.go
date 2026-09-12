package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Reasoning models on OpenAI-compatible endpoints return the chain-of-thought
// as "reasoning_content" (DeepSeek, Qwen, GLM, vLLM) or "reasoning" (some
// gateways), and reject either field on input.
func TestReasoningChat(t *testing.T) {
	for _, field := range []string{"reasoning_content", "reasoning"} {
		c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"id":"c1","choices":[{"index":0,"message":{"role":"assistant","content":"4",`+
				`"`+field+`":"2+2 is 4"},"finish_reason":"stop"}]}`)
		})
		resp, err := c.Chat(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("2+2?")}})
		srv.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.Reasoning() != "2+2 is 4" {
			t.Errorf("%s: reasoning = %q", field, resp.Reasoning())
		}
		// Never echoed back: every such API rejects it on input.
		b, err := json.Marshal(resp.Choices[0].Message)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "reasoning") {
			t.Errorf("%s: marshalled back onto the wire: %s", field, b)
		}
	}
}

func TestReasoningStream(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ch := range []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"2+2 "},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"reasoning":"is 4"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":"4"},"finish_reason":"stop"}]}`,
		} {
			io.WriteString(w, "data: "+ch+"\n\n")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	})
	defer srv.Close()

	var thinking strings.Builder
	final, err := c.StreamFunc(context.Background(), &Request{Model: "m"}, func(chunk *ChatCompletionChunk) error {
		if len(chunk.Choices) > 0 {
			thinking.WriteString(chunk.Choices[0].Delta.Thinking())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if thinking.String() != "2+2 is 4" {
		t.Errorf("streamed reasoning = %q", thinking.String())
	}
	if final.Reasoning() != "2+2 is 4" {
		t.Errorf("accumulated reasoning = %q", final.Reasoning())
	}
}
