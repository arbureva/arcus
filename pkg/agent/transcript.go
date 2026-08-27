package agent

import (
	"fmt"
	"sort"
	"sync"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/tool"
)

// ToolReturn pairs a model-issued tool call with the result the host produced
// for it. Transcripts translate a batch of ToolReturns into the provider's
// native tool-result turn (tool-role messages for OpenAI/DeepSeek, a single
// user message of tool_result blocks for Anthropic).
type ToolReturn struct {
	Call   chat.ToolCall `json:"call"`
	Result *tool.Result  `json:"result"`
}

// Transcript owns a conversation's native message history for one provider.
// It is the only place in the agent loop where provider-specific message
// shapes exist; the Agent itself never sees them.
//
// A Transcript is seeded with the provider's native request value (model,
// system prompt, sampling params, any pre-existing messages) and mutates the
// message list as the loop advances. It is not safe for concurrent use — one
// Transcript belongs to one running conversation.
type Transcript interface {
	// Provider identifies which native protocol this transcript speaks.
	Provider() adapter.Provider

	// User appends a plain-text user message.
	User(text string)

	// Assistant folds the assistant turn back into the history, including any
	// tool calls it issued. Implementations should prefer the provider-native
	// message inside comp.Raw when present (it preserves fields the normalized
	// Completion cannot carry, e.g. Anthropic thinking blocks and their
	// signatures) and fall back to rebuilding from the normalized fields when
	// Raw is absent (streaming).
	Assistant(comp *chat.Completion)

	// ToolResults appends the native tool-result turn answering the calls
	// echoed by the preceding Assistant.
	ToolResults(returns []ToolReturn)

	// Request snapshots the current history into an adapter.Request carrying
	// the given provider-agnostic tools. The returned request must be safe to
	// hand to a chat driver without accumulating state across calls (drivers
	// append rendered tools to the native request's tool list, so a fresh
	// snapshot is required each step).
	Request(tools []interface{}) adapter.Request

	// Messages returns the native message history (e.g. []openai.Message,
	// []anthropic.Message) for callers that want to inspect or persist it.
	// The dynamic type is provider-specific by design.
	Messages() interface{}
}

// TranscriptFactory builds a Transcript from the provider's native request
// value, mirroring chat.Driver.Open: native may be the request struct, a
// pointer to it, or a type the implementation documents. A mismatch returns
// ecode.TypeMismatch.
type TranscriptFactory func(native any) (Transcript, error)

var (
	transcriptsMu sync.RWMutex
	transcripts   = make(map[adapter.Provider]TranscriptFactory)
)

// RegisterTranscript makes a transcript factory available for a provider. It is
// intended to be called from an implementation package's init, so applications
// select providers with blank imports:
//
//	import _ "github.com/arbureva/arcus/pkg/agent/transcripts/openai"
//
// It panics on a nil factory or a duplicate registration.
func RegisterTranscript(p adapter.Provider, f TranscriptFactory) {
	transcriptsMu.Lock()
	defer transcriptsMu.Unlock()
	if f == nil {
		panic("agent: RegisterTranscript factory is nil")
	}
	if _, dup := transcripts[p]; dup {
		panic("agent: RegisterTranscript called twice for provider " + string(p))
	}
	transcripts[p] = f
}

// TranscriptProviders lists providers with a registered transcript factory.
func TranscriptProviders() []adapter.Provider {
	transcriptsMu.RLock()
	defer transcriptsMu.RUnlock()
	out := make([]adapter.Provider, 0, len(transcripts))
	for p := range transcripts {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// NewTranscript builds a Transcript for p seeded with the provider's native
// request value (e.g. *openai.Request). The native value is owned by the
// transcript afterwards; do not mutate it concurrently.
func NewTranscript(p adapter.Provider, native any) (Transcript, error) {
	transcriptsMu.RLock()
	f, ok := transcripts[p]
	transcriptsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("agent: no transcript registered for provider %q (missing blank import?)", p)
	}
	return f(native)
}
