package mimo

import "encoding/json"

// Request is the body of POST /chat/completions. Pointer fields are omitted
// when nil so the server applies its own defaults.
//
// See https://mimo.mi.com/docs/zh-CN/api/chat/openai-api.
type Request struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`

	// MaxCompletionTokens caps reasoning + visible output together
	// ([1, 131072]). MiMo documents this field only, not max_tokens.
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`

	// Sampling parameters. In thinking mode temperature/top_p are forced to
	// 1.0/0.95 regardless of what is sent.
	Temperature      *float64 `json:"temperature,omitempty"` // [0, 1.5]
	TopP             *float64 `json:"top_p,omitempty"`       // [0.01, 1.0]
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	Stop             []string `json:"stop,omitempty"` // up to 4

	// Stream is set by Client.Stream. The final chunk always carries usage;
	// there is no stream_options.
	Stream bool `json:"stream,omitempty"`

	Tools []Tool `json:"tools,omitempty"`
	// ToolChoice: MiMo only honours "auto"; any other value is dropped
	// server-side, so it is not modelled beyond a plain string.
	ToolChoice string `json:"tool_choice,omitempty"`

	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`

	// Thinking toggles deep thinking. It is enabled by default; MiMo has no
	// reasoning_effort, the switch is the only control.
	Thinking *Thinking `json:"thinking,omitempty"`
}

// Thinking toggles thinking (chain-of-thought) mode.
type Thinking struct {
	Type string `json:"type"` // "enabled" | "disabled"
}

// EnableThinking / DisableThinking are convenience constructors.
func EnableThinking() *Thinking  { return &Thinking{Type: "enabled"} }
func DisableThinking() *Thinking { return &Thinking{Type: "disabled"} }

// Tool is a tool/function definition. Type is "function".
type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

// Function describes a callable function exposed to the model. Names are
// limited to [a-zA-Z0-9_-]{1,64}.
type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict,omitempty"`
}

// NewTool is a convenience constructor for a function tool.
func NewTool(name, description string, parameters json.RawMessage) Tool {
	return Tool{
		Type:     "function",
		Function: Function{Name: name, Description: description, Parameters: parameters},
	}
}

// ResponseFormat controls structured output. Type is "text" or
// "json_object"; json_schema is not supported.
type ResponseFormat struct {
	Type string `json:"type"`
}

// JSONObjectFormat is a convenience constructor.
func JSONObjectFormat() *ResponseFormat { return &ResponseFormat{Type: "json_object"} }
