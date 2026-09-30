package mimo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The request carries thinking, never reasoning_effort / stream_options, and
// echoes reasoning_content on assistant turns (MiMo 400s without it on
// tool-call turns in thinking mode).
func TestRequestWire(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"1","choices":[{"index":0,"message":{"role":"assistant","content":"hi","reasoning_content":"cot"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":null,"completion_tokens_details":{"reasoning_tokens":1}}}`)
	}))
	defer srv.Close()
	c := New(Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})

	asst := Message{Role: RoleAssistant, ReasoningContent: "prev cot",
		ToolCalls: []ToolCall{{ID: "c1", Type: "function", Function: FunctionCall{Name: "f", Arguments: "{}"}}}}
	resp, err := c.Chat(context.Background(), &Request{
		Model:    "mimo-v2.6-flash",
		Messages: []Message{UserParts(TextPart("look"), ImageURLPart("data:image/png;base64,AA")), asst, ToolMessage("c1", "ok")},
		Thinking: DisableThinking(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text() != "hi" || resp.Reasoning() != "cot" || resp.Usage.CompletionTokensDetails.ReasoningTokens != 1 {
		t.Errorf("resp = %+v", resp)
	}

	if th, _ := got["thinking"].(map[string]any); th["type"] != "disabled" {
		t.Errorf("thinking = %v", got["thinking"])
	}
	for _, k := range []string{"reasoning_effort", "stream_options", "max_tokens", "stream"} {
		if _, ok := got[k]; ok {
			t.Errorf("unexpected field %q in body", k)
		}
	}
	msgs := got["messages"].([]any)
	if parts, _ := msgs[0].(map[string]any)["content"].([]any); len(parts) != 2 {
		t.Errorf("multimodal content = %v", msgs[0])
	}
	if rc := msgs[1].(map[string]any)["reasoning_content"]; rc != "prev cot" {
		t.Errorf("reasoning_content not echoed: %v", msgs[1])
	}
}

func TestStreamReasoningAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ch := range []string{
			`{"id":"1","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think"},"finish_reason":null}]}`,
			`{"id":"1","choices":[{"index":0,"delta":{"content":"Final"},"finish_reason":"repetition_truncation"}]}`,
			`{"id":"1","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
		} {
			io.WriteString(w, "data: "+ch+"\n\n")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := New(Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})

	final, err := c.StreamFunc(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if final.Reasoning() != "think" || final.Text() != "Final" ||
		final.FinishReason() != FinishRepetitionTruncation || final.Usage == nil || final.Usage.TotalTokens != 3 {
		t.Errorf("final = %+v", final)
	}
}

// TokenHub sends a numeric error code.
func TestNumericErrorCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"type":"invalid_request_error","code":400001,"message":"bad"}}`)
	}))
	defer srv.Close()
	c := New(Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})

	_, err := c.Chat(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err == nil || !strings.Contains(err.Error(), "400001") || !strings.Contains(err.Error(), "bad") {
		t.Errorf("err = %v", err)
	}
}
