package mimo

import "encoding/json"

// Roles.
const (
	RoleDeveloper = "developer"
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Finish reasons.
const (
	FinishStop          = "stop"
	FinishLength        = "length"
	FinishToolCalls     = "tool_calls"
	FinishContentFilter = "content_filter"
	// FinishRepetitionTruncation: the model detected itself looping.
	FinishRepetitionTruncation = "repetition_truncation"
)

// Message is a chat message, used both as request input and response output.
// Content holds the common single-string form; MultiContent holds the
// multimodal array form (user messages only) and takes precedence when
// non-empty.
//
// ReasoningContent round-trips: unlike DeepSeek, MiMo wants it echoed back —
// an assistant turn with tool calls whose reasoning_content is missing is
// rejected with 400 in thinking mode.
type Message struct {
	Role         string
	Content      string
	MultiContent []ContentPart

	Name             string
	ToolCalls        []ToolCall
	ToolCallID       string
	ReasoningContent string
}

type messageWire struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content,omitempty"`
	Name             string          `json:"name,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	w := messageWire{
		Role:             m.Role,
		Name:             m.Name,
		ToolCalls:        m.ToolCalls,
		ToolCallID:       m.ToolCallID,
		ReasoningContent: m.ReasoningContent,
	}
	var err error
	switch {
	case len(m.MultiContent) > 0:
		w.Content, err = json.Marshal(m.MultiContent)
	case m.Content != "":
		w.Content, err = json.Marshal(m.Content)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(w)
}

func (m *Message) UnmarshalJSON(b []byte) error {
	var w messageWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	m.Role = w.Role
	m.Name = w.Name
	m.ToolCalls = w.ToolCalls
	m.ToolCallID = w.ToolCallID
	m.ReasoningContent = w.ReasoningContent
	m.Content, m.MultiContent = "", nil
	if len(w.Content) > 0 {
		switch w.Content[0] {
		case '"':
			return json.Unmarshal(w.Content, &m.Content)
		case '[':
			return json.Unmarshal(w.Content, &m.MultiContent)
		}
	}
	return nil
}

// ContentPart is one element of a multimodal content array. Only mimo-v2.5
// accepts non-text parts.
type ContentPart struct {
	Type     string    `json:"type"` // "text" | "image_url" | "video_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`

	// Video sampling, siblings of video_url.
	FPS             float64 `json:"fps,omitempty"`              // [0.1, 10], default 2
	MediaResolution string  `json:"media_resolution,omitempty"` // "default" | "max"
}

// MediaURL references media by URL or data: URI.
type MediaURL struct {
	URL string `json:"url"`
}

// TextPart / ImageURLPart / VideoURLPart build content parts.
func TextPart(text string) ContentPart { return ContentPart{Type: "text", Text: text} }
func ImageURLPart(url string) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &MediaURL{URL: url}}
}
func VideoURLPart(url string) ContentPart {
	return ContentPart{Type: "video_url", VideoURL: &MediaURL{URL: url}}
}

// ToolCall is a tool invocation requested by the model. Index is only set on
// streaming deltas (to correlate fragments across chunks).
type ToolCall struct {
	Index    *int         `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall is the function name + JSON-encoded arguments of a tool call.
type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// Usage reports token accounting. The details objects may be null.
type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails breaks down prompt tokens (cache hits, media).
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
	AudioTokens  int `json:"audio_tokens,omitempty"`
	ImageTokens  int `json:"image_tokens,omitempty"`
	VideoTokens  int `json:"video_tokens,omitempty"`
}

// CompletionTokensDetails breaks down completion tokens.
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// ChatCompletion is the non-streaming response object.
type ChatCompletion struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice is one completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Text returns the content of the first choice, or "" if there is none.
func (r *ChatCompletion) Text() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}

// Reasoning returns the chain-of-thought of the first choice (thinking mode).
func (r *ChatCompletion) Reasoning() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.ReasoningContent
}

// FinishReason returns the finish reason of the first choice.
func (r *ChatCompletion) FinishReason() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].FinishReason
}

// ToolCalls returns the tool calls of the first choice.
func (r *ChatCompletion) ToolCalls() []ToolCall {
	if len(r.Choices) == 0 {
		return nil
	}
	return r.Choices[0].Message.ToolCalls
}

// ----- Convenience constructors -------------------------------------------

func SystemMessage(text string) Message    { return Message{Role: RoleSystem, Content: text} }
func UserMessage(text string) Message      { return Message{Role: RoleUser, Content: text} }
func AssistantMessage(text string) Message { return Message{Role: RoleAssistant, Content: text} }

// UserParts builds a multimodal user message.
func UserParts(parts ...ContentPart) Message { return Message{Role: RoleUser, MultiContent: parts} }

// ToolMessage builds a tool-result message answering a prior tool call.
func ToolMessage(toolCallID, content string) Message {
	return Message{Role: RoleTool, ToolCallID: toolCallID, Content: content}
}
