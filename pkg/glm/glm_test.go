package glm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestWire(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"1","request_id":"r1","choices":[{"index":0,"message":{"role":"assistant","content":"hi","reasoning_content":"cot"},"finish_reason":"sensitive"}],
			"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":1}}}`)
	}))
	defer srv.Close()
	c := New(Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})

	keep := false
	resp, err := c.Chat(context.Background(), &Request{
		Model:           "glm-5.2",
		Messages:        []Message{UserMessage("hi"), {Role: RoleAssistant, Content: "a", ReasoningContent: "prev"}},
		MaxTokens:       100,
		Thinking:        &Thinking{Type: "enabled", ClearThinking: &keep},
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.RequestID != "r1" || resp.Reasoning() != "cot" || resp.FinishReason() != FinishSensitive ||
		resp.Usage.PromptTokensDetails.CachedTokens != 1 {
		t.Errorf("resp = %+v", resp)
	}

	th, _ := got["thinking"].(map[string]any)
	if th["type"] != "enabled" || th["clear_thinking"] != false {
		t.Errorf("thinking = %v", got["thinking"])
	}
	if got["reasoning_effort"] != "high" || got["max_tokens"] != float64(100) {
		t.Errorf("body = %v", got)
	}
	if _, ok := got["stream_options"]; ok {
		t.Error("stream_options must not be sent")
	}
	if rc := got["messages"].([]any)[1].(map[string]any)["reasoning_content"]; rc != "prev" {
		t.Errorf("reasoning_content not echoed: %v", rc)
	}
}

func TestErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		status    int
		code      string
		retryable bool
	}{
		{429, "1302", true},  // concurrency
		{429, "1113", false}, // arrears
		{400, "1211", false}, // model not found
		{500, "1234", true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			io.WriteString(w, `{"error":{"code":"`+tc.code+`","message":"m"}}`)
		}))
		c := New(Config{APIKey: "k", BaseURL: srv.URL, MaxRetries: -1})
		_, err := c.Chat(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
		srv.Close()

		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Code != tc.code || apiErr.Retryable() != tc.retryable {
			t.Errorf("%d/%s: err = %v", tc.status, tc.code, err)
		}
	}
}
