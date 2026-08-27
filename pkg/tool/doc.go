// Package tool is Arcus's single tool abstraction. It is deliberately small:
// one interface (Tool), one built-in implementation (Func, name + schema +
// handler), a Result type, an ordered Set registry, and a dependency-free JSON
// Schema reflector (Reflect).
//
// A Tool is a callable: it advertises a Definition (name, description, argument
// schema) and an Invoke that runs it. The handler signature is the lowest common
// denominator — func(ctx, json.RawMessage) (*Result, error) — so every richer
// notion of a "kind" of tool (typed parameters, MCP-backed, skill-backed,
// CLI-backed, client-delegated) lives in a higher-level package that wraps Tool,
// not in here. Downstream code holds a Tool and never needs to know which kind
// it is.
//
//	type Args struct {
//	    City string `json:"city" desc:"City name"`
//	}
//	weather := tool.Func("get_weather", "Look up weather", tool.Reflect(Args{}),
//	    func(ctx context.Context, raw json.RawMessage) (*tool.Result, error) {
//	        var a Args
//	        if err := json.Unmarshal(raw, &a); err != nil {
//	            return tool.Errf("bad arguments: %v", err), nil
//	        }
//	        return tool.Textf("Sunny in %s.", a.City), nil
//	    })
//
//	set := tool.NewSet(weather)
//
// Advertise the set to a request — chat drivers render each Definition into the
// provider's native tool shape, so the same set works across OpenAI, DeepSeek,
// and Anthropic:
//
//	req := adapter.Request{
//	    Provider: adapter.OpenAI,
//	    Data:     &openai.Request{Model: "gpt-4o", Messages: msgs},
//	    Tools:    set.RequestTools(),
//	}
//	msg, _ := cli.Chat(ctx, req)
//	out, _ := chat.Result(msg)
//
// Dispatch the model's tool calls back against the same set:
//
//	for _, call := range out.ToolCalls {
//	    res, err := set.Invoke(ctx, call.Name, call.Args)
//	    _ = res // feed res.Content back as the provider's tool / tool_result message
//	    _ = err // non-nil err means the call could not be run (unknown name, ...)
//	}
//
// The package has no internal dependencies and adds nothing to go.mod.
package tool
