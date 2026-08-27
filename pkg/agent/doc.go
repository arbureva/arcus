// Package agent implements a provider-agnostic ReAct loop on top of pkg/chat
// and pkg/tool: the model is called, its tool calls are dispatched through a
// tool source, the results are folded back into the conversation, and the loop
// repeats until the model produces a final answer (or the step budget runs out).
//
// The one genuinely provider-specific job in such a loop — rebuilding the
// assistant tool-call turn and the tool-result turn as native messages — lives
// behind the Transcript interface. Transcript implementations follow the exact
// same driver model as pkg/chat: the core imports no provider package, and a
// blank import of pkg/agent/transcripts/<provider> registers the native
// implementation:
//
//	import (
//	    _ "github.com/arbureva/arcus/pkg/chat/drivers/openai"
//	    _ "github.com/arbureva/arcus/pkg/agent/transcripts/openai"
//	)
//
//	ag := agent.New(cli, agent.WithTools(tools))
//	tr, _ := agent.NewTranscript(adapter.OpenAI, &openai.Request{Model: "gpt-4o"})
//	tr.User("What's the weather in Shanghai?")
//	out, _ := ag.Run(ctx, tr)
//	fmt.Println(out.Final.Text)
//
// As everywhere else in Arcus, the caller hands the agent the provider's
// native request value and therefore always knows exactly which protocol it is
// speaking; the agent only owns the loop.
package agent
