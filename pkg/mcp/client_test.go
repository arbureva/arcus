package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeServer speaks newline-delimited JSON-RPC over pipes, implementing just
// enough MCP for the client tests.
func fakeServer(t *testing.T, r io.Reader, w io.WriteCloser) {
	t.Helper()
	defer w.Close() // like a real subprocess: stdout closes when the server exits
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	reply := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "%s\n", b)
	}
	for sc.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			t.Errorf("server: bad frame %q", sc.Text())
			continue
		}
		switch msg.Method {
		case "initialize":
			reply(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "0.1"},
				"instructions":    "be gentle",
			}})
		case "notifications/initialized":
			// notification, no reply
		case "tools/list":
			var p listToolsParams
			_ = json.Unmarshal(msg.Params, &p)
			if p.Cursor == "" {
				reply(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{
					"tools": []map[string]any{{
						"name":        "add",
						"description": "Add two numbers.",
						"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
							"a": map[string]any{"type": "number"}, "b": map[string]any{"type": "number"},
						}},
					}},
					"nextCursor": "page2",
				}})
			} else {
				reply(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{
					"tools": []map[string]any{{"name": "fail", "description": "Always fails."}},
				}})
			}
		case "tools/call":
			var p callToolParams
			_ = json.Unmarshal(msg.Params, &p)
			switch p.Name {
			case "add":
				var args struct{ A, B float64 }
				_ = json.Unmarshal(p.Arguments, &args)
				reply(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{
					"content":           []map[string]any{{"type": "text", "text": fmt.Sprintf("%g", args.A+args.B)}},
					"structuredContent": map[string]any{"sum": args.A + args.B},
				}})
			case "fail":
				reply(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "boom"}},
					"isError": true,
				}})
			}
		}
	}
}

type pipeEndpoint struct{ t *testing.T }

func (e pipeEndpoint) dial(context.Context) (transport, error) {
	clientR, serverW := io.Pipe() // server -> client
	serverR, clientW := io.Pipe() // client -> server
	go fakeServer(e.t, serverR, serverW)
	return newPipeTransport(clientW, clientR, nil), nil
}

func TestDialAndCallOverPipe(t *testing.T) {
	c, err := Dial(context.Background(), pipeEndpoint{t}, WithToolPrefix("math_"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if c.ServerInfo().Name != "fake" || c.Instructions() != "be gentle" {
		t.Fatalf("handshake: %+v %q", c.ServerInfo(), c.Instructions())
	}

	tools, err := c.Tools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 { // pagination followed
		t.Fatalf("tools = %d", len(tools))
	}
	def := tools[0].Definition()
	if def.Name != "math_add" || !strings.Contains(string(def.Schema), `"a"`) {
		t.Fatalf("definition = %+v", def)
	}

	res, err := tools[0].Invoke(context.Background(), json.RawMessage(`{"a":2,"b":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "5" || res.IsError {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(string(res.Meta), `"sum":5`) {
		t.Fatalf("structured meta = %s", res.Meta)
	}

	res, err = tools[1].Invoke(context.Background(), nil)
	if err != nil || !res.IsError || res.Content != "boom" {
		t.Fatalf("fail tool: %+v %v", res, err)
	}
}

// TestHTTPTransport runs the same handshake against an httptest server that
// answers initialize as SSE and everything else as plain JSON, and checks the
// session header round-trips.
func TestHTTPTransport(t *testing.T) {
	var sawSession, sawProtoVer atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &msg)
		if r.Header.Get("Mcp-Session-Id") == "s-123" {
			sawSession.Store(true)
		}
		if r.Header.Get("MCP-Protocol-Version") == ProtocolVersion {
			sawProtoVer.Store(true)
		}
		switch msg.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s-123")
			w.Header().Set("Content-Type", "text/event-stream")
			result, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{
				"protocolVersion": ProtocolVersion,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "http-fake", "version": "0.1"},
			}})
			fmt.Fprintf(w, ": ping comment\n\n")
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", result)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID), "result": map[string]any{
				"tools": []map[string]any{{"name": "ping", "description": "Ping."}},
			}})
		}
	}))
	defer srv.Close()

	c, err := Dial(context.Background(), HTTP(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.ServerInfo().Name != "http-fake" {
		t.Fatalf("server info = %+v", c.ServerInfo())
	}
	tools, err := c.Tools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	if !sawSession.Load() {
		t.Fatal("session id was not echoed back")
	}
	if !sawProtoVer.Load() {
		t.Fatal("protocol version header missing after initialize")
	}
}
