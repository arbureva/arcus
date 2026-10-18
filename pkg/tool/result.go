package tool

import (
	"encoding/json"
	"fmt"
)

// Result is what a tool returns. Content is the textual payload fed back to the
// model as the tool's output (it becomes the provider's tool / tool_result
// message). IsError tells the model the call failed without aborting the loop.
// Images are pictures handed to the model with the result; only protocols whose
// tool results carry content arrays (Anthropic) deliver them, the others send
// TextOnly instead. Meta is optional out-of-band data for the host (logging,
// UI, tracing) and is never sent to the model.
type Result struct {
	Content string          `json:"content"`
	IsError bool            `json:"is_error,omitempty"`
	Images  []Image         `json:"images,omitempty"`
	Meta    json.RawMessage `json:"meta,omitempty"`
}

// Image is raw image bytes plus their media type (image/png, image/jpeg, ...).
type Image struct {
	MIME string `json:"mime"`
	Data []byte `json:"data"`
}

// ImagesUnsupported is appended by TextOnly when a result carries images the
// protocol cannot deliver, so the model knows it was not shown them.
const ImagesUnsupported = "（当前协议不支持在工具结果里看图）"

// Text returns a successful Result carrying s verbatim.
func Text(s string) *Result { return &Result{Content: s} }

// Textf is Text with fmt formatting.
func Textf(format string, a ...any) *Result {
	return &Result{Content: fmt.Sprintf(format, a...)}
}

// JSON marshals v as the Result content. A marshalling failure comes back as an
// error (not an IsError result): it is a host bug, not a tool outcome.
func JSON(v any) (*Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("tool: marshal result: %w", err)
	}
	return &Result{Content: string(b)}, nil
}

// Err returns a Result flagged as an error, carrying msg so the model can read
// what went wrong.
func Err(msg string) *Result { return &Result{Content: msg, IsError: true} }

// Errf is Err with fmt formatting.
func Errf(format string, a ...any) *Result {
	return &Result{Content: fmt.Sprintf(format, a...), IsError: true}
}

// WithImage attaches an image and returns r for chaining.
func (r *Result) WithImage(mime string, data []byte) *Result {
	r.Images = append(r.Images, Image{MIME: mime, Data: data})
	return r
}

// TextOnly is the result as plain text for protocols without content arrays: an
// empty error becomes "tool failed", and dropped images leave ImagesUnsupported.
func (r *Result) TextOnly() string {
	s := r.Content
	if r.IsError && s == "" {
		s = "tool failed"
	}
	if len(r.Images) > 0 {
		if s != "" {
			s += "\n"
		}
		s += ImagesUnsupported
	}
	return s
}

// WithMeta attaches host-only metadata and returns r for chaining.
func (r *Result) WithMeta(meta json.RawMessage) *Result {
	r.Meta = meta
	return r
}
