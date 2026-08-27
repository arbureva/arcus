// Package deepseek registers the DeepSeek transcript for Arcus's agent layer.
// Blank-import it to enable agent.NewTranscript(adapter.Deepseek, ...):
//
//	import _ "github.com/arbureva/arcus/pkg/agent/transcripts/deepseek"
package deepseek

import (
	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/agent"
	"github.com/arbureva/arcus/pkg/chat"
	sdk "github.com/arbureva/arcus/pkg/deepseek"
	"github.com/arbureva/arcus/pkg/ecode"
)

func init() {
	agent.RegisterTranscript(adapter.Deepseek, newTranscript)
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

func (t *transcript) Provider() adapter.Provider { return adapter.Deepseek }
func (t *transcript) Messages() interface{}      { return t.msgs }

func (t *transcript) User(text string) {
	t.msgs = append(t.msgs, sdk.UserMessage(text))
}

func (t *transcript) Assistant(comp *chat.Completion) {
	var m sdk.Message
	if raw, ok := comp.Raw.(*sdk.ChatCompletion); ok && len(raw.Choices) > 0 {
		m = raw.Choices[0].Message
	} else {
		m = sdk.Message{Role: sdk.RoleAssistant, Content: comp.Text}
		for _, call := range comp.ToolCalls {
			m.ToolCalls = append(m.ToolCalls, sdk.ToolCall{
				ID:       call.ID,
				Type:     "function",
				Function: sdk.FunctionCall{Name: call.Name, Arguments: string(call.Args)},
			})
		}
	}
	// DeepSeek rejects reasoning_content echoed back into the context; the
	// chain-of-thought is response-only, so strip it before re-sending.
	m.ReasoningContent = ""
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
	r := t.req
	r.Messages = t.msgs
	r.Tools = append([]sdk.Tool(nil), t.req.Tools...)
	return adapter.Request{Provider: adapter.Deepseek, Data: &r, Tools: tools}
}
