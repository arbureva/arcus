package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/tool"
)

// ErrMaxSteps is returned (wrapped in the error, alongside a partial Outcome)
// when the loop hits its step budget while the model is still asking for tools.
var ErrMaxSteps = errors.New("agent: max steps reached")

// Tools is the tool source an Agent draws from. *tool.Set satisfies it as-is;
// *toolbox.Box satisfies it too, adding progressive disclosure — the agent
// re-reads RequestTools every step, so a source whose visible tools change
// mid-run (a Box after an activation) Just Works.
type Tools interface {
	// RequestTools returns the tools to advertise on the next request, boxed
	// for adapter.Request.Tools.
	RequestTools() []interface{}

	// Invoke dispatches one model tool-call by name.
	Invoke(ctx context.Context, name string, args json.RawMessage) (*tool.Result, error)
}

// Hooks are optional observation points. Nil funcs are skipped. They run
// synchronously inside the loop, so keep them fast.
type Hooks struct {
	// OnCompletion fires after every model turn, before tools are dispatched.
	OnCompletion func(step int, comp *chat.Completion)
	// OnToolCall fires before a tool is invoked.
	OnToolCall func(step int, call chat.ToolCall)
	// OnToolResult fires after a tool returns. err is the host-level failure
	// (unknown tool, wiring); a tool-level failure arrives as res.IsError.
	OnToolResult func(step int, call chat.ToolCall, res *tool.Result, err error)
}

// Agent runs the tool-use loop. It is stateless across runs and safe for
// concurrent use — all conversation state lives in the Transcript.
type Agent struct {
	cli      *chat.Client
	tools    Tools
	maxSteps int
	hooks    Hooks
}

// Option configures an Agent.
type Option func(*Agent)

// WithTools sets the tool source (a *tool.Set, a *toolbox.Box, or anything
// else implementing Tools). Without it the agent is a plain chat loop that
// stops after the first completion.
func WithTools(t Tools) Option { return func(a *Agent) { a.tools = t } }

// WithMaxSteps caps how many model turns a single Run may take (default 8).
// One "step" is one completion; tool dispatch between turns is free.
func WithMaxSteps(n int) Option {
	return func(a *Agent) {
		if n > 0 {
			a.maxSteps = n
		}
	}
}

// WithHooks installs observation hooks.
func WithHooks(h Hooks) Option { return func(a *Agent) { a.hooks = h } }

// New builds an Agent over an already-configured chat.Client.
func New(cli *chat.Client, opts ...Option) *Agent {
	a := &Agent{cli: cli, maxSteps: 8}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Step records one model turn and the tool exchanges it triggered.
type Step struct {
	Completion *chat.Completion `json:"completion"`
	Returns    []ToolReturn     `json:"returns,omitempty"`
}

// Outcome is the result of a Run: the final completion, every intermediate
// step, and aggregated token usage across all turns.
type Outcome struct {
	Final *chat.Completion `json:"final,omitempty"`
	Steps []Step           `json:"steps"`
	Usage chat.Usage       `json:"usage"`
}

// Text is a convenience accessor for the final answer's text ("" if the run
// produced no final completion).
func (o *Outcome) Text() string {
	if o == nil || o.Final == nil {
		return ""
	}
	return o.Final.Text
}

func (o *Outcome) addUsage(u *chat.Usage) {
	if u == nil {
		return
	}
	o.Usage.InputTokens += u.InputTokens
	o.Usage.OutputTokens += u.OutputTokens
	o.Usage.TotalTokens += u.TotalTokens
}

func (a *Agent) requestTools() []interface{} {
	if a.tools == nil {
		return nil
	}
	return a.tools.RequestTools()
}

// dispatch runs every tool call in order and returns the paired results. A
// host-level failure (unknown tool, nil source) is surfaced to the model as an
// IsError result so the loop can continue.
func (a *Agent) dispatch(ctx context.Context, step int, calls []chat.ToolCall) []ToolReturn {
	returns := make([]ToolReturn, 0, len(calls))
	for _, call := range calls {
		if a.hooks.OnToolCall != nil {
			a.hooks.OnToolCall(step, call)
		}
		var (
			res *tool.Result
			err error
		)
		if a.tools == nil {
			err = errors.New("agent: no tool source configured")
		} else {
			res, err = a.tools.Invoke(ctx, call.Name, call.Args)
		}
		if a.hooks.OnToolResult != nil {
			a.hooks.OnToolResult(step, call, res, err)
		}
		if err != nil {
			res = tool.Errf("tool %q could not be run: %v", call.Name, err)
		}
		if res == nil {
			res = tool.Err("tool returned no result")
		}
		returns = append(returns, ToolReturn{Call: call, Result: res})
	}
	return returns
}

// Run executes the loop until the model stops calling tools or the step budget
// is exhausted. On ErrMaxSteps the partial Outcome (with all steps and usage so
// far) is still returned alongside the error.
func (a *Agent) Run(ctx context.Context, tr Transcript) (*Outcome, error) {
	out := &Outcome{}
	for step := 1; step <= a.maxSteps; step++ {
		msg, err := a.cli.Chat(ctx, tr.Request(a.requestTools()))
		if err != nil {
			return out, err
		}
		comp, ok := chat.Result(msg)
		if !ok {
			return out, errors.New("agent: driver returned a non-Completion result")
		}
		out.addUsage(comp.Usage)
		if a.hooks.OnCompletion != nil {
			a.hooks.OnCompletion(step, comp)
		}

		if len(comp.ToolCalls) == 0 {
			out.Final = comp
			out.Steps = append(out.Steps, Step{Completion: comp})
			return out, nil
		}

		tr.Assistant(comp)
		returns := a.dispatch(ctx, step, comp.ToolCalls)
		tr.ToolResults(returns)
		out.Steps = append(out.Steps, Step{Completion: comp, Returns: returns})

		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
	return out, ErrMaxSteps
}

var _ Tools = (*tool.Set)(nil)
