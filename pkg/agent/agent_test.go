package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Arbureva/ice-adk/pkg/adapter"
	"github.com/Arbureva/ice-adk/pkg/chat"
	"github.com/Arbureva/ice-adk/pkg/tool"
)

const mockProvider = adapter.Provider("mock")

// mockConn replays a script of completions, one per Chat/Stream call.
type mockConn struct {
	script []*chat.Completion
	calls  int
	// seenTools records how many tools were advertised on each request.
	seenTools []int
}

type mockDriver struct{ conn *mockConn }

func (c *mockConn) next() (*chat.Completion, error) {
	if c.calls >= len(c.script) {
		return nil, errors.New("mock: script exhausted")
	}
	comp := c.script[c.calls]
	c.calls++
	return comp, nil
}

func (c *mockConn) Chat(_ context.Context, req adapter.Request) (*adapter.MessageAdapter, error) {
	c.seenTools = append(c.seenTools, len(req.Tools))
	comp, err := c.next()
	if err != nil {
		return nil, err
	}
	return &adapter.MessageAdapter{Provider: mockProvider, Role: adapter.RoleAssistant, Data: comp}, nil
}

func (c *mockConn) Stream(_ context.Context, req adapter.Request, emit func(adapter.ChunkMessageAdapter) bool) error {
	c.seenTools = append(c.seenTools, len(req.Tools))
	comp, err := c.next()
	if err != nil {
		return err
	}
	if comp.Text != "" {
		emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkText, Data: comp.Text})
	}
	for i, call := range comp.ToolCalls {
		// split args across two fragments to exercise reassembly
		args := string(call.Args)
		emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkToolCall,
			Data: &chat.ToolCallChunk{Index: i, ID: call.ID, Name: call.Name, ArgsDelta: args[:len(args)/2]}})
		emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkToolCall,
			Data: &chat.ToolCallChunk{Index: i, ArgsDelta: args[len(args)/2:]}})
	}
	emit(adapter.ChunkMessageAdapter{Kind: chat.ChunkStop, Data: "stop"})
	return nil
}

// memTranscript is a provider-free Transcript for tests.
type memTranscript struct {
	log []string
}

func (t *memTranscript) Provider() adapter.Provider { return mockProvider }
func (t *memTranscript) Messages() interface{}      { return t.log }
func (t *memTranscript) User(text string)           { t.log = append(t.log, "user:"+text) }
func (t *memTranscript) Assistant(c *chat.Completion) {
	t.log = append(t.log, fmt.Sprintf("assistant:%d calls", len(c.ToolCalls)))
}
func (t *memTranscript) ToolResults(rs []ToolReturn) {
	for _, r := range rs {
		t.log = append(t.log, "result:"+r.Result.Content)
	}
}
func (t *memTranscript) Request(tools []interface{}) adapter.Request {
	return adapter.Request{Provider: mockProvider, Data: nil, Tools: tools}
}

func newMockClient(t *testing.T, conn *mockConn) *chat.Client {
	t.Helper()
	cli := chat.New()
	// chat.Register panics on duplicates; use a per-test provider via Use with
	// a fresh driver each time is impossible, so register once lazily.
	registerOnce(conn)
	if err := cli.Use(mockProvider, nil); err != nil {
		t.Fatal(err)
	}
	return cli
}

var registered *mockDriver

func registerOnce(conn *mockConn) {
	if registered == nil {
		registered = &mockDriver{conn: conn}
		chat.Register(mockProvider, registered)
		return
	}
	registered.conn = conn
}

// Open returns the shared conn; swap the script per test.
func (d *mockDriver) Open(any) (chat.Conn, error) { return d.conn, nil }

func echoTool() tool.Tool {
	return tool.Func("echo", "echoes", nil,
		func(_ context.Context, raw json.RawMessage) (*tool.Result, error) {
			return tool.Textf("echo=%s", string(raw)), nil
		})
}

func toolCallCompletion(calls ...chat.ToolCall) *chat.Completion {
	return &chat.Completion{ToolCalls: calls, StopReason: "tool_calls",
		Usage: &chat.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}}
}

