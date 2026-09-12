package openai

import "encoding/json"

// Roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleDeveloper = "developer" // o-series replacement for system
)

// Finish reasons.
const (
	FinishStop          = "stop"
	FinishLength        = "length"
	FinishToolCalls     = "tool_calls"
	FinishContentFilter = "content_filter"
	FinishFunctionCall  = "function_call"
)

// Message is a chat message, used both as request input and response output.
// Content holds the common single-string form; MultiContent holds the
// multimodal array form and takes precedence when non-empty. Custom JSON
// (un)marshalling projects these onto the polymorphic "content" field.
type Message struct {
	Role         string
	Content      string
	MultiContent []ContentPart

	Name       string
	ToolCalls  []ToolCall
	ToolCallID string
	Refusal    string

	// ReasoningContent is the chain-of-thought returned by reasoning models on
	// OpenAI-compatible endpoints (DeepSeek, Qwen, GLM, vLLM, OpenRouter, ...).
	// The wire field is "reasoning_content", or "reasoning" on some gateways.
	// Decode-only: every such API rejects it on input, so it is never
	// marshalled back.
	ReasoningContent string
}

type messageWire struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Refusal    string          `json:"refusal,omitempty"`

	// Decode-only, see Message.ReasoningContent.
	ReasoningContent string `json:"reasoning_content,omitempty"`
	Reasoning        string `json:"reasoning,omitempty"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	w := messageWire{
		Role:       m.Role,
		Name:       m.Name,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
		Refusal:    m.Refusal,
	}
	switch {
	case len(m.MultiContent) > 0:
		b, err := json.Marshal(m.MultiContent)
		if err != nil {
			return nil, err
		}
		w.Content = b
	case m.Content != "":
		b, err := json.Marshal(m.Content)
		if err != nil {
			return nil, err
		}
		w.Content = b
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
	m.Refusal = w.Refusal
	m.ReasoningContent = firstNonEmpty(w.ReasoningContent, w.Reasoning)
	if len(w.Content) > 0 && string(w.Content) != "null" {
		switch w.Content[0] {
		case '"':
			if err := json.Unmarshal(w.Content, &m.Content); err != nil {
				return err
			}
		case '[':
			if err := json.Unmarshal(w.Content, &m.MultiContent); err != nil {
				return err
			}
		}
	}
	return nil
}

// ContentPart is one element of a multimodal message content array.
type ContentPart struct {
	Type     string    `json:"type"` // "text" | "image_url" | ...
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL references an image, either as a URL or a data: URI.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "auto" | "low" | "high"
}

// TextPart / ImageURLPart build content parts.
func TextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}
func ImageURLPart(url, detail string) ContentPart {
	return ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: url, Detail: detail}}
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

// Usage reports token accounting.
type Usage struct {
	PromptTokens            int             `json:"prompt_tokens"`
	CompletionTokens        int             `json:"completion_tokens"`
	TotalTokens             int             `json:"total_tokens"`
	PromptTokensDetails     json.RawMessage `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails json.RawMessage `json:"completion_tokens_details,omitempty"`
}

// ChatCompletion is the non-streaming response object.
type ChatCompletion struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
	Choices           []Choice `json:"choices"`
	Usage             *Usage   `json:"usage,omitempty"`
}

// Choice is one completion choice.
type Choice struct {
	Index        int             `json:"index"`
	Message      Message         `json:"message"`
	FinishReason string          `json:"finish_reason"`
	Logprobs     json.RawMessage `json:"logprobs,omitempty"`
}

// Text returns the content of the first choice, or "" if there is none.
func (r *ChatCompletion) Text() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}

// Reasoning returns the chain-of-thought of the first choice, for reasoning
// models on OpenAI-compatible endpoints ("" when the model returns none).
func (r *ChatCompletion) Reasoning() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.ReasoningContent
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
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
func DeveloperMessage(text string) Message { return Message{Role: RoleDeveloper, Content: text} }
func UserMessage(text string) Message      { return Message{Role: RoleUser, Content: text} }
func AssistantMessage(text string) Message { return Message{Role: RoleAssistant, Content: text} }

// UserParts builds a multimodal user message.
func UserParts(parts ...ContentPart) Message {
	return Message{Role: RoleUser, MultiContent: parts}
}

// ToolMessage builds a tool-result message answering a prior tool call.
func ToolMessage(toolCallID, content string) Message {
	return Message{Role: RoleTool, ToolCallID: toolCallID, Content: content}
}

// NewMessageFromJson parses a single OpenAI message from raw JSON.
func NewMessageFromJson(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
