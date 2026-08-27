package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/arbureva/arcus/pkg/tool"
)

// subAgentArgs is the argument shape a delegating model fills in when it calls
// a sub-agent tool.
type subAgentArgs struct {
	Task string `json:"task" desc:"The complete, self-contained task for the sub-agent. Include all necessary context: the sub-agent cannot see this conversation."`
}

// AsTool wraps an Agent as a tool.Tool, which is all multi-agent coordination
// amounts to in Arcus: a coordinator agent delegates by function-calling into
// sub-agents, exactly as it calls any other tool. Because the result is a plain
// tool.Tool it composes with everything else — put it in a tool.Set, fold it
// into a toolbox.Box namespace, or hand it to yet another agent.
//
// seed builds a fresh Transcript per delegation, so every sub-task runs in an
// isolated context window (the coordinator's history is not leaked). The
// model-supplied task is appended as the sub-agent's first user message and the
// sub-agent's final answer text is returned as the tool result:
//
//	researcher := agent.AsTool("researcher",
//	    "Delegate a research question to a dedicated research agent.",
//	    subAgent,
//	    func() (agent.Transcript, error) {
//	        return agent.NewTranscript(adapter.OpenAI, &openai.Request{
//	            Model:    "gpt-4o",
//	            Messages: []openai.Message{openai.SystemMessage("你是一名研究员……")},
//	        })
//	    })
//	coordinator := agent.New(cli, agent.WithTools(tool.NewSet(researcher, ...)))
//
// The sub-agent may use a different provider or model than the coordinator —
// the Transcript decides.
func AsTool(name, description string, a *Agent, seed func() (Transcript, error)) tool.Tool {
	if a == nil {
		panic("agent: AsTool with nil agent")
	}
	if seed == nil {
		panic("agent: AsTool with nil transcript seed")
	}
	return tool.Func(name, description, tool.Reflect(subAgentArgs{}),
		func(ctx context.Context, raw json.RawMessage) (*tool.Result, error) {
			var args subAgentArgs
			if err := json.Unmarshal(raw, &args); err != nil {
				return tool.Errf("bad arguments: %v", err), nil
			}
			if args.Task == "" {
				return tool.Err("task must not be empty"), nil
			}
			tr, err := seed()
			if err != nil {
				return nil, err // host wiring problem, not a model-visible failure
			}
			tr.User(args.Task)
			out, err := a.Run(ctx, tr)
			switch {
			case errors.Is(err, ErrMaxSteps):
				return tool.Err("sub-agent hit its step budget before finishing"), nil
			case err != nil:
				return tool.Errf("sub-agent failed: %v", err), nil
			}
			if out.Text() == "" {
				return tool.Err("sub-agent produced no answer"), nil
			}
			return tool.Text(out.Text()), nil
		})
}
