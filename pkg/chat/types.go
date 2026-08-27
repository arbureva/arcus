package chat

import (
	"encoding/json"
	"fmt"

	"github.com/arbureva/arcus/pkg/adapter"
)

// Normalized stream chunk kinds. Each is carried in the Kind field of an
// adapter.ChunkMessageAdapter; the Kind determines the dynamic type of Data:
//
//	ChunkText     -> Data is string          (incremental assistant text)
//	ChunkThinking -> Data is string          (incremental reasoning / CoT)
//	ChunkToolCall -> Data is *ToolCallChunk  (a tool-call fragment; args streamed raw)
//	ChunkStop     -> Data is string          (stop / finish reason)
//	ChunkUsage    -> Data is *Usage          (token accounting)
//	ChunkError    -> Data is error           (terminal stream error)
const (
	ChunkText     = "text"
	ChunkThinking = "thinking"
	ChunkToolCall = "tool_call"
	ChunkStop     = "stop"
	ChunkUsage    = "usage"
	ChunkError    = "error"
)

// ToolCall is a fully-assembled tool call on a non-streaming result.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// ToolCallChunk is a single tool-call fragment emitted during streaming. The
// arguments are passed through verbatim as ArgsDelta: the chat layer does NOT
// reassemble them — correlate fragments by Index and concatenate ArgsDelta to
// rebuild the full JSON. ID and Name are present on the first fragment of a
// given Index (they may be empty on later fragments).
type ToolCallChunk struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	ArgsDelta string `json:"args_delta,omitempty"`
}

// Usage is normalized token accounting. Raw holds the provider-native usage
// object for fields that don't generalize (cache hits, reasoning tokens, ...).
type Usage struct {
	InputTokens  int         `json:"input_tokens"`
	OutputTokens int         `json:"output_tokens"`
	TotalTokens  int         `json:"total_tokens"`
	Raw          interface{} `json:"-"`
}

// Completion is the normalized non-streaming result. It is what a chat.Chat
// call returns inside adapter.MessageAdapter.Data, so business code reads the
// same shape regardless of provider. Raw holds the provider-native response
// (*openai.ChatCompletion, *anthropic.Message, *deepseek.ChatCompletion) for
// callers that need provider-specific detail.
type Completion struct {
	Text       string      `json:"text,omitempty"`
	Reasoning  string      `json:"reasoning,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	StopReason string      `json:"stop_reason,omitempty"`
	Usage      *Usage      `json:"usage,omitempty"`
	Raw        interface{} `json:"-"`
}

// Result extracts the normalized Completion from a non-streaming response.
func Result(msg *adapter.MessageAdapter) (*Completion, bool) {
	if msg == nil {
		return nil, false
	}
	c, ok := msg.Data.(*Completion)
	return c, ok
}

func ChunkResult[T any](msg *adapter.ChunkMessageAdapter) (T, bool) {
	//	ChunkText     -> Data is string          (incremental assistant text)
	//	ChunkThinking -> Data is string          (incremental reasoning / CoT)
	//	ChunkToolCall -> Data is *ToolCallChunk  (a tool-call fragment; args streamed raw)
	//	ChunkStop     -> Data is string          (stop / finish reason)
	//	ChunkUsage    -> Data is *Usage          (token accounting)
	//	ChunkError    -> Data is error           (terminal stream error)
	if msg == nil {
		var zero T
		return zero, false
	}

	res, ok := msg.Data.(T)
	return res, ok
}

// chunkAs 先校验 Kind，再断言类型。Kind 不匹配（或 msg 为 nil）直接判失败，
// 不再做断言——这样 ChunkText/ChunkThinking/ChunkStop 这三个同为 string 的
// kind 不会互相串味。
func chunkAs[T any](msg *adapter.ChunkMessageAdapter, want string) (T, bool) {
	if msg == nil || msg.Kind != want {
		var zero T
		return zero, false
	}
	return ChunkResult[T](msg)
}

func mustChunkAs[T any](msg *adapter.ChunkMessageAdapter, want string) T {
	v, ok := chunkAs[T](msg, want)
	if !ok {
		panic(fmt.Sprintf("chat: chunk is not %v (%s)", want, describeChunk(msg)))
	}
	return v
}

func describeChunk(msg *adapter.ChunkMessageAdapter) string {
	if msg == nil {
		return "nil chunk"
	}
	return fmt.Sprintf("got kind=%v data=%T", msg.Kind, msg.Data)
}

// ── (value, ok) ──────────────────────────────────────────────

func AsText(msg *adapter.ChunkMessageAdapter) (string, bool) {
	return chunkAs[string](msg, ChunkText)
}
func AsThinking(msg *adapter.ChunkMessageAdapter) (string, bool) {
	return chunkAs[string](msg, ChunkThinking)
}
func AsToolCall(msg *adapter.ChunkMessageAdapter) (*ToolCallChunk, bool) {
	return chunkAs[*ToolCallChunk](msg, ChunkToolCall)
}
func AsStop(msg *adapter.ChunkMessageAdapter) (string, bool) {
	return chunkAs[string](msg, ChunkStop)
}
func AsUsage(msg *adapter.ChunkMessageAdapter) (*Usage, bool) {
	return chunkAs[*Usage](msg, ChunkUsage)
}
func AsError(msg *adapter.ChunkMessageAdapter) (error, bool) {
	return chunkAs[error](msg, ChunkError)
}

// ── Must（不匹配则 panic）────────────────────────────────────

func MustText(msg *adapter.ChunkMessageAdapter) string {
	return mustChunkAs[string](msg, ChunkText)
}
func MustThinking(msg *adapter.ChunkMessageAdapter) string {
	return mustChunkAs[string](msg, ChunkThinking)
}
func MustToolCall(msg *adapter.ChunkMessageAdapter) *ToolCallChunk {
	return mustChunkAs[*ToolCallChunk](msg, ChunkToolCall)
}
func MustStop(msg *adapter.ChunkMessageAdapter) string {
	return mustChunkAs[string](msg, ChunkStop)
}
func MustUsage(msg *adapter.ChunkMessageAdapter) *Usage {
	return mustChunkAs[*Usage](msg, ChunkUsage)
}
func MustError(msg *adapter.ChunkMessageAdapter) error {
	return mustChunkAs[error](msg, ChunkError)
}
