package glm

import "encoding/json"

// Request is the body of POST /chat/completions. Pointer fields are omitted
// when nil so the server applies its own defaults.
//
// See https://docs.bigmodel.cn/api-reference/模型-api/对话补全.
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`

	// MaxTokens caps reasoning + answer ([1, 131072]; default per model).
	MaxTokens int `json:"max_tokens,omitempty"`

	// Sampling. DoSample=false ignores temperature/top_p (greedy).
	DoSample    *bool    `json:"do_sample,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"` // [0, 1], two decimals
	TopP        *float64 `json:"top_p,omitempty"`       // [0.01, 1]
	Stop        []string `json:"stop,omitempty"`

	// Stream is set by Client.Stream. Usage arrives on the final chunk;
	// there is no stream_options.
	Stream bool `json:"stream,omitempty"`
	// ToolStream streams tool-call arguments incrementally (needs Stream).
	ToolStream bool `json:"tool_stream,omitempty"`

	Tools []Tool `json:"tools,omitempty"` // up to 128
	// ToolChoice: GLM only supports "auto".
	ToolChoice string `json:"tool_choice,omitempty"`

	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`

	// Thinking toggles deep thinking (enabled by default). GLM-5.3 rejects
	// "disabled".
	Thinking *Thinking `json:"thinking,omitempty"`

	// ReasoningEffort sets thinking depth on GLM-5.2+ (default "max").
	// GLM-5.3 accepts only max/high/low; GLM-5.2 also takes
	// xhigh/medium/minimal/none. Older models reject the field.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	RequestID string `json:"request_id,omitempty"` // 6–64 chars, echoed back
	UserID    string `json:"user_id,omitempty"`    // 6–128 chars, abuse tracking
}

// Thinking toggles thinking mode.
type Thinking struct {
	Type string `json:"type"` // "enabled" | "disabled"

	// ClearThinking (default true) drops earlier turns' reasoning_content
	// server-side. Set false for Preserved Thinking, which requires every
	// historical reasoning_content to be sent back in order.
	ClearThinking *bool `json:"clear_thinking,omitempty"`
}

// EnableThinking / DisableThinking are convenience constructors.
func EnableThinking() *Thinking  { return &Thinking{Type: "enabled"} }
func DisableThinking() *Thinking { return &Thinking{Type: "disabled"} }

// Tool is a tool definition. Only "function" is modelled; GLM's built-in
// retrieval / web_search / mcp tools are not.
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function describes a callable function exposed to the model.
type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// NewTool is a convenience constructor for a function tool.
func NewTool(name, description string, parameters json.RawMessage) Tool {
	return Tool{
		Type:     "function",
		Function: Function{Name: name, Description: description, Parameters: parameters},
	}
}

// ResponseFormat controls structured output. Type is "text" or
// "json_object" (text models only).
type ResponseFormat struct {
	Type string `json:"type"`
}

// JSONObjectFormat is a convenience constructor.
func JSONObjectFormat() *ResponseFormat { return &ResponseFormat{Type: "json_object"} }
