package transcripts_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/arbureva/arcus/pkg/adapter"
	"github.com/arbureva/arcus/pkg/agent"
	ansdk "github.com/arbureva/arcus/pkg/anthropic"
	"github.com/arbureva/arcus/pkg/chat"
	dssdk "github.com/arbureva/arcus/pkg/deepseek"
	oasdk "github.com/arbureva/arcus/pkg/openai"
	"github.com/arbureva/arcus/pkg/tool"

	_ "github.com/arbureva/arcus/pkg/agent/transcripts/anthropic"
	_ "github.com/arbureva/arcus/pkg/agent/transcripts/deepseek"
	_ "github.com/arbureva/arcus/pkg/agent/transcripts/openai"
)

func toolTurn() (*chat.Completion, []agent.ToolReturn) {
	comp := &chat.Completion{
		Text: "let me check",
		ToolCalls: []chat.ToolCall{
			{ID: "c1", Name: "get_weather", Args: json.RawMessage(`{"city":"Shanghai"}`)},
			{ID: "c2", Name: "get_time", Args: json.RawMessage(`{}`)},
		},
	}
	returns := []agent.ToolReturn{
		{Call: comp.ToolCalls[0], Result: tool.Text("24C")},
		{Call: comp.ToolCalls[1], Result: tool.Err("clock broken")},
	}
	return comp, returns
}

func TestOpenAITranscript(t *testing.T) {
	tr, err := agent.NewTranscript(adapter.OpenAI, &oasdk.Request{
		Model:    "gpt-4o",
		Messages: []oasdk.Message{oasdk.SystemMessage("be brief")},
	})
	if err != nil {
		t.Fatal(err)
	}
	tr.User("weather?")
	comp, returns := toolTurn()
	tr.Assistant(comp)
	tr.ToolResults(returns)

	msgs := tr.Messages().([]oasdk.Message)
	if len(msgs) != 5 { // system, user, assistant, tool, tool
		t.Fatalf("len(msgs) = %d", len(msgs))
	}
	if msgs[2].Role != oasdk.RoleAssistant || len(msgs[2].ToolCalls) != 2 {
		t.Fatalf("assistant turn = %+v", msgs[2])
	}
	if msgs[3].Role != oasdk.RoleTool || msgs[3].ToolCallID != "c1" || msgs[3].Content != "24C" {
		t.Fatalf("tool turn = %+v", msgs[3])
	}

	// Request must snapshot: two consecutive snapshots must not share the
	// native Tools slice (drivers append rendered tools into it).
	r1 := tr.Request(nil).Data.(*oasdk.Request)
	r1.Tools = append(r1.Tools, oasdk.Tool{Type: "function"})
	r2 := tr.Request(nil).Data.(*oasdk.Request)
	if len(r2.Tools) != 0 {
		t.Fatalf("tools leaked across snapshots: %d", len(r2.Tools))
	}
	if len(r2.Messages) != 5 {
		t.Fatalf("snapshot messages = %d", len(r2.Messages))
	}
}

