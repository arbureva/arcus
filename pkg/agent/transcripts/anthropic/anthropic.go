// Package anthropic registers the Anthropic transcript for Arcus's agent
// layer. Blank-import it to enable agent.NewTranscript(adapter.Anthropic, ...):
//
//	import _ "github.com/arbureva/arcus/pkg/agent/transcripts/anthropic"
package anthropic

import (
	"encoding/base64"
	"encoding/json"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/agent"
	sdk "github.com/arbureva/arcus/pkg/anthropic"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/ecode"
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
		blocks = append(blocks, toolResult(r))
	}
	t.msgs = append(t.msgs, sdk.UserBlocks(blocks...))
}

// toolResult carries images as base64 image blocks in the tool_result content
// array; text-only results keep the plain-string form ElideToolResults trims.
func toolResult(r agent.ToolReturn) sdk.ContentBlock {
	if len(r.Result.Images) == 0 {
		return sdk.ToolResultText(r.Call.ID, r.Result.Content, r.Result.IsError)
	}
	var parts []sdk.ContentBlock
	if r.Result.Content != "" {
		parts = append(parts, sdk.TextBlock(r.Result.Content))
	}
	for _, img := range r.Result.Images {
		parts = append(parts, sdk.ImageBase64Block(img.MIME, base64.StdEncoding.EncodeToString(img.Data)))
	}
	b, _ := sdk.ToolResultBlocks(r.Call.ID, parts, r.Result.IsError) // blocks of strings always marshal
	return b
}

// ElideToolResults implements agent.ToolResultEliding. Results ride as
// tool_result blocks inside user messages; only the plain-text form (what
// ToolResults writes) is elided — block-array content is left alone.
func (t *transcript) ElideToolResults(keep, max int) {
	type ref struct{ msg, block int }
	var refs []ref
	for i := range t.msgs {
		for j, b := range t.msgs[i].Content {
			if b.Type == sdk.BlockToolResult {
				refs = append(refs, ref{i, j})
			}
		}
	}
	texts := make([]*string, len(refs))
	for k, r := range refs {
		var s string
		if json.Unmarshal(t.msgs[r.msg].Content[r.block].Content, &s) == nil {
			texts[k] = &s
		}
	}
	agent.ElideTail(len(refs), func(k int) *string { return texts[k] }, keep, max)
	for k, r := range refs {
		if texts[k] != nil {
			raw, _ := json.Marshal(*texts[k])
			t.msgs[r.msg].Content[r.block].Content = raw
		}
	}
}

func (t *transcript) Request(tools []interface{}) adapter.Request {
	r := t.req
	r.Messages = t.msgs
	r.Tools = append([]sdk.Tool(nil), t.req.Tools...)
	return adapter.Request{Provider: adapter.Anthropic, Data: &r, Tools: tools}
}
var _ agent.ToolResultEliding = (*transcript)(nil)
