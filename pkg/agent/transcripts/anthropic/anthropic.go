// Package anthropic registers the Anthropic transcript for IceADK's agent
// layer. Blank-import it to enable agent.NewTranscript(adapter.Anthropic, ...):
//
//	import _ "github.com/Arbureva/ice-adk/pkg/agent/transcripts/anthropic"
package anthropic

import (
	"github.com/Arbureva/ice-adk/pkg/adapter"
	"github.com/Arbureva/ice-adk/pkg/agent"
	sdk "github.com/Arbureva/ice-adk/pkg/anthropic"
	"github.com/Arbureva/ice-adk/pkg/chat"
	"github.com/Arbureva/ice-adk/pkg/ecode"
)

func init() {
	agent.RegisterTranscript(adapter.Anthropic, newTranscript)
}

func newTranscript(native any) (agent.Transcript, error) {
	var req sdk.Request
	switch v := native.(type) {
	case *sdk.Request:
		req = *v
	case sdk.Request:
		req = v
	default:
		return nil, ecode.TypeMismatch
	}
	t := &transcript{req: req, msgs: append([]sdk.Message(nil), req.Messages...)}
	t.req.Messages = nil
	return t, nil
}

type transcript struct {
	req  sdk.Request
	msgs []sdk.Message
}

func (t *transcript) Provider() adapter.Provider { return adapter.Anthropic }
func (t *transcript) Messages() interface{}      { return t.msgs }

func (t *transcript) User(text string) {
	t.msgs = append(t.msgs, sdk.UserText(text))
}

func (t *transcript) Assistant(comp *chat.Completion) {
	// Prefer the wire-exact content blocks: this preserves thinking blocks and
	// their signatures, which a rebuild from normalized fields cannot.
	if raw, ok := comp.Raw.(*sdk.Message); ok && len(raw.Content) > 0 {
		t.msgs = append(t.msgs, sdk.Message{Role: sdk.RoleAssistant, Content: raw.Content})
		return
	}
	// Fallback (streaming): text block + tool_use blocks. Thinking is dropped —
	// without its signature Anthropic would reject the echoed block anyway.
	var blocks []sdk.ContentBlock
	if comp.Text != "" {
		blocks = append(blocks, sdk.TextBlock(comp.Text))
	}
	for _, call := range comp.ToolCalls {
		blocks = append(blocks, sdk.ToolUseBlock(call.ID, call.Name, call.Args))
	}
	if len(blocks) == 0 {
		blocks = append(blocks, sdk.TextBlock(""))
	}
	t.msgs = append(t.msgs, sdk.AssistantBlocks(blocks...))
}

func (t *transcript) ToolResults(returns []agent.ToolReturn) {
	// Anthropic pairs results to calls by tool_use_id, and all results of a
	// turn ride back together in a single user message.
	blocks := make([]sdk.ContentBlock, 0, len(returns))
	for _, r := range returns {
		blocks = append(blocks, sdk.ToolResultText(r.Call.ID, r.Result.Content, r.Result.IsError))
	}
	t.msgs = append(t.msgs, sdk.UserBlocks(blocks...))
}

func (t *transcript) Request(tools []interface{}) adapter.Request {
	r := t.req
	r.Messages = t.msgs
	r.Tools = append([]sdk.Tool(nil), t.req.Tools...)
	return adapter.Request{Provider: adapter.Anthropic, Data: &r, Tools: tools}
}
