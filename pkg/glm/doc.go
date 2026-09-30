// Package glm implements the Zhipu GLM (BigModel) chat completions API
// natively.
//
// The wire shape resembles OpenAI's chat completions but is modelled
// independently here. GLM-specific behaviour is first-class: the Thinking
// switch with clear_thinking, reasoning_effort on GLM-5.2+, do_sample,
// tool_stream, request_id/user_id, string business error codes, and the
// sensitive / network_error / model_context_window_exceeded finish reasons.
//
// Gateways that host GLM under its own dialect (e.g. Tencent TokenHub) work
// by overriding BaseURL.
//
//	c := glm.New(glm.Config{APIKey: key})
//
//	resp, err := c.Chat(ctx, &glm.Request{
//		Model:    "glm-5.1",
//		Messages: []glm.Message{glm.UserMessage("9.11 or 9.8 — which is greater?")},
//		Thinking: glm.EnableThinking(),
//	})
package glm
