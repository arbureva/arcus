package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
)

// A stream that dies mid-answer must not look like a clean end: the caller
// would otherwise show a half-written answer as complete.
func TestStreamTruncated(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"Hel"},"finish_reason":null}]}`+"\n\n")
		// connection drops here: no finish_reason, no [DONE]
	})
	defer srv.Close()

	final, err := c.StreamFunc(context.Background(), &Request{Model: "m"}, nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want ErrUnexpectedEOF", err)
	}
	if final.Text() != "Hel" {
		t.Errorf("partial text should still be available, got %q", final.Text())
	}
}

// A gateway that closes right after the final chunk, without "[DONE]", is a
// clean end — finish_reason already said why the model stopped.
func TestStreamNoSentinelButFinished(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`+"\n\n")
	})
	defer srv.Close()

	final, err := c.StreamFunc(context.Background(), &Request{Model: "m"}, nil)
	if err != nil {
		t.Fatalf("err = %v, want clean end", err)
	}
	if final.Text() != "hi" {
		t.Errorf("text = %q", final.Text())
	}
}
