package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/tool"
)

// recTranscript keeps the completions it was handed, so a test can check what
// the next turn would actually be sent.
type recTranscript struct {
	memTranscript
	assistants []*chat.Completion
}

func (t *recTranscript) Assistant(c *chat.Completion) {
	t.assistants = append(t.assistants, c)
	t.memTranscript.Assistant(c)
}

// The final answer must land in the transcript, or a follow-up turn on the same
// transcript asks the model to elaborate on an answer it can no longer see.
func TestFinalAnswerRecorded(t *testing.T) {
	final := &chat.Completion{Text: "final", Reasoning: "because"}
	conn := &mockConn{script: []*chat.Completion{
		toolCallCompletion(chat.ToolCall{ID: "1", Name: "echo", Args: json.RawMessage(`{}`)}),
		final,
	}}
	ag := New(newMockClient(t, conn), WithTools(tool.NewSet(echoTool())))

	tr := &recTranscript{}
	if _, err := ag.Run(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	if len(tr.assistants) != 2 || tr.assistants[1].Text != "final" {
		t.Fatalf("assistant turns = %+v", tr.assistants)
	}
	if tr.assistants[1].Reasoning != "because" {
		t.Errorf("reasoning lost: %q", tr.assistants[1].Reasoning)
	}
}

// The streaming path must reach the transcript with the same fidelity as the
// non-streaming one: reasoning text plus the native result (ChunkDone), which
// is the only carrier of Anthropic thinking signatures.
func TestRunStreamKeepsThinkingAndRaw(t *testing.T) {
	native := &struct{ Marker string }{"native-with-signature"}
	conn := &mockConn{script: []*chat.Completion{
		{Text: "answer", Reasoning: "step by step", Raw: native},
	}}
	ag := New(newMockClient(t, conn))

	tr := &recTranscript{}
	ch, err := ag.RunStream(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	var thinking string
	for c := range ch {
		if s, ok := chat.AsThinking(&c); ok {
			thinking += s
		}
		if e, ok := chat.AsError(&c); ok {
			t.Fatalf("stream error: %v", e)
		}
	}
	if thinking != "step by step" {
		t.Errorf("streamed thinking = %q", thinking)
	}
	if len(tr.assistants) != 1 {
		t.Fatalf("assistant turns = %d, want 1", len(tr.assistants))
	}
	got := tr.assistants[0]
	if got.Reasoning != "step by step" {
		t.Errorf("transcript reasoning = %q", got.Reasoning)
	}
	if got.Raw != native {
		t.Errorf("native result lost: %#v", got.Raw)
	}
}
