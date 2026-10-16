package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/arbureva/arcus/pkg/chat"
	"github.com/arbureva/arcus/pkg/tool"
)

func TestElide(t *testing.T) {
	s := strings.Repeat("头", 100) + strings.Repeat("尾", 100) // 3 bytes per rune
	got := Elide(s, 100)
	if !utf8.ValidString(got) {
		t.Fatal("cut inside a rune")
	}
	if !strings.HasPrefix(got, "头") || !strings.HasSuffix(got, "尾") || !strings.Contains(got, elidedMark) {
		t.Fatalf("got %q", got)
	}
	if Elide(got, 100) != got {
		t.Fatal("not idempotent")
	}
	if Elide("short", 100) != "short" {
		t.Fatal("short input changed")
	}
}

func TestElideTailKeepsNewest(t *testing.T) {
	msgs := []string{"r1-" + strings.Repeat("x", 50), "user", "r2-" + strings.Repeat("x", 50)}
	ElideTail(len(msgs), func(i int) *string {
		if msgs[i] == "user" {
			return nil
		}
		return &msgs[i]
	}, 1, 10)
	if !strings.Contains(msgs[0], elidedMark) || strings.Contains(msgs[2], elidedMark) || msgs[1] != "user" {
		t.Fatalf("msgs = %q", msgs)
	}
}

func TestRunWrapUpInsteadOfMaxSteps(t *testing.T) {
	loop := toolCallCompletion(chat.ToolCall{ID: "1", Name: "echo", Args: json.RawMessage(`{}`)})
	conn := &mockConn{script: []*chat.Completion{loop, loop, {Text: "progress report"}}}
	var steps []int
	ag := New(newMockClient(t, conn), WithTools(tool.NewSet(echoTool())), WithMaxSteps(2),
		WithWrapUp("wrap up"), WithHooks(Hooks{OnCompletion: func(s int, _ *chat.Completion) { steps = append(steps, s) }}))

	tr := &memTranscript{}
	out, err := ag.Run(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	if out.Text() != "progress report" {
		t.Fatalf("final = %q", out.Text())
	}
	if conn.seenTools[2] != 0 {
		t.Fatal("wrap-up turn must not advertise tools")
	}
	if tr.log[len(tr.log)-2] != "user:wrap up" {
		t.Fatalf("transcript = %q", tr.log)
	}
	if len(steps) != 3 || steps[2] != 3 {
		t.Fatalf("steps = %v", steps)
	}
}

func TestRunStreamGateStopsEarly(t *testing.T) {
	loop := toolCallCompletion(chat.ToolCall{ID: "1", Name: "echo", Args: json.RawMessage(`{}`)})
	conn := &mockConn{script: []*chat.Completion{loop, {Text: "out of credit"}}}
	ag := New(newMockClient(t, conn), WithTools(tool.NewSet(echoTool())), WithMaxSteps(10),
		WithWrapUp("wrap up"), WithStepGate(func(step int) bool { return step < 2 }))

	ch, err := ag.RunStream(context.Background(), &memTranscript{})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for c := range ch {
		if s, ok := chat.AsText(&c); ok {
			text += s
		}
		if e, ok := chat.AsError(&c); ok {
			t.Fatalf("stream error: %v", e)
		}
	}
	if text != "out of credit" || conn.calls != 2 {
		t.Fatalf("text = %q, calls = %d", text, conn.calls)
	}
}
