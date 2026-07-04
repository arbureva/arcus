package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/Arbureva/ice-adk/pkg/adapter"
	"github.com/Arbureva/ice-adk/pkg/chat"
)

// Agent-level chunk kinds, emitted by RunStream in addition to the chat-level
// kinds it passes through:
//
//	ChunkToolResult -> Data is *ToolReturn (a tool ran between model turns)
const (
	ChunkToolResult = "tool_result"
)

// AsToolReturn extracts a *ToolReturn from a ChunkToolResult chunk.
func AsToolReturn(msg *adapter.ChunkMessageAdapter) (*ToolReturn, bool) {
	if msg == nil || msg.Kind != ChunkToolResult {
		return nil, false
	}
	r, ok := msg.Data.(*ToolReturn)
	return r, ok
}

// RunStream executes the same loop as Run, but streams. Every chat-level chunk
// (text / thinking / tool_call / stop / usage) is passed through as it arrives;
// when a model turn ends in tool calls, the agent assembles them from the
// fragments, dispatches them, emits one ChunkToolResult per call, and starts
// the next turn on the same channel. The channel closes when the run ends —
// after the final answer, on ErrMaxSteps, or after a terminal ChunkError.
func (a *Agent) RunStream(ctx context.Context, tr Transcript) (<-chan adapter.ChunkMessageAdapter, error) {
	out := make(chan adapter.ChunkMessageAdapter)
	go func() {
		defer close(out)
		emit := func(m adapter.ChunkMessageAdapter) bool {
			select {
			case out <- m:
				return true
			case <-ctx.Done():
				return false
			}
		}
		fail := func(err error) { emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkError, Data: err}) }

		for step := 1; step <= a.maxSteps; step++ {
			ch, err := a.cli.ChatStream(ctx, tr.Request(a.requestTools()))
			if err != nil {
				fail(err)
				return
			}

			comp, ok := a.consume(ch, emit)
			if !ok { // consumer gone or terminal stream error already emitted
				return
			}
			if a.hooks.OnCompletion != nil {
				a.hooks.OnCompletion(step, comp)
			}
			if len(comp.ToolCalls) == 0 {
				return // final answer already streamed through
			}

			tr.Assistant(comp)
			returns := a.dispatch(ctx, step, comp.ToolCalls)
			for i := range returns {
				if !emit(adapter.ChunkMessageAdapter{Kind: ChunkToolResult, Data: &returns[i]}) {
					return
				}
			}
			tr.ToolResults(returns)
		}
		fail(ErrMaxSteps)
	}()
	return out, nil
}

// consume drains one model turn's stream, passing chunks through and
// assembling a Completion from them. It returns ok=false when the run must
// stop (consumer cancelled, or a ChunkError was passed through).
func (a *Agent) consume(ch <-chan adapter.ChunkMessageAdapter, emit func(adapter.ChunkMessageAdapter) bool) (*chat.Completion, bool) {
	var (
		text      strings.Builder
		reasoning strings.Builder
		stop      string
		usage     *chat.Usage
		calls     = map[int]*pendingCall{}
		failed    bool
	)
	for c := range ch {
		if !emit(c) {
			return nil, false
		}
		switch c.Kind {
		case chat.ChunkText:
			if s, ok := chat.AsText(&c); ok {
				text.WriteString(s)
			}
		case chat.ChunkThinking:
			if s, ok := chat.AsThinking(&c); ok {
				reasoning.WriteString(s)
			}
		case chat.ChunkToolCall:
			if tc, ok := chat.AsToolCall(&c); ok {
				p := calls[tc.Index]
				if p == nil {
					p = &pendingCall{}
					calls[tc.Index] = p
				}
				if tc.ID != "" {
					p.id = tc.ID
				}
				if tc.Name != "" {
					p.name = tc.Name
				}
				p.args.WriteString(tc.ArgsDelta)
			}
		case chat.ChunkStop:
			if s, ok := chat.AsStop(&c); ok {
				stop = s
			}
		case chat.ChunkUsage:
			if u, ok := chat.AsUsage(&c); ok {
				usage = u
			}
		case chat.ChunkError:
			failed = true
		}
	}
	if failed {
		return nil, false
	}

	comp := &chat.Completion{
		Text:       text.String(),
		Reasoning:  reasoning.String(),
		StopReason: stop,
		Usage:      usage,
	}
	if len(calls) > 0 {
		idx := make([]int, 0, len(calls))
		for i := range calls {
			idx = append(idx, i)
		}
		sort.Ints(idx)
		for _, i := range idx {
			p := calls[i]
			args := p.args.String()
			if args == "" {
				args = "{}"
			}
			comp.ToolCalls = append(comp.ToolCalls, chat.ToolCall{
				ID:   p.id,
				Name: p.name,
				Args: json.RawMessage(args),
			})
		}
	}
	return comp, true
}

type pendingCall struct {
	id   string
	name string
	args strings.Builder
}
