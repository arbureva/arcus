package openai

import "encoding/json"

// ChatCompletionChunk is one streamed chunk (object "chat.completion.chunk").
type ChatCompletionChunk struct {
	ID                string        `json:"id"`
	Object            string        `json:"object"`
	Created           int64         `json:"created"`
	Model             string        `json:"model"`
	SystemFingerprint string        `json:"system_fingerprint,omitempty"`
	Choices           []ChunkChoice `json:"choices"`
	// Usage is populated only on the final chunk when
	// StreamOptions.IncludeUsage is set; choices is then empty.
	Usage *Usage `json:"usage,omitempty"`
}

// ChunkChoice is one streamed choice. FinishReason is a pointer because it is
// null on every chunk except the final one for that choice.
type ChunkChoice struct {
	Index        int             `json:"index"`
	Delta        Delta           `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
	Logprobs     json.RawMessage `json:"logprobs,omitempty"`
}

// Delta is the incremental payload of a streamed choice. ReasoningContent
// carries chain-of-thought fragments from reasoning models on
// OpenAI-compatible endpoints, at the same level as Content; some gateways
// name the field "reasoning" instead.
type Delta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Reasoning        string     `json:"reasoning,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	Refusal          string     `json:"refusal,omitempty"`
}

// Thinking returns this delta's chain-of-thought fragment, whichever field
// name the endpoint used.
func (d Delta) Thinking() string { return firstNonEmpty(d.ReasoningContent, d.Reasoning) }
