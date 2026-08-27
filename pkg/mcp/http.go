package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

// HTTPEndpoint connects to a remote MCP server over the Streamable HTTP
// transport (client side only — Arcus never exposes an HTTP service). Build
// one with HTTP, optionally set Header/Client, then hand it to Dial.
type HTTPEndpoint struct {
	URL string
	// Header is attached to every request — the place for Authorization.
	Header http.Header
	// Client is the http.Client to use (nil = http.DefaultClient).
	Client *http.Client
}

// HTTP describes a remote MCP server:
//
//	ep := mcp.HTTP("https://example.com/mcp")
//	ep.Header = http.Header{"Authorization": {"Bearer " + token}}
//	c, err := mcp.Dial(ctx, ep)
func HTTP(url string) *HTTPEndpoint {
	return &HTTPEndpoint{URL: url, Header: http.Header{}}
}

func (e *HTTPEndpoint) dial(context.Context) (transport, error) {
	if e.URL == "" {
		return nil, fmt.Errorf("mcp: HTTP endpoint has no URL")
	}
	cli := e.Client
	if cli == nil {
		cli = http.DefaultClient
	}
	return &httpTransport{url: e.URL, client: cli, header: e.Header}, nil
}

type httpTransport struct {
	url    string
	client *http.Client
	header http.Header

	mu       sync.Mutex
	session  string // Mcp-Session-Id assigned by the server
	protoVer string // negotiated version, echoed on every request post-init
}

func (t *httpTransport) setProtocolVersion(v string) {
	t.mu.Lock()
	t.protoVer = v
	t.mu.Unlock()
}

func (t *httpTransport) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vs := range t.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	if t.session != "" {
		req.Header.Set("Mcp-Session-Id", t.session)
	}
	if t.protoVer != "" {
		req.Header.Set("MCP-Protocol-Version", t.protoVer)
	}
	t.mu.Unlock()
	return req, nil
}

func (t *httpTransport) do(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := t.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.session = sid
		t.mu.Unlock()
	}
	return resp, nil
}

func (t *httpTransport) call(ctx context.Context, rpc *rpcRequest) (*rpcResponse, error) {
	body, err := json.Marshal(rpc)
	if err != nil {
		return nil, err
	}
	resp, err := t.do(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp: server returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch ct {
	case "text/event-stream":
		return readSSEResponse(resp.Body, rpc.ID)
	default: // application/json (or unlabeled)
		var out rpcResponse
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return nil, fmt.Errorf("mcp: decode response: %w", err)
		}
		return &out, nil
	}
}

func (t *httpTransport) notify(ctx context.Context, method string, params any) error {
	body, err := json.Marshal(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	resp, err := t.do(ctx, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)) //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("mcp: notification %q returned %s", method, resp.Status)
	}
	return nil
}

// close best-effort terminates the session (DELETE with the session id); many
// servers don't support it, which is fine.
func (t *httpTransport) close() error {
	t.mu.Lock()
	session := t.session
	t.mu.Unlock()
	if session == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodDelete, t.url, nil)
	if err != nil {
		return nil
	}
	for k, vs := range t.header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Mcp-Session-Id", session)
	if resp, err := t.client.Do(req); err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1024)) //nolint:errcheck
		resp.Body.Close()
	}
	return nil
}

// readSSEResponse scans a text/event-stream body until it sees the JSON-RPC
// response matching id, ignoring interleaved notifications and other events.
func readSSEResponse(r io.Reader, id int64) (*rpcResponse, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var data strings.Builder
	flush := func() (*rpcResponse, bool) {
		if data.Len() == 0 {
			return nil, false
		}
		payload := data.String()
		data.Reset()
		var msg rpcResponse
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			return nil, false
		}
		if msg.Method != "" || len(msg.ID) == 0 {
			return nil, false // request or notification from server; skip
		}
		var gotID int64
		if err := json.Unmarshal(msg.ID, &gotID); err != nil || gotID != id {
			return nil, false
		}
		return &msg, true
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if msg, ok := flush(); ok {
				return msg, nil
			}
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		default:
			// id:, event:, retry:, comments — irrelevant to correlation here.
		}
	}
	if msg, ok := flush(); ok {
		return msg, nil
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("mcp: read event stream: %w", err)
	}
	return nil, fmt.Errorf("mcp: event stream ended without a response for request %d", id)
}
