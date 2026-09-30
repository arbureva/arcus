package mimo

// ChatCompletionChunk is one streamed chunk (object "chat.completion.chunk").
type ChatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []ChunkChoice `json:"choices"`
	// Usage arrives on a final chunk whose choices is empty.
	Usage *Usage `json:"usage,omitempty"`
}

// ChunkChoice is one streamed choice. FinishReason is a pointer because it is
// null on every chunk except the final one for that choice.
type ChunkChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// Delta is the incremental payload of a streamed choice. ReasoningContent
// streams before Content in thinking mode.
type Delta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
}
