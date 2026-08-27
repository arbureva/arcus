// Package openai registers the OpenAI transcript for Arcus's agent layer.
// Blank-import it to enable agent.NewTranscript(adapter.OpenAI, ...):
//
//	import _ "github.com/arbureva/arcus/pkg/agent/transcripts/openai"
package openai

import (
	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/agent"
	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/ecode"
	sdk "github.com/arbureva/arcus/pkg/openai"
)

func init() {
	agent.RegisterTranscript(adapter.OpenAI, newTranscript)
}

// newTranscript accepts *sdk.Request or sdk.Request. Any messages already on
// the request become the opening history (system prompt, few-shot turns, ...).
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
	req  sdk.Request // template: model, params, natively-set tools — no messages
	msgs []sdk.Message
}

func (t *transcript) Provider() adapter.Provider { return adapter.OpenAI }
func (t *transcript) Messages() interface{}      { return t.msgs }

func (t *transcript) User(text string) {
	t.msgs = append(t.msgs, sdk.UserMessage(text))
}

func (t *transcript) Assistant(comp *chat.Completion) {
	// Prefer the wire-exact native message so nothing is lost in translation.
	if raw, ok := comp.Raw.(*sdk.ChatCompletion); ok && len(raw.Choices) > 0 {
		t.msgs = append(t.msgs, raw.Choices[0].Message)
		return
	}
	// Fallback (streaming): rebuild from the normalized fields.
	m := sdk.Message{Role: sdk.RoleAssistant, Content: comp.Text}
	for _, call := range comp.ToolCalls {
		m.ToolCalls = append(m.ToolCalls, sdk.ToolCall{
			ID:       call.ID,
			Type:     "function",
			Function: sdk.FunctionCall{Name: call.Name, Arguments: string(call.Args)},
		})
	}
	t.msgs = append(t.msgs, m)
}

func (t *transcript) ToolResults(returns []agent.ToolReturn) {
	for _, r := range returns {
		content := r.Result.Content
		if r.Result.IsError && content == "" {
			content = "tool failed"
		}
		t.msgs = append(t.msgs, sdk.ToolMessage(r.Call.ID, content))
	}
}

func (t *transcript) Request(tools []interface{}) adapter.Request {
	r := t.req // copy the template
	r.Messages = t.msgs
	// Drivers append rendered tools to r.Tools; give them a private slice so
	// repeated snapshots never accumulate duplicates.
	r.Tools = append([]sdk.Tool(nil), t.req.Tools...)
	return adapter.Request{Provider: adapter.OpenAI, Data: &r, Tools: tools}
}
