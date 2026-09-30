// Package mimo implements the Xiaomi MiMo chat completions API natively.
//
// The wire shape resembles OpenAI's chat completions but is modelled
// independently here. MiMo-specific behaviour is first-class: the Thinking
// switch (MiMo has no reasoning_effort), max_completion_tokens covering
// reasoning + answer, ReasoningContent that must be echoed back on tool-call
// turns, cached/media token counters in Usage, and the repetition_truncation
// finish reason.
//
// Gateways that host MiMo under its own dialect (e.g. Tencent TokenHub) work
// by overriding BaseURL.
//
//	c := mimo.New(mimo.Config{APIKey: key})
//
//	resp, err := c.Chat(ctx, &mimo.Request{
//		Model:    "mimo-v2.6-flash",
//		Messages: []mimo.Message{mimo.UserMessage("9.11 or 9.8 — which is greater?")},
//		Thinking: mimo.DisableThinking(),
//	})
package mimo