func TestAnthropicTranscriptBatchesResults(t *testing.T) {
	tr, err := agent.NewTranscript(adapter.Anthropic, &ansdk.Request{Model: "m", MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	tr.User("weather?")
	comp, returns := toolTurn()
	tr.Assistant(comp)
	tr.ToolResults(returns)

	msgs := tr.Messages().([]ansdk.Message)
	if len(msgs) != 3 { // user, assistant(tool_use), user(tool_result x2)
		t.Fatalf("len(msgs) = %d", len(msgs))
	}
	asst := msgs[1]
	if asst.Role != ansdk.RoleAssistant || len(asst.ToolUses()) != 2 {
		t.Fatalf("assistant turn = %+v", asst)
	}
	res := msgs[2]
	if res.Role != ansdk.RoleUser || len(res.Content) != 2 {
		t.Fatalf("result turn = %+v", res)
	}
	if res.Content[0].Type != ansdk.BlockToolResult {
		t.Fatalf("result block = %+v", res.Content[0])
	}
}

func TestAnthropicTranscriptPrefersRaw(t *testing.T) {
	tr, _ := agent.NewTranscript(adapter.Anthropic, ansdk.Request{Model: "m", MaxTokens: 1})
	raw := &ansdk.Message{
		Role: ansdk.RoleAssistant,
		Content: []ansdk.ContentBlock{
			{Type: ansdk.BlockThinking, Thinking: "hmm", Signature: "sig"},
			ansdk.ToolUseBlock("c1", "t", json.RawMessage(`{}`)),
		},
	}
	tr.Assistant(&chat.Completion{ToolCalls: []chat.ToolCall{{ID: "c1", Name: "t"}}, Raw: raw})
	msgs := tr.Messages().([]ansdk.Message)
	if len(msgs[0].Content) != 2 || msgs[0].Content[0].Signature != "sig" {
		t.Fatalf("raw content not preserved: %+v", msgs[0].Content)
	}
}

func TestDeepseekTranscriptStripsReasoning(t *testing.T) {
	tr, err := agent.NewTranscript(adapter.Deepseek, &dssdk.Request{Model: "deepseek-reasoner"})
	if err != nil {
		t.Fatal(err)
	}
	raw := &dssdk.ChatCompletion{Choices: []dssdk.Choice{{
		Message: dssdk.Message{Role: dssdk.RoleAssistant, Content: "hi", ReasoningContent: "secret chain"},
	}}}
	tr.Assistant(&chat.Completion{Text: "hi", Reasoning: "secret chain", Raw: raw})
	msgs := tr.Messages().([]dssdk.Message)
	if msgs[0].ReasoningContent != "" {
		t.Fatal("reasoning_content must be stripped before re-sending")
	}
	if msgs[0].Content != "hi" {
		t.Fatalf("content = %q", msgs[0].Content)
	}
}

func TestTranscriptTypeMismatch(t *testing.T) {
	if _, err := agent.NewTranscript(adapter.OpenAI, 42); err == nil {
		t.Fatal("expected type mismatch")
	}
}

// Every built-in transcript must shrink old tool results and leave the newest.
func TestTranscriptsElideOldToolResults(t *testing.T) {
	long := strings.Repeat("x", 500)
	for _, c := range []struct {
		p      adapter.Provider
		native any
		texts  func(any) []string
	}{
		{adapter.OpenAI, &oasdk.Request{Model: "m"}, func(m any) (out []string) {
			for _, x := range m.([]oasdk.Message) {
				if x.Role == oasdk.RoleTool {
					out = append(out, x.Content)
				}
			}
			return
		}},
		{adapter.Anthropic, &ansdk.Request{Model: "m"}, func(m any) (out []string) {
			for _, x := range m.([]ansdk.Message) {
				for _, b := range x.Content {
					if b.Type == ansdk.BlockToolResult {
						var s string
						_ = json.Unmarshal(b.Content, &s)
						out = append(out, s)
					}
				}
			}
			return
		}},
	} {
		tr, err := agent.NewTranscript(c.p, c.native)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b"} {
			call := chat.ToolCall{ID: id, Name: "bash", Args: json.RawMessage(`{}`)}
			tr.Assistant(&chat.Completion{ToolCalls: []chat.ToolCall{call}})
			tr.ToolResults([]agent.ToolReturn{{Call: call, Result: tool.Text(long)}})
		}
		tr.(agent.ToolResultEliding).ElideToolResults(1, 100)
		got := c.texts(tr.Messages())
		if len(got) != 2 || len(got[0]) >= len(long) || got[1] != long {
			t.Fatalf("%s: lens = %d, %d", c.p, len(got[0]), len(got[1]))
		}
	}
}

// 带图的结果：Anthropic 进 tool_result 的 content 数组（text + base64 image），
// 其它协议只回文字并注明看不了图。
func TestToolResultImages(t *testing.T) {
	call := chat.ToolCall{ID: "c1", Name: "view_image", Args: json.RawMessage(`{}`)}
	ret := []agent.ToolReturn{{Call: call, Result: tool.Text("preview").WithImage("image/png", []byte{1, 2, 3})}}

	tr, _ := agent.NewTranscript(adapter.Anthropic, &ansdk.Request{Model: "m", MaxTokens: 1})
	tr.Assistant(&chat.Completion{ToolCalls: []chat.ToolCall{call}})
	tr.ToolResults(ret)
	got, _ := json.Marshal(tr.Messages().([]ansdk.Message)[1])
	want := `{"role":"user","content":[{"type":"tool_result","tool_use_id":"c1","content":[{"type":"text","text":"preview"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AQID"}}]}]}`
	if string(got) != want {
		t.Fatalf("anthropic tool_result =\n%s\nwant\n%s", got, want)
	}

	tr, _ = agent.NewTranscript(adapter.OpenAI, &oasdk.Request{Model: "m"})
	tr.Assistant(&chat.Completion{ToolCalls: []chat.ToolCall{call}})
	tr.ToolResults(ret)
	if c := tr.Messages().([]oasdk.Message)[1].Content; c != "preview\n"+tool.ImagesUnsupported {
		t.Fatalf("openai tool content = %q", c)
	}
}