func TestRunLoop(t *testing.T) {
	conn := &mockConn{script: []*chat.Completion{
		toolCallCompletion(chat.ToolCall{ID: "1", Name: "echo", Args: json.RawMessage(`{"a":1}`)}),
		{Text: "final answer", Usage: &chat.Usage{TotalTokens: 7}},
	}}
	cli := newMockClient(t, conn)
	ag := New(cli, WithTools(tool.NewSet(echoTool())))

	tr := &memTranscript{}
	tr.User("hi")
	out, err := ag.Run(context.Background(), tr)
	if err != nil {
		t.Fatal(err)
	}
	if out.Text() != "final answer" {
		t.Fatalf("final = %q", out.Text())
	}
	if len(out.Steps) != 2 || len(out.Steps[0].Returns) != 1 {
		t.Fatalf("steps = %+v", out.Steps)
	}
	if got := out.Steps[0].Returns[0].Result.Content; got != `echo={"a":1}` {
		t.Fatalf("tool result = %q", got)
	}
	if out.Usage.TotalTokens != 22 {
		t.Fatalf("usage = %+v", out.Usage)
	}
	want := []string{"user:hi", "assistant:1 calls", `result:echo={"a":1}`}
	for i, w := range want {
		if tr.log[i] != w {
			t.Fatalf("transcript[%d] = %q, want %q", i, tr.log[i], w)
		}
	}
}

func TestRunUnknownToolSurfacesToModel(t *testing.T) {
	conn := &mockConn{script: []*chat.Completion{
		toolCallCompletion(chat.ToolCall{ID: "1", Name: "nope", Args: json.RawMessage(`{}`)}),
		{Text: "recovered"},
	}}
	cli := newMockClient(t, conn)
	ag := New(cli, WithTools(tool.NewSet(echoTool())))

	out, err := ag.Run(context.Background(), &memTranscript{})
	if err != nil {
		t.Fatal(err)
	}
	ret := out.Steps[0].Returns[0]
	if !ret.Result.IsError {
		t.Fatal("expected IsError result for unknown tool")
	}
	if out.Text() != "recovered" {
		t.Fatalf("final = %q", out.Text())
	}
}

func TestRunMaxSteps(t *testing.T) {
	loop := toolCallCompletion(chat.ToolCall{ID: "1", Name: "echo", Args: json.RawMessage(`{}`)})
	conn := &mockConn{script: []*chat.Completion{loop, loop, loop}}
	cli := newMockClient(t, conn)
	ag := New(cli, WithTools(tool.NewSet(echoTool())), WithMaxSteps(2))

	out, err := ag.Run(context.Background(), &memTranscript{})
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("err = %v", err)
	}
	if len(out.Steps) != 2 {
		t.Fatalf("steps = %d", len(out.Steps))
	}
}

func TestRunStreamAssemblesAndContinues(t *testing.T) {
	conn := &mockConn{script: []*chat.Completion{
		toolCallCompletion(chat.ToolCall{ID: "c1", Name: "echo", Args: json.RawMessage(`{"city":"sh"}`)}),
		{Text: "done"},
	}}
	cli := newMockClient(t, conn)
	ag := New(cli, WithTools(tool.NewSet(echoTool())))

	ch, err := ag.RunStream(context.Background(), &memTranscript{})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var results []*ToolReturn
	for c := range ch {
		if s, ok := chat.AsText(&c); ok {
			text += s
		}
		if r, ok := AsToolReturn(&c); ok {
			results = append(results, r)
		}
		if e, ok := chat.AsError(&c); ok {
			t.Fatalf("stream error: %v", e)
		}
	}
	if text != "done" {
		t.Fatalf("text = %q", text)
	}
	if len(results) != 1 || results[0].Call.Name != "echo" {
		t.Fatalf("results = %+v", results)
	}
	if got := results[0].Result.Content; got != `echo={"city":"sh"}` {
		t.Fatalf("reassembled args wrong: %q", got)
	}
}
